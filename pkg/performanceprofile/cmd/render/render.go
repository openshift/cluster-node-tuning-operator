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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	apicfgv1 "github.com/openshift/api/config/v1"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/machineconfig"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/manifestset"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"k8s.io/klog"
)

const (
	clusterConfigResourceName = "cluster"
)

const (
	ownerRefModeNone      = "none"
	ownerRefModeK8S       = "k8s"
	ownerRefModeLabelName = "label-name"
)

const (
	weakOwnerReferenceNameLabel = "performance.openshift.io/weak-owner-reference-name"
)

var (
	manifestScheme  = runtime.NewScheme()
	codecFactory    serializer.CodecFactory
	runtimeDecoder  runtime.Decoder
	defaultMCPNames = []string{"master", "worker"}
)

func init() {
	utilruntime.Must(performancev2.AddToScheme(manifestScheme))
	utilruntime.Must(apicfgv1.Install(manifestScheme))
	utilruntime.Must(mcfgv1.Install(manifestScheme))
	codecFactory = serializer.NewCodecFactory(manifestScheme)
	runtimeDecoder = codecFactory.UniversalDecoder(
		performancev2.GroupVersion,
		apicfgv1.GroupVersion,
		mcfgv1.GroupVersion,
	)
}

// Render will traverse the input directory and generate the proper performance profile files
// in to the output dir based on PerformanceProfile manifests contained in the input directory.
func render(ownerRefMode, inputDir, outputDir string) error {
	klog.Infof("Rendering files into: %s (ownerRefMode=%v)", outputDir, ownerRefMode)

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
		perfProfiles []*performancev2.PerformanceProfile
		mcPools      []*mcfgv1.MachineConfigPool
		mcConfigs    []*mcfgv1.MachineConfig
		infra        *apicfgv1.Infrastructure
		ctrcfgs      []*mcfgv1.ContainerRuntimeConfig
	)
	// Iterate through the file paths and read in desired files
	for _, path := range filePaths {
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("error opening %s: %w", file.Name(), err)
		}
		defer file.Close()

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

		if ownerRefMode == ownerRefModeK8S {
			err = addOwnerReference(components, pp)
			if err != nil {
				return err
			}
		} else if ownerRefMode == ownerRefModeLabelName {
			err = addWeakOwnerReferenceLabel(components, pp)
			if err != nil {
				return err
			}
		}

		for kind, manifest := range components.ToManifestTable() {
			err = writeObject(outputDir, fmt.Sprintf("%s_%s.yaml", pp.Name, strings.ToLower(kind)), manifest)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func writeObject(outputDir, fileName string, manifest interface{}) error {
	fullFilePath := filepath.Join(outputDir, fileName)
	klog.Infof("Writing file: %s -> %s", fileName, fullFilePath)

	b, err := yaml.Marshal(manifest)
	if err != nil {
		return err
	}

	return os.WriteFile(fullFilePath, b, 0644)
}

func addOwnerReference(components *manifestset.ManifestResultSet, pp *performancev2.PerformanceProfile) error {
	if pp == nil || pp.UID == types.UID("") {
		return fmt.Errorf("Missing UID from performance profile")
	}

	or := []v1.OwnerReference{
		{
			Kind:       pp.Kind,
			Name:       pp.Name,
			APIVersion: pp.APIVersion,
			UID:        pp.UID,
		},
	}

	for _, componentObj := range components.ToObjects() {
		componentObj.SetOwnerReferences(or)
	}

	return nil
}

func addWeakOwnerReferenceLabel(components *manifestset.ManifestResultSet, pp *performancev2.PerformanceProfile) error {
	lab := weakOwnerReferenceNameLabel // shortcut
	val := pp.Name                     // shortcut

	for _, componentObj := range components.ToObjects() {
		labels := componentObj.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels[lab] = val
		componentObj.SetLabels(labels)
	}

	return nil
}

func isValidOwnerRefMode(val string) bool {
	return val == ownerRefModeNone || val == ownerRefModeK8S || val == ownerRefModeLabelName
}

// isLegacySNOWorkloadPinningMethod provides a check for situations where the user is creating an SNO cluster with the
// legacy method for CPU Partitioning. In order to make sure the bootstrap and running cluster MCs are synced up we check the MCs
// provided by the user, if any one of them contain the file addition to `/etc/kubernetes/openshift-workload-pinning` and have not set
// the API CPUPartitioningAllNodes at install time, we assume a legacy intention and alter the flag to generate the new bootstrap manifests.
//
// Note:
//   - This will only trigger when Control plane is SNO or the CPU Partitioning API is NOT set to AllNodes
//   - We do not alter the API flag here, when NTO starts up in cluster, it will notice the flag and
//     update the flag and ignore the create error since the files already exist.
func isLegacySNOWorkloadPinningMethod(mcs []*mcfgv1.MachineConfig, infra *apicfgv1.Infrastructure, partitioningMode *apicfgv1.CPUPartitioningMode) bool {

	// If we can't determine SNO topology, we return.
	if infra == nil {
		return false
	}

	if infra.Status.ControlPlaneTopology != apicfgv1.SingleReplicaTopologyMode || (partitioningMode != nil && *partitioningMode == apicfgv1.CPUPartitioningAllNodes) {
		return false
	}

	// This file name is stable and currently hardcoded in kubelet
	// https://github.com/openshift/kubernetes/blob/ba1825544533d273d86b405195ee791e500b74c7/pkg/kubelet/managed/managed.go#L31
	const kubernetesPinningConfFile = "/etc/kubernetes/openshift-workload-pinning"

	for _, mc := range mcs {
		ign := &igntypes.Config{}
		err := json.Unmarshal(mc.Spec.Config.Raw, ign)
		if err != nil {
			klog.Errorf("skipping legacy check on mc (%s) unable to marshal raw config to ignition struct: %s", mc.Name, err)
			continue
		}

		for _, file := range ign.Storage.Files {
			if file.Node.Path == kubernetesPinningConfFile {
				klog.Infof("mc (%s) contains file path (%s), using legacy signal for workload pinning", mc.Name, kubernetesPinningConfFile)
				return true
			}
		}
	}

	return false
}

// genBootstrapWorkloadPinningManifests is used to generate the appropriate bootstrap workload pinning manifests
// based on the cluster CPU Partitioning Mode and the given MachineConfigPool names. The created manifests are the
// default state configs for the node which make no assumption for which CPU's are used for workload pinning.
//
// The generated manifests will not be owned by a PerformanceProfile and serve as the default state when a PerformanceProfile
// does not exist on a CPU partitioned cluster. The manifests will have a name of 01-<mcp role>-cpu-partitioning, meaning they will
// be in lower lexical order. This is done with the intention that when a PerformanceProfile is created those values will take higher
// priority and override the values in this file. This file is intended to always be present in a CPU Partitioned cluster.
// Since we currently do not support a user reverting a CPU partitioned cluster to a regular cluster, in the event that a
// PerformanceProfile does not exist, these manifests will be the fallback.
func genBootstrapWorkloadPinningManifests(partitioningMode *apicfgv1.CPUPartitioningMode, outputDir string, mcpNames ...string) error {
	if partitioningMode == nil || *partitioningMode != apicfgv1.CPUPartitioningAllNodes {
		return nil
	}

	for _, name := range mcpNames {
		mc, err := machineconfig.BootstrapWorkloadPinningMC(name, partitioningMode)
		if err != nil {
			return err
		}

		b, err := yaml.Marshal(mc)
		if err != nil {
			return err
		}

		fileName := fmt.Sprintf("01_%s_workload_pinning_%s.yaml", mc.Name, strings.ToLower(mc.Kind))
		err = os.WriteFile(filepath.Join(outputDir, fileName), b, 0644)
		if err != nil {
			return err
		}
		klog.Info(fileName)
	}

	return nil
}

func selectMachineConfigPool(pools []*mcfgv1.MachineConfigPool, selectors map[string]string) (*mcfgv1.MachineConfigPool, error) {
	profileNodeSelector := labels.Set(selectors)
	var (
		mcp   *mcfgv1.MachineConfigPool
		count = 0
	)

	for _, pool := range pools {
		if pool.Spec.NodeSelector == nil {
			continue
		}

		mcpNodeSelector, err := v1.LabelSelectorAsSelector(pool.Spec.NodeSelector)
		if err != nil {
			return nil, err
		}

		if mcpNodeSelector.Matches(profileNodeSelector) {
			mcp = pool
			count += 1
		}
	}

	if count == 0 {
		return nil, fmt.Errorf("no MCP found that matches performance profile node selector %q", profileNodeSelector.String())
	}

	if count > 1 {
		return nil, fmt.Errorf("more than one MCP found that matches performance profile node selector %q", profileNodeSelector.String())
	}

	return mcp, nil
}

func getContainerRuntimeName(profile *performancev2.PerformanceProfile, mcp *mcfgv1.MachineConfigPool, ctrcfgs []*mcfgv1.ContainerRuntimeConfig) (mcfgv1.ContainerRuntimeDefaultRuntime, error) {
	mcpLabels := labels.Set(mcp.Labels)
	var matchingCtrConfigs []*mcfgv1.ContainerRuntimeConfig
	for _, ctrcfg := range ctrcfgs {
		ctrcfgSelector, err := v1.LabelSelectorAsSelector(ctrcfg.Spec.MachineConfigPoolSelector)
		if err != nil {
			return "", err
		}
		if ctrcfgSelector.Matches(mcpLabels) {
			matchingCtrConfigs = append(matchingCtrConfigs, ctrcfg)
		}
	}

	if len(matchingCtrConfigs) == 0 {
		klog.Infof("no ContainerRuntimeConfig found that matches MCP labels %s that associated with performance profile %q; using default container runtime", mcpLabels.String(), profile.Name)
		return mcfgv1.ContainerRuntimeDefaultRuntimeRunc, nil
	}

	if len(matchingCtrConfigs) > 1 {
		return "", fmt.Errorf("more than one ContainerRuntimeConfig found that matches MCP labels %s that associated with performance profile %q", mcpLabels.String(), profile.Name)
	}

	return matchingCtrConfigs[0].Spec.ContainerRuntimeConfig.DefaultRuntime, nil
}
