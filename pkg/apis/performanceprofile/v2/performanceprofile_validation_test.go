package v2

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

const (
	NodeSelectorRole = "barRole"
)

const (
	// HugePageSize defines the huge page size used for tests
	HugePageSize1G = HugePageSize("1G")
	// HugePagesCount defines the huge page count used for tests
	HugePagesCount = 4
	// IsolatedCPUs defines the isolated CPU set used for tests
	IsolatedCPUs = CPUSet("4-6")
	// ReservedCPUs defines the reserved CPU set used for tests
	ReservedCPUs = CPUSet("0-3")
	// ReservedCPUs defines the reserved CPU set used for tests
	OfflinedCPUs = CPUSet("7")
	// SingleNUMAPolicy defines the topologyManager policy used for tests
	SingleNUMAPolicy = "single-numa-node"
	//MachineConfigLabelKey defines the MachineConfig label key of the test profile
	MachineConfigLabelKey = "mcKey"
	//MachineConfigLabelValue defines the MachineConfig label value of the test profile
	MachineConfigLabelValue = "mcValue"
	//MachineConfigPoolLabelKey defines the MachineConfigPool label key of the test profile
	MachineConfigPoolLabelKey = "mcpKey"
	//MachineConfigPoolLabelValue defines the MachineConfigPool label value of the test profile
	MachineConfigPoolLabelValue = "mcpValue"
	//NetDeviceName defines a net device name for the test profile
	NetDeviceName = "enp0s4"
	//NetDeviceVendorID defines a net device vendor ID for the test profile
	NetDeviceVendorID = "0x1af4"
	//NetDeviceModelID defines a net device model ID for the test profile
	NetDeviceModelID = "0x1000"
)

// This type is used to define the inputs for the validator client
type NodeSpecifications struct {
	architecture string
	cpuCapacity  int64
	name         string
}

// Get a fake node object with a specified architecture and cpu capacity
func GetFakeNode(specs NodeSpecifications) corev1.Node {
	Expect(specs.architecture).To(BeElementOf([2]string{amd64, aarch64}))
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:            specs.name,
			ResourceVersion: "1.0",
			Labels: map[string]string{
				"nodekey": "nodeValue",
			},
		},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU: *resource.NewMilliQuantity(specs.cpuCapacity, resource.DecimalSI),
			},
			NodeInfo: corev1.NodeSystemInfo{
				Architecture: specs.architecture,
			},
		},
	}
}

func GetFakeValidatorClient(nodeSpecs []NodeSpecifications) client.Client {
	// Create all the nodes first from the provided specifications
	nodes := []corev1.Node{}
	for _, node := range nodeSpecs {
		nodes = append(nodes, GetFakeNode(node))
	}

	// Convert the slice of nodes into a NodeList object
	nodeList := corev1.NodeList{}
	nodeList.Items = nodes

	// Build the client with the new NodeList included
	return fake.NewClientBuilder().WithLists(&nodeList).Build()
}

// NewPerformanceProfile returns new performance profile object that used for tests
func NewPerformanceProfile(name string) *PerformanceProfile {
	size := HugePageSize1G
	isolatedCPUs := IsolatedCPUs
	reservedCPUs := ReservedCPUs
	offlinedCPUs := OfflinedCPUs
	numaPolicy := SingleNUMAPolicy

	netDeviceName := NetDeviceName
	netDeviceVendorID := NetDeviceVendorID
	netDeviceModelID := NetDeviceModelID

	return &PerformanceProfile{
		TypeMeta: metav1.TypeMeta{Kind: "PerformanceProfile"},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			UID:  "11111111-1111-1111-1111-1111111111111",
		},
		Spec: PerformanceProfileSpec{
			CPU: &CPU{
				Isolated: &isolatedCPUs,
				Reserved: &reservedCPUs,
				Offlined: &offlinedCPUs,
			},
			HugePages: &HugePages{
				DefaultHugePagesSize: &size,
				Pages: []HugePage{
					{
						Count: HugePagesCount,
						Size:  size,
					},
				},
			},
			RealTimeKernel: &RealTimeKernel{
				Enabled: pointer.Bool(true),
			},
			NUMA: &NUMA{
				TopologyPolicy: &numaPolicy,
			},
			Net: &Net{
				UserLevelNetworking: pointer.Bool(true),
				Devices: []Device{
					{
						InterfaceName: &netDeviceName,
						VendorID:      &netDeviceVendorID,
						DeviceID:      &netDeviceModelID,
					},
				},
			},
			MachineConfigLabel: map[string]string{
				MachineConfigLabelKey: MachineConfigLabelValue,
			},
			MachineConfigPoolSelector: map[string]string{
				MachineConfigPoolLabelKey: MachineConfigPoolLabelValue,
			},
			NodeSelector: map[string]string{
				"nodekey": "nodeValue",
			},
		},
	}
}

var _ = Describe("PerformanceProfile", func() {
	var profile *PerformanceProfile

	BeforeEach(func() {
		profile = NewPerformanceProfile("test")
	})

	Describe("CPU validation", func() {
		It("should have CPU fields populated", func() {
			errors := profile.validateCPUs()
			Expect(errors).To(BeEmpty(), "should not have validation errors with populated CPU fields")

			profile.Spec.CPU.Isolated = nil
			errors = profile.validateCPUs()
			Expect(errors).NotTo(BeEmpty(), "should have validation error with missing CPU Isolated field")
			Expect(errors[0].Error()).To(ContainSubstring("isolated CPUs required"))

			cpus := CPUSet("0")
			profile.Spec.CPU.Isolated = &cpus
			profile.Spec.CPU.Reserved = nil
			errors = profile.validateCPUs()
			Expect(errors).NotTo(BeEmpty(), "should have validation error with missing CPU reserved field")
			Expect(errors[0].Error()).To(ContainSubstring("reserved CPUs required"))

			invalidCPUs := CPUSet("bla")
			profile.Spec.CPU.Isolated = &invalidCPUs
			errors = profile.validateCPUs()
			Expect(errors).NotTo(BeEmpty(), "should have validation error when isolated CPUs has invalid format")

			profile.Spec.CPU = nil
			errors = profile.validateCPUs()
			Expect(errors).NotTo(BeEmpty(), "should have validation error with missing CPU")
			Expect(errors[0].Error()).To(ContainSubstring("cpu section required"))
		})

		It("should reject cpus allocation with no reserved CPUs", func() {
			reservedCPUs := CPUSet("")
			isolatedCPUs := CPUSet("0-6")
			offlinedCPUs := CPUSet("7")
			profile.Spec.CPU.Reserved = &reservedCPUs
			profile.Spec.CPU.Isolated = &isolatedCPUs
			profile.Spec.CPU.Offlined = &offlinedCPUs
			errors := profile.validateCPUs()
			Expect(errors[0].Error()).To(ContainSubstring("reserved CPUs can not be empty"))
		})

		It("should reject cpus allocation with no isolated CPUs", func() {
			reservedCPUs := CPUSet("0-3")
			isolatedCPUs := CPUSet("")
			profile.Spec.CPU.Reserved = &reservedCPUs
			profile.Spec.CPU.Isolated = &isolatedCPUs
			errors := profile.validateCPUs()
			Expect(errors).NotTo(BeEmpty())
			Expect(errors[0].Error()).To(ContainSubstring("isolated CPUs can not be empty"))
		})

		It("should allow cpus allocation with no offlined CPUs", func() {
			cpusIsolaled := CPUSet("0")
			cpusReserved := CPUSet("1")
			profile.Spec.CPU.Isolated = &cpusIsolaled
			profile.Spec.CPU.Reserved = &cpusReserved
			profile.Spec.CPU.Offlined = nil
			errors := profile.validateCPUs()
			Expect(errors).To(BeEmpty())
		})

		It("should reject cpus allocation with overlapping sets between reserved and isolated", func() {
			reservedCPUs := CPUSet("0-7")
			isolatedCPUs := CPUSet("0-15")
			profile.Spec.CPU.Reserved = &reservedCPUs
			profile.Spec.CPU.Isolated = &isolatedCPUs
			profile.Spec.CPU.Offlined = nil
			errors := profile.validateCPUs()
			Expect(errors).NotTo(BeEmpty(), "should have validation error when reserved and isolation CPUs have overlap")
			Expect(errors[0].Error()).To(Or(ContainSubstring("reserved and isolated cpus overlap"), ContainSubstring("isolated and reserved cpus overlap")))
		})

		It("should reject cpus allocation with overlapping sets between reserved and offlined", func() {
			reservedCPUs := CPUSet("0-7")
			isolatedCPUs := CPUSet("8-11")
			offlinedCPUs := CPUSet("0,12-15")
			profile.Spec.CPU.Reserved = &reservedCPUs
			profile.Spec.CPU.Isolated = &isolatedCPUs
			profile.Spec.CPU.Offlined = &offlinedCPUs
			errors := profile.validateCPUs()
			Expect(errors).NotTo(BeEmpty(), "should have validation error when reserved and offlined CPUs have overlap")
			Expect(errors[0].Error()).To(Or(ContainSubstring("reserved and offlined cpus overlap"), ContainSubstring("offlined and reserved cpus overlap")))
		})

		It("should reject cpus allocation with overlapping sets between isolated and offlined", func() {
			reservedCPUs := CPUSet("0-7")
			isolatedCPUs := CPUSet("8-11")
			offlinedCPUs := CPUSet("10-15")
			profile.Spec.CPU.Reserved = &reservedCPUs
			profile.Spec.CPU.Isolated = &isolatedCPUs
			profile.Spec.CPU.Offlined = &offlinedCPUs
			errors := profile.validateCPUs()
			Expect(errors).NotTo(BeEmpty(), "should have validation error when isolated and offlined CPUs have overlap")
			Expect(errors[0].Error()).To(Or(ContainSubstring("isolated and offlined cpus overlap"), ContainSubstring("offlined and isolated cpus overlap")))
		})

		It("should reject cpus allocation with overlapping sets between isolated and shared", func() {
			reservedCPUs := CPUSet("0-6")
			isolatedCPUs := CPUSet("8-11")
			sharedCPUs := CPUSet("10-15")
			profile.Spec.CPU.Reserved = &reservedCPUs
			profile.Spec.CPU.Isolated = &isolatedCPUs
			profile.Spec.CPU.Shared = &sharedCPUs
			errors := profile.validateCPUs()
			Expect(errors).NotTo(BeEmpty(), "should have validation error when isolated and shared CPUs have overlap")
			Expect(errors[0].Error()).To(Or(ContainSubstring("isolated and shared cpus overlap"), ContainSubstring("shared and isolated cpus overlap")))
		})
	})

	Describe("CPU Frequency validation", func() {
		It("should reject if isolated CPU frequency is declared, while reserved CPU frequency is empty", func() {
			isolatedCpuFrequency := CPUfrequency(2500000)
			profile.Spec.HardwareTuning = &HardwareTuning{
				IsolatedCpuFreq: &isolatedCpuFrequency,
			}

			errors := profile.validateCpuFrequency()
			Expect(errors[0].Error()).To(ContainSubstring("both isolated and reserved cpu frequency must be declared"))
		})

		It("should reject if reserved CPU frequency isdeclared, while isolated CPU frequency is empty", func() {
			reservedCpuFrequency := CPUfrequency(2800000)
			profile.Spec.HardwareTuning = &HardwareTuning{
				ReservedCpuFreq: &reservedCpuFrequency,
			}

			errors := profile.validateCpuFrequency()
			Expect(errors[0].Error()).To(ContainSubstring("both isolated and reserved cpu frequency must be declared"))
		})

		It("should have CPU frequency fields populated", func() {
			isolatedCpuFrequency := CPUfrequency(2500000)
			reservedCpuFrequency := CPUfrequency(2800000)
			profile.Spec.HardwareTuning = &HardwareTuning{
				IsolatedCpuFreq: &isolatedCpuFrequency,
				ReservedCpuFreq: &reservedCpuFrequency,
			}

			errors := profile.validateCpuFrequency()
			Expect(errors).To(BeEmpty(), "should not have validation errors with populated CPU fields")
		})

		It("should reject invalid(0) frequency for isolated CPUs", func() {
			isolatedCpuFrequency := CPUfrequency(0)
			reservedCpuFrequency := CPUfrequency(2800000)
			profile.Spec.HardwareTuning = &HardwareTuning{
				IsolatedCpuFreq: &isolatedCpuFrequency,
				ReservedCpuFreq: &reservedCpuFrequency,
			}
			errors := profile.validateCpuFrequency()
			Expect(errors).NotTo(BeEmpty(), "should have validation error when isolated CPU frequency has invalid format")
			Expect(errors[0].Error()).To(ContainSubstring("isolated cpu frequency can not be equal to 0"))
		})

		It("should reject invalid(0) frequency for reserved CPUs", func() {
			isolatedCpuFrequency := CPUfrequency(2500000)
			reservedCpuFrequency := CPUfrequency(0)
			profile.Spec.HardwareTuning = &HardwareTuning{
				IsolatedCpuFreq: &isolatedCpuFrequency,
				ReservedCpuFreq: &reservedCpuFrequency,
			}
			errors := profile.validateCpuFrequency()
			Expect(errors).NotTo(BeEmpty(), "should have validation error when reserved CPU frequency has invalid format")
			Expect(errors[0].Error()).To(ContainSubstring("reserved cpu frequency can not be equal to 0"))
		})

	})

	Describe("Label selectors validation", func() {
		It("should have 0 or 1 MachineConfigLabels", func() {
			errors := profile.validateSelectors()
			Expect(errors).To(BeEmpty(), "should not have validation errors when the profile has only 1 MachineConfigSelector")

			profile.Spec.MachineConfigLabel["foo"] = "bar"
			errors = profile.validateSelectors()
			Expect(errors).NotTo(BeEmpty(), "should have validation error when the profile has two machine config selectors")
			Expect(errors[0].Error()).To(ContainSubstring("you should provide only 1 MachineConfigLabel"))

			profile.Spec.MachineConfigLabel = nil
			setValidNodeSelector(profile)

			errors = profile.validateSelectors()
			Expect(profile.validateSelectors()).To(BeEmpty(), "should not have validation errors when machine config selector nil")
		})

		It("should should have 0 or 1 MachineConfigPoolSelector labels", func() {
			errors := profile.validateSelectors()
			Expect(errors).To(BeEmpty(), "should not have validation errors when the profile has only 1 MachineConfigPoolSelector")

			profile.Spec.MachineConfigPoolSelector["foo"] = "bar"
			errors = profile.validateSelectors()
			Expect(errors).NotTo(BeEmpty(), "should have validation error when the profile has two machine config pool selectors")
			Expect(errors[0].Error()).To(ContainSubstring("you should provide only 1 MachineConfigPoolSelector"))

			profile.Spec.MachineConfigPoolSelector = nil
			setValidNodeSelector(profile)

			errors = profile.validateSelectors()
			Expect(profile.validateSelectors()).To(BeEmpty(), "should not have validation errors when machine config pool selector nil")
		})

		It("should have sensible NodeSelector in case MachineConfigLabel or MachineConfigPoolSelector is empty", func() {
			profile.Spec.MachineConfigLabel = nil
			errors := profile.validateSelectors()
			Expect(errors).NotTo(BeEmpty(), "should have validation error with invalid NodeSelector")
			Expect(errors[0].Error()).To(ContainSubstring("invalid NodeSelector label key that can't be split into domain/role"))

			setValidNodeSelector(profile)
			errors = profile.validateSelectors()
			Expect(errors).To(BeEmpty(), "should not have validation errors when the node selector has correct format")
		})
	})

	Describe("The getNodesList helper function", func() {
		It("should pass when at least one node is detected", func() {
			// Get client with one node to test this case
			nodeSpecs := []NodeSpecifications{}
			nodeSpecs = append(nodeSpecs, NodeSpecifications{architecture: amd64, cpuCapacity: 1000, name: "node"})
			validatorClient = GetFakeValidatorClient(nodeSpecs)

			// There should be a non-empty node list and no error present
			nodes, err := profile.getNodesList()
			Expect(err).To(BeNil())
			Expect(nodes.Items).ToNot(BeEmpty())
		})
		It("should pass when zero nodes is detected", func() {
			// Get client with no nodes to test this case
			nodeSpecs := []NodeSpecifications{}
			validatorClient = GetFakeValidatorClient(nodeSpecs)

			// There should be an empty node list and no error present
			nodes, err := profile.getNodesList()
			Expect(err).To(BeNil())
			Expect(nodes.Items).To(BeEmpty())
		})
		It("should not crash when validator client is nil", func() {
			// Some external callers do not have a validator client present
			// See OCPBUGS-44477 for more information

			// There should be an empty node list and no error
			nodes, err := profile.getNodesList()
			Expect(err).To(BeNil())
			Expect(nodes.Items).To(BeEmpty())
		})
	})

	Describe("Same CPU Architecture validation", func() {
		It("should pass when both nodes are the same architecture (x86)", func() {
			// Get client with two x86 nodes
			nodeSpecs := []NodeSpecifications{}
			nodeSpecs = append(nodeSpecs, NodeSpecifications{architecture: amd64, cpuCapacity: 1000, name: "node1"})
			nodeSpecs = append(nodeSpecs, NodeSpecifications{architecture: amd64, cpuCapacity: 1000, name: "node2"})
			validatorClient = GetFakeValidatorClient(nodeSpecs)

			nodes, err := profile.getNodesList()
			Expect(err).To(BeNil())

			errors := profile.validateAllNodesAreSameCpuArchitecture(nodes)
			Expect(errors).To(BeEmpty())
		})
		It("should pass when both nodes are the same architecture (aarch64)", func() {
			// Get client with two aarch64 nodes
			nodeSpecs := []NodeSpecifications{}
			nodeSpecs = append(nodeSpecs, NodeSpecifications{architecture: aarch64, cpuCapacity: 1000, name: "node1"})
			nodeSpecs = append(nodeSpecs, NodeSpecifications{architecture: aarch64, cpuCapacity: 1000, name: "node2"})
			validatorClient = GetFakeValidatorClient(nodeSpecs)

			nodes, err := profile.getNodesList()
			Expect(err).To(BeNil())

			errors := profile.validateAllNodesAreSameCpuArchitecture(nodes)
			Expect(errors).To(BeEmpty())
		})
		It("should fail when nodes are the different architecture", func() {
			// Get client with two different nodes: one x86 and one aarch64
			nodeSpecs := []NodeSpecifications{}
			nodeSpecs = append(nodeSpecs, NodeSpecifications{architecture: amd64, cpuCapacity: 1000, name: "node1"})
			nodeSpecs = append(nodeSpecs, NodeSpecifications{architecture: aarch64, cpuCapacity: 1000, name: "node2"})
			validatorClient = GetFakeValidatorClient(nodeSpecs)

			nodes, err := profile.getNodesList()
			Expect(err).To(BeNil())

			errors := profile.validateAllNodesAreSameCpuArchitecture(nodes)
			Expect(errors).ToNot(BeEmpty())
		})
		It("should pass when no nodes are detected", func() {
			// Get client with zero nodes
			nodeSpecs := []NodeSpecifications{}
			validatorClient = GetFakeValidatorClient(nodeSpecs)

			nodes, err := profile.getNodesList()
			Expect(err).To(BeNil())
			Expect(nodes.Items).To(BeEmpty())

			errors := profile.validateAllNodesAreSameCpuArchitecture(nodes)
			Expect(errors).To(BeNil())
		})
	})

	Describe("Same CPU Capacity validation", func() {
		It("should pass when both nodes are the same capacity", func() {
			// Get client with two nodes with the same cpu capacity
			nodeSpecs := []NodeSpecifications{}
			nodeSpecs = append(nodeSpecs, NodeSpecifications{architecture: amd64, cpuCapacity: 1000, name: "node1"})
			nodeSpecs = append(nodeSpecs, NodeSpecifications{architecture: amd64, cpuCapacity: 1000, name: "node2"})
			validatorClient = GetFakeValidatorClient(nodeSpecs)

			nodes, err := profile.getNodesList()
			Expect(err).To(BeNil())

			errors := profile.validateAllNodesAreSameCpuCapacity(nodes)
			Expect(errors).To(BeEmpty())
		})
		It("should fail when nodes are the different capacity", func() {
			// Get client with two nodes with different cpu capacity
			nodeSpecs := []NodeSpecifications{}
			nodeSpecs = append(nodeSpecs, NodeSpecifications{architecture: amd64, cpuCapacity: 1000, name: "node1"})
			nodeSpecs = append(nodeSpecs, NodeSpecifications{architecture: amd64, cpuCapacity: 2000, name: "node2"})
			validatorClient = GetFakeValidatorClient(nodeSpecs)

			nodes, err := profile.getNodesList()
			Expect(err).To(BeNil())

			errors := profile.validateAllNodesAreSameCpuCapacity(nodes)
			Expect(errors).ToNot(BeEmpty())
		})
		It("should pass when no nodes are detected", func() {
			// Get client with zero nodes
			nodeSpecs := []NodeSpecifications{}
			validatorClient = GetFakeValidatorClient(nodeSpecs)

			nodes, err := profile.getNodesList()
			Expect(err).To(BeNil())
			Expect(nodes.Items).To(BeEmpty())

			errors := profile.validateAllNodesAreSameCpuCapacity(nodes)
			Expect(errors).To(BeNil())
		})
	})

	Describe("Hugepages validation", func() {
		It("should reject on incorrect default hugepages size (x86)", func() {
			nodeSpecs := []NodeSpecifications{}
			nodeSpecs = append(nodeSpecs, NodeSpecifications{architecture: amd64, cpuCapacity: 1000, name: "node"})
			validatorClient = GetFakeValidatorClient(nodeSpecs)

			nodes, err := profile.getNodesList()
			Expect(err).To(BeNil())

			incorrectDefaultSize := HugePageSize("!#@")
			profile.Spec.HugePages.DefaultHugePagesSize = &incorrectDefaultSize

			errors := profile.validateHugePages(nodes)
			Expect(errors).NotTo(BeEmpty(), "should have validation error when default huge pages size has invalid value")
			Expect(errors[0].Error()).To(ContainSubstring("hugepages default size should be equal"))
		})

		It("should reject on incorrect default hugepages size (aarch64)", func() {
			nodeSpecs := []NodeSpecifications{}
			nodeSpecs = append(nodeSpecs, NodeSpecifications{architecture: aarch64, cpuCapacity: 1000, name: "node"})
			validatorClient = GetFakeValidatorClient(nodeSpecs)

			nodes, err := profile.getNodesList()
			Expect(err).To(BeNil())

			incorrectDefaultSize := HugePageSize("!#@")
			profile.Spec.HugePages.DefaultHugePagesSize = &incorrectDefaultSize

			errors := profile.validateHugePages(nodes)
			Expect(errors).NotTo(BeEmpty(), "should have validation error when default huge pages size has invalid value")
			Expect(errors[0].Error()).To(ContainSubstring("hugepages default size should be equal"))
		})

		It("should reject hugepages allocation with unexpected page size (x86)", func() {
			nodeSpecs := []NodeSpecifications{}
			nodeSpecs = append(nodeSpecs, NodeSpecifications{architecture: amd64, cpuCapacity: 1000, name: "node"})
			validatorClient = GetFakeValidatorClient(nodeSpecs)

			nodes, err := profile.getNodesList()
			Expect(err).To(BeNil())

			profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, HugePage{
				Count: 128,
				Node:  pointer.Int32(0),
				Size:  "14M",
			})
			errors := profile.validateHugePages(nodes)
			Expect(errors).NotTo(BeEmpty(), "should have validation error when page with invalid format presents")
			Expect(errors[0].Error()).To(ContainSubstring(fmt.Sprintf("the page size should be equal to one of %v", x86ValidHugepagesSizes)))
		})

		It("should reject hugepages allocation with unexpected page size (aarch64)", func() {
			nodeSpecs := []NodeSpecifications{}
			nodeSpecs = append(nodeSpecs, NodeSpecifications{architecture: aarch64, cpuCapacity: 1000, name: "node"})
			validatorClient = GetFakeValidatorClient(nodeSpecs)

			nodes, err := profile.getNodesList()
			Expect(err).To(BeNil())

			defaultSize := HugePageSize(hugepagesSize2M)
			profile.Spec.HugePages.DefaultHugePagesSize = &defaultSize

			profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, HugePage{
				Count: 128,
				Node:  pointer.Int32(0),
				Size:  "14M",
			})
			errors := profile.validateHugePages(nodes)
			Expect(errors).NotTo(BeEmpty(), "should have validation error when page with invalid format presents")
			Expect(errors[0].Error()).To(ContainSubstring(fmt.Sprintf("the page size should be equal to one of %v", aarch64ValidHugepagesSizes)))
		})

		It("should pass when no nodes are detected with a valid hugepage size", func() {
			// Get client with zero nodes
			nodeSpecs := []NodeSpecifications{}
			validatorClient = GetFakeValidatorClient(nodeSpecs)

			nodes, err := profile.getNodesList()
			Expect(err).To(BeNil())
			Expect(nodes.Items).To(BeEmpty())

			errors := profile.validateHugePages(nodes)
			Expect(errors).To(BeNil())
		})

		It("should fail when no nodes are detected with a invalid hugepage size", func() {
			// Get client with zero nodes
			nodeSpecs := []NodeSpecifications{}
			validatorClient = GetFakeValidatorClient(nodeSpecs)

			defaultSize := HugePageSize(hugepagesSize2M)
			profile.Spec.HugePages.DefaultHugePagesSize = &defaultSize

			profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, HugePage{
				Count: 128,
				Node:  pointer.Int32(0),
				Size:  "14M",
			})

			nodes, err := profile.getNodesList()
			Expect(err).To(BeNil())
			Expect(nodes.Items).To(BeEmpty())

			errors := profile.validateHugePages(nodes)
			Expect(errors).ToNot(BeEmpty())
			Expect(errors[0].Error()).To(ContainSubstring(("the page size should be equal to one of")))
		})

		When("pages have duplication", func() {
			Context("with specified NUMA node", func() {
				It("should raise the validation error (x86)", func() {
					nodeSpecs := []NodeSpecifications{}
					nodeSpecs = append(nodeSpecs, NodeSpecifications{architecture: amd64, cpuCapacity: 1000, name: "node"})
					validatorClient = GetFakeValidatorClient(nodeSpecs)

					nodes, err := profile.getNodesList()
					Expect(err).To(BeNil())

					profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, HugePage{
						Count: 128,
						Size:  hugepagesSize1G,
						Node:  pointer.Int32(0),
					})
					profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, HugePage{
						Count: 64,
						Size:  hugepagesSize1G,
						Node:  pointer.Int32(0),
					})
					errors := profile.validateHugePages(nodes)
					Expect(errors).NotTo(BeEmpty())
					Expect(errors[0].Error()).To(ContainSubstring(fmt.Sprintf("the page with the size %q and with specified NUMA node 0, has duplication", hugepagesSize1G)))
				})
			})

			Context("without specified NUMA node", func() {
				It("should raise the validation error (x86)", func() {
					nodeSpecs := []NodeSpecifications{}
					nodeSpecs = append(nodeSpecs, NodeSpecifications{architecture: amd64, cpuCapacity: 1000, name: "node"})
					validatorClient = GetFakeValidatorClient(nodeSpecs)

					nodes, err := profile.getNodesList()
					Expect(err).To(BeNil())

					profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, HugePage{
						Count: 128,
						Size:  hugepagesSize1G,
					})
					errors := profile.validateHugePages(nodes)
					Expect(errors).NotTo(BeEmpty())
					Expect(errors[0].Error()).To(ContainSubstring(fmt.Sprintf("the page with the size %q and without the specified NUMA node, has duplication", hugepagesSize1G)))
				})
			})

			Context("with not sequentially duplication blocks", func() {
				It("should raise the validation error (x86)", func() {
					nodeSpecs := []NodeSpecifications{}
					nodeSpecs = append(nodeSpecs, NodeSpecifications{architecture: amd64, cpuCapacity: 1000, name: "node"})
					validatorClient = GetFakeValidatorClient(nodeSpecs)

					nodes, err := profile.getNodesList()
					Expect(err).To(BeNil())

					profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, HugePage{
						Count: 128,
						Size:  hugepagesSize2M,
					})
					profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, HugePage{
						Count: 128,
						Size:  hugepagesSize1G,
					})
					errors := profile.validateHugePages(nodes)
					Expect(errors).NotTo(BeEmpty())
					Expect(errors[0].Error()).To(ContainSubstring(fmt.Sprintf("the page with the size %q and without the specified NUMA node, has duplication", hugepagesSize1G)))
				})
			})
		})
	})

	Describe("Net validation", func() {
		Context("with properly populated fields", func() {
			It("should have net fields properly populated", func() {
				errors := profile.validateNet()
				Expect(errors).To(BeEmpty(), "should not have validation errors with properly populated net devices fields")
			})
		})
		Context("with misconfigured fields", func() {
			It("should raise the validation syntax errors", func() {
				invalidVendor := "123"
				invalidDevice := "0x12345"
				profile.Spec.Net.Devices[0].InterfaceName = pointer.String("")
				profile.Spec.Net.Devices[0].VendorID = pointer.String(invalidVendor)
				profile.Spec.Net.Devices[0].DeviceID = pointer.String(invalidDevice)
				errors := profile.validateNet()
				Expect(len(errors)).To(Equal(3))
				Expect(errors[0].Error()).To(ContainSubstring(fmt.Sprintf("device name cannot be empty")))
				Expect(errors[1].Error()).To(ContainSubstring(fmt.Sprintf("device vendor ID %s has an invalid format. Vendor ID should be represented as 0x<4 hexadecimal digits> (16 bit representation)", invalidVendor)))
				Expect(errors[2].Error()).To(ContainSubstring(fmt.Sprintf("device model ID %s has an invalid format. Model ID should be represented as 0x<4 hexadecimal digits> (16 bit representation)", invalidDevice)))

			})
			It("should raise the validation errors for missing fields", func() {
				profile.Spec.Net.Devices[0].VendorID = nil
				profile.Spec.Net.Devices[0].DeviceID = pointer.String("0x1")
				errors := profile.validateNet()
				Expect(errors).NotTo(BeEmpty())
				Expect(errors[0].Error()).To(ContainSubstring(fmt.Sprintf("device model ID can not be used without specifying the device vendor ID.")))
			})
		})

		Describe("Workload hints validation", func() {
			When("realtime kernel is enabled and realtime workload hint is explicitly disabled", func() {
				It("should raise validation error", func() {
					profile.Spec.WorkloadHints = &WorkloadHints{
						RealTime: pointer.Bool(false),
					}
					profile.Spec.RealTimeKernel = &RealTimeKernel{
						Enabled: pointer.Bool(true),
					}
					errors := profile.validateWorkloadHints()
					Expect(errors).NotTo(BeEmpty())
					Expect(errors[0].Error()).To(ContainSubstring("realtime kernel is enabled, but realtime workload hint is explicitly disable"))
				})
			})
			When("HighPowerConsumption hint is enabled and PerPodPowerManagement hint is enabled", func() {
				It("should raise validation error", func() {
					profile.Spec.WorkloadHints = &WorkloadHints{
						HighPowerConsumption:  pointer.Bool(true),
						PerPodPowerManagement: pointer.Bool(true),
					}
					errors := profile.validateWorkloadHints()
					Expect(errors).NotTo(BeEmpty())
					Expect(errors[0].Error()).To(ContainSubstring("Invalid WorkloadHints configuration: HighPowerConsumption and PerPodPowerManagement can not be both enabled"))
				})
			})
			When("MixedCPUs hint is enabled but no shared CPUs are specified", func() {
				It("should raise validation error", func() {
					profile.Spec.WorkloadHints = &WorkloadHints{
						MixedCpus: pointer.Bool(true),
					}
					errors := profile.validateWorkloadHints()
					Expect(errors).NotTo(BeEmpty())
					Expect(errors[0].Error()).To(ContainSubstring("Invalid WorkloadHints configuration: MixedCpus enabled but no shared CPUs were specified"))
				})
			})

		})
	})

	Describe("validation of validateFields function", func() {
		It("should check all fields (x86)", func() {
			nodeSpecs := []NodeSpecifications{}
			nodeSpecs = append(nodeSpecs, NodeSpecifications{architecture: amd64, cpuCapacity: 1000, name: "node"})
			validatorClient = GetFakeValidatorClient(nodeSpecs)

			// config all specs to rise an error in every func inside validateFields()
			reservedCPUs := CPUSet("")
			isolatedCPUs := CPUSet("0-6")
			offlinedCPUs := CPUSet("7")
			profile.Spec.CPU.Reserved = &reservedCPUs
			profile.Spec.CPU.Isolated = &isolatedCPUs
			profile.Spec.CPU.Offlined = &offlinedCPUs

			profile.Spec.MachineConfigLabel["foo"] = "bar"

			incorrectDefaultSize := HugePageSize("!#@")
			profile.Spec.HugePages.DefaultHugePagesSize = &incorrectDefaultSize

			profile.Spec.WorkloadHints = &WorkloadHints{
				RealTime: pointer.Bool(false),
			}
			profile.Spec.RealTimeKernel = &RealTimeKernel{
				Enabled: pointer.Bool(true),
			}

			invalidVendor := "123"
			invalidDevice := "0x12345"
			profile.Spec.Net.Devices[0].InterfaceName = pointer.String("")
			profile.Spec.Net.Devices[0].VendorID = pointer.String(invalidVendor)
			profile.Spec.Net.Devices[0].DeviceID = pointer.String(invalidDevice)

			errors := profile.ValidateBasicFields()

			type void struct{}
			var member void
			errorMsgs := make(map[string]void)

			errorMsgs["reserved CPUs can not be empty"] = member
			errorMsgs["you should provide only 1 MachineConfigLabel"] = member
			errorMsgs[fmt.Sprintf("hugepages default size should be equal to one of %v", x86ValidHugepagesSizes)] = member
			errorMsgs["device name cannot be empty"] = member
			errorMsgs[fmt.Sprintf("device vendor ID %s has an invalid format. Vendor ID should be represented as 0x<4 hexadecimal digits> (16 bit representation)", invalidVendor)] = member
			errorMsgs[fmt.Sprintf("device model ID %s has an invalid format. Model ID should be represented as 0x<4 hexadecimal digits> (16 bit representation)", invalidDevice)] = member
			errorMsgs["realtime kernel is enabled, but realtime workload hint is explicitly disable"] = member

			for _, err := range errors {
				_, exists := errorMsgs[err.Detail]
				if exists {
					delete(errorMsgs, err.Detail)
				}
			}

			for errorMsg, ok := range errorMsgs {
				Expect(ok).To(BeTrue(), "missing expected error: %q", errorMsg)
			}
		})
	})
})

func setValidNodeSelector(profile *PerformanceProfile) {
	selector := make(map[string]string)
	selector["fooDomain/"+NodeSelectorRole] = ""
	profile.Spec.NodeSelector = selector
}
