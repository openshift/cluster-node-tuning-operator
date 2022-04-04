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
	"io/ioutil"
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
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"

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
	// ValidPowerConsumptionModes are a set of valid power consumption modes
	// default => no args
	// low-latency => "nmi_watchdog=0", "audit=0",  "mce=off"
	// ultra-low-latency: low-latency values + "processor.max_cstate=1", "intel_idle.max_cstate=0", "idle=poll"
	// For more information on CPU "C-states" please refer to https://gist.github.com/wmealing/2dd2b543c4d3cff6cab7
	ValidPowerConsumptionModes = []string{"default", "low-latency", "ultra-low-latency"}
	lowLatencyKernelArgs       = map[string]bool{"nmi_watchdog=0": true, "audit=0": true, "mce=off": true}
	ultraLowLatencyKernelArgs  = map[string]bool{"processor.max_cstate=1": true, "intel_idle.max_cstate=0": true, "idle=poll": true}
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

	nodes, err := ioutil.ReadDir(nodePath)
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

	mcpFiles, err := ioutil.ReadDir(mcpPath)
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
// The behaviour of this function depends on ghw data representation.
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
			// a processing unit for a core. GHW API gurantees that the logicalProcessors correspond
			// to hyperthread pairs and in the code below we select only the first hyperthread (id=0)
			// of the available logical processors.
			for id, logicalProcessor := range processorCore.LogicalProcessors {
				// Please refer to https://www.kernel.org/doc/Documentation/x86/topology.txt for more information on
				// x86 hardware topology. This document clarifies the main aspects of x86 topology modelling and
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

// GetReservedAndIsolatedCPUs returns Reserved and Isolated CPUs
func (ghwHandler GHWHandler) GetReservedAndIsolatedCPUs(reservedCPUCount int, splitReservedCPUsAcrossNUMA bool, disableHTFlag bool) (cpuset.CPUSet, cpuset.CPUSet, error) {
	cpuInfo, err := ghwHandler.CPU()
	if err != nil {
		return cpuset.CPUSet{}, cpuset.CPUSet{}, fmt.Errorf("can't obtain CPU info from GHW snapshot: %v", err)
	}

	if reservedCPUCount <= 0 || reservedCPUCount >= int(cpuInfo.TotalThreads) {
		return cpuset.CPUSet{}, cpuset.CPUSet{}, fmt.Errorf("please specify the reserved CPU count in the range [1,%d]", cpuInfo.TotalThreads-1)
	}
	topologyInfo, err := ghwHandler.SortedTopology()
	if err != nil {
		return cpuset.CPUSet{}, cpuset.CPUSet{}, fmt.Errorf("can't obtain Topology Info from GHW snapshot: %v", err)
	}
	htEnabled, err := ghwHandler.IsHyperthreadingEnabled()
	if err != nil {
		return cpuset.CPUSet{}, cpuset.CPUSet{}, fmt.Errorf("can't determine if Hyperthreading is enabled or not: %v", err)
	}
	//currently HT is enabled on the system and the user wants to disable HT
	if htEnabled && disableHTFlag {
		htEnabled = false
		log.Infof("Currently hyperthreading is enabled and the performance profile will disable it")
		topologyInfo = topologyHTDisabled(topologyInfo)

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
		return ghwHandler.getCPUsSplitAcrossNUMA(reservedCPUCount, htEnabled, topologyInfo.Nodes)
	}
	return ghwHandler.getCPUsSequentially(reservedCPUCount, htEnabled, topologyInfo.Nodes)
}

type cpuAccumulator struct {
	builder *cpuset.Builder
	count   int
}

func newCPUAccumulator() *cpuAccumulator {
	return &cpuAccumulator{
		builder: cpuset.NewBuilder(),
	}
}

// AddCores adds logical cores from the slice of *cpu.ProcessorCore to a CPUset till the cpuset size is equal to the max value specified
// In case the max is specified as allCores, all the cores from the slice of *cpu.ProcessorCore are added to the CPUSet
func (ca *cpuAccumulator) AddCores(max int, cores []*cpu.ProcessorCore) {
	for _, processorCore := range cores {
		for _, core := range processorCore.LogicalProcessors {
			if ca.count < max || max == allCores {
				ca.builder.Add(core)
				ca.count++
			}
		}
	}
}

func (ca *cpuAccumulator) Result() cpuset.CPUSet {
	return ca.builder.Result()
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
func (ghwHandler GHWHandler) getCPUsSplitAcrossNUMA(reservedCPUCount int, htEnabled bool, topologyInfoNodes []*topology.Node) (cpuset.CPUSet, cpuset.CPUSet, error) {
	reservedCPUs := newCPUAccumulator()
	var isolatedCPUSet cpuset.CPUSet
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
			return reservedCPUs.Result(), isolatedCPUSet, fmt.Errorf("can't allocate odd number of CPUs from a NUMA Node")
		}
		reservedCPUs.AddCores(max, node.Cores)
	}
	totalCPUSet := totalCPUSetFromTopology(topologyInfoNodes)
	reservedCPUSet := reservedCPUs.Result()
	isolatedCPUSet = totalCPUSet.Difference(reservedCPUSet)
	return reservedCPUSet, isolatedCPUSet, nil
}

// getCPUsSequentially returns Reserved and Isolated CPUs sequentially
func (ghwHandler GHWHandler) getCPUsSequentially(reservedCPUCount int, htEnabled bool, topologyInfoNodes []*topology.Node) (cpuset.CPUSet, cpuset.CPUSet, error) {
	reservedCPUs := newCPUAccumulator()
	var isolatedCPUSet cpuset.CPUSet
	if reservedCPUCount%2 != 0 && htEnabled {
		return reservedCPUs.Result(), isolatedCPUSet, fmt.Errorf("can't allocate odd number of CPUs from a NUMA Node")
	}
	for _, node := range topologyInfoNodes {
		reservedCPUs.AddCores(reservedCPUCount, node.Cores)
	}
	totalCPUSet := totalCPUSetFromTopology(topologyInfoNodes)
	reservedCPUSet := reservedCPUs.Result()
	isolatedCPUSet = totalCPUSet.Difference(reservedCPUSet)
	return reservedCPUSet, isolatedCPUSet, nil
}

func totalCPUSetFromTopology(topologyInfoNodes []*topology.Node) cpuset.CPUSet {
	totalCPUs := newCPUAccumulator()
	for _, node := range topologyInfoNodes {
		//all the cores from node.Cores need to be added, hence allCores is specified as the max value
		totalCPUs.AddCores(allCores, node.Cores)
	}
	return totalCPUs.Result()
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

// GetAdditionalKernelArgs returns a set of kernel parameters based on the power mode
func GetAdditionalKernelArgs(powerMode string, disableHT bool) []string {
	kernelArgsSet := make(map[string]bool)
	kernelArgsSlice := make([]string, 0, 6)
	switch powerMode {
	//default
	case ValidPowerConsumptionModes[0]:
		kernelArgsSlice = []string{}
	//low-latency
	case ValidPowerConsumptionModes[1]:
		for arg, exist := range lowLatencyKernelArgs {
			kernelArgsSet[arg] = exist
		}
	//ultra-low-latency
	case ValidPowerConsumptionModes[2]:
		//computing the union for two sets (lowLatencyKernelArgs,ultraLowLatencyKernelArgs)
		for arg, exist := range lowLatencyKernelArgs {
			kernelArgsSet[arg] = exist
		}
		for arg, exist := range ultraLowLatencyKernelArgs {
			kernelArgsSet[arg] = exist
		}
	}

	for arg, exist := range kernelArgsSet {
		if exist {
			kernelArgsSlice = append(kernelArgsSlice, arg)
		}
	}
	if disableHT {
		kernelArgsSlice = append(kernelArgsSlice, noSMTKernelArg)
	}
	sort.Strings(kernelArgsSlice)
	log.Infof("Additional Kernel Args based on the power consumption mode (%s):%v", powerMode, kernelArgsSlice)
	return kernelArgsSlice
}
