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

	"sigs.k8s.io/yaml"

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
	"k8s.io/apimachinery/pkg/util/uuid"

	"k8s.io/klog"
)

const (
	clusterConfigResourceName = "cluster"
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
func render(inputDir, outputDir string) error {

	klog.Info("Rendering files into: ", outputDir)

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

		// Decode manifest files and
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

	var pinnedMode *apicfgv1.CPUPartitioningMode
	if infra != nil {
		pinnedMode = &infra.Status.CPUPartitioning
	}

	if err := genBootstrapWorkloadPinningManifests(pinnedMode, outputDir, defaultMCPNames...); err != nil {
		return err
	}

	// If the user supplies extra machine pools, we ingest them here
	for _, pool := range mcPools {
		if err := genBootstrapWorkloadPinningManifests(pinnedMode, outputDir, pool.Name); err != nil {
			return err
		}
	}

	for _, pp := range perfProfiles {
		mcp, err := selectMachineConfigPool(mcPools, pp.Spec.NodeSelector)
		if err != nil {
			return err
		}

		defaultRuntime, err := getContainerRuntimeName(pp, mcp, ctrcfgs)
		if err != nil {
			return fmt.Errorf("render: could not determine high-performance runtime class container-runtime for profile %q; %w", pp.Name, err)
		}

		components, err := manifestset.GetNewComponents(pp, mcp, pinnedMode, defaultRuntime)
		if err != nil {
			return err
		}

		uid := pp.UID
		if uid == types.UID("") {
			uid = uuid.NewUUID()
		}

		or := []v1.OwnerReference{
			{
				Kind:       pp.Kind,
				Name:       pp.Name,
				APIVersion: pp.APIVersion,
				UID:        uid,
			},
		}

		for _, componentObj := range components.ToObjects() {
			componentObj.SetOwnerReferences(or)
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
	for _, ctrcfg := range ctrcfgs {
		ctrcfgSelector, err := v1.LabelSelectorAsSelector(ctrcfg.Spec.MachineConfigPoolSelector)
		if err != nil {
			return "", err
		}
		if ctrcfgSelector.Matches(mcpLabels) {
			ctrcfgs = append(ctrcfgs, ctrcfg)
		}
	}

	if len(ctrcfgs) == 0 {
		klog.Infof("no ContainerRuntimeConfig found that matches MCP labels %s that associated with performance profile %q; using default container runtime", mcpLabels.String(), profile.Name)
		return mcfgv1.ContainerRuntimeDefaultRuntimeRunc, nil
	}

	if len(ctrcfgs) > 1 {
		return "", fmt.Errorf("more than one ContainerRuntimeConfig found that matches MCP labels %s that associated with performance profile %q", mcpLabels.String(), profile.Name)
	}

	return ctrcfgs[0].Spec.ContainerRuntimeConfig.DefaultRuntime, nil
}
