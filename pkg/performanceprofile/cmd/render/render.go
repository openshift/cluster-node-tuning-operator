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

	"github.com/ghodss/yaml"

	apicfgv1 "github.com/openshift/api/config/v1"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
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

var (
	manifestScheme = runtime.NewScheme()
	codecFactory   serializer.CodecFactory
	runtimeDecoder runtime.Decoder
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
		pools        []*mcfgv1.MachineConfigPool
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
				pools = append(pools, obj)
			default:
				klog.Infof("skipping %q [%d] manifest because of unhandled %T", file.Name(), idx+1, obji)
			}
		}
	}

	if len(perfProfiles) == 0 {
		klog.Warning("zero performance profiles were found")
	}
	for _, pp := range perfProfiles {
		mcp, err := selectMachineConfigPool(pools, pp.Spec.NodeSelector)
		if err != nil {
			return err
		}

		components, err := manifestset.GetNewComponents(pp, mcp)
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
		}
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

	if count > 1 {
		return nil, fmt.Errorf("more than one MCP found that matches performance profile node selector %q", profileNodeSelector.String())
	}

	return mcp, nil
}
