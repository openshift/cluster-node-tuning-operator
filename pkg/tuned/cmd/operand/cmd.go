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

package operand

import (
	"flag"

	"github.com/openshift/cluster-node-tuning-operator/pkg/signals"
	"github.com/openshift/cluster-node-tuning-operator/pkg/tuned"
	"github.com/openshift/cluster-node-tuning-operator/version"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"k8s.io/klog/v2"
)

type tunedOpts struct {
	inCluster bool
}

func NewTunedCommand() *cobra.Command {
	tunedOpts := tunedOpts{}

	cmd := &cobra.Command{
		Use:   version.OperandFilename,
		Short: "Start NTO operand",
		Run: func(cmd *cobra.Command, args []string) {
			if err := tunedOpts.Validate(); err != nil {
				klog.Fatal(err)
			}

			if err := tunedOpts.Run(); err != nil {
				klog.Fatal(err)
			}
		},
	}

	addKlogFlags(cmd)
	tunedOpts.AddFlags(cmd.Flags())
	return cmd
}

func (t *tunedOpts) AddFlags(fs *pflag.FlagSet) {
	fs.BoolVar(&t.inCluster, "in-cluster", true, "In-cluster operand run.")
}

func addKlogFlags(cmd *cobra.Command) {
	fs := flag.NewFlagSet("", flag.PanicOnError)
	klog.InitFlags(fs)
	cmd.Flags().AddGoFlagSet(fs)
}

func (t *tunedOpts) Validate() error {
	return nil
}

func (t *tunedOpts) Run() error {
	return tunedOperandRun(t.inCluster)
}

func tunedOperandRun(inCluster bool) error {
	stopCh := signals.SetupSignalHandler()
	return tuned.RunOperand(stopCh, version.Version, inCluster)
}
