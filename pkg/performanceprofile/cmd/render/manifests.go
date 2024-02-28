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
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
)

type manifest struct {
	Raw []byte
}

// UnmarshalJSON unmarshals bytes of single kubernetes object to manifest.
func (m *manifest) UnmarshalJSON(in []byte) error {
	if m == nil {
		return errors.New("manifest: UnmarshalJSON on nil pointer")
	}

	// This happens when marshalling
	// <yaml>
	// ---	(this between two `---`)
	// ---
	// <yaml>
	if bytes.Equal(in, []byte("null")) {
		m.Raw = nil
		return nil
	}

	m.Raw = append(m.Raw[0:0], in...)
	return nil
}

// parseManifests parses a YAML or JSON document that may contain one or more
// kubernetes resources.
func parseManifests(filename string, r io.Reader) ([]manifest, error) {
	d := yamlutil.NewYAMLOrJSONDecoder(r, 1024)
	var manifests []manifest
	for {
		m := manifest{}
		if err := d.Decode(&m); err != nil {
			if err == io.EOF {
				return manifests, nil
			}
			return manifests, fmt.Errorf("error parsing %q: %w", filename, err)
		}
		m.Raw = bytes.TrimSpace(m.Raw)
		if len(m.Raw) == 0 || bytes.Equal(m.Raw, []byte("null")) {
			continue
		}
		manifests = append(manifests, m)
	}
}

func listFiles(dirPaths string) ([]string, error) {
	dirs := strings.Split(dirPaths, ",")
	results := []string{}
	for _, dir := range dirs {
		err := filepath.WalkDir(dir,
			func(path string, info os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if info.IsDir() {
					return nil
				}
				results = append(results, path)
				return nil
			})
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}

// AppendMissingDefaultMCPManifests When default MCPs are missing, it is desirable to still generate the relevant
// files based off of the standard MCP labels and node selectors.
//
// Here we create the default `master` and `worker` MCP if they are missing with their respective base Labels and NodeSelector Labels,
// this allows any resource such as PAO to utilize the default during bootstrap rendering.
func AppendMissingDefaultMCPManifests(currentMCPs []*mcfgv1.MachineConfigPool) []*mcfgv1.MachineConfigPool {
	const (
		master             = "master"
		worker             = "worker"
		labelPrefix        = "pools.operator.machineconfiguration.openshift.io/"
		masterLabels       = labelPrefix + master
		workerLabels       = labelPrefix + worker
		masterNodeSelector = components.NodeRoleLabelPrefix + master
		workerNodeSelector = components.NodeRoleLabelPrefix + worker
	)
	var (
		finalMCPList = []*mcfgv1.MachineConfigPool{}
		defaultMCPs  = []*mcfgv1.MachineConfigPool{
			{
				ObjectMeta: v1.ObjectMeta{
					Labels: map[string]string{
						masterLabels: "",
					},
					Name: master,
				},
				Spec: mcfgv1.MachineConfigPoolSpec{
					NodeSelector: v1.AddLabelToSelector(&v1.LabelSelector{}, masterNodeSelector, ""),
				},
			},
			{
				ObjectMeta: v1.ObjectMeta{
					Labels: map[string]string{
						workerLabels: "",
					},
					Name: worker,
				},
				Spec: mcfgv1.MachineConfigPoolSpec{
					NodeSelector: v1.AddLabelToSelector(&v1.LabelSelector{}, workerNodeSelector, ""),
				},
			},
		}
	)

	if len(currentMCPs) == 0 {
		return defaultMCPs
	}

	for _, defaultMCP := range defaultMCPs {
		missing := true
		for _, mcp := range currentMCPs {
			// Since users can supply MCP files and these will be raw files with out going through the API
			// server validation, we normalize the file name so as to not mask any configuration errors.
			if strings.ToLower(mcp.Name) == defaultMCP.Name {
				missing = false
				break
			}
		}
		if missing {
			finalMCPList = append(finalMCPList, defaultMCP)
		}
	}

	return append(finalMCPList, currentMCPs...)
}
