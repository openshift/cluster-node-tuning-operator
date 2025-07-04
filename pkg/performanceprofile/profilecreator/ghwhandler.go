/*
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
 *
 * Copyright 2021 Red Hat, Inc.
 */

package profilecreator

import (
	"fmt"
	"os"
	"path"
	"sort"

	"github.com/jaypipes/ghw"
	"github.com/jaypipes/ghw/pkg/cpu"
	"github.com/jaypipes/ghw/pkg/option"
	"github.com/jaypipes/ghw/pkg/topology"

	v1 "k8s.io/api/core/v1"
)

// NewGHWHandler is a handler to use ghw options corresponding to a node
func NewGHWHandler(mustGatherDirPath string, node *v1.Node) (*GHWHandler, error) {
	nodeName := node.GetName()
	nodePathSuffix := path.Join(Nodes)
	nodepath, err := getMustGatherFullPathsWithFilter(mustGatherDirPath, nodePathSuffix, ClusterScopedResources)
	if err != nil {
		return nil, fmt.Errorf("can't obtain the node path %s: %v", nodeName, err)
	}
	_, err = os.Stat(path.Join(nodepath, nodeName, SysInfoFileName))
	if err != nil {
		return nil, fmt.Errorf("can't obtain the path: %s for node %s: %v", nodeName, nodepath, err)
	}
	options := ghw.WithSnapshot(ghw.SnapshotOptions{
		Path: path.Join(nodepath, nodeName, SysInfoFileName),
	})
	ghwHandler := &GHWHandler{snapShotOptions: options, Node: node}
	return ghwHandler, nil
}

// GHWHandler is a wrapper around ghw to get the API object
type GHWHandler struct {
	snapShotOptions *option.Option
	Node            *v1.Node
}

// CPU returns a CPUInfo struct that contains information about the CPUs on the host system
func (ghwHandler GHWHandler) CPU() (*cpu.Info, error) {
	return ghw.CPU(ghwHandler.snapShotOptions)
}

func (ghwHandler GHWHandler) SortedCPU() (*cpu.Info, error) {
	cpuInfo, err := ghw.CPU(ghwHandler.snapShotOptions)
	if err != nil {
		return nil, fmt.Errorf("can't obtain cpuInfo info from GHW snapshot: %v", err)
	}

	sort.Slice(cpuInfo.Processors, func(x, y int) bool {
		return cpuInfo.Processors[x].ID < cpuInfo.Processors[y].ID
	})

	for _, processor := range cpuInfo.Processors {
		for _, core := range processor.Cores {
			sort.Slice(core.LogicalProcessors, func(x, y int) bool {
				return core.LogicalProcessors[x] < core.LogicalProcessors[y]
			})
		}

		sort.Slice(processor.Cores, func(x, y int) bool {
			return processor.Cores[x].ID < processor.Cores[y].ID
		})
	}

	return cpuInfo, nil
}

// SortedTopology returns a TopologyInfo struct that contains information about the Topology sorted by numa ids and cpu ids on the host system
func (ghwHandler GHWHandler) SortedTopology() (*topology.Info, error) {
	topologyInfo, err := ghw.Topology(ghwHandler.snapShotOptions)
	if err != nil {
		return nil, fmt.Errorf("can't obtain topology info from GHW snapshot: %v", err)
	}
	sortTopology(topologyInfo)
	return topologyInfo, nil
}

func sortTopology(topologyInfo *topology.Info) {
	sort.Slice(topologyInfo.Nodes, func(x, y int) bool {
		return topologyInfo.Nodes[x].ID < topologyInfo.Nodes[y].ID
	})
	for _, node := range topologyInfo.Nodes {
		for _, core := range node.Cores {
			sort.Slice(core.LogicalProcessors, func(x, y int) bool {
				return core.LogicalProcessors[x] < core.LogicalProcessors[y]
			})
		}
		sort.Slice(node.Cores, func(i, j int) bool {
			return node.Cores[i].LogicalProcessors[0] < node.Cores[j].LogicalProcessors[0]
		})
	}
}

func (ghwHandler GHWHandler) GatherSystemInfo() (*systemInfo, error) {
	cpuInfo, err := ghwHandler.SortedCPU()
	if err != nil {
		return nil, err
	}

	topologyInfo, err := ghwHandler.SortedTopology()
	if err != nil {
		return nil, err
	}

	htEnabled, err := ghwHandler.IsHyperthreadingEnabled()
	if err != nil {
		return nil, err
	}

	return &systemInfo{
		CpuInfo: &extendedCPUInfo{
			CpuInfo:                  cpuInfo,
			NumLogicalProcessorsUsed: make(map[int]int, len(cpuInfo.Processors)),
			LogicalProcessorsUsed:    make(map[int]struct{}),
		},
		TopologyInfo: topologyInfo,
		HtEnabled:    htEnabled,
	}, nil
}
