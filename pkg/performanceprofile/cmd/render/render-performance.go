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

	"gopkg.in/ini.v1"
	"sigs.k8s.io/yaml"

	apicfgv1 "github.com/openshift/api/config/v1"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunev1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/operator"
	pputils "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/manifestset"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/tuned"
	"github.com/openshift/cluster-node-tuning-operator/pkg/util"
	"github.com/openshift/cluster-node-tuning-operator/version"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"

	"k8s.io/klog"
)

func getKernelArguments(profile *performancev2.PerformanceProfile) (string, error) {
	var kernelArguments string

	isolatedCores := string(*profile.Spec.CPU.Isolated)
	notIsolatedCoresExpanded := string(performancev2.CPUSet(string(*profile.Spec.CPU.Reserved)))
	notIsolatedCPUMask, err := pputils.CPUListToMaskList(notIsolatedCoresExpanded)
	if err != nil {
		return kernelArguments, err
	}

	replacements := map[string]string{
		"${isolated_cores}":              isolatedCores,
		"${not_isolated_cpumask}":        notIsolatedCPUMask,
		"${not_isolated_cores_expanded}": notIsolatedCoresExpanded,
	}

	performanceTuned, err := tuned.NewNodePerformance(profile)
	if err != nil {
		return kernelArguments, err
	}

	data := *performanceTuned.Spec.Profile[0].Data
	config, err := ini.Load([]byte(data))
	if err != nil {
		klog.Infof("Failed to load performance tune data (%s)", data)
		return kernelArguments, err
	}
	bootSection, err := config.GetSection("bootloader")
	if err != nil {
		klog.Infof("No bootloader section in performace tune data (%s)", data)
		return kernelArguments, err
	}
	cmdlines := []string{
		"cmdline_network_latency",
		"cmdline_cpu_part",
		"cmdline_isolation",
		"cmdline_realtime",
		"cmdline_hugepages",
		"cmdline_additionalArg",
		"cmdline_pstate",
	}

	for _, cmd := range cmdlines {
		cmd_opts := bootSection.Key(cmd).String()
		// Remove the "+" prefix from the string
		cmd_opts = strings.TrimPrefix(cmd_opts, "+")
		for old, new := range replacements {
			cmd_opts = strings.ReplaceAll(cmd_opts, old, new)
		}
		kernelArguments = kernelArguments + " " + cmd_opts
	}
	return kernelArguments, nil
}

// Render will traverse the input directory and generate the proper performance profile files
// in to the output dir based on PerformanceProfile manifests contained in the input directory.
func renderPerformance(inputDir, outputDir string, renderTune bool) error {

	klog.Info("Rendering peerformance files into: ", outputDir)

	// Read asset directory fileInfo
	filePaths, err := listFiles(inputDir)
	klog.V(4).Infof("listed files: %v", filePaths)
	if err != nil {
		return err
	}

	// Make output dir if not present
	err = os.MkdirAll(outputDir, os.ModePerm)
	if err != nil {
		return err
	}

	var (
		perfProfiles   []*performancev2.PerformanceProfile
		mcPools        []*mcfgv1.MachineConfigPool
		mcConfigs      []*mcfgv1.MachineConfig
		infra          *apicfgv1.Infrastructure
		ctrcfgs        []*mcfgv1.ContainerRuntimeConfig
		tune           []*tunev1.Tuned
		extractedFiles []string
		configmap      *corev1.ConfigMap
	)

	// Iterate through the file paths and check for performance configmap
	for _, path := range filePaths {
		klog.Infof("processing path %s", path)
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("error opening %s: %w", file.Name(), err)
		}
		defer file.Close()
		klog.Infof("input manifest %s", file.Name())
		manifests, err := parseManifests(file.Name(), file)
		if err != nil {
			return fmt.Errorf("error parsing manifests from %s: %w", file.Name(), err)
		}

		// Decode manifest files
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
			case *corev1.ConfigMap:
				klog.Info(obj.Name)
				if obj.Name == "nto-render-performance" {
					configmap = obj
					klog.Info("found configmap nto-render-performance")
				}
			default:
				klog.Infof("Skipping %s", obj.GetObjectKind().GroupVersionKind().Kind)
			}

		}
	}
	if configmap == nil {
		klog.Info("not found configmap nto-render-performance")
		return nil
	}
	dname, err := os.MkdirTemp("", "")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dname)

	for k, v := range configmap.Data {
		path := filepath.Join(dname, k)
		err = os.WriteFile(path, []byte(v), 0644)
		if err != nil {
			return err
		}
		extractedFiles = append(extractedFiles, path)
	}

	klog.Info(extractedFiles)
	// Iterate through the file paths and read in desired files
	for _, path := range extractedFiles {
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("error opening %s: %w", file.Name(), err)
		}
		defer file.Close()
		klog.Infof("input manifest %s", file.Name())
		manifests, err := parseManifests(file.Name(), file)
		if err != nil {
			return fmt.Errorf("error parsing manifests from %s: %w", file.Name(), err)
		}
		// Decode manifest files
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
			case *apicfgv1.Infrastructure:
				if obj.Name == clusterConfigResourceName {
					infra = obj
				}
			case *mcfgv1.ContainerRuntimeConfig:
				ctrcfgs = append(ctrcfgs, obj)
			case *tunev1.Tuned:
				tune = append(tune, obj)
			default:
				klog.Infof("skipping %q [%d] manifest because of unhandled %T", file.Name(), idx+1, obji)
			}
		}
	}

	if len(perfProfiles) == 0 {
		klog.Warning("zero performance profiles were found")
	}

	var partitioningMode *apicfgv1.CPUPartitioningMode
	if infra != nil {
		partitioningMode = &infra.Status.CPUPartitioning
	}

	if renderTune {
		if len(tune) == 0 {
			klog.Warning("zero tune patch were found")
		}
		if isLegacySNOWorkloadPinningMethod(mcConfigs, infra, partitioningMode) {
			legacyAllNodes := apicfgv1.CPUPartitioningAllNodes
			partitioningMode = &legacyAllNodes
		}

		if err := genBootstrapWorkloadPinningManifests(partitioningMode, outputDir, defaultMCPNames...); err != nil {
			return err
		}

		// If the user supplies extra machine pools, we ingest them here
		for _, pool := range mcPools {
			if err := genBootstrapWorkloadPinningManifests(partitioningMode, outputDir, pool.Name); err != nil {
				return err
			}
		}

		for _, tt := range tune {
			var kernelArguments []string

			mc := tt.Spec.Recommend[0].MachineConfigLabels["machineconfiguration.openshift.io/role"]
			var mcp *mcfgv1.MachineConfigPool
			for _, pool := range mcPools {
				if pool.Name == mc {
					mcp = pool
				}
			}
			if mcp == nil {
				klog.Infof("render: No MachineConfigPool found for Tune patch %s", tt.Name)
				continue
			}

			tunedData := []byte(*tt.Spec.Profile[0].Data)
			cfg, err := ini.Load(tunedData)
			if err != nil {
				klog.Infof("Failed to load tune data (%s)", *tt.Spec.Profile[0].Data)
				continue
			}

			mainSection, err := cfg.GetSection("main")
			if err != err {
				klog.Infof("No main section in tune data (%s)", tunedData)
				continue
			}

			profileName := mainSection.Key("include").String()
			prefix := "openshift-node-performance-"
			realName := strings.TrimPrefix(profileName, prefix)
			for _, pp := range perfProfiles {
				if pp.ObjectMeta.Name == realName {
					cmdlines, err := getKernelArguments(pp)
					if err != nil {
						continue
					}
					kernelArguments = util.SplitKernelArguments(cmdlines)
				}
			}

			bootLoaderSection, err := cfg.GetSection("bootloader")
			if err != err {
				klog.Infof("No bootloader section in tune data (%s)", tunedData)
				continue
			}

			cmdline_crash := bootLoaderSection.Key("cmdline_crash").String()
			kernelArguments = append(kernelArguments, cmdline_crash)

			var sb strings.Builder
			sb.WriteString(operator.MachineConfigPrefix)
			sb.WriteString("-")
			sb.WriteString(mcp.ObjectMeta.Name)

			labels := tt.Spec.Recommend[0].MachineConfigLabels
			annotations := map[string]string{operator.GeneratedByControllerVersionAnnotationKey: version.Version}

			// kernelArguments is a slice of strings one parameter per string
			mcNew := operator.NewMachineConfig(sb.String(), annotations, labels, kernelArguments)
			var manifest interface{} = &mcNew
			b, err := yaml.Marshal(manifest)
			if err != nil {
				return err
			}
			fileName := fmt.Sprintf("%s.yaml", sb.String())
			fullFilePath := filepath.Join(outputDir, fileName)
			klog.Info("Writing file: ", fullFilePath)

			err = os.WriteFile(fullFilePath, b, 0644)
			if err != nil {
				return err
			}
			klog.Info(fileName)
		}
	}
	for _, pp := range perfProfiles {
		mcp, err := selectMachineConfigPool(mcPools, pp.Spec.NodeSelector)
		if err != nil {
			return err
		}

		if mcp == nil {
			klog.Infof("render: No MachineConfigPool found for PerformanceProfile %s", pp.Name)
			continue
		}

		defaultRuntime, err := getContainerRuntimeName(pp, mcp, ctrcfgs)
		if err != nil {
			return fmt.Errorf("render: could not determine high-performance runtime class container-runtime for profile %q; %w", pp.Name, err)
		}

		components, err := manifestset.GetNewComponents(pp, mcp, partitioningMode, defaultRuntime)
		if err != nil {
			return err
		}

		uid := pp.UID
		if uid == types.UID("") {
			uid = uuid.NewUUID()
		}

		if !renderTune {
			or := []v1.OwnerReference{
				{
					Kind:       pp.Kind,
					Name:       pp.Name,
					APIVersion: pp.APIVersion,
					UID:        uid,
				},
			}

			for _, componentObj := range components.ToObjects() {
				// if componentObj.GetName()
				componentObj.SetOwnerReferences(or)
			}
		}
		for kind, manifest := range components.ToManifestTable() {

			b, err := yaml.Marshal(manifest)
			if err != nil {
				return err
			}

			fileName := fmt.Sprintf("%s_%s.yaml", pp.Name, strings.ToLower(kind))
			fullFilePath := filepath.Join(outputDir, fileName)
			klog.Info("Writing file: ", fullFilePath)

			err = os.WriteFile(fullFilePath, b, 0644)
			if err != nil {
				return err
			}
			klog.Info(fileName)
		}
	}

	return nil
}
