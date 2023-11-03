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
	"flag"
	"fmt"

	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"k8s.io/klog"
)

type renderOpts struct {
	assetsInDir  []string
	assetsOutDir string
}

func NewRenderBootCmdMCCommand() *cobra.Command {
	renderOpts := renderOpts{}

	cmd := &cobra.Command{
		Use:   "render-bootcmd-mc",
		Short: "Render MC with kernel args",
		Run: func(cmd *cobra.Command, args []string) {
			if err := renderOpts.Validate(); err != nil {
				klog.Fatal(err)
			}

			if err := renderOpts.Run(); err != nil {
				klog.Fatal(err)
			}
		},
	}

	addKlogFlags(cmd)
	renderOpts.AddFlags(cmd.Flags())
	return cmd
}

func (r *renderOpts) AddFlags(fs *pflag.FlagSet) {
	fs.StringArrayVar(&r.assetsInDir, "asset-input-dir", []string{components.AssetsDir}, "Input path for the assets directory. (Can use it more than one to define multiple directories)")
	fs.StringVar(&r.assetsOutDir, "asset-output-dir", r.assetsOutDir, "Output path for the rendered manifests.")
}

func (r *renderOpts) Validate() error {
	var err string
	if len(r.assetsInDir) == 0 {
		err += "asset-input-dir must be specified. "
	}
	if len(r.assetsOutDir) == 0 {
		err += "asset-output-dir must be specified. "
	}

	if len(err) == 0 {
		return nil
	}
	return fmt.Errorf(err)
}

func (r *renderOpts) Run() error {
	return render(r.assetsInDir, r.assetsOutDir)
}

func addKlogFlags(cmd *cobra.Command) {
	fs := flag.NewFlagSet("", flag.PanicOnError)
	klog.InitFlags(fs)
	cmd.Flags().AddGoFlagSet(fs)
}
