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
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/jaypipes/ghw"
	"github.com/jaypipes/ghw/pkg/cpu"
	"github.com/jaypipes/ghw/pkg/option"
	"github.com/jaypipes/ghw/pkg/topology"
	log "github.com/sirupsen/logrus"

	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/utils/cpuset"

	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	v1 "k8s.io/api/core/v1"
)

const (
	// ClusterScopedResources defines the subpath, relative to the top-level must-gather directory.
	// A top-level must-gather directory is of the following format:
	// must-gather-dir/quay-io-openshift-kni-performance-addon-operator-must-gather-sha256-<Image SHA>
	// Here we find the cluster-scoped definitions saved by must-gather
	ClusterScopedResources = "cluster-scoped-resources"
	// CoreNodes defines the subpath, relative to ClusterScopedResources, on which we find node-specific data
	CoreNodes = "core/nodes"
	// MCPools defines the subpath, relative to ClusterScopedResources, on which we find the machine config pool definitions
	MCPools = "machineconfiguration.openshift.io/machineconfigpools"
	// YAMLSuffix is the extension of the yaml files saved by must-gather
	YAMLSuffix = ".yaml"
	// Nodes defines the subpath, relative to top-level must-gather directory, on which we find node-specific data
	Nodes = "nodes"
	// SysInfoFileName defines the name of the file where ghw snapshot is stored
	SysInfoFileName = "sysinfo.tgz"
	// noSMTKernelArg is the kernel arg value to disable SMT in a system
	noSMTKernelArg = "nosmt"
	// allCores correspond to the value when all the processorCores need to be added to the generated CPUset
	allCores = -1
)

var (
	// This filter is used to avoid offlining the first logical processor of each core.
	// LogicalProcessors is a slice of integers representing the logical processor IDs assigned to
	// a processing unit for a core. GHW API guarantees that the logicalProcessors correspond
	// to hyperthread pairs and in the code below we select only the first hyperthread (id=0)
	// of the available logical processors.
	// Please refer to https://www.kernel.org/doc/Documentation/x86/topology.txt for more information on
	// x86 hardware topology. This document clarifies the main aspects of x86 topology modeling and
	// representation in the linux kernel and explains why we select id=0 for obtaining the first
	// hyperthread (logical core).
	filterFirstLogicalProcessorInCore = func(index, lpID int) bool { return index != 0 }
)

func getMustGatherFullPathsWithFilter(mustGatherPath string, suffix string, filter string) (string, error) {
	var paths []string

	// don't assume directory names, only look for the suffix, filter out files having "filter" in their names
	err := filepath.Walk(mustGatherPath, func(path string, info os.FileInfo, err error) error {
		if strings.HasSuffix(path, suffix) {
			if len(filter) == 0 || !strings.Contains(path, filter) {
				paths = append(paths, path)
			}
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to get the path mustGatherPath:%s, suffix:%s %v", mustGatherPath, suffix, err)
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("no match for the specified must gather directory path: %s and suffix: %s", mustGatherPath, suffix)
	}
	if len(paths) > 1 {
		log.Infof("Multiple matches for the specified must gather directory path: %s and suffix: %s", mustGatherPath, suffix)
		return "", fmt.Errorf("Multiple matches for the specified must gather directory path: %s and suffix: %s.\n Expected only one performance-addon-operator-must-gather* directory, please check the must-gather tarball", mustGatherPath, suffix)
	}
	// returning one possible path
	return paths[0], err
}

func getMustGatherFullPaths(mustGatherPath string, suffix string) (string, error) {
	return getMustGatherFullPathsWithFilter(mustGatherPath, suffix, "")
}

func getNode(mustGatherDirPath, nodeName string) (*v1.Node, error) {
	var node v1.Node
	nodePathSuffix := path.Join(ClusterScopedResources, CoreNodes, nodeName)
	path, err := getMustGatherFullPaths(mustGatherDirPath, nodePathSuffix)
	if err != nil {
		return nil, fmt.Errorf("failed to get MachineConfigPool for %s: %v", nodeName, err)
	}

	src, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open %q: %v", path, err)
	}
	defer src.Close()

	dec := k8syaml.NewYAMLOrJSONDecoder(src, 1024)
	if err := dec.Decode(&node); err != nil {
		return nil, fmt.Errorf("failed to decode %q: %v", path, err)
	}
	return &node, nil
}

// GetNodeList returns the list of nodes using the Node YAMLs stored in Must Gather
func GetNodeList(mustGatherDirPath string) ([]*v1.Node, error) {
	machines := make([]*v1.Node, 0)

	nodePathSuffix := path.Join(ClusterScopedResources, CoreNodes)
	nodePath, err := getMustGatherFullPaths(mustGatherDirPath, nodePathSuffix)
	if err != nil {
		return nil, fmt.Errorf("failed to get Nodes from must gather directory: %v", err)
	}
	if nodePath == "" {
		return nil, fmt.Errorf("failed to get Nodes from must gather directory: %v", err)
	}

	nodes, err := os.ReadDir(nodePath)
	if err != nil {
		return nil, fmt.Errorf("failed to list mustGatherPath directories: %v", err)
	}
	for _, node := range nodes {
		nodeName := node.Name()
		node, err := getNode(mustGatherDirPath, nodeName)
		if err != nil {
			return nil, fmt.Errorf("failed to get Nodes %s: %v", nodeName, err)
		}
		machines = append(machines, node)
	}
	return machines, nil
}

// GetMCPList returns the list of MCPs using the mcp YAMLs stored in Must Gather
func GetMCPList(mustGatherDirPath string) ([]*machineconfigv1.MachineConfigPool, error) {
	pools := make([]*machineconfigv1.MachineConfigPool, 0)

	mcpPathSuffix := path.Join(ClusterScopedResources, MCPools)
	mcpPath, err := getMustGatherFullPaths(mustGatherDirPath, mcpPathSuffix)
	if err != nil {
		return nil, fmt.Errorf("failed to get MCPs: %v", err)
	}
	if mcpPath == "" {
		return nil, fmt.Errorf("failed to get MCPs path: %v", err)
	}

	mcpFiles, err := os.ReadDir(mcpPath)
	if err != nil {
		return nil, fmt.Errorf("failed to list mustGatherPath directories: %v", err)
	}
	for _, mcp := range mcpFiles {
		mcpName := strings.TrimSuffix(mcp.Name(), filepath.Ext(mcp.Name()))

		mcp, err := GetMCP(mustGatherDirPath, mcpName)
		// master pool relevant only when pods can be scheduled on masters, e.g. SNO
		if mcpName != "master" && err != nil {
			return nil, fmt.Errorf("can't obtain MCP %s: %v", mcpName, err)
		}
		pools = append(pools, mcp)
	}
	return pools, nil
}

// GetMCP returns an MCP object corresponding to a specified MCP Name
func GetMCP(mustGatherDirPath, mcpName string) (*machineconfigv1.MachineConfigPool, error) {
	var mcp machineconfigv1.MachineConfigPool

	mcpPathSuffix := path.Join(ClusterScopedResources, MCPools, mcpName+YAMLSuffix)
	mcpPath, err := getMustGatherFullPaths(mustGatherDirPath, mcpPathSuffix)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain MachineConfigPool %s: %v", mcpName, err)
	}
	if mcpPath == "" {
		return nil, fmt.Errorf("failed to obtain MachineConfigPool, mcp:%s does not exist: %v", mcpName, err)
	}

	src, err := os.Open(mcpPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open %q: %v", mcpPath, err)
	}
	defer src.Close()
	dec := k8syaml.NewYAMLOrJSONDecoder(src, 1024)
	if err := dec.Decode(&mcp); err != nil {
		return nil, fmt.Errorf("failed to decode %q: %v", mcpPath, err)
	}
	return &mcp, nil
}

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
	return topologyInfo, nil
}

// topologyHTDisabled returns topologyinfo in case Hyperthreading needs to be disabled.
// It receives a pointer to Topology.Info and deletes logicalprocessors from individual cores.
// The behavior of this function depends on ghw data representation.
func topologyHTDisabled(info *topology.Info) *topology.Info {
	disabledHTTopology := &topology.Info{
		Architecture: info.Architecture,
	}
	newNodes := []*topology.Node{}
	for _, node := range info.Nodes {
		var newNode *topology.Node
		cores := []*cpu.ProcessorCore{}
		for _, processorCore := range node.Cores {
			newCore := cpu.ProcessorCore{ID: processorCore.ID,
				Index:      processorCore.Index,
				NumThreads: 1,
			}
			// LogicalProcessors is a slice of ints representing the logical processor IDs assigned to
			// a processing unit for a core. GHW API guarantees that the logicalProcessors correspond
			// to hyperthread pairs and in the code below we select only the first hyperthread (id=0)
			// of the available logical processors.
			for id, logicalProcessor := range processorCore.LogicalProcessors {
				// Please refer to https://www.kernel.org/doc/Documentation/x86/topology.txt for more information on
				// x86 hardware topology. This document clarifies the main aspects of x86 topology modeling and
				// representation in the linux kernel and explains why we select id=0 for obtaining the first
				// hyperthread (logical core).
				if id == 0 {
					newCore.LogicalProcessors = []int{logicalProcessor}
					cores = append(cores, &newCore)
				}
			}
			newNode = &topology.Node{Cores: cores,
				ID: node.ID,
			}
		}
		newNodes = append(newNodes, newNode)
		disabledHTTopology.Nodes = newNodes
	}
	return disabledHTTopology
}

type extendedCPUInfo struct {
	CpuInfo *cpu.Info
	// Number of logicalprocessors already reserved for each Processor (aka Socket)
	NumLogicalProcessorsUsed map[int]int
	LogicalProcessorsUsed    map[int]struct{}
}

type SystemInfo struct {
	CpuInfo      *extendedCPUInfo
	TopologyInfo *topology.Info
	HtEnabled    bool
}

func (ghwHandler GHWHandler) GatherSystemInfo() (*SystemInfo, error) {
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

	return &SystemInfo{
		CpuInfo: &extendedCPUInfo{
			CpuInfo:                  cpuInfo,
			NumLogicalProcessorsUsed: make(map[int]int, len(cpuInfo.Processors)),
			LogicalProcessorsUsed:    make(map[int]struct{}),
		},
		TopologyInfo: topologyInfo,
		HtEnabled:    htEnabled,
	}, nil
}

// Calculates the resevered, isolated and offlined cpuSets.
func CalculateCPUSets(systemInfo *SystemInfo, reservedCPUCount int, offlinedCPUCount int, splitReservedCPUsAcrossNUMA bool, disableHTFlag bool, highPowerConsumptionMode bool) (cpuset.CPUSet, cpuset.CPUSet, cpuset.CPUSet, error) {
	topologyInfo := systemInfo.TopologyInfo
	htEnabled := systemInfo.HtEnabled

	// Need to update Topology info to avoid using sibling Logical processors
	// if user want to "disable" them in the kernel
	updatedTopologyInfo, err := updateTopologyInfo(topologyInfo, disableHTFlag, systemInfo.HtEnabled)
	if err != nil {
		return cpuset.CPUSet{}, cpuset.CPUSet{}, cpuset.CPUSet{}, err
	}

	updatedExtCPUInfo, err := updateExtendedCPUInfo(systemInfo.CpuInfo, cpuset.CPUSet{}, disableHTFlag, htEnabled)
	if err != nil {
		return cpuset.CPUSet{}, cpuset.CPUSet{}, cpuset.CPUSet{}, err
	}

	cpuInfo := updatedExtCPUInfo.CpuInfo
	// Check limits are in range
	if reservedCPUCount <= 0 || reservedCPUCount >= int(cpuInfo.TotalThreads) {
		return cpuset.CPUSet{}, cpuset.CPUSet{}, cpuset.CPUSet{}, fmt.Errorf("please specify the reserved CPU count in the range [1,%d]", cpuInfo.TotalThreads-1)
	}

	if offlinedCPUCount < 0 || offlinedCPUCount >= int(cpuInfo.TotalThreads) {
		return cpuset.CPUSet{}, cpuset.CPUSet{}, cpuset.CPUSet{}, fmt.Errorf("please specify the offlined CPU count in the range [0,%d]", cpuInfo.TotalThreads-1)
	}

	if reservedCPUCount+offlinedCPUCount >= int(cpuInfo.TotalThreads) {
		return cpuset.CPUSet{}, cpuset.CPUSet{}, cpuset.CPUSet{}, fmt.Errorf("please ensure that reserved-cpu-count plus offlined-cpu-count should be in the range [0,%d]", cpuInfo.TotalThreads-1)
	}

	// Calculate reserved cpus.
	reserved, err := getReservedCPUs(updatedTopologyInfo, reservedCPUCount, splitReservedCPUsAcrossNUMA, disableHTFlag, htEnabled)
	if err != nil {
		return cpuset.CPUSet{}, cpuset.CPUSet{}, cpuset.CPUSet{}, err
	}

	updatedExtCPUInfo, err = updateExtendedCPUInfo(updatedExtCPUInfo, reserved, disableHTFlag, htEnabled)
	if err != nil {
		return cpuset.CPUSet{}, cpuset.CPUSet{}, cpuset.CPUSet{}, err
	}
	//Calculate offlined cpus
	// note this takes into account the reserved cpus from the step above
	offlined, err := getOfflinedCPUs(updatedExtCPUInfo, offlinedCPUCount, disableHTFlag, htEnabled, highPowerConsumptionMode)
	if err != nil {
		return cpuset.CPUSet{}, cpuset.CPUSet{}, cpuset.CPUSet{}, err
	}

	// Calculate isolated cpus.
	// Note that topology info could have been modified by "GetReservedCPUS" so
	// to properly calculate isolated CPUS we need to use the updated topology information.
	isolated, err := getIsolatedCPUs(updatedTopologyInfo.Nodes, reserved, offlined)
	if err != nil {
		return cpuset.CPUSet{}, cpuset.CPUSet{}, cpuset.CPUSet{}, err
	}

	return reserved, isolated, offlined, nil
}

// Calculates Isolated cpuSet as the difference between all the cpus in the topology and those already chosen as reserved or offlined.
// all cpus thar are not offlined or reserved belongs to the isolated cpuSet
func getIsolatedCPUs(topologyInfoNodes []*topology.Node, reserved, offlined cpuset.CPUSet) (cpuset.CPUSet, error) {
	total, err := totalCPUSetFromTopology(topologyInfoNodes)
	if err != nil {
		return cpuset.CPUSet{}, err
	}
	return total.Difference(reserved.Union(offlined)), nil
}

func AreAllLogicalProcessorsFromSocketUnused(extCpuInfo *extendedCPUInfo, socketId int) bool {
	if val, ok := extCpuInfo.NumLogicalProcessorsUsed[socketId]; ok {
		return val == 0
	} else {
		return true
	}
}

func getOfflinedCPUs(extCpuInfo *extendedCPUInfo, offlinedCPUCount int, disableHTFlag bool, htEnabled bool, highPowerConsumption bool) (cpuset.CPUSet, error) {
	offlined := newCPUAccumulator()
	lpOfflined := 0

	// unless we are in a high power consumption scenario
	// try to offline complete sockets first
	if !highPowerConsumption {
		cpuInfo := extCpuInfo.CpuInfo

		for _, processor := range cpuInfo.Processors {
			//can we offline a complete socket?
			if processor.NumThreads <= uint32(offlinedCPUCount-lpOfflined) && AreAllLogicalProcessorsFromSocketUnused(extCpuInfo, processor.ID) {
				acc, err := offlined.AddCores(offlinedCPUCount, processor.Cores)
				if err != nil {
					return cpuset.CPUSet{}, err
				}
				lpOfflined += acc
			}
		}
	}

	// if we still need to offline more cpus
	// try to offline sibling threads
	if lpOfflined < offlinedCPUCount {
		cpuInfo := extCpuInfo.CpuInfo

		for _, processor := range cpuInfo.Processors {
			acc, err := offlined.AddCoresWithFilter(offlinedCPUCount, processor.Cores, func(index, lpID int) bool {
				return filterFirstLogicalProcessorInCore(index, lpID) && !IsLogicalProcessorUsed(extCpuInfo, lpID)
			})
			if err != nil {
				return cpuset.CPUSet{}, err
			}

			lpOfflined += acc
		}
	}

	// if we still need to offline more cpus
	// just try to offline any cpu
	if lpOfflined < offlinedCPUCount {
		cpuInfo := extCpuInfo.CpuInfo

		for _, processor := range cpuInfo.Processors {
			acc, err := offlined.AddCoresWithFilter(offlinedCPUCount, processor.Cores, func(index, lpId int) bool {
				return !IsLogicalProcessorUsed(extCpuInfo, lpId)
			})
			if err != nil {
				return cpuset.CPUSet{}, err
			}

			lpOfflined += acc
		}
	}

	if lpOfflined < offlinedCPUCount {
		log.Warnf("could not offline enough logical processors (required:%d, offlined:%d)", offlinedCPUCount, lpOfflined)
	}
	return offlined.Result(), nil
}

func updateTopologyInfo(topoInfo *topology.Info, disableHTFlag bool, htEnabled bool) (*topology.Info, error) {
	//currently HT is enabled on the system and the user wants to disable HT

	if htEnabled && disableHTFlag {
		log.Infof("Updating Topology info because currently hyperthreading is enabled and the performance profile will disable it")
		return topologyHTDisabled(topoInfo), nil
	}
	return topoInfo, nil
}

func getReservedCPUs(topologyInfo *topology.Info, reservedCPUCount int, splitReservedCPUsAcrossNUMA bool, disableHTFlag bool, htEnabled bool) (cpuset.CPUSet, error) {
	if htEnabled && disableHTFlag {
		log.Infof("Currently hyperthreading is enabled and the performance profile will disable it")
		htEnabled = false
	}
	log.Infof("NUMA cell(s): %d", len(topologyInfo.Nodes))
	totalCPUs := 0
	for id, node := range topologyInfo.Nodes {
		coreList := []int{}
		for _, core := range node.Cores {
			coreList = append(coreList, core.LogicalProcessors...)
		}
		log.Infof("NUMA cell %d : %v", id, coreList)
		totalCPUs += len(coreList)
	}

	log.Infof("CPU(s): %d", totalCPUs)

	if splitReservedCPUsAcrossNUMA {
		res, err := getCPUsSplitAcrossNUMA(reservedCPUCount, htEnabled, topologyInfo.Nodes)
		return res, err
	}
	res, err := getCPUsSequentially(reservedCPUCount, htEnabled, topologyInfo.Nodes)
	return res, err
}

type cpuAccumulator struct {
	elems map[int]struct{}
	count int
	done  bool
}

func newCPUAccumulator() *cpuAccumulator {
	return &cpuAccumulator{
		elems: map[int]struct{}{},
		count: 0,
		done:  false,
	}
}

// AddCores adds logical cores from the slice of *cpu.ProcessorCore to a CPUset till the cpuset size is equal to the max value specified
// In case the max is specified as allCores, all the cores from the slice of *cpu.ProcessorCore are added to the CPUSet
func (ca *cpuAccumulator) AddCores(max int, cores []*cpu.ProcessorCore) (int, error) {
	allLogicalProcessors := func(int, int) bool { return true }
	return ca.AddCoresWithFilter(max, cores, allLogicalProcessors)
}

func (ca *cpuAccumulator) AddCoresWithFilter(max int, cores []*cpu.ProcessorCore, filterLogicalProcessor func(int, int) bool) (int, error) {
	if ca.done {
		return -1, fmt.Errorf("CPU accumulator finalized")
	}
	initialCount := ca.count
	for _, processorCore := range cores {
		for index, logicalProcessorId := range processorCore.LogicalProcessors {
			if ca.count < max || max == allCores {
				if filterLogicalProcessor(index, logicalProcessorId) {
					_, found := ca.elems[logicalProcessorId]
					ca.elems[logicalProcessorId] = struct{}{}
					if !found {
						ca.count++
					}
				}
			}
		}
	}
	return ca.count - initialCount, nil
}

func (ca *cpuAccumulator) Result() cpuset.CPUSet {
	ca.done = true

	var keys []int
	for k := range ca.elems {
		keys = append(keys, k)
	}

	return cpuset.New(keys...)
}

// getCPUsSplitAcrossNUMA returns Reserved and Isolated CPUs split across NUMA nodes
// We identify the right number of CPUs that need to be allocated per NUMA node, meaning reservedPerNuma + (the additional number based on the remainder and the NUMA node)
// E.g. If the user requests 15 reserved cpus and we have 4 numa nodes, we find reservedPerNuma in this case is 3 and remainder = 3.
// For each numa node we find a max which keeps track of the cumulative resources that should be allocated for each NUMA node:
// max = (numaID+1)*reservedPerNuma + (numaNodeNum - remainder)
// For NUMA node 0 max = (0+1)*3 + 4-3 = 4 remainder is decremented => remainder is 2
// For NUMA node 1 max = (1+1)*3 + 4-2 = 8 remainder is decremented => remainder is 1
// For NUMA node 2 max = (2+1)*3 + 4-2 = 12 remainder is decremented => remainder is 0
// For NUMA Node 3 remainder = 0 so max = 12 + 3 = 15.
func getCPUsSplitAcrossNUMA(reservedCPUCount int, htEnabled bool, topologyInfoNodes []*topology.Node) (cpuset.CPUSet, error) {
	reservedCPUs := newCPUAccumulator()

	numaNodeNum := len(topologyInfoNodes)

	max := 0
	reservedPerNuma := reservedCPUCount / numaNodeNum
	remainder := reservedCPUCount % numaNodeNum
	if remainder != 0 {
		log.Warnf("The reserved CPUs cannot be split equally across NUMA Nodes")
	}
	for numaID, node := range topologyInfoNodes {
		if remainder != 0 {
			max = (numaID+1)*reservedPerNuma + (numaNodeNum - remainder)
			remainder--
		} else {
			max = max + reservedPerNuma
		}
		if max%2 != 0 && htEnabled {
			return reservedCPUs.Result(), fmt.Errorf("can't allocate odd number of CPUs from a NUMA Node")
		}
		if _, err := reservedCPUs.AddCores(max, node.Cores); err != nil {
			return cpuset.CPUSet{}, err
		}
	}

	return reservedCPUs.Result(), nil
}

func getCPUsSequentially(reservedCPUCount int, htEnabled bool, topologyInfoNodes []*topology.Node) (cpuset.CPUSet, error) {
	reservedCPUs := newCPUAccumulator()

	if reservedCPUCount%2 != 0 && htEnabled {
		return reservedCPUs.Result(), fmt.Errorf("can't allocate odd number of CPUs from a NUMA Node")
	}
	for _, node := range topologyInfoNodes {
		if _, err := reservedCPUs.AddCores(reservedCPUCount, node.Cores); err != nil {
			return cpuset.CPUSet{}, err
		}
	}
	return reservedCPUs.Result(), nil
}

func totalCPUSetFromTopology(topologyInfoNodes []*topology.Node) (cpuset.CPUSet, error) {
	totalCPUs := newCPUAccumulator()
	for _, node := range topologyInfoNodes {
		//all the cores from node.Cores need to be added, hence allCores is specified as the max value
		if _, err := totalCPUs.AddCores(allCores, node.Cores); err != nil {
			return cpuset.CPUSet{}, err
		}
	}
	return totalCPUs.Result(), nil
}

// IsHyperthreadingEnabled checks if hyperthreading is enabled on the system or not
func (ghwHandler GHWHandler) IsHyperthreadingEnabled() (bool, error) {
	cpuInfo, err := ghwHandler.CPU()
	if err != nil {
		return false, fmt.Errorf("can't obtain CPU Info from GHW snapshot: %v", err)
	}
	// Since there is no way to disable flags per-processor (not system wide) we check the flags of the first available processor.
	// A following implementation will leverage the /sys/devices/system/cpu/smt/active file which is the "standard" way to query HT.
	return contains(cpuInfo.Processors[0].Capabilities, "ht"), nil
}

// contains checks if a string is present in a slice
func contains(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}
	return false
}

// EnsureNodesHaveTheSameHardware returns an error if all the input nodes do not have the same hardware configuration
func EnsureNodesHaveTheSameHardware(nodeHandlers []*GHWHandler) error {
	if len(nodeHandlers) < 1 {
		return fmt.Errorf("no suitable nodes to compare")
	}

	firstHandle := nodeHandlers[0]
	firstTopology, err := firstHandle.SortedTopology()
	if err != nil {
		return fmt.Errorf("can't obtain Topology info from GHW snapshot for %s: %v", firstHandle.Node.GetName(), err)
	}

	for _, handle := range nodeHandlers[1:] {
		if err != nil {
			return fmt.Errorf("can't obtain GHW snapshot handle for %s: %v", handle.Node.GetName(), err)
		}

		topology, err := handle.SortedTopology()
		if err != nil {
			return fmt.Errorf("can't obtain Topology info from GHW snapshot for %s: %v", handle.Node.GetName(), err)
		}
		err = ensureSameTopology(firstTopology, topology)
		if err != nil {
			return fmt.Errorf("nodes %s and %s have different topology: %v", firstHandle.Node.GetName(), handle.Node.GetName(), err)
		}
	}

	return nil
}

func ensureSameTopology(topology1, topology2 *topology.Info) error {
	if topology1.Architecture != topology2.Architecture {
		return fmt.Errorf("the architecture is different: %v vs %v", topology1.Architecture, topology2.Architecture)
	}

	if len(topology1.Nodes) != len(topology2.Nodes) {
		return fmt.Errorf("the number of NUMA nodes differ: %v vs %v", len(topology1.Nodes), len(topology2.Nodes))
	}

	for i, node1 := range topology1.Nodes {
		node2 := topology2.Nodes[i]
		if node1.ID != node2.ID {
			return fmt.Errorf("the NUMA node ids differ: %v vs %v", node1.ID, node2.ID)
		}

		cores1 := node1.Cores
		cores2 := node2.Cores
		if len(cores1) != len(cores2) {
			return fmt.Errorf("the number of CPU cores in NUMA node %d differ: %v vs %v",
				node1.ID, len(topology1.Nodes), len(topology2.Nodes))
		}

		for j, core1 := range cores1 {
			if !reflect.DeepEqual(core1, cores2[j]) {
				return fmt.Errorf("the CPU corres differ: %v vs %v", core1, cores2[j])
			}
		}
	}

	return nil
}

// GetAdditionalKernelArgs returns a set of kernel parameters based on configuration
func GetAdditionalKernelArgs(disableHT bool) []string {
	var kernelArgs []string
	if disableHT {
		kernelArgs = append(kernelArgs, noSMTKernelArg)
	}
	sort.Strings(kernelArgs)
	log.Infof("Additional Kernel Args based on configuration: %v", kernelArgs)
	return kernelArgs
}

func updateExtendedCPUInfo(extCpuInfo *extendedCPUInfo, used cpuset.CPUSet, disableHT, htEnabled bool) (*extendedCPUInfo, error) {
	retCpuInfo := &cpu.Info{
		TotalCores:   0,
		TotalThreads: 0,
	}

	ret := &extendedCPUInfo{
		CpuInfo:                  retCpuInfo,
		NumLogicalProcessorsUsed: make(map[int]int, len(extCpuInfo.NumLogicalProcessorsUsed)),
		LogicalProcessorsUsed:    make(map[int]struct{}),
	}
	for k, v := range extCpuInfo.NumLogicalProcessorsUsed {
		ret.NumLogicalProcessorsUsed[k] = v
	}
	for k, v := range extCpuInfo.LogicalProcessorsUsed {
		ret.LogicalProcessorsUsed[k] = v
	}

	cpuInfo := extCpuInfo.CpuInfo
	for _, socket := range cpuInfo.Processors {
		s := &cpu.Processor{
			ID:           socket.ID,
			Vendor:       socket.Vendor,
			Model:        socket.Model,
			Capabilities: socket.Capabilities,
			NumCores:     0,
			NumThreads:   0,
		}

		for _, core := range socket.Cores {
			c := &cpu.ProcessorCore{
				ID:         core.ID,
				Index:      core.Index,
				NumThreads: 0,
			}

			for index, lp := range core.LogicalProcessors {
				if used.Contains(lp) {
					if val, ok := ret.NumLogicalProcessorsUsed[socket.ID]; ok {
						ret.NumLogicalProcessorsUsed[socket.ID] = val + 1
					} else {
						ret.NumLogicalProcessorsUsed[socket.ID] = 1
					}
					ret.LogicalProcessorsUsed[lp] = struct{}{}
				}
				if htEnabled && disableHT {
					if index == 0 {
						c.LogicalProcessors = append(c.LogicalProcessors, lp)
						c.NumThreads++
					}
				} else {
					c.LogicalProcessors = append(c.LogicalProcessors, lp)
					c.NumThreads++
				}
			}

			if c.NumThreads > 0 {
				s.NumThreads += c.NumThreads
				s.NumCores++
				s.Cores = append(s.Cores, c)
			}
		}

		if s.NumCores > 0 {
			retCpuInfo.TotalThreads += s.NumThreads
			retCpuInfo.TotalCores += s.NumCores
			retCpuInfo.Processors = append(retCpuInfo.Processors, s)
		}
	}

	return ret, nil
}

func IsLogicalProcessorUsed(extCPUInfo *extendedCPUInfo, logicalProcessor int) bool {
	_, ok := extCPUInfo.LogicalProcessorsUsed[logicalProcessor]
	return ok
}
