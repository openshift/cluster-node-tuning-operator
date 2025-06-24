/*
 * Copyright 2024 Red Hat, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package ctrreschk

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/openshift-kni/debug-tools/pkg/environ"
	"github.com/openshift-kni/debug-tools/pkg/machineinformer"
)

type K8SOptions struct{}

func NewK8SMachineInfoCommand(env *environ.Environ, opts *Options) *cobra.Command {
	machineInfoCmd := &cobra.Command{
		Use:   "machineinfo",
		Short: "show machineinfo as JSON",
		RunE: func(cmd *cobra.Command, args []string) error {
			handle := machineinformer.Handle{Out: os.Stdout}
			handle.Run()
			return MainLoop(opts)
		},
		Args: cobra.NoArgs,
	}
	return machineInfoCmd
}

func NewK8SCommand(env *environ.Environ, opts *Options) *cobra.Command {
	k8sCmd := &cobra.Command{
		Use:   "k8s",
		Short: "show properties in kubernetes format",
		RunE: func(cmd *cobra.Command, args []string) error {
			return ShowHelp(cmd, args)
		},
		Args: cobra.NoArgs,
	}
	k8sCmd.AddCommand(NewK8SMachineInfoCommand(env, opts))
	return k8sCmd
}
