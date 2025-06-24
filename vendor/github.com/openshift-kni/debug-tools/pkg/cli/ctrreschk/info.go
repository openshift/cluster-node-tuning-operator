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
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/jaypipes/ghw/pkg/memory"
	"github.com/jaypipes/ghw/pkg/topology"
	"github.com/spf13/cobra"

	"github.com/openshift-kni/debug-tools/pkg/environ"
	"github.com/openshift-kni/debug-tools/pkg/machine"
)

type InfoOptions struct{}

func cacheProcessorsToString(cache memory.Cache) string {
	if len(cache.LogicalProcessors) == 0 {
		return ""
	}
	if len(cache.LogicalProcessors) == 1 {
		return strconv.Itoa(int(cache.LogicalProcessors[0]))
	}
	cpuids := append([]uint32{}, cache.LogicalProcessors...)
	slices.Sort(cpuids)
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d", cpuids[0])
	for _, cpuid := range cpuids[1:] {
		fmt.Fprintf(&sb, ",%d", cpuid)
	}
	return sb.String()
}

func NewInfoCacheCommand(env *environ.Environ, opts *Options) *cobra.Command {
	infoCacheCmd := &cobra.Command{
		Use:   "cache",
		Short: "show machine cache properties",
		RunE: func(cmd *cobra.Command, args []string) error {
			machine, err := machine.Discover(env)
			if err != nil {
				return err
			}

			memoryCacheType := map[memory.CacheType]string{
				memory.CACHE_TYPE_INSTRUCTION: "i",
				memory.CACHE_TYPE_DATA:        "d",
			}

			for _, node := range machine.Topology.Nodes {
				fmt.Printf("Node #%-2d:\n", node.ID)
				for _, cache := range node.Caches {
					fmt.Printf("  Cache L%d%1s %6d KiB: CPUs: %s\n", cache.Level, memoryCacheType[cache.Type], cache.SizeBytes/1024, cacheProcessorsToString(*cache))
				}
			}
			return MainLoop(opts)
		},
		Args: cobra.NoArgs,
	}
	return infoCacheCmd
}

func NewInfoCommand(env *environ.Environ, opts *Options) *cobra.Command {
	infoCmd := &cobra.Command{
		Use:   "info",
		Short: "show machine properties",
		RunE: func(cmd *cobra.Command, args []string) error {
			machine, err := machine.Discover(env)
			if err != nil {
				return err
			}
			// fixup ghw quirks
			machine.Topology.Architecture = topology.ARCHITECTURE_NUMA
			data, err := machine.ToJSON()
			if err != nil {
				return err
			}
			fmt.Printf("%s", data)
			return MainLoop(opts)
		},
		Args: cobra.NoArgs,
	}

	infoCmd.PersistentFlags().StringVarP(&env.DataPath, "machinedata", "M", "", "read fake machine data from path, don't read real data from the system")

	infoCmd.AddCommand(NewInfoCacheCommand(env, opts))
	return infoCmd
}
