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
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/ghodss/yaml"
	performancev2 "github.com/openshift-kni/performance-addon-operators/api/v2"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components/manifestset"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
)

type renderOpts struct {
	performanceProfileInputFiles performanceProfileFiles
	assetsInDir                  string
	assetsOutDir                 string
}

type performanceProfileFiles []string

func (ppf *performanceProfileFiles) String() string {
	return fmt.Sprint(*ppf)
}

func (ppf *performanceProfileFiles) Type() string {
	return "performanceProfileFiles"
}

// Set parse performance-profile-input-files flag and store it in ppf
func (ppf *performanceProfileFiles) Set(value string) error {
	if len(*ppf) > 0 {
		return errors.New("performance-profile-input-files flag already set")
	}

	for _, s := range strings.Split(value, ",") {
		*ppf = append(*ppf, s)
	}
	return nil
}

//NewRenderCommand creates a render command.
func NewRenderCommand() *cobra.Command {
	renderOpts := renderOpts{}

	cmd := &cobra.Command{
		Use:   "render",
		Short: "Render performance-addon-operator manifests",
		Run: func(cmd *cobra.Command, args []string) {

			if err := renderOpts.Validate(); err != nil {
				klog.Fatal(err)
			}

			if err := renderOpts.Run(); err != nil {
				klog.Fatal(err)
			}
		},
	}

	renderOpts.AddFlags(cmd.Flags())

	return cmd
}

func (r *renderOpts) AddFlags(fs *pflag.FlagSet) {
	fs.Var(&r.performanceProfileInputFiles, "performance-profile-input-files", "A comma-separated list of performance-profile manifests.")
	fs.StringVar(&r.assetsInDir, "asset-input-dir", components.AssetsDir, "Input path for the assets directory.")
	fs.StringVar(&r.assetsOutDir, "asset-output-dir", r.assetsOutDir, "Output path for the rendered manifests.")
	// environment variables has precedence over standard input
	r.readFlagsFromEnv()
}

func (r *renderOpts) readFlagsFromEnv() {
	if ppInFiles := os.Getenv("PERFORMANCE_PROFILE_INPUT_FILES"); len(ppInFiles) > 0 {
		r.performanceProfileInputFiles.Set(ppInFiles)
	}

	if assetInDir := os.Getenv("ASSET_INPUT_DIR"); len(assetInDir) > 0 {
		r.assetsInDir = assetInDir
	}

	if assetsOutDir := os.Getenv("ASSET_OUTPUT_DIR"); len(assetsOutDir) > 0 {
		r.assetsOutDir = assetsOutDir
	}
}

func (r *renderOpts) Validate() error {
	if len(r.performanceProfileInputFiles) == 0 {
		return fmt.Errorf("performance-profile-input-files must be specified")
	}

	if len(r.assetsOutDir) == 0 {
		return fmt.Errorf("asset-output-dir must be specified")
	}

	return nil
}

func (r *renderOpts) Run() error {
	for _, pp := range r.performanceProfileInputFiles {
		b, err := ioutil.ReadFile(pp)
		if err != nil {
			return err
		}

		profile := &performancev2.PerformanceProfile{}
		err = yaml.Unmarshal(b, profile)
		if err != nil {
			return err
		}

		components, err := manifestset.GetNewComponents(profile, nil)
		if err != nil {
			return err
		}
		or := []v1.OwnerReference{
			{
				Kind: profile.Kind,
				Name: profile.Name,
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

			fileName := fmt.Sprintf("%s_%s.yaml", profile.Name, strings.ToLower(kind))
			err = ioutil.WriteFile(filepath.Join(r.assetsOutDir, fileName), b, 0644)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
