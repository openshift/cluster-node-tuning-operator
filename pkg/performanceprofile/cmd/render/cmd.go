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

	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"k8s.io/klog"
)

type renderOpts struct {
	assetsInDir  string
	assetsOutDir string
}

// NewRenderCommand creates a render command.
// The render command will read in the asset directory and walk the paths to ingest relevant data.
// It will generate the machine configs based off of the supplied PerformanceProfiles and any manifest
// needed to generate the machine configs.
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
	fs.StringVar(&r.assetsInDir, "asset-input-dir", components.AssetsDir, "Input path for the assets directory. (Can be a comma separated list of directories.)")
	fs.StringVar(&r.assetsOutDir, "asset-output-dir", r.assetsOutDir, "Output path for the rendered manifests.")
	// environment variables has precedence over standard input
	r.readFlagsFromEnv()
}

func (r *renderOpts) readFlagsFromEnv() {
	if assetInDir := os.Getenv("ASSET_INPUT_DIR"); len(assetInDir) > 0 {
		r.assetsInDir = assetInDir
	}

	if assetsOutDir := os.Getenv("ASSET_OUTPUT_DIR"); len(assetsOutDir) > 0 {
		r.assetsOutDir = assetsOutDir
	}
}

func (r *renderOpts) Validate() error {
	if len(r.assetsOutDir) == 0 {
		return fmt.Errorf("asset-output-dir must be specified")
	}

	return nil
}

func (r *renderOpts) Run() error {
	return render(r.assetsInDir, r.assetsOutDir)
}
