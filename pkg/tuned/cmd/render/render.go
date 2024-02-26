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
	"io"
	"os"
	"path/filepath"
	"strings"

	assets "github.com/openshift/cluster-node-tuning-operator/assets/tuned"
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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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

func decodeManifestsFromFile(filename string,
	r io.Reader,
	runtimeDecoder runtime.Decoder,
	perfProfiles *[]*performancev2.PerformanceProfile,
	mcPools *[]*mcfgv1.MachineConfigPool,
	mcConfigs *[]*mcfgv1.MachineConfig,
	tuneD *[]*tunedv1.Tuned) error {
	manifests, err := util.ParseManifests(filename, r)
	if err != nil {
		return fmt.Errorf("error parsing manifests from %s: %w", filename, err)
	}

	// Decode manifest files
	klog.V(4).Infof("decoding manifests for file %s...", filename)
	for idx, m := range manifests {
		obji, err := runtime.Decode(runtimeDecoder, m.Raw)
		if err != nil {
			if runtime.IsNotRegisteredError(err) {
				klog.Infof("skipping path %q [%d] manifest because it is not part of expected api group: %v", filename, idx+1, err)
				continue
			}
			klog.Errorf("error parsing %q [%d] manifest: %v", filename, idx+1, err)
			return fmt.Errorf("error parsing %q [%d] manifest: %w", filename, idx+1, err)
		}

		switch obj := obji.(type) {
		case *performancev2.PerformanceProfile:
			klog.Infof("Adding PerformanceProfile Manifest %q from %q", obj.Name, filename)
			*perfProfiles = append(*perfProfiles, obj)
		case *mcfgv1.MachineConfigPool:
			klog.Infof("Adding MachineConfigPool Manifest %q from %q", obj.Name, filename)
			*mcPools = append(*mcPools, obj)
		case *mcfgv1.MachineConfig:
			klog.Infof("Adding MachineConfig Manifest %q from %q", obj.Name, filename)
			*mcConfigs = append(*mcConfigs, obj)
		case *tunedv1.Tuned:
			klog.Infof("Adding TuneD Manifest %q from %q", obj.Name, filename)
			*tuneD = append(*tuneD, obj)
		default:
			klog.V(4).Infof("skipping %q [%d] manifest because of unhandled %T", filename, idx+1, obji)
		}
	}

	return nil
}
func render(inputDir []string, outputDir string, mcpName string) error {
	klog.Info("Rendering files from: ", inputDir)
	klog.Info("Rendering files into: ", outputDir)
	klog.Info("Using MachineConfigPool: ", mcpName)

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
	klog.V(4).Infof("listed files: %v", filePaths)
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

		err = decodeManifestsFromFile(file.Name(), file, runtimeDecoder, &perfProfiles, &mcPools, &mcConfigs, &tuneD)
		if err != nil {
			return err
		}
	}

	if len(perfProfiles) == 0 {
		klog.Infof("No PerformanceProfile found on input dirs %s", strings.Join(inputDir, ","))
	}

	// Append any missing default manifests (i.e. `master`/`worker`)
	mcPools = util.AppendMissingDefaultMCPManifests(mcPools)

	mcp := findMachineConfigPoolByName(mcPools, mcpName)
	if mcp == nil {
		klog.Errorf("Unable to find MachineConfigPool:%q in input folders", mcpName)
		return fmt.Errorf("Unable to find MachineConfigPool:%q in input folders", mcpName)
	}

	filteredPerformanceProfiles, err := filterPerformanceProfilesByMachineConfigPool(perfProfiles, mcp)
	if err != nil {
		klog.Errorf("Unable to find a PerformanceProfile that matches the MachineConfigPool %s. error : %v", mcpName, err)
		return fmt.Errorf("Unable to get PerformanceProfile to apply using MachineConfigPool %s. error : %w", mcpName, err)
	}

	if len(filteredPerformanceProfiles) == 0 {
		klog.Infof("No PerformanceProfile found for MachineConfigPool %s", mcpName)
	}
	for _, pp := range filteredPerformanceProfiles {
		tunedFromPP, err := tuned.NewNodePerformance(pp)
		if err != nil {
			e := fmt.Errorf("unable to get tuned from PerformanceProfile:%s. error: %w", pp.Name, err)
			klog.Error(e)
			return e
		}
		// add tuned from PP to the list
		tuneD = append(tuneD, tunedFromPP)
	}

	klog.Infof("working with %d additional tuneD profiles", len(tuneD))

	if err := loadDefaultCrTuneD(&tuneD); err != nil {
		e := fmt.Errorf("unable to load default cr tuned %w", err)
		klog.Error(e)
		return e
	}

	tuneDrecommended := operator.TunedRecommend(tuneD)
	if len(tuneDrecommended) == 0 {
		e := fmt.Errorf("unable to get recommended profile")
		klog.Error(e)
		return e
	}

	recommendedProfile := *tuneDrecommended[0].Profile
	klog.Infof("RecommendedProfile found :%s", recommendedProfile)

	err = tunedpkg.TunedRecommendFileWrite(recommendedProfile)
	if err != nil {
		err := fmt.Errorf("error writing recommended profile %q : %v", recommendedProfile, err)
		klog.Error(err.Error())
		return err
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

	mc, err := renderMachineConfig(mcp, bootcmdline, mcConfigs, mcp.Spec.MachineConfigSelector.MatchLabels)
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

		fileName := fmt.Sprintf("%s_%s_kargs.yaml", "kernelcmdargs", strings.ToLower(mc.Kind))
		fullFilePath := filepath.Join(outputDir, fileName)
		klog.Info("Writing file: ", fullFilePath)
		err = os.WriteFile(fullFilePath, byteOutput, 0644)
		if err != nil {
			klog.Errorf("Unable to write output file %s. error : %v", fullFilePath, err)
			return err
		}

		klog.Infof("MachineConfig written at : %s", fullFilePath)
	}

	return nil
}

func loadDefaultCrTuneD(tuneD *[]*tunedv1.Tuned) error {
	var (
		dummyPerfProfiles []*performancev2.PerformanceProfile
		dummyMcPools      []*mcfgv1.MachineConfigPool
		dummyMcConfigs    []*mcfgv1.MachineConfig
	)

	klog.Infof("Unable to get tuned recommended profile with current info. Adding default tuneD")

	fileName := "default-cr-tuned.yaml"
	f, err := assets.Manifests.Open(filepath.Join("manifests", fileName))
	if err != nil {
		return err
	}
	defer f.Close()

	err = decodeManifestsFromFile(fileName, f, runtimeDecoder, &dummyPerfProfiles, &dummyMcPools, &dummyMcConfigs, tuneD)
	if err != nil {
		return err
	}
	return nil
}

func renderMachineConfig(pool *mcfgv1.MachineConfigPool, bootcmdline string, mConfigs []*mcfgv1.MachineConfig, mcLabels map[string]string) (*mcfgv1.MachineConfig, error) {
	if len(bootcmdline) == 0 {
		klog.Info("Empty cmdbootline. Avoid creating MachineConfig")
		return nil, nil
	}

	pools := []*mcfgv1.MachineConfigPool{pool}
	mcName := operator.GetMachineConfigNameForPools(pools)
	kernelArgs := util.SplitKernelArguments(bootcmdline)
	annotations := map[string]string{operator.GeneratedByControllerVersionAnnotationKey: version.Version}

	mc := getMachineConfigByName(mConfigs, mcName)
	if mc == nil { //not found
		// Expect only one PerformanceProfile => one TuneD
		mc = operator.NewMachineConfig(mcName, annotations, mcLabels, kernelArgs)
		klog.Infof("rendered MachineConfig %s with%s", mc.ObjectMeta.Name, operator.MachineConfigGenerationLogLine(len(bootcmdline) != 0, bootcmdline))
		return mc, nil
	}

	// found a MC need to modify it
	mcNew := operator.NewMachineConfig(mcName, annotations, mcLabels, kernelArgs)

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

func findMachineConfigPoolByName(mcPools []*mcfgv1.MachineConfigPool, mcpName string) *mcfgv1.MachineConfigPool {
	for _, mcp := range mcPools {
		if mcp.Name == mcpName {
			return mcp
		}
	}
	return nil
}

func filterPerformanceProfilesByMachineConfigPool(performanceProfiles []*performancev2.PerformanceProfile, mcp *mcfgv1.MachineConfigPool) ([]*performancev2.PerformanceProfile, error) {
	mcpSelector, err := metav1.LabelSelectorAsSelector(mcp.Spec.NodeSelector)
	if err != nil {
		return nil, fmt.Errorf("Unable to get NodeSelector from MachineConfigPool: %s. error: %w", mcp.Name, err)
	}

	result := make([]*performancev2.PerformanceProfile, 0, len(performanceProfiles))

	for _, perfProfile := range performanceProfiles {
		if perfProfile.Spec.NodeSelector == nil {
			//NOTE - this is not a valid PerformanceProfile as NodeSelect should not be empty
			continue
		}

		perfProfileNodeSelector := labels.Set(perfProfile.Spec.NodeSelector)

		if mcpSelector.Matches(perfProfileNodeSelector) {
			result = append(result, perfProfile)
		}
	}

	return result, nil
}
