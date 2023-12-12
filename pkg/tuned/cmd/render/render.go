/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package render

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/manifests"
	"github.com/openshift/cluster-node-tuning-operator/pkg/operator"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/tuned"
	"sigs.k8s.io/yaml"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	tunedpkg "github.com/openshift/cluster-node-tuning-operator/pkg/tuned"

	"github.com/openshift/cluster-node-tuning-operator/pkg/util"
	"github.com/openshift/cluster-node-tuning-operator/version"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog"
)

var (
	manifestScheme = runtime.NewScheme()
	codecFactory   serializer.CodecFactory
	runtimeDecoder runtime.Decoder
)

func init() {
	utilruntime.Must(performancev2.AddToScheme(manifestScheme))
	utilruntime.Must(mcfgv1.Install(manifestScheme))
	utilruntime.Must(tunedv1.AddToScheme(manifestScheme))
	codecFactory = serializer.NewCodecFactory(manifestScheme)
	runtimeDecoder = codecFactory.UniversalDecoder(
		performancev2.GroupVersion,
		mcfgv1.GroupVersion,
		tunedv1.SchemeGroupVersion,
	)
}

func render(inputDir []string, outputDir string) error {
	klog.Info("Rendering files from: ", inputDir)
	klog.Info("Rendering files into: ", outputDir)

	bootstrapSafeEnv := os.Getenv("CLUSTER_NODE_TUNED_BOOTSTRAP_SAFE_ENV")
	if len(bootstrapSafeEnv) == 0 {
		return fmt.Errorf("Should only be run on bootstrap safe environment. Please define env var 'CLUSTER_NODE_TUNED_BOOTSTRAP_SAFE_ENV' ")
	}

	// Get pools, mConfigs and profile from inputDir
	// Read asset directory fileInfo
	filePaths, err := util.ListFilesFromMultiplePaths(inputDir)
	if err != nil {
		return fmt.Errorf("error while listing files: %w", err)
	}
	klog.Infof("listed files: %v", filePaths)
	// Make output dir if not present
	err = os.MkdirAll(outputDir, os.ModePerm)
	if err != nil {
		return fmt.Errorf("Error while creating outputdir %s : %w", outputDir, err)
	}

	var (
		perfProfiles []*performancev2.PerformanceProfile
		mcPools      []*mcfgv1.MachineConfigPool
		mcConfigs    []*mcfgv1.MachineConfig
		tuneD        []*tunedv1.Tuned
	)

	// Iterate through the file paths and read in desired files
	klog.Info("Iterating over listed files ... ")
	for _, path := range filePaths {
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("error opening %s: %w", file.Name(), err)
		}
		defer file.Close()

		manifests, err := util.ParseManifests(file.Name(), file)
		if err != nil {
			return fmt.Errorf("error parsing manifests from %s: %w", file.Name(), err)
		}

		// Decode manifest files
		klog.V(4).Infof("decoding manifests for file %s...", path)
		for idx, m := range manifests {
			obji, err := runtime.Decode(runtimeDecoder, m.Raw)
			if err != nil {
				if runtime.IsNotRegisteredError(err) {
					klog.V(4).Infof("skipping path %q [%d] manifest because it is not part of expected api group: %v", file.Name(), idx+1, err)
					continue
				}
				return fmt.Errorf("error parsing %q [%d] manifest: %w", file.Name(), idx+1, err)
			}

			switch obj := obji.(type) {
			case *performancev2.PerformanceProfile:
				perfProfiles = append(perfProfiles, obj)
			case *mcfgv1.MachineConfigPool:
				mcPools = append(mcPools, obj)
			case *mcfgv1.MachineConfig:
				mcConfigs = append(mcConfigs, obj)
			case *tunedv1.Tuned:
				tuneD = append(tuneD, obj)
			default:
				klog.Infof("skipping %q [%d] manifest because of unhandled %T", file.Name(), idx+1, obji)
			}
		}
	}

	perfProfilesTuneDProfiles := make(map[*performancev2.PerformanceProfile]*tunedv1.Tuned, len(perfProfiles))
	for _, profile := range perfProfiles {
		tunedFromPP, err := tuned.NewNodePerformance(profile)
		if err != nil {
			klog.Errorf("Unable to get tuned. error : %v", err)
			return fmt.Errorf("unable to get tuned from profile: %w", err)
		}
		perfProfilesTuneDProfiles[profile] = tunedFromPP
		// add tuned from PP to the list
		tuneD = append(tuneD, tunedFromPP)
	}

	tuneDrecommended := operator.TunedRecommend(tuneD)
	if len(tuneDrecommended) == 0 {
		klog.Error("Unable to get tuned recommended profile.")
		return fmt.Errorf("Unable to get tuned recommended profile.")
	}

	recommendedProfile := *tuneDrecommended[0].Profile
	err = tunedpkg.TunedRecommendFileWrite(recommendedProfile)
	if err != nil {
		klog.Errorf("error writing recommended profile %q : %v", recommendedProfile, err)
		return fmt.Errorf("error writing recommended profile %q : %w", recommendedProfile, err)
	}

	t := manifests.TunedRenderedResource(tuneD)
	//extract all the profiles.
	_, _, _, err = tunedpkg.ProfilesExtract(t.Spec.Profile, recommendedProfile)
	if err != nil {
		klog.Errorf("error extracting tuned profiles : %v", err)
		return fmt.Errorf("error extracting tuned profiles: %w", err)
	}

	//Should run tuned
	tunedCmd := tunedpkg.TunedCreateCmd(false)
	err = tunedpkg.TunedRunNoDaemon(tunedCmd)
	if err != nil {
		klog.Errorf("Unable to run tuned error : %v", err)
		return err
	}

	bootcmdline, err := tunedpkg.GetBootcmdline()
	if err != nil {
		klog.Errorf("Unable to get bootcmdline. error : %v", err)
		return err
	}

	for profile, tunedFromPP := range perfProfilesTuneDProfiles {
		mc, err := renderMachineConfig(mcPools, bootcmdline, mcConfigs, profile, tunedFromPP)
		if err != nil {
			klog.Errorf("error while rendering machine config  %v", err)
			return fmt.Errorf("error while rendering machine config: %w", err)
		}

		if mc != nil {
			//Render mc in output dir
			byteOutput, err := yaml.Marshal(mc)
			if err != nil {
				klog.Errorf("Unable to render output machineconfig. error : %v", err)
				return err
			}

			fileName := fmt.Sprintf("%s_%s_kargs.yaml", profile.Name, strings.ToLower(mc.Kind))
			fullFilePath := filepath.Join(outputDir, fileName)
			klog.Info("Writing file: ", fullFilePath)
			// err = os.WriteFile(fullFilePath, byteOutput, 0644)
			// if err != nil {
			// 	klog.Errorf("Unable to write output file %s. error : %v", fullFilePath, err)
			// 	return err
			// }
			file, err := os.Create(fullFilePath)
			if err != nil {
				klog.Errorf("unable to create output file %s. error : %v", fullFilePath, err)
				return err
			}
			_, err = file.Write(byteOutput)
			if err != nil {
				klog.Errorf("unable to write output file %s. error : %v", fullFilePath, err)
				return err
			}
			err = file.Sync()
			if err != nil {
				klog.Errorf("unable to sync output file %s. error : %v", fullFilePath, err)
				return err
			}
			klog.Infof("MachineConfig written at : %s", fullFilePath)
		}
	}
	return nil
}

func renderMachineConfig(pools []*mcfgv1.MachineConfigPool, bootcmdline string, mConfigs []*mcfgv1.MachineConfig, profile *performancev2.PerformanceProfile, tunedMf *tunedv1.Tuned) (*mcfgv1.MachineConfig, error) {
	if len(bootcmdline) == 0 {
		klog.Info("Empty cmdbootline. Avoid creating MachineConfig")
		return nil, nil
	}

	mcName := operator.GetMachineConfigNameForPools(pools)
	kernelArgs := util.SplitKernelArguments(bootcmdline)
	annotations := map[string]string{operator.GeneratedByControllerVersionAnnotationKey: version.Version}
	labels := tunedMf.Spec.Recommend[0].MachineConfigLabels

	mc := getMachineConfigByName(mConfigs, mcName)
	if mc == nil { //not found
		// Expect only one PerformanceProfile => one TuneD
		mc = operator.NewMachineConfig(mcName, annotations, labels, kernelArgs)
		klog.Infof("rendered MachineConfig %s with%s", mc.ObjectMeta.Name, operator.MachineConfigGenerationLogLine(len(bootcmdline) != 0, bootcmdline))
		return mc, nil
	}

	// found a MC need to modify it
	mcNew := operator.NewMachineConfig(mcName, annotations, labels, kernelArgs)

	kernelArgsEq := util.StringSlicesEqual(mc.Spec.KernelArguments, kernelArgs)
	if kernelArgsEq {
		// No update needed
		klog.Infof("renderMachineConfig: MachineConfig %s doesn't need updating", mc.ObjectMeta.Name)
		return nil, nil
	}

	mc = mc.DeepCopy() // never update the objects from cache
	mc.ObjectMeta.Annotations = mcNew.ObjectMeta.Annotations
	mc.Spec.KernelArguments = removeDuplicates(append(mc.Spec.KernelArguments, kernelArgs...))
	mc.Spec.Config = mcNew.Spec.Config
	l := operator.MachineConfigGenerationLogLine(!kernelArgsEq, bootcmdline)
	klog.Infof("renderMachineConfig: updating MachineConfig %s with%s", mc.ObjectMeta.Name, l)

	return mc, nil
}

func getMachineConfigByName(mConfigs []*mcfgv1.MachineConfig, name string) *mcfgv1.MachineConfig {
	for _, mc := range mConfigs {
		if mc.Name == name {
			return mc
		}
	}
	return nil
}

func removeDuplicates[T string | int](sliceList []T) []T {
	allKeys := make(map[T]bool)
	list := []T{}
	for _, item := range sliceList {
		if _, value := allKeys[item]; !value {
			allKeys[item] = true
			list = append(list, item)
		}
	}
	return list
}
