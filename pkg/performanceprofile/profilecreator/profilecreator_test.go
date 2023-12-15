package profilecreator

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/jaypipes/ghw/pkg/cpu"
	"github.com/jaypipes/ghw/pkg/topology"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	log "github.com/sirupsen/logrus"

	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/utils/cpuset"
)

const (
	mustGatherDirPath    = "../../../test/e2e/performanceprofile/testdata/must-gather/must-gather.bare-metal"
	mustGatherSNODirPath = "../../../test/e2e/performanceprofile/testdata/must-gather/must-gather.sno"
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

var _ = Describe("Performance profile creator: test with a simple cpu architecture to see algorithm easely", func() {
	Context("With 16 cores, 32 Threads, 2 sockets 2 NUMA zones", func() {

		var sysInfo SystemInfo

		BeforeEach(func() {
			core0 := cpu.ProcessorCore{
				ID:                0,
				Index:             0,
				NumThreads:        2,
				LogicalProcessors: []int{0, 8},
			}
			core1 := cpu.ProcessorCore{
				ID:                1,
				Index:             1,
				NumThreads:        2,
				LogicalProcessors: []int{1, 9},
			}
			core2 := cpu.ProcessorCore{
				ID:                2,
				Index:             2,
				NumThreads:        2,
				LogicalProcessors: []int{2, 10},
			}
			core3 := cpu.ProcessorCore{
				ID:                3,
				Index:             3,
				NumThreads:        2,
				LogicalProcessors: []int{3, 11},
			}
			core4 := cpu.ProcessorCore{
				ID:                4,
				Index:             4,
				NumThreads:        2,
				LogicalProcessors: []int{4, 12},
			}
			core5 := cpu.ProcessorCore{
				ID:                5,
				Index:             5,
				NumThreads:        2,
				LogicalProcessors: []int{5, 13},
			}
			core6 := cpu.ProcessorCore{
				ID:                6,
				Index:             6,
				NumThreads:        2,
				LogicalProcessors: []int{6, 14},
			}
			core7 := cpu.ProcessorCore{
				ID:                7,
				Index:             7,
				NumThreads:        2,
				LogicalProcessors: []int{7, 15},
			}

			core8 := cpu.ProcessorCore{
				ID:                8,
				Index:             8,
				NumThreads:        2,
				LogicalProcessors: []int{16, 24},
			}
			core9 := cpu.ProcessorCore{
				ID:                9,
				Index:             9,
				NumThreads:        2,
				LogicalProcessors: []int{17, 25},
			}
			core10 := cpu.ProcessorCore{
				ID:                10,
				Index:             10,
				NumThreads:        2,
				LogicalProcessors: []int{18, 26},
			}
			core11 := cpu.ProcessorCore{
				ID:                11,
				Index:             11,
				NumThreads:        2,
				LogicalProcessors: []int{19, 27},
			}
			core12 := cpu.ProcessorCore{
				ID:                12,
				Index:             12,
				NumThreads:        2,
				LogicalProcessors: []int{20, 28},
			}
			core13 := cpu.ProcessorCore{
				ID:                13,
				Index:             13,
				NumThreads:        2,
				LogicalProcessors: []int{21, 29},
			}
			core14 := cpu.ProcessorCore{
				ID:                14,
				Index:             14,
				NumThreads:        2,
				LogicalProcessors: []int{22, 30},
			}
			core15 := cpu.ProcessorCore{
				ID:                15,
				Index:             15,
				NumThreads:        2,
				LogicalProcessors: []int{23, 31},
			}

			topologyInfoNodes := []*topology.Node{
				{
					ID: 0,
					Cores: []*cpu.ProcessorCore{
						&core0,
						&core1,
						&core2,
						&core3,
					},
				},
				{
					ID: 1,
					Cores: []*cpu.ProcessorCore{
						&core4,
						&core5,
						&core6,
						&core7,
					},
				},
				{
					ID: 2,
					Cores: []*cpu.ProcessorCore{
						&core8,
						&core9,
						&core10,
						&core11,
					},
				},
				{
					ID: 3,
					Cores: []*cpu.ProcessorCore{
						&core12,
						&core13,
						&core14,
						&core15,
					},
				},
			}

			topologyInfo := topology.Info{
				Architecture: topology.ARCHITECTURE_NUMA,
				Nodes:        topologyInfoNodes,
			}

			processors := []*cpu.Processor{
				{
					ID:         0,
					NumCores:   8,
					NumThreads: 16,
					Vendor:     "redhat",
					Model:      "rh-testing",
					Cores: []*cpu.ProcessorCore{
						&core0,
						&core1,
						&core2,
						&core3,
						&core4,
						&core5,
						&core6,
						&core7,
					},
				},
				{
					ID:         1,
					NumCores:   8,
					NumThreads: 16,
					Vendor:     "redhat",
					Model:      "rh-testing",
					Cores: []*cpu.ProcessorCore{
						&core8,
						&core9,
						&core10,
						&core11,
						&core12,
						&core13,
						&core14,
						&core15,
					},
				},
			}

			cpuInfo := cpu.Info{
				TotalCores:   16,
				TotalThreads: 32,
				Processors:   processors,
			}

			extCpuInfo := extendedCPUInfo{
				CpuInfo:                  &cpuInfo,
				NumLogicalProcessorsUsed: make(map[int]int),
				LogicalProcessorsUsed:    make(map[int]struct{}),
			}

			sysInfo = SystemInfo{
				CpuInfo:      &extCpuInfo,
				TopologyInfo: &topologyInfo,
				HtEnabled:    true,
			}
		})

		It("diag0002 can offline a full socket", func() {
			reservedCPUCount := 8
			offlinedCPUCount := 16
			splitReservedCPUsAcrossNUMA := false
			disableHT := false
			highPowerConsumptionMode := false

			reserved, isolated, offlined, err := CalculateCPUSets(&sysInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, highPowerConsumptionMode)
			Expect(err).ToNot(HaveOccurred())

			log.Infof("Input:")
			log.Infof("\treserved-cpu-count:\t\t%d", reservedCPUCount)
			log.Infof("\tofflined-cpu-count:\t\t%d", offlinedCPUCount)
			log.Infof("\tdisable-ht:\t\t\t%v", disableHT)
			log.Infof("\tsplitReservedCPUsAcrossNUMA:\t%v", splitReservedCPUsAcrossNUMA)
			log.Infof("\thighConsumptionMode:\t\t%v", highPowerConsumptionMode)
			log.Infof("Output:")
			log.Infof("\treserved: %s", reserved.String())
			log.Infof("\tisolated: %s", isolated.String())
			log.Infof("\tofflined: %s", offlined.String())

			Expect(offlined.Intersection(reserved).IsEmpty()).To(BeTrue())
			Expect(offlined.Intersection(isolated).IsEmpty()).To(BeTrue())
			Expect(offlined.Size()).To(Equal(offlinedCPUCount))

			Expect(reserved.String()).To(Equal("0-3,8-11"))
			Expect(isolated.String()).To(Equal("4-7,12-15"))
			Expect(offlined.String()).To(Equal("16-31"))
		})

		It("diag0003 cannot offline a full socket because reserved are splitted over all NUMA zones", func() {
			reservedCPUCount := 8
			offlinedCPUCount := 16
			splitReservedCPUsAcrossNUMA := true
			disableHT := false
			highPowerConsumptionMode := false

			reserved, isolated, offlined, err := CalculateCPUSets(&sysInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, highPowerConsumptionMode)
			Expect(err).ToNot(HaveOccurred())

			log.Infof("Input:")
			log.Infof("\treserved-cpu-count:\t\t%d", reservedCPUCount)
			log.Infof("\tofflined-cpu-count:\t\t%d", offlinedCPUCount)
			log.Infof("\tdisable-ht:\t\t\t%v", disableHT)
			log.Infof("\tsplitReservedCPUsAcrossNUMA:\t%v", splitReservedCPUsAcrossNUMA)
			log.Infof("\thighConsumptionMode:\t\t%v", highPowerConsumptionMode)
			log.Infof("Output:")
			log.Infof("\treserved: %s", reserved.String())
			log.Infof("\tisolated: %s", isolated.String())
			log.Infof("\tofflined: %s", offlined.String())

			Expect(offlined.Intersection(reserved).IsEmpty()).To(BeTrue())
			Expect(offlined.Intersection(isolated).IsEmpty()).To(BeTrue())
			Expect(offlined.Size()).To(Equal(offlinedCPUCount))

			Expect(reserved.String()).To(Equal("0,4,8,12,16,20,24,28"))
			Expect(isolated.String()).To(Equal("6-7,17-19,21-23"))
			Expect(offlined.String()).To(Equal("1-3,5,9-11,13-15,25-27,29-31"))
		})

		It("diag0004 in high-power-consumption-mode so do not offline a socket, go for sibling threads instead", func() {
			reservedCPUCount := 8
			offlinedCPUCount := 16
			splitReservedCPUsAcrossNUMA := false
			disableHT := false
			highPowerConsumptionMode := true

			reserved, isolated, offlined, err := CalculateCPUSets(&sysInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, highPowerConsumptionMode)
			Expect(err).ToNot(HaveOccurred())

			log.Infof("Input:")
			log.Infof("\treserved-cpu-count:\t\t%d", reservedCPUCount)
			log.Infof("\tofflined-cpu-count:\t\t%d", offlinedCPUCount)
			log.Infof("\tdisable-ht:\t\t\t%v", disableHT)
			log.Infof("\tsplitReservedCPUsAcrossNUMA:\t%v", splitReservedCPUsAcrossNUMA)
			log.Infof("\thighConsumptionMode:\t\t%v", highPowerConsumptionMode)
			log.Infof("Output:")
			log.Infof("\treserved: %s", reserved.String())
			log.Infof("\tisolated: %s", isolated.String())
			log.Infof("\tofflined: %s", offlined.String())

			Expect(offlined.Intersection(reserved).IsEmpty()).To(BeTrue())
			Expect(offlined.Intersection(isolated).IsEmpty()).To(BeTrue())
			Expect(offlined.Size()).To(Equal(offlinedCPUCount))

			Expect(reserved.String()).To(Equal("0-3,8-11"))
			Expect(isolated.String()).To(Equal("16-23"))
			Expect(offlined.String()).To(Equal("4-7,12-15,24-31"))
		})

		It("diag0005 as socket is too big to be offlined completely we go for the siblings", func() {
			reservedCPUCount := 8
			offlinedCPUCount := 12
			splitReservedCPUsAcrossNUMA := false
			disableHT := false
			highPowerConsumptionMode := false

			reserved, isolated, offlined, err := CalculateCPUSets(&sysInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, highPowerConsumptionMode)
			Expect(err).ToNot(HaveOccurred())

			log.Infof("Input:")
			log.Infof("\treserved-cpu-count:\t\t%d", reservedCPUCount)
			log.Infof("\tofflined-cpu-count:\t\t%d", offlinedCPUCount)
			log.Infof("\tdisable-ht:\t\t\t%v", disableHT)
			log.Infof("\tsplitReservedCPUsAcrossNUMA:\t%v", splitReservedCPUsAcrossNUMA)
			log.Infof("\thighConsumptionMode:\t\t%v", highPowerConsumptionMode)
			log.Infof("Output:")
			log.Infof("\treserved: %s", reserved.String())
			log.Infof("\tisolated: %s", isolated.String())
			log.Infof("\tofflined: %s", offlined.String())

			Expect(offlined.Intersection(reserved).IsEmpty()).To(BeTrue())
			Expect(offlined.Intersection(isolated).IsEmpty()).To(BeTrue())
			Expect(offlined.Size()).To(Equal(offlinedCPUCount))

			Expect(reserved.String()).To(Equal("0-3,8-11"))
			Expect(isolated.String()).To(Equal("4-7,16-23"))
			Expect(offlined.String()).To(Equal("12-15,24-31"))
		})

		It("diag0006 can set offline a complete socket and then goes for the siblings", func() {
			reservedCPUCount := 8
			offlinedCPUCount := 19
			splitReservedCPUsAcrossNUMA := false
			disableHT := false
			highPowerConsumptionMode := false

			reserved, isolated, offlined, err := CalculateCPUSets(&sysInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, highPowerConsumptionMode)
			Expect(err).ToNot(HaveOccurred())

			log.Infof("Input:")
			log.Infof("\treserved-cpu-count:\t\t%d", reservedCPUCount)
			log.Infof("\tofflined-cpu-count:\t\t%d", offlinedCPUCount)
			log.Infof("\tdisable-ht:\t\t\t%v", disableHT)
			log.Infof("\tsplitReservedCPUsAcrossNUMA:\t%v", splitReservedCPUsAcrossNUMA)
			log.Infof("\thighConsumptionMode:\t\t%v", highPowerConsumptionMode)
			log.Infof("Output:")
			log.Infof("\treserved: %s", reserved.String())
			log.Infof("\tisolated: %s", isolated.String())
			log.Infof("\tofflined: %s", offlined.String())

			Expect(offlined.Intersection(reserved).IsEmpty()).To(BeTrue())
			Expect(offlined.Intersection(isolated).IsEmpty()).To(BeTrue())
			Expect(offlined.Size()).To(Equal(offlinedCPUCount))

			Expect(reserved.String()).To(Equal("0-3,8-11"))
			Expect(isolated.String()).To(Equal("4-7,15"))
			Expect(offlined.String()).To(Equal("12-14,16-31"))
		})

		It("diag0007 as sibling threads will be disabled by kernel they are not handled in the sets", func() {
			reservedCPUCount := 4
			offlinedCPUCount := 8
			splitReservedCPUsAcrossNUMA := false
			disableHT := true
			highPowerConsumptionMode := false

			reserved, isolated, offlined, err := CalculateCPUSets(&sysInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, highPowerConsumptionMode)
			Expect(err).ToNot(HaveOccurred())

			log.Infof("Input:")
			log.Infof("\treserved-cpu-count:\t\t%d", reservedCPUCount)
			log.Infof("\tofflined-cpu-count:\t\t%d", offlinedCPUCount)
			log.Infof("\tdisable-ht:\t\t\t%v", disableHT)
			log.Infof("\tsplitReservedCPUsAcrossNUMA:\t%v", splitReservedCPUsAcrossNUMA)
			log.Infof("\thighConsumptionMode:\t\t%v", highPowerConsumptionMode)
			log.Infof("Output:")
			log.Infof("\treserved: %s", reserved.String())
			log.Infof("\tisolated: %s", isolated.String())
			log.Infof("\tofflined: %s", offlined.String())

			Expect(offlined.Intersection(reserved).IsEmpty()).To(BeTrue())
			Expect(offlined.Intersection(isolated).IsEmpty()).To(BeTrue())
			Expect(offlined.Size()).To(Equal(offlinedCPUCount))

			Expect(reserved.String()).To(Equal("0-3"))
			Expect(isolated.String()).To(Equal("4-7"))
			Expect(offlined.String()).To(Equal("16-23"))
		})
	})
})

var _ = Describe("PerformanceProfileCreator: Populating Reserved and Isolated CPUs in Performance Profile", func() {
	var mustGatherDirAbsolutePath string
	var node *v1.Node
	var handle *GHWHandler
	var splitReservedCPUsAcrossNUMA, disableHT bool
	var reservedCPUCount int
	var offlinedCPUCount int
	var err error

	BeforeEach(func() {
		node = newTestNode("worker1")
	})
	Context("Check if reserved and isolated CPUs are properly populated in the performance profile", func() {
		It("Ensure reserved CPUs populated are correctly when splitReservedCPUsAcrossNUMA is disabled and disableHT is disabled", func() {
			reservedCPUCount = 20 // random number, no special meaning
			offlinedCPUCount = 0
			splitReservedCPUsAcrossNUMA = false
			disableHT = false
			highPowerConsumptionMode := false
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			reservedCPUSet, isolatedCPUSet, offlinedCPUSet, err := CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, highPowerConsumptionMode)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.String()).To(Equal("0,2,4,6,8,10,12,14,16,18,40,42,44,46,48,50,52,54,56,58"))
			Expect(isolatedCPUSet.String()).To(Equal("1,3,5,7,9,11,13,15,17,19-39,41,43,45,47,49,51,53,55,57,59-79"))
			Expect(offlinedCPUSet.String()).To(BeEmpty())
		})
		It("Ensure reserved CPUs populated are correctly when splitReservedCPUsAcrossNUMA is enabled and disableHT is disabled", func() {
			reservedCPUCount = 20 // random number, no special meaning
			offlinedCPUCount = 0
			splitReservedCPUsAcrossNUMA = true
			disableHT = false
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			reservedCPUSet, isolatedCPUSet, offlinedCPUSet, err := CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.String()).To(Equal("0-9,40-49"))
			Expect(isolatedCPUSet.String()).To(Equal("10-39,50-79"))
			Expect(offlinedCPUSet.String()).To(BeEmpty())
		})
		It("Errors out in case negative reservedCPUCount is specified", func() {
			reservedCPUCount = -2 // random negative number, no special meaning
			offlinedCPUCount = 0
			splitReservedCPUsAcrossNUMA = true
			disableHT = false
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			_, _, _, err = CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, false)
			Expect(err).To(HaveOccurred())
		})
		It("Errors out in case specified reservedCPUCount is greater than the total CPUs present in the system and disableHT is disabled", func() {
			reservedCPUCount = 100 // random positive number greater than that total number of CPUs
			offlinedCPUCount = 0
			splitReservedCPUsAcrossNUMA = true
			disableHT = false
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			_, _, _, err = CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, false)
			Expect(err).To(HaveOccurred())
		})
		It("Errors out in case hyperthreading is enabled, splitReservedCPUsAcrossNUMA is enabled, disableHT is disabled and number of reserved CPUs per number of NUMA nodes are odd", func() {
			reservedCPUCount = 21 // random number which results in a CPU split per NUMA node (11 + 10 in this case) such that odd number of reserved CPUs (11) have to be allocated from a NUMA node
			offlinedCPUCount = 0
			splitReservedCPUsAcrossNUMA = true
			disableHT = false
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			_, _, _, err = CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, false)
			Expect(err).To(HaveOccurred())
		})
		It("Errors out in case hyperthreading is enabled, splitReservedCPUsAcrossNUMA is disabled,, disableHT is disabled and number of reserved CPUs are odd", func() {
			reservedCPUCount = 21 // random number which results in odd number (21) of CPUs to be allocated from a NUMA node
			offlinedCPUCount = 0
			splitReservedCPUsAcrossNUMA = false
			disableHT = false
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			_, _, _, err = CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, false)
			Expect(err).To(HaveOccurred())
		})
		It("Ensure reserved CPUs populated are correctly when splitReservedCPUsAcrossNUMA is disabled, disableHT is enabled", func() {
			reservedCPUCount = 20 // random number, no special meaning
			offlinedCPUCount = 0
			splitReservedCPUsAcrossNUMA = false
			disableHT = true
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			reservedCPUSet, isolatedCPUSet, offlinedCPUSet, err := CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.String()).To(Equal("0,2,4,6,8,10,12,14,16,18,20,22,24,26,28,30,32,34,36,38"))
			Expect(isolatedCPUSet.String()).To(Equal("1,3,5,7,9,11,13,15,17,19,21,23,25,27,29,31,33,35,37,39"))
			Expect(offlinedCPUSet.String()).To(BeEmpty())
		})
		It("Ensure reserved CPUs populated are correctly when splitReservedCPUsAcrossNUMA is enabled and disableHT is enabled", func() {
			reservedCPUCount = 20 // random number, no special meaning
			offlinedCPUCount = 0
			splitReservedCPUsAcrossNUMA = true
			disableHT = true
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			reservedCPUSet, isolatedCPUSet, offlinedCPUSet, err := CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.String()).To(Equal("0-19"))
			Expect(isolatedCPUSet.String()).To(Equal("20-39"))
			Expect(offlinedCPUSet.String()).To(BeEmpty())
		})
		It("Do not error out in case hyperthreading is currently enabled, splitReservedCPUsAcrossNUMA is disabled, disableHT is enabled and number of reserved CPUs allocated from a NUMA node are odd", func() {
			reservedCPUCount = 11 // random number which results in odd number (11) of CPUs to be allocated from a NUMA node
			offlinedCPUCount = 0
			splitReservedCPUsAcrossNUMA = false
			disableHT = true
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			_, _, _, err = CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, false)
			Expect(err).ToNot(HaveOccurred())
		})
		It("Do not error out in case hyperthreading is currently enabled, splitReservedCPUsAcrossNUMA is enabled, disableHT is enabled and number of reserved CPUs allocated from a NUMA node are odd", func() {
			reservedCPUCount = 2 // random number which results in odd number (1) of CPUs to be allocated from a NUMA node
			offlinedCPUCount = 0
			splitReservedCPUsAcrossNUMA = true
			disableHT = true
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			_, _, _, err = CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, false)
			Expect(err).ToNot(HaveOccurred())
		})
		It("Do not error out in case of a system where hyperthreading is not enabled initially, splitReservedCPUsAcrossNUMA is disabled, disableHT is enabled and number of reserved CPUs allocated are odd", func() {
			node = newTestNode("ocp47sno-master-0.demo.lab")
			reservedCPUCount = 3 // random number which results in odd number (3) of CPUs to be allocated
			offlinedCPUCount = 0
			splitReservedCPUsAcrossNUMA = false
			disableHT = true
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherSNODirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			_, _, _, err = CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, false)
			Expect(err).ToNot(HaveOccurred())
		})

	})
	Context("with new offlined parameter, check if reserved, isolated and offlined CPUs are properly populated in the performance profile", func() {
		It("Errors out in case negative offlinedCPUCount is specified", func() {
			reservedCPUCount = 20 // random number, no meaning
			offlinedCPUCount = -1 // random negative number, no special meaning
			splitReservedCPUsAcrossNUMA = true
			disableHT = false
			highPowerConsumptionMode := false
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			_, _, _, err = CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, highPowerConsumptionMode)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("please specify the offlined CPU count in the range"))
		})

		It("Errors out in case offlinedCPUCount specified is greater than the number of CPUs in system", func() {
			reservedCPUCount = 20  // random number, no meaning
			offlinedCPUCount = 100 // random number should be greater than the actual number of CPUs in must-gather
			splitReservedCPUsAcrossNUMA = true
			disableHT = false
			highPowerConsumptionMode := false
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			_, _, _, err = CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, highPowerConsumptionMode)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("please specify the offlined CPU count in the range"))
		})
		It("Errors out in case offlinedCPUCount plus reservedCPUCount specified is greater than the number of CPUs", func() {
			reservedCPUCount = 40
			offlinedCPUCount = 41
			splitReservedCPUsAcrossNUMA = true
			disableHT = false
			highPowerConsumptionMode := false

			Expect(reservedCPUCount + offlinedCPUCount).To(BeNumerically(">", 80))
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			_, _, _, err = CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, highPowerConsumptionMode)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("please ensure that reserved-cpu-count plus offlined-cpu-count should be in the range"))
		})
		It("with disable-ht true siblings does NOT count: Errors out in case offlinedCPUCount plus reservedCPUCount specified is greater than the number of CPUs", func() {
			reservedCPUCount = 20
			offlinedCPUCount = 21
			splitReservedCPUsAcrossNUMA = true
			disableHT = true
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			_, _, _, err = CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("please ensure that reserved-cpu-count plus offlined-cpu-count should be in the range"))
		})
		It("with disable-ht true siblings does NOT count: Errors out in case offlinedCPUCount specified is greater than the number of CPUs in system", func() {
			reservedCPUCount = 10
			offlinedCPUCount = 41 // should be greater than the number of cores
			splitReservedCPUsAcrossNUMA = true
			disableHT = true
			highPowerConsumptionMode := false
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			_, _, _, err = CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, highPowerConsumptionMode)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("please specify the offlined CPU count in the range"))
		})
		It("Everything is ok in case offlinedCPUCount plus reservedCPUCount specified is equal to the number of CPUs in system", func() {
			reservedCPUCount = 20 // random number, no meaning
			offlinedCPUCount = 59 // random number --> both `reservedCPUCount` plus `offlinedCPUCount` should sum 79 (which is the number of CPUs in the must-gather minus 1)
			Expect(reservedCPUCount + offlinedCPUCount).To(Equal(79))
			splitReservedCPUsAcrossNUMA = true
			disableHT = false
			highPowerConsumptionMode := false

			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			_, _, _, err = CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, highPowerConsumptionMode)
			Expect(err).NotTo(HaveOccurred())
		})
		It("when splitReservedCPUsAcrossNUMA is disabled, disableHT is disabled and offlined-cpu-count is greater than number of cpus per socket then offline a complete socket", func() {
			reservedCPUCount = 20 // random number, no special meaning
			offlinedCPUCount = 40 // NOT A random number: It is the number of CPUS in a socket
			splitReservedCPUsAcrossNUMA = false
			disableHT = false
			highPowerConsumptionMode := false

			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			reservedCPUSet, isolatedCPUSet, offlinedCPUSet, err := CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, highPowerConsumptionMode)
			Expect(err).ToNot(HaveOccurred())

			Expect(offlinedCPUSet.Intersection(reservedCPUSet).IsEmpty()).To(BeTrue())
			Expect(offlinedCPUSet.Intersection(isolatedCPUSet).IsEmpty()).To(BeTrue())
			Expect(offlinedCPUSet.Size()).To(Equal(offlinedCPUCount))

			log.Infof("offlined:%s", offlinedCPUSet.String())
			log.Infof("reserved:%s", reservedCPUSet.String())
			log.Infof("isolated:%s", isolatedCPUSet.String())

			totalCPUSet, err := GetTotalCPUSetFromGHW(handle, disableHT)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.Union(offlinedCPUSet).Union(isolatedCPUSet)).To(Equal(totalCPUSet))

			Expect(offlinedCPUSet.String()).To(Equal("1,3,5,7,9,11,13,15,17,19,21,23,25,27,29,31,33,35,37,39,41,43,45,47,49,51,53,55,57,59,61,63,65,67,69,71,73,75,77,79"))
			Expect(reservedCPUSet.String()).To(Equal("0,2,4,6,8,10,12,14,16,18,40,42,44,46,48,50,52,54,56,58"))
			Expect(isolatedCPUSet.String()).To(Equal("20,22,24,26,28,30,32,34,36,38,60,62,64,66,68,70,72,74,76,78"))
		})
		It("when splitReservedCPUsAcrossNUMA is enabled, disableHT is disabled and offlined-cpu-count is greater than number of cpus per socket then can NOT offline a complete socket", func() {
			reservedCPUCount = 20 // random number, no special meaning
			offlinedCPUCount = 40 // NOT A random number: It is the number of CPUS in a socket
			splitReservedCPUsAcrossNUMA = true
			disableHT = false
			highPowerConsumptionMode := false

			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			reservedCPUSet, isolatedCPUSet, offlinedCPUSet, err := CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, highPowerConsumptionMode)
			Expect(err).ToNot(HaveOccurred())

			Expect(offlinedCPUSet.Intersection(reservedCPUSet).IsEmpty()).To(BeTrue())
			Expect(offlinedCPUSet.Intersection(isolatedCPUSet).IsEmpty()).To(BeTrue())
			Expect(offlinedCPUSet.Size()).To(Equal(offlinedCPUCount))

			log.Infof("offlined:%s", offlinedCPUSet.String())
			log.Infof("reserved:%s", reservedCPUSet.String())
			log.Infof("isolated:%s", isolatedCPUSet.String())

			totalCPUSet, err := GetTotalCPUSetFromGHW(handle, disableHT)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.Union(offlinedCPUSet).Union(isolatedCPUSet)).To(Equal(totalCPUSet))

			Expect(offlinedCPUSet.String()).To(Equal("10,12,14,16,18,20,22,24,26,28,50-79"))
			Expect(reservedCPUSet.String()).To(Equal("0-9,40-49"))
			Expect(isolatedCPUSet.String()).To(Equal("11,13,15,17,19,21,23,25,27,29-39"))
		})

		It("when in high power consumption mode, splitReservedCPUsAcrossNUMA is disabled, disableHT is disabled and offlined-cpu-count is greater than number of cpus per socket then do NOT offline a complete socket", func() {
			reservedCPUCount = 20 // random number, no special meaning
			offlinedCPUCount = 40 // NOT A random number: It is the number of CPUS in a socket
			splitReservedCPUsAcrossNUMA = false
			disableHT = false
			highPowerConsumptionMode := true

			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			reservedCPUSet, isolatedCPUSet, offlinedCPUSet, err := CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, highPowerConsumptionMode)
			Expect(err).ToNot(HaveOccurred())

			Expect(offlinedCPUSet.Intersection(reservedCPUSet).IsEmpty()).To(BeTrue())
			Expect(offlinedCPUSet.Intersection(isolatedCPUSet).IsEmpty()).To(BeTrue())
			Expect(offlinedCPUSet.Size()).To(Equal(offlinedCPUCount))

			log.Infof("offlined:%s", offlinedCPUSet.String())
			log.Infof("reserved:%s", reservedCPUSet.String())
			log.Infof("isolated:%s", isolatedCPUSet.String())

			totalCPUSet, err := GetTotalCPUSetFromGHW(handle, disableHT)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.Union(offlinedCPUSet).Union(isolatedCPUSet)).To(Equal(totalCPUSet))

			Expect(offlinedCPUSet.String()).To(Equal("20,22,24,26,28,30,32,34,36,38,41,43,45,47,49,51,53,55,57,59-79"))
			Expect(reservedCPUSet.String()).To(Equal("0,2,4,6,8,10,12,14,16,18,40,42,44,46,48,50,52,54,56,58"))
			Expect(isolatedCPUSet.String()).To(Equal("1,3,5,7,9,11,13,15,17,19,21,23,25,27,29,31,33,35,37,39"))
		})

		It("when splitReservedCPUsAcrossNUMA is disabled, disableHT is disabled and offlined-cpu-count is less than number of cpus per socket then do NOT offline a complete socket", func() {
			reservedCPUCount = 20 // random number, no special meaning
			offlinedCPUCount = 20 // NOT A random number: Should be less than the number of CPUS in a socket
			splitReservedCPUsAcrossNUMA = false
			disableHT = false
			highPowerConsumptionMode := false

			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			reservedCPUSet, isolatedCPUSet, offlinedCPUSet, err := CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, highPowerConsumptionMode)
			Expect(err).ToNot(HaveOccurred())

			Expect(offlinedCPUSet.Intersection(reservedCPUSet).IsEmpty()).To(BeTrue())
			Expect(offlinedCPUSet.Intersection(isolatedCPUSet).IsEmpty()).To(BeTrue())
			Expect(offlinedCPUSet.Size()).To(Equal(offlinedCPUCount))

			log.Infof("offlined:%s", offlinedCPUSet.String())
			log.Infof("reserved:%s", reservedCPUSet.String())
			log.Infof("isolated:%s", isolatedCPUSet.String())

			totalCPUSet, err := GetTotalCPUSetFromGHW(handle, disableHT)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.Union(offlinedCPUSet).Union(isolatedCPUSet)).To(Equal(totalCPUSet))

			Expect(offlinedCPUSet.String()).To(Equal("41,43,45,47,49,51,53,55,57,59-60,62,64,66,68,70,72,74,76,78"))
			Expect(reservedCPUSet.String()).To(Equal("0,2,4,6,8,10,12,14,16,18,40,42,44,46,48,50,52,54,56,58"))
			Expect(isolatedCPUSet.String()).To(Equal("1,3,5,7,9,11,13,15,17,19-39,61,63,65,67,69,71,73,75,77,79"))
		})

		It("when splitReservedCPUsAcrossNUMA is disabled, disableHT is disabled and offlined-cpu-count is greater than number of cpus per socket then offline a complete socket and then go for siblings", func() {
			reservedCPUCount = 20 // random number, no special meaning
			offlinedCPUCount = 45 // NOT A random number: Should be less than the number of CPUS in a socket
			splitReservedCPUsAcrossNUMA = false
			disableHT = false
			highPowerConsumptionMode := false

			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			reservedCPUSet, isolatedCPUSet, offlinedCPUSet, err := CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, highPowerConsumptionMode)
			Expect(err).ToNot(HaveOccurred())

			Expect(offlinedCPUSet.Intersection(reservedCPUSet).IsEmpty()).To(BeTrue())
			Expect(offlinedCPUSet.Intersection(isolatedCPUSet).IsEmpty()).To(BeTrue())
			Expect(offlinedCPUSet.Size()).To(Equal(offlinedCPUCount))

			log.Infof("offlined:%s", offlinedCPUSet.String())
			log.Infof("reserved:%s", reservedCPUSet.String())
			log.Infof("isolated:%s", isolatedCPUSet.String())

			totalCPUSet, err := GetTotalCPUSetFromGHW(handle, disableHT)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.Union(offlinedCPUSet).Union(isolatedCPUSet)).To(Equal(totalCPUSet))

			Expect(offlinedCPUSet.String()).To(Equal("1,3,5,7,9,11,13,15,17,19,21,23,25,27,29,31,33,35,37,39,41,43,45,47,49,51,53,55,57,59-69,71,73,75,77,79"))
			Expect(reservedCPUSet.String()).To(Equal("0,2,4,6,8,10,12,14,16,18,40,42,44,46,48,50,52,54,56,58"))
			Expect(isolatedCPUSet.String()).To(Equal("20,22,24,26,28,30,32,34,36,38,70,72,74,76,78"))
		})
		It("when splitReservedCPUsAcrossNUMA is disabled, disableHT is enabled and offlined-cpu-count is the number of cpus per socket then siblings does not enter the calcs and offline a complete", func() {
			reservedCPUCount = 10 // random number, no special meaning
			offlinedCPUCount = 20 // NOT A random number: number of CPUs in a socket (without siblings)
			splitReservedCPUsAcrossNUMA = false
			disableHT = true
			highPowerConsumptionMode := false

			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			reservedCPUSet, isolatedCPUSet, offlinedCPUSet, err := CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, highPowerConsumptionMode)
			Expect(err).ToNot(HaveOccurred())

			Expect(offlinedCPUSet.Intersection(reservedCPUSet).IsEmpty()).To(BeTrue())
			Expect(offlinedCPUSet.Intersection(isolatedCPUSet).IsEmpty()).To(BeTrue())
			Expect(offlinedCPUSet.Size()).To(Equal(offlinedCPUCount))

			log.Infof("offlined:%s", offlinedCPUSet.String())
			log.Infof("reserved:%s", reservedCPUSet.String())
			log.Infof("isolated:%s", isolatedCPUSet.String())

			totalCPUSet, err := GetTotalCPUSetFromGHW(handle, disableHT)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.Union(offlinedCPUSet).Union(isolatedCPUSet)).To(Equal(totalCPUSet))

			Expect(offlinedCPUSet.String()).To(Equal("1,3,5,7,9,11,13,15,17,19,21,23,25,27,29,31,33,35,37,39"))
			Expect(reservedCPUSet.String()).To(Equal("0,2,4,6,8,10,12,14,16,18"))
			Expect(isolatedCPUSet.String()).To(Equal("20,22,24,26,28,30,32,34,36,38"))
		})
		It("Ensure offlined CPUs populated are correctly when splitReservedCPUsAcrossNUMA is disabled, disableHT is enabled and offlinedCount is less than the number of CPUs in a socket", func() {
			reservedCPUCount = 20 // random number, no special meaning
			offlinedCPUCount = 4  // NOT a random number. Should be less than 40 (which is the number of CPUS in one socket)
			splitReservedCPUsAcrossNUMA = false
			disableHT = true
			mustGatherDirAbsolutePath, err = filepath.Abs(mustGatherDirPath)
			Expect(err).ToNot(HaveOccurred())
			handle, err = NewGHWHandler(mustGatherDirAbsolutePath, node)
			Expect(err).ToNot(HaveOccurred())
			systemInfo, err := handle.GatherSystemInfo()
			Expect(err).ToNot(HaveOccurred())
			reservedCPUSet, isolatedCPUSet, offlinedCPUSet, err := CalculateCPUSets(systemInfo, reservedCPUCount, offlinedCPUCount, splitReservedCPUsAcrossNUMA, disableHT, false)
			Expect(err).ToNot(HaveOccurred())

			Expect(offlinedCPUSet.Intersection(reservedCPUSet).IsEmpty()).To(BeTrue())
			Expect(offlinedCPUSet.Intersection(isolatedCPUSet).IsEmpty()).To(BeTrue())
			Expect(offlinedCPUSet.Size()).To(Equal(offlinedCPUCount))

			log.Infof("offlined:%s", offlinedCPUSet.String())
			log.Infof("reserved:%s", reservedCPUSet.String())
			log.Infof("isolated:%s", isolatedCPUSet.String())

			totalCPUSet, err := GetTotalCPUSetFromGHW(handle, disableHT)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.Union(offlinedCPUSet).Union(isolatedCPUSet)).To(Equal(totalCPUSet))

			Expect(offlinedCPUSet.String()).To(Equal("1,5,7,9"))
			Expect(reservedCPUSet.String()).To(Equal("0,2,4,6,8,10,12,14,16,18,20,22,24,26,28,30,32,34,36,38"))
			Expect(isolatedCPUSet.String()).To(Equal("3,11,13,15,17,19,21,23,25,27,29,31,33,35,37,39"))
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
	var reservedCPUCount int
	var topologyInfoNodes, htDisabledTopologyInfoNodes []*topology.Node
	var htEnabled bool

	BeforeEach(func() {
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
			offlinedCPUs := cpuset.CPUSet{}
			htEnabled = true

			reservedCPUSet, err := getCPUsSplitAcrossNUMA(reservedCPUCount, htEnabled, topologyInfoNodes)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.String()).To(Equal("0-9,40-49"))

			isolatedCPUSet, err := getIsolatedCPUs(topologyInfoNodes, reservedCPUSet, offlinedCPUs)
			Expect(err).ToNot(HaveOccurred())
			Expect(isolatedCPUSet.String()).To(Equal("10-39,50-79"))
		})
		It("Ensure reserved and isolated CPUs populated are correctly by getCPUsSplitAcrossNUMAwhen when splitReservedCPUsAcrossNUMA is enabled and htEnabled is disabled", func() {
			reservedCPUCount = 20 // random number, no special meaning
			offlinedCPUs := cpuset.CPUSet{}
			htEnabled = false

			reservedCPUSet, err := getCPUsSplitAcrossNUMA(reservedCPUCount, htEnabled, htDisabledTopologyInfoNodes)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.String()).To(Equal("0-19"))

			isolatedCPUSet, err := getIsolatedCPUs(htDisabledTopologyInfoNodes, reservedCPUSet, offlinedCPUs)
			Expect(err).ToNot(HaveOccurred())
			Expect(isolatedCPUSet.String()).To(Equal("20-39"))
		})
		It("Errors out in case hyperthreading is enabled, splitReservedCPUsAcrossNUMA is enabled, htEnabled is enabled and the number of reserved CPUs per number of NUMA nodes are odd", func() {
			reservedCPUCount = 21 // random number which results in a CPU split per NUMA node (11 + 10 in this case) such that odd number of reserved CPUs (11) have to be allocated from a NUMA node
			htEnabled = true

			_, err := getCPUsSplitAcrossNUMA(reservedCPUCount, htEnabled, topologyInfoNodes)
			Expect(err).To(HaveOccurred())
		})
		It("Works without error in case hyperthreading is disabled, splitReservedCPUsAcrossNUMA is enabled, htEnabled is disabled and number of reserved CPUs per number of NUMA nodes are odd", func() {
			reservedCPUCount = 11 // random number which results in a CPU split per NUMA node (5 + 6 in this case) such that odd number of reserved CPUs (5) have to be allocated from a NUMA node
			htEnabled = false

			_, err := getCPUsSplitAcrossNUMA(reservedCPUCount, htEnabled, topologyInfoNodes)
			Expect(err).ToNot(HaveOccurred())
		})
		It("Ensure reserved and isolated CPUs populated are correctly by getCPUsSequentially when splitReservedCPUsAcrossNUMA is disabled", func() {
			reservedCPUCount = 20 // random number, no special meaning
			offlinedCPUs := cpuset.CPUSet{}
			htEnabled = true

			reservedCPUSet, err := getCPUsSequentially(reservedCPUCount, htEnabled, topologyInfoNodes)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedCPUSet.String()).To(Equal("0,2,4,6,8,10,12,14,16,18,40,42,44,46,48,50,52,54,56,58"))

			isolatedCPUSet, err := getIsolatedCPUs(topologyInfoNodes, reservedCPUSet, offlinedCPUs)
			Expect(err).ToNot(HaveOccurred())
			Expect(isolatedCPUSet.String()).To(Equal("1,3,5,7,9,11,13,15,17,19-39,41,43,45,47,49,51,53,55,57,59-79"))
		})
		It("Errors out in case hyperthreading is enabled, splitReservedCPUsAcrossNUMA is disabled and number of reserved CPUs are odd", func() {
			reservedCPUCount = 21 // random number which results in odd number (21) of CPUs to be allocated from a NUMA node
			htEnabled = true

			_, err := getCPUsSequentially(reservedCPUCount, htEnabled, topologyInfoNodes)
			Expect(err).To(HaveOccurred())
		})
		It("Works without error in case hyperthreading is disabled, splitReservedCPUsAcrossNUMA is disabled and number of reserved CPUs are odd", func() {
			reservedCPUCount = 11 // random number which results in odd number (11) of CPUs to be allocated from a NUMA node
			htEnabled = false

			_, err := getCPUsSequentially(reservedCPUCount, htEnabled, topologyInfoNodes)
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
	var disableHT bool

	Context("with hyper-threading enabled", func() {
		It("should not append additional kernel arguments", func() {
			disableHT = false
			kernelArgs := GetAdditionalKernelArgs(disableHT)
			Expect(kernelArgs).To(BeEmpty())
		})
	})

	Context("with hyper-threading disable", func() {
		It("should append nosmt argument", func() {
			disableHT = true
			expectedArgs := []string{"nosmt"}
			sort.Strings(expectedArgs) // sort to avoid inequality due to difference in order
			kernelArgs := GetAdditionalKernelArgs(disableHT)
			Expect(kernelArgs).To(BeEquivalentTo(expectedArgs))
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
				_, err := acc.AddCores(allCores, node.Cores)
				Expect(err).NotTo(HaveOccurred())
			}
			cores := acc.Result().List()
			Expect(cores).Should(Equal([]int{0, 1, 2, 3, 4, 5, 6, 7}))
		})
		It("should accumulate cores up to the max", func() {
			acc := newCPUAccumulator()
			for _, node := range topology1.Nodes {
				_, err := acc.AddCores(3, node.Cores)
				Expect(err).NotTo(HaveOccurred())
			}
			cores := acc.Result().List()
			Expect(cores).Should(Equal([]int{0, 1, 2}))
		})

		It("should not count elements already in the accumulator", func() {
			acc := newCPUAccumulator()
			node := topology1.Nodes[0]
			n1, err := acc.AddCores(allCores, node.Cores)
			Expect(err).NotTo(HaveOccurred())
			Expect(n1).To(Equal(4))

			n2, err := acc.AddCores(allCores, node.Cores)
			Expect(err).NotTo(HaveOccurred())
			Expect(n2).To(Equal(0))

			cores := acc.Result().List()
			Expect(cores).Should(Equal([]int{0, 1, 2, 3}))
		})

		It("should not be modified after calling Result", func() {
			acc := newCPUAccumulator()
			node := topology1.Nodes[0]
			n1, err := acc.AddCores(allCores, node.Cores)
			Expect(err).NotTo(HaveOccurred())
			Expect(n1).To(Equal(4))

			cores := acc.Result().List()
			Expect(cores).Should(Equal([]int{0, 1, 2, 3}))

			_, err = acc.AddCores(allCores, node.Cores)
			Expect(err).To(HaveOccurred())
		})

	})
})

func GetTotalCPUSetFromCPUInfo(cpuInfo cpu.Info, disableHT bool) (cpuset.CPUSet, error) {
	acc := newCPUAccumulator()
	numSiblings := 0
	for _, socket := range cpuInfo.Processors {
		var err error
		if disableHT {
			_, err = acc.AddCoresWithFilter(allCores, socket.Cores, func(index, lpID int) bool {
				if index == 0 {
					numSiblings++
					return true
				}
				return false
			})
		} else {
			_, err = acc.AddCores(allCores, socket.Cores)
		}

		if err != nil {
			return cpuset.CPUSet{}, err
		}
	}

	ret := acc.Result()
	if cpuInfo.TotalThreads-uint32(numSiblings) != uint32(ret.Size()) {
		return cpuset.CPUSet{}, fmt.Errorf("error calculating total logical processors. Shold be %d but found %d", cpuInfo.TotalThreads, ret.Size())
	}

	return ret, nil
}

func GetTotalCPUSetFromGHW(handler *GHWHandler, disableHT bool) (cpuset.CPUSet, error) {
	sortedCpuInfo, err := handler.SortedCPU()
	if err != nil {
		return cpuset.CPUSet{}, err
	}
	totalCPUSet, err := GetTotalCPUSetFromCPUInfo(*sortedCpuInfo, disableHT)
	if err != nil {
		return cpuset.CPUSet{}, err
	}
	return totalCPUSet, nil
}
