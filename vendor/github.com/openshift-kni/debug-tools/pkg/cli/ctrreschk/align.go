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
	"encoding/json"
	"os"

	"github.com/spf13/cobra"

	"github.com/openshift-kni/debug-tools/pkg/align"
	"github.com/openshift-kni/debug-tools/pkg/environ"
	"github.com/openshift-kni/debug-tools/pkg/machine"
	"github.com/openshift-kni/debug-tools/pkg/resources"
)

type AlignOptions struct{}

func NewAlignCommand(env *environ.Environ, opts *Options) *cobra.Command {
	alignCmd := &cobra.Command{
		Use:   "align",
		Short: "show resource alignment properties",
		RunE: func(cmd *cobra.Command, args []string) error {
			container, err := resources.Discover(env)
			if err != nil {
				return err
			}
			machine, err := machine.Discover(env)
			if err != nil {
				return err
			}
			result, err := align.Check(env, container, machine)
			if err != nil {
				return err
			}
			err = json.NewEncoder(os.Stdout).Encode(result)
			if err != nil {
				return err
			}
			return MainLoop(opts)
		},
		Args: cobra.NoArgs,
	}

	alignCmd.PersistentFlags().StringVarP(&env.DataPath, "machinedata", "M", "", "read fake machine data from path, don't read real data from the system")

	return alignCmd
}
