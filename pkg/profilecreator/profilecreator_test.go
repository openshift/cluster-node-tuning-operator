package profilecreator

import (
	"path/filepath"
	"sort"

	"github.com/jaypipes/ghw/pkg/cpu"
	"github.com/jaypipes/ghw/pkg/topology"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components"

	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	v1 "k8s.io/api/core/v1"
)

const (
	mustGatherDirPath    = "../../testdata/must-gather/must-gather.bare-metal"
	mustGatherSNODirPath = "../../testdata/must-gather/must-gather.sno"
)

var _ = Describe("PerformanceProfileCreator: MCP and Node Matching", func() {
	var nodes []*v1.Node
	var mcps []*mcfgv1.MachineConfigPool

	BeforeEach(func() {
		var err error

		nodes, err = GetNodeList(mustGatherDirPath)
		Expect(err).ToNot(HaveOccurred())
		mcps, err = GetMCPList(mustGatherDirPath)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("Identifying Nodes targeted by MCP", func() {
		It("should find one machine in cnf-worker MCP", func() {
			mcp, err := GetMCP(mustGatherDirPath, "worker-cnf")
			Expect(err).ToNot(HaveOccurred())

			matchedNodes, err := GetNodesForPool(mcp, mcps, nodes)
			Expect(err).ToNot(HaveOccurred())
			Expect(matchedNodes).ToNot(BeNil())
			Expect(len(matchedNodes)).To(Equal(1))
			Expect(matchedNodes[0].GetName()).To(Equal("worker1"))
		})
		It("should find 1 machine in worker MCP", func() {
			mcp, err := GetMCP(mustGatherDirPath, "worker")
			Expect(err).ToNot(HaveOccurred())

			matchedNodes, err := GetNodesForPool(mcp, mcps, nodes)
			Expect(err).ToNot(HaveOccurred())
			Expect(matchedNodes).ToNot(BeNil())
			Expect(len(matchedNodes)).To(Equal(1))
			Expect(matchedNodes[0].GetName()).To(Equal("worker2"))
		})
	})

	Context("Ensure the correct MCP selector is used", func() {
		It("should detect the cnf-worker MCP selector", func() {
			mcp, err := GetMCP(mustGatherDirPath, "worker-cnf")
			Expect(err).ToNot(HaveOccurred())

			mcpSelector, err := GetMCPSelector(mcp, mcps)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(mcpSelector)).To(Equal(1))

			for key, value := range mcpSelector {
				Expect(key).To(Equal("machineconfiguration.openshift.io/role"))
				Expect(value).To(Equal("worker-cnf"))
				break
			}
		})

		It("should detect the worker MCP selector", func() {
			mcp, err := GetMCP(mustGatherDirPath, "worker")
			Expect(err).ToNot(HaveOccurred())

			mcpSelector, err := GetMCPSelector(mcp, mcps)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(mcpSelector)).To(Equal(1))

			for key, value := range mcpSelector {
				Expect(key).To(Equal("pools.operator.machineconfiguration.openshift.io/worker"))
				Expect(value).To(Equal(""))
				break
			}
		})
	})
})

var _ = Describe("PerformanceProfileCreator: MCP and Node Matching in SNO", func() {
	var nodes []*v1.Node
	var mcps []*mcfgv1.MachineConfigPool

	BeforeEach(func() {
		var err error

		nodes, err = GetNodeList(mustGatherSNODirPath)
		Expect(err).ToNot(HaveOccurred())
		mcps, err = GetMCPList(mustGatherSNODirPath)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("Identifying Nodes targeted by MCP in SNO", func() {
		It("should find no nodes in worker MCP", func() {
			mcp, err := GetMCP(mustGatherSNODirPath, "worker")
			Expect(err).ToNot(HaveOccurred())

			matchedNodes, err := GetNodesForPool(mcp, mcps, nodes)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(matchedNodes)).To(Equal(0))
		})
		It("should find 1 machine in master MCP", func() {
			mcp, err := GetMCP(mustGatherSNODirPath, "master")
			Expect(err).ToNot(HaveOccurred())

			matchedNodes, err := GetNodesForPool(mcp, mcps, nodes)
			Expect(err).ToNot(HaveOccurred())
			Expect(matchedNodes).ToNot(BeNil())
			Expect(len(matchedNodes)).To(Equal(1))
			Expect(matchedNodes[0].GetName()).To(Equal("ocp47sno-master-0.demo.lab"))
		})
	})

	Context("Ensure the correct MCP selector is used in SNO", func() {
		It("should detect the worker MCP selector", func() {
			mcp, err := GetMCP(mustGatherSNODirPath, "worker")
			Expect(err).ToNot(HaveOccurred())

			mcpSelector, err := GetMCPSelector(mcp, mcps)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(mcpSelector)).To(Equal(1))

			for key, value := range mcpSelector {
				Expect(key).To(Equal("pools.operator.machineconfiguration.openshift.io/worker"))
				Expect(value).To(Equal(""))
				break
			}
		})
		It("should detect the master MCP selector", func() {
			mcp, err := GetMCP(mustGatherSNODirPath, "master")
			Expect(err).ToNot(HaveOccurred())

			mcpSelector, err := GetMCPSelector(mcp, mcps)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(mcpSelector)).To(Equal(1))

			for key, value := range mcpSelector {
				Expect(key).To(Equal("pools.operator.machineconfiguration.openshift.io/master"))
				Expect(value).To(Equal(""))
				break
			}
		})
	})
})

var _ = Describe("PerformanceProfileCreator: Getting MCP from Must Gather", func() {
	var mcpName, mcpNodeSelectorKey, mustGatherDirAbsolutePath string
	var err error
	Context("Identifying Nodes targetted by MCP", func() {
		It("gets the MCP successfully", func() {
			mcpName = "worker-cnf"
			mcpNodeSelectorKey = "node-role.kubernetes.io/worker-cnf"
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			mcp, err := GetMCP(mustGatherDirAbsolutePath, mcpName)
			k, _ := components.GetFirstKeyAndValue(mcp.Spec.NodeSelector.MatchLabels)
			Expect(err).ToNot(HaveOccurred())
			Expect(k).To(Equal(mcpNodeSelectorKey))
		})
		It("fails to get MCP as an MCP with that name doesn't exist", func() {
			mcpName = "foo"
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			mcp, err := GetMCP(mustGatherDirAbsolutePath, mcpName)
			Expect(mcp).To(BeNil())
			Expect(err).To(HaveOccurred())
		})
		It("fails to get MCP due to misconfigured must-gather path", func() {
			mcpName = "worker-cnf"
			mustGatherDirAbsolutePath, err = filepath.Abs("foo-path")
			Expect(err).ToNot(HaveOccurred())
			_, err := GetMCP(mustGatherDirAbsolutePath, mcpName)
			Expect(err).To(HaveOccurred())
		})

	})
})

var _ = Describe("PerformanceProfileCreator: Getting Nodes from Must Gather", func() {
	var mustGatherDirAbsolutePath string
	var err error

	Context("Identifying Nodes in the cluster", func() {
		It("gets the Nodes successfully", func() {
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			nodes, err := GetNodeList(mustGatherDirAbsolutePath)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(nodes)).To(Equal(5))
		})
		It("fails to get Nodes due to misconfigured must-gather path", func() {
			mustGatherDirAbsolutePath, err = filepath.Abs("foo-path")
			_, err := GetNodeList(mustGatherDirAbsolutePath)
			Expect(err).To(HaveOccurred())
		})

	})
})

var _ = Describe("PerformanceProfileCreator: Consuming GHW Snapshot from Must Gather", func() {
	var mustGatherDirAbsolutePath string
	var node *v1.Node
	var err error

	Context("Identifying Nodes Info of the nodes cluster", func() {
		It("gets the Nodes Info successfully", func() {
			node = newTestNode("worker1")
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err := NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			cpuInfo, err := handle.CPU()
			Expect(err).ToNot(HaveOccurred())
			Expect(len(cpuInfo.Processors)).To(Equal(2))
			Expect(int(cpuInfo.TotalCores)).To(Equal(40))
			Expect(int(cpuInfo.TotalThreads)).To(Equal(80))
			topologyInfo, err := handle.SortedTopology()
			Expect(err).ToNot(HaveOccurred())
			Expect(len(topologyInfo.Nodes)).To(Equal(2))
		})
		It("fails to get Nodes Info due to misconfigured must-gather path", func() {
			mustGatherDirAbsolutePath, err = filepath.Abs("foo-path")
			_, err := NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).To(HaveOccurred())
		})
		It("fails to get Nodes Info for a node that does not exist", func() {
			node = newTestNode("foo")
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			_, err := NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).To(HaveOccurred())
		})

	})
})

var _ = Describe("PerformanceProfileCreator: Populating Reserved and Isolated CPUs in Performance Profile", func() {
	var mustGatherDirAbsolutePath string
	var node *v1.Node
	var handle *GHWHandler
	var splitReservedCPUsAcrossNUMA, disableHT bool
	var reservedCPUCount int
	var err error

	BeforeEach(func() {
		node = newTestNode("worker1")
	})
	Context("Check if reserved and isolated CPUs are properly populated in the performance profile", func() {
		It("Ensure reserved CPUs populated are correctly when splitReservedCPUsAcrossNUMA is disabled and disableHT is disabled", func() {
			reservedCPUCount = 20 // random number, no special meaning
			splitReservedCPUsAcrossNUMA = false
			disableHT = false
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			reservedCPUSet, isolatedCPUSet, err := handle.GetReservedAndIsolatedCPUs(reservedCPUCount, splitReservedCPUsAcrossNUMA, disableHT)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.String()).To(Equal("0,2,4,6,8,10,12,14,16,18,40,42,44,46,48,50,52,54,56,58"))
			Expect(isolatedCPUSet.String()).To(Equal("1,3,5,7,9,11,13,15,17,19-39,41,43,45,47,49,51,53,55,57,59-79"))
		})
		It("Ensure reserved CPUs populated are correctly when splitReservedCPUsAcrossNUMA is enabled and disableHT is disabled", func() {
			reservedCPUCount = 20 // random number, no special meaning
			splitReservedCPUsAcrossNUMA = true
			disableHT = false
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			reservedCPUSet, isolatedCPUSet, err := handle.GetReservedAndIsolatedCPUs(reservedCPUCount, splitReservedCPUsAcrossNUMA, disableHT)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.String()).To(Equal("0-9,40-49"))
			Expect(isolatedCPUSet.String()).To(Equal("10-39,50-79"))
		})
		It("Errors out in case negative reservedCPUCount is specified", func() {
			reservedCPUCount = -2 // random negative number, no special meaning
			splitReservedCPUsAcrossNUMA = true
			disableHT = false
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			_, _, err := handle.GetReservedAndIsolatedCPUs(reservedCPUCount, splitReservedCPUsAcrossNUMA, disableHT)
			Expect(err).To(HaveOccurred())
		})
		It("Errors out in case specified reservedCPUCount is greater than the total CPUs present in the system and disableHT is disabled", func() {
			reservedCPUCount = 100 // random positive number greater than that total number of CPUs
			splitReservedCPUsAcrossNUMA = true
			disableHT = false
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			_, _, err := handle.GetReservedAndIsolatedCPUs(reservedCPUCount, splitReservedCPUsAcrossNUMA, disableHT)
			Expect(err).To(HaveOccurred())
		})
		It("Errors out in case hyperthreading is enabled, splitReservedCPUsAcrossNUMA is enabled, disableHT is disabled and number of reserved CPUs per number of NUMA nodes are odd", func() {
			reservedCPUCount = 21 // random number which results in a CPU split per NUMA node (11 + 10 in this case) such that odd number of reserved CPUs (11) have to be allocated from a NUMA node
			splitReservedCPUsAcrossNUMA = true
			disableHT = false
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			_, _, err := handle.GetReservedAndIsolatedCPUs(reservedCPUCount, splitReservedCPUsAcrossNUMA, disableHT)
			Expect(err).To(HaveOccurred())
		})
		It("Errors out in case hyperthreading is enabled, splitReservedCPUsAcrossNUMA is disabled,, disableHT is disabled and number of reserved CPUs are odd", func() {
			reservedCPUCount = 21 // random number which results in odd number (21) of CPUs to be allocated from a NUMA node
			splitReservedCPUsAcrossNUMA = false
			disableHT = false
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			_, _, err := handle.GetReservedAndIsolatedCPUs(reservedCPUCount, splitReservedCPUsAcrossNUMA, disableHT)
			Expect(err).To(HaveOccurred())
		})
		It("Ensure reserved CPUs populated are correctly when splitReservedCPUsAcrossNUMA is disabled, disableHT is enabled", func() {
			reservedCPUCount = 20 // random number, no special meaning
			splitReservedCPUsAcrossNUMA = false
			disableHT = true
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			reservedCPUSet, isolatedCPUSet, err := handle.GetReservedAndIsolatedCPUs(reservedCPUCount, splitReservedCPUsAcrossNUMA, disableHT)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.String()).To(Equal("0,2,4,6,8,10,12,14,16,18,20,22,24,26,28,30,32,34,36,38"))
			Expect(isolatedCPUSet.String()).To(Equal("1,3,5,7,9,11,13,15,17,19,21,23,25,27,29,31,33,35,37,39"))
		})
		It("Ensure reserved CPUs populated are correctly when splitReservedCPUsAcrossNUMA is enabled and disableHT is enabled", func() {
			reservedCPUCount = 20 // random number, no special meaning
			splitReservedCPUsAcrossNUMA = true
			disableHT = true
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			reservedCPUSet, isolatedCPUSet, err := handle.GetReservedAndIsolatedCPUs(reservedCPUCount, splitReservedCPUsAcrossNUMA, disableHT)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.String()).To(Equal("0-19"))
			Expect(isolatedCPUSet.String()).To(Equal("20-39"))
		})
		It("Do not error out in case hyperthreading is currently enabled, splitReservedCPUsAcrossNUMA is disabled, disableHT is enabled and number of reserved CPUs allocated from a NUMA node are odd", func() {
			reservedCPUCount = 11 // random number which results in odd number (11) of CPUs to be allocated from a NUMA node
			splitReservedCPUsAcrossNUMA = false
			disableHT = true
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			_, _, err := handle.GetReservedAndIsolatedCPUs(reservedCPUCount, splitReservedCPUsAcrossNUMA, disableHT)
			Expect(err).ToNot(HaveOccurred())
		})
		It("Do not error out in case hyperthreading is currently enabled, splitReservedCPUsAcrossNUMA is enabled, disableHT is enabled and number of reserved CPUs allocated from a NUMA node are odd", func() {
			reservedCPUCount = 2 // random number which results in odd number (1) of CPUs to be allocated from a NUMA node
			splitReservedCPUsAcrossNUMA = true
			disableHT = true
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			_, _, err := handle.GetReservedAndIsolatedCPUs(reservedCPUCount, splitReservedCPUsAcrossNUMA, disableHT)
			Expect(err).ToNot(HaveOccurred())
		})
		It("Do not error out in case of a system where hyperthreading is not enabled initially, splitReservedCPUsAcrossNUMA is disabled, disableHT is enabled and number of reserved CPUs allocated are odd", func() {
			node = newTestNode("ocp47sno-master-0.demo.lab")
			reservedCPUCount = 3 // random number which results in odd number (3) of CPUs to be allocated
			splitReservedCPUsAcrossNUMA = false
			disableHT = true
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherSNODirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			_, _, err := handle.GetReservedAndIsolatedCPUs(reservedCPUCount, splitReservedCPUsAcrossNUMA, disableHT)
			Expect(err).ToNot(HaveOccurred())
		})

	})
})

var _ = Describe("PerformanceProfileCreator: Check if Hyperthreading enabled/disabled in a system to correctly populate reserved and isolated CPUs in the performance profile", func() {
	var mustGatherDirAbsolutePath string
	var node *v1.Node
	var handle *GHWHandler
	var err error

	Context("Check if hyperthreading is enabled on the system or not", func() {
		It("Ensure we detect correctly that hyperthreading is enabled on a system", func() {
			node = newTestNode("worker1")
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			htEnabled, err := handle.IsHyperthreadingEnabled()
			Expect(err).ToNot(HaveOccurred())
			Expect(htEnabled).To(Equal(true))
		})
		It("Ensure we detect correctly that hyperthreading is disabled on a system", func() {
			node = newTestNode("worker2")
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			htEnabled, err := handle.IsHyperthreadingEnabled()
			Expect(err).ToNot(HaveOccurred())
			Expect(htEnabled).To(Equal(false))
		})
	})
})

var _ = Describe("PerformanceProfileCreator: Test Helper Functions getCPUsSplitAcrossNUMA and getCPUsSequentially", func() {
	var mustGatherDirAbsolutePath string
	var node *v1.Node
	var handle *GHWHandler
	var reservedCPUCount int
	var topologyInfoNodes, htDisabledTopologyInfoNodes []*topology.Node
	var htEnabled bool
	var err error

	BeforeEach(func() {
		node = newTestNode("worker1")
		topologyInfoNodes = []*topology.Node{
			{
				ID: 0,
				Cores: []*cpu.ProcessorCore{
					{ID: 0, Index: 0, NumThreads: 2, LogicalProcessors: []int{0, 40}},
					{ID: 4, Index: 6, NumThreads: 2, LogicalProcessors: []int{2, 42}},
					{ID: 1, Index: 17, NumThreads: 2, LogicalProcessors: []int{4, 44}},
					{ID: 3, Index: 18, NumThreads: 2, LogicalProcessors: []int{6, 46}},
					{ID: 2, Index: 19, NumThreads: 2, LogicalProcessors: []int{8, 48}},
					{ID: 12, Index: 1, NumThreads: 2, LogicalProcessors: []int{10, 50}},
					{ID: 8, Index: 2, NumThreads: 2, LogicalProcessors: []int{12, 52}},
					{ID: 11, Index: 3, NumThreads: 2, LogicalProcessors: []int{14, 54}},
					{ID: 9, Index: 4, NumThreads: 2, LogicalProcessors: []int{16, 56}},
					{ID: 10, Index: 5, NumThreads: 2, LogicalProcessors: []int{18, 58}},
					{ID: 16, Index: 7, NumThreads: 2, LogicalProcessors: []int{20, 60}},
					{ID: 20, Index: 8, NumThreads: 2, LogicalProcessors: []int{22, 62}},
					{ID: 17, Index: 9, NumThreads: 2, LogicalProcessors: []int{24, 64}},
					{ID: 19, Index: 10, NumThreads: 2, LogicalProcessors: []int{26, 66}},
					{ID: 18, Index: 11, NumThreads: 2, LogicalProcessors: []int{28, 68}},
					{ID: 28, Index: 12, NumThreads: 2, LogicalProcessors: []int{30, 70}},
					{ID: 24, Index: 13, NumThreads: 2, LogicalProcessors: []int{32, 72}},
					{ID: 27, Index: 14, NumThreads: 2, LogicalProcessors: []int{34, 74}},
					{ID: 25, Index: 15, NumThreads: 2, LogicalProcessors: []int{36, 76}},
					{ID: 26, Index: 16, NumThreads: 2, LogicalProcessors: []int{38, 78}},
				},
			},
			{
				ID: 1,
				Cores: []*cpu.ProcessorCore{
					{ID: 0, Index: 0, NumThreads: 2, LogicalProcessors: []int{1, 41}},
					{ID: 4, Index: 11, NumThreads: 2, LogicalProcessors: []int{3, 43}},
					{ID: 1, Index: 17, NumThreads: 2, LogicalProcessors: []int{5, 45}},
					{ID: 3, Index: 18, NumThreads: 2, LogicalProcessors: []int{7, 47}},
					{ID: 2, Index: 19, NumThreads: 2, LogicalProcessors: []int{9, 49}},
					{ID: 12, Index: 1, NumThreads: 2, LogicalProcessors: []int{11, 51}},
					{ID: 8, Index: 2, NumThreads: 2, LogicalProcessors: []int{13, 53}},
					{ID: 11, Index: 3, NumThreads: 2, LogicalProcessors: []int{15, 55}},
					{ID: 9, Index: 4, NumThreads: 2, LogicalProcessors: []int{17, 57}},
					{ID: 10, Index: 5, NumThreads: 2, LogicalProcessors: []int{19, 59}},
					{ID: 16, Index: 6, NumThreads: 2, LogicalProcessors: []int{21, 61}},
					{ID: 20, Index: 7, NumThreads: 2, LogicalProcessors: []int{23, 63}},
					{ID: 17, Index: 8, NumThreads: 2, LogicalProcessors: []int{25, 65}},
					{ID: 19, Index: 9, NumThreads: 2, LogicalProcessors: []int{27, 67}},
					{ID: 18, Index: 10, NumThreads: 2, LogicalProcessors: []int{29, 69}},
					{ID: 28, Index: 12, NumThreads: 2, LogicalProcessors: []int{31, 71}},
					{ID: 24, Index: 13, NumThreads: 2, LogicalProcessors: []int{33, 73}},
					{ID: 27, Index: 14, NumThreads: 2, LogicalProcessors: []int{35, 75}},
					{ID: 25, Index: 15, NumThreads: 2, LogicalProcessors: []int{37, 77}},
					{ID: 26, Index: 16, NumThreads: 2, LogicalProcessors: []int{39, 79}},
				},
			},
		}

		htDisabledTopologyInfoNodes = []*topology.Node{
			{
				ID: 0,
				Cores: []*cpu.ProcessorCore{
					{ID: 0, Index: 0, NumThreads: 1, LogicalProcessors: []int{0}},
					{ID: 4, Index: 6, NumThreads: 1, LogicalProcessors: []int{2}},
					{ID: 1, Index: 17, NumThreads: 1, LogicalProcessors: []int{4}},
					{ID: 3, Index: 18, NumThreads: 1, LogicalProcessors: []int{6}},
					{ID: 2, Index: 19, NumThreads: 1, LogicalProcessors: []int{8}},
					{ID: 12, Index: 1, NumThreads: 1, LogicalProcessors: []int{10}},
					{ID: 8, Index: 2, NumThreads: 1, LogicalProcessors: []int{12}},
					{ID: 11, Index: 3, NumThreads: 1, LogicalProcessors: []int{14}},
					{ID: 9, Index: 4, NumThreads: 1, LogicalProcessors: []int{16}},
					{ID: 10, Index: 5, NumThreads: 1, LogicalProcessors: []int{18}},
					{ID: 16, Index: 7, NumThreads: 1, LogicalProcessors: []int{20}},
					{ID: 20, Index: 8, NumThreads: 1, LogicalProcessors: []int{22}},
					{ID: 17, Index: 9, NumThreads: 1, LogicalProcessors: []int{24}},
					{ID: 19, Index: 10, NumThreads: 1, LogicalProcessors: []int{26}},
					{ID: 18, Index: 11, NumThreads: 1, LogicalProcessors: []int{28}},
					{ID: 28, Index: 12, NumThreads: 1, LogicalProcessors: []int{30}},
					{ID: 24, Index: 13, NumThreads: 1, LogicalProcessors: []int{32}},
					{ID: 27, Index: 14, NumThreads: 1, LogicalProcessors: []int{34}},
					{ID: 25, Index: 15, NumThreads: 1, LogicalProcessors: []int{36}},
					{ID: 26, Index: 16, NumThreads: 1, LogicalProcessors: []int{38}},
				},
			},
			{
				ID: 1,
				Cores: []*cpu.ProcessorCore{
					{ID: 0, Index: 0, NumThreads: 1, LogicalProcessors: []int{1}},
					{ID: 4, Index: 11, NumThreads: 1, LogicalProcessors: []int{3}},
					{ID: 1, Index: 17, NumThreads: 1, LogicalProcessors: []int{5}},
					{ID: 3, Index: 18, NumThreads: 1, LogicalProcessors: []int{7}},
					{ID: 2, Index: 19, NumThreads: 1, LogicalProcessors: []int{9}},
					{ID: 12, Index: 1, NumThreads: 1, LogicalProcessors: []int{11}},
					{ID: 8, Index: 2, NumThreads: 1, LogicalProcessors: []int{13}},
					{ID: 11, Index: 3, NumThreads: 1, LogicalProcessors: []int{15}},
					{ID: 9, Index: 4, NumThreads: 1, LogicalProcessors: []int{17}},
					{ID: 10, Index: 5, NumThreads: 1, LogicalProcessors: []int{19}},
					{ID: 16, Index: 6, NumThreads: 1, LogicalProcessors: []int{21}},
					{ID: 20, Index: 7, NumThreads: 1, LogicalProcessors: []int{23}},
					{ID: 17, Index: 8, NumThreads: 1, LogicalProcessors: []int{25}},
					{ID: 19, Index: 9, NumThreads: 1, LogicalProcessors: []int{27}},
					{ID: 18, Index: 10, NumThreads: 1, LogicalProcessors: []int{29}},
					{ID: 28, Index: 12, NumThreads: 1, LogicalProcessors: []int{31}},
					{ID: 24, Index: 13, NumThreads: 1, LogicalProcessors: []int{33}},
					{ID: 27, Index: 14, NumThreads: 1, LogicalProcessors: []int{35}},
					{ID: 25, Index: 15, NumThreads: 1, LogicalProcessors: []int{37}},
					{ID: 26, Index: 16, NumThreads: 1, LogicalProcessors: []int{39}},
				},
			},
		}
	})
	Context("Check if getCPUsSplitAcrossNUMA and getCPUsSequentially are working correctly and reserved and isolated CPUs are properly populated in the performance profile", func() {
		It("Ensure reserved and isolated CPUs populated are correctly by getCPUsSplitAcrossNUMAwhen when splitReservedCPUsAcrossNUMA is enabled", func() {
			reservedCPUCount = 20 // random number, no special meaning
			htEnabled = true
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			reservedCPUSet, isolatedCPUSet, err := handle.getCPUsSplitAcrossNUMA(reservedCPUCount, htEnabled, topologyInfoNodes)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.String()).To(Equal("0-9,40-49"))
			Expect(isolatedCPUSet.String()).To(Equal("10-39,50-79"))
		})
		It("Ensure reserved and isolated CPUs populated are correctly by getCPUsSplitAcrossNUMAwhen when splitReservedCPUsAcrossNUMA is enabled and htEnabled is disabled ", func() {
			reservedCPUCount = 20 // random number, no special meaning
			htEnabled = false
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			reservedCPUSet, isolatedCPUSet, err := handle.getCPUsSplitAcrossNUMA(reservedCPUCount, htEnabled, htDisabledTopologyInfoNodes)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.String()).To(Equal("0-19"))
			Expect(isolatedCPUSet.String()).To(Equal("20-39"))
		})
		It("Errors out in case hyperthreading is enabled, splitReservedCPUsAcrossNUMA is enabled, htEnabled is enabled and the number of reserved CPUs per number of NUMA nodes are odd", func() {
			reservedCPUCount = 21 // random number which results in a CPU split per NUMA node (11 + 10 in this case) such that odd number of reserved CPUs (11) have to be allocated from a NUMA node
			htEnabled = true
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			_, _, err := handle.getCPUsSplitAcrossNUMA(reservedCPUCount, htEnabled, topologyInfoNodes)
			Expect(err).To(HaveOccurred())
		})
		It("Works without error in case hyperthreading is disabled, splitReservedCPUsAcrossNUMA is enabled, htEnabled is disabled and number of reserved CPUs per number of NUMA nodes are odd", func() {
			reservedCPUCount = 11 // random number which results in a CPU split per NUMA node (5 + 6 in this case) such that odd number of reserved CPUs (5) have to be allocated from a NUMA node
			htEnabled = false
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			_, _, err := handle.getCPUsSplitAcrossNUMA(reservedCPUCount, htEnabled, topologyInfoNodes)
			Expect(err).ToNot(HaveOccurred())
		})
		It("Ensure reserved and isolated CPUs populated are correctly by getCPUsSequentially when splitReservedCPUsAcrossNUMA is disabled", func() {
			reservedCPUCount = 20 // random number, no special meaning
			htEnabled = true
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			reservedCPUSet, isolatedCPUSet, err := handle.getCPUsSequentially(reservedCPUCount, htEnabled, topologyInfoNodes)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.String()).To(Equal("0,2,4,6,8,10,12,14,16,18,40,42,44,46,48,50,52,54,56,58"))
			Expect(isolatedCPUSet.String()).To(Equal("1,3,5,7,9,11,13,15,17,19-39,41,43,45,47,49,51,53,55,57,59-79"))
		})
		It("Errors out in case hyperthreading is enabled, splitReservedCPUsAcrossNUMA is disabled and number of reserved CPUs are odd", func() {
			reservedCPUCount = 21 // random number which results in odd number (21) of CPUs to be allocated from a NUMA node
			htEnabled = true
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			_, _, err := handle.getCPUsSequentially(reservedCPUCount, htEnabled, topologyInfoNodes)
			Expect(err).To(HaveOccurred())
		})
		It("Works without error in case hyperthreading is disabled, splitReservedCPUsAcrossNUMA is disabled and number of reserved CPUs are odd", func() {
			reservedCPUCount = 11 // random number which results in odd number (11) of CPUs to be allocated from a NUMA node
			htEnabled = false
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			_, _, err := handle.getCPUsSequentially(reservedCPUCount, htEnabled, topologyInfoNodes)
			Expect(err).ToNot(HaveOccurred())
		})

	})
})

var _ = Describe("PerformanceProfileCreator: Ensuring Nodes hardware equality", func() {
	Context("Testing matching nodes with the same hardware ", func() {
		It("should pass hardware equality test", func() {
			mustGatherDirAbsolutePath, err := filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())

			node1, err := getNode(mustGatherDirAbsolutePath, "worker1.yaml")
			Expect(err).ToNot(HaveOccurred())
			node1Handle, err := NewGHWHandler(mustGatherDirAbsolutePath, node1)
			Expect(err).ToNot(HaveOccurred())

			node2, err := getNode(mustGatherDirAbsolutePath, "worker1.yaml")
			Expect(err).ToNot(HaveOccurred())
			node2Handle, err := NewGHWHandler(mustGatherDirAbsolutePath, node2)
			Expect(err).ToNot(HaveOccurred())

			nodeHandles := []*GHWHandler{node1Handle, node2Handle}
			err = EnsureNodesHaveTheSameHardware(nodeHandles)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("Testing matching nodes with different hardware ", func() {
		It("should fail hardware equality test", func() {
			mustGatherDirAbsolutePath, err := filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())

			node1, err := getNode(mustGatherDirAbsolutePath, "worker1.yaml")
			Expect(err).ToNot(HaveOccurred())
			node1Handle, err := NewGHWHandler(mustGatherDirAbsolutePath, node1)
			Expect(err).ToNot(HaveOccurred())

			node2, err := getNode(mustGatherDirAbsolutePath, "worker2.yaml")
			Expect(err).ToNot(HaveOccurred())
			node2Handle, err := NewGHWHandler(mustGatherDirAbsolutePath, node2)
			Expect(err).ToNot(HaveOccurred())

			nodeHandles := []*GHWHandler{node1Handle, node2Handle}
			err = EnsureNodesHaveTheSameHardware(nodeHandles)
			Expect(err).To(HaveOccurred())
		})
	})
})

var _ = Describe("PerformanceProfileCreator: Test Helper Function ensureSameTopology", func() {
	var nodes2 []*topology.Node
	var topology2 topology.Info

	nodes1 := []*topology.Node{
		{
			ID: 0,
			Cores: []*cpu.ProcessorCore{
				{ID: 0, Index: 0, NumThreads: 2, LogicalProcessors: []int{0, 1}},
				{ID: 1, Index: 1, NumThreads: 2, LogicalProcessors: []int{2, 3}},
			},
		},
		{
			ID: 1,
			Cores: []*cpu.ProcessorCore{
				{ID: 2, Index: 2, NumThreads: 2, LogicalProcessors: []int{4, 5}},
				{ID: 3, Index: 3, NumThreads: 2, LogicalProcessors: []int{6, 7}},
			},
		},
	}
	topology1 := topology.Info{
		Architecture: topology.ARCHITECTURE_NUMA,
		Nodes:        nodes1,
	}

	BeforeEach(func() {
		nodes2 = []*topology.Node{
			{
				ID: 0,
				Cores: []*cpu.ProcessorCore{
					{ID: 0, Index: 0, NumThreads: 2, LogicalProcessors: []int{0, 1}},
					{ID: 1, Index: 1, NumThreads: 2, LogicalProcessors: []int{2, 3}},
				},
			},
			{
				ID: 1,
				Cores: []*cpu.ProcessorCore{
					{ID: 2, Index: 2, NumThreads: 2, LogicalProcessors: []int{4, 5}},
					{ID: 3, Index: 3, NumThreads: 2, LogicalProcessors: []int{6, 7}},
				},
			},
		}
		topology2 = topology.Info{
			Architecture: topology.ARCHITECTURE_NUMA,
			Nodes:        nodes2,
		}
	})

	Context("Check if ensureSameTopology is working correctly", func() {
		It("nodes with similar topology should not return error", func() {
			err := ensureSameTopology(&topology1, &topology2)
			Expect(err).ToNot(HaveOccurred())
		})
		It("nodes with different architecture should return error", func() {
			topology2.Architecture = topology.ARCHITECTURE_SMP
			err := ensureSameTopology(&topology1, &topology2)
			Expect(err).To(HaveOccurred())
		})
		It("nodes with different number of NUMA nodes should return error", func() {
			topology2.Nodes = topology2.Nodes[1:]
			err := ensureSameTopology(&topology1, &topology2)
			Expect(err).To(HaveOccurred())
		})
		It("nodes with different number threads per core should return error", func() {
			topology2.Nodes[1].Cores[1].NumThreads = 1
			err := ensureSameTopology(&topology1, &topology2)
			Expect(err).To(HaveOccurred())
		})
		It("nodes with different thread IDs should return error", func() {
			topology2.Nodes[1].Cores[1].LogicalProcessors[1] = 15
			err := ensureSameTopology(&topology1, &topology2)
			Expect(err).To(HaveOccurred())
		})
	})
})

var _ = Describe("PerformanceProfileCreator: Test Helper Function GetAdditionalKernelArgs", func() {
	var powerMode string
	var disableHT bool
	Context("Ensure kernel args are populated correctly", func() {
		It("Ensure kernel args are populated correctly in case of low-latency ", func() {
			powerMode = "default"
			disableHT = false
			kernelArgs := GetAdditionalKernelArgs(powerMode, disableHT)
			Expect(kernelArgs).To(BeEquivalentTo([]string{}))
		})

	})
	Context("Ensure kernel args are populated correctly", func() {
		It("Ensure kernel args are populated correctly in case of low-latency ", func() {
			powerMode = "low-latency"
			disableHT = false
			args := []string{"audit=0",
				"mce=off",
				"nmi_watchdog=0",
			}
			kernelArgs := GetAdditionalKernelArgs(powerMode, disableHT)
			sort.Strings(kernelArgs) // sort to avoid inequality due to difference in order
			Expect(kernelArgs).To(BeEquivalentTo(args))
		})

	})
	Context("Ensure kernel args are populated correctly", func() {
		It("Ensure kernel args are populated correctly in case of ultra-low-latency ", func() {
			powerMode = "ultra-low-latency"
			disableHT = false
			args := []string{"audit=0",
				"idle=poll",
				"intel_idle.max_cstate=0",
				"mce=off",
				"nmi_watchdog=0",
				"processor.max_cstate=1",
			}
			kernelArgs := GetAdditionalKernelArgs(powerMode, disableHT)
			sort.Strings(kernelArgs) // sort to avoid inequality due to difference in order
			Expect(kernelArgs).To(BeEquivalentTo(args))
		})

	})
	Context("Ensure kernel args are populated correctly", func() {
		It("Ensure kernel args are populated correctly in case of disableHT=true ", func() {
			powerMode = "ultra-low-latency"
			disableHT = true
			args := []string{"audit=0",
				"idle=poll",
				"intel_idle.max_cstate=0",
				"mce=off",
				"nmi_watchdog=0",
				"nosmt",
				"processor.max_cstate=1",
			}
			kernelArgs := GetAdditionalKernelArgs(powerMode, disableHT)
			sort.Strings(kernelArgs) // sort to avoid inequality due to difference in order
			Expect(kernelArgs).To(BeEquivalentTo(args))
		})

	})
})

var _ = Describe("PerformanceProfileCreator: Test Helper cpuAccumulator", func() {
	nodes1 := []*topology.Node{
		{
			ID: 0,
			Cores: []*cpu.ProcessorCore{
				{ID: 0, Index: 0, NumThreads: 2, LogicalProcessors: []int{0, 1}},
				{ID: 1, Index: 1, NumThreads: 2, LogicalProcessors: []int{2, 3}},
			},
		},
		{
			ID: 1,
			Cores: []*cpu.ProcessorCore{
				{ID: 2, Index: 2, NumThreads: 2, LogicalProcessors: []int{4, 5}},
				{ID: 3, Index: 3, NumThreads: 2, LogicalProcessors: []int{6, 7}},
			},
		},
	}
	topology1 := topology.Info{
		Architecture: topology.ARCHITECTURE_NUMA,
		Nodes:        nodes1,
	}

	Context("Check if cpuAccumulator is working correctly", func() {
		It("should accumulate allCores", func() {
			acc := newCPUAccumulator()
			for _, node := range topology1.Nodes {
				acc.AddCores(allCores, node.Cores)
			}
			cores := acc.Result().ToSlice()
			Expect(cores).Should(Equal([]int{0, 1, 2, 3, 4, 5, 6, 7}))
		})
		It("should accumulate cores up to the max", func() {
			acc := newCPUAccumulator()
			for _, node := range topology1.Nodes {
				acc.AddCores(3, node.Cores)
			}
			cores := acc.Result().ToSlice()
			Expect(cores).Should(Equal([]int{0, 1, 2}))
		})

	})
})
