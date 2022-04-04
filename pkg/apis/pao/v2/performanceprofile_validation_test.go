package v2

import (
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

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
	IsolatedCPUs = CPUSet("4-7")
	// ReservedCPUs defines the reserved CPU set used for tests
	ReservedCPUs = CPUSet("0-3")
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

// NewPerformanceProfile returns new performance profile object that used for tests
func NewPerformanceProfile(name string) *PerformanceProfile {
	size := HugePageSize1G
	isolatedCPUs := IsolatedCPUs
	reservedCPUs := ReservedCPUs
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
				Enabled: pointer.BoolPtr(true),
			},
			NUMA: &NUMA{
				TopologyPolicy: &numaPolicy,
			},
			Net: &Net{
				UserLevelNetworking: pointer.BoolPtr(true),
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

		It("should allow cpus allocation with no reserved CPUs", func() {
			reservedCPUs := CPUSet("")
			isolatedCPUs := CPUSet("0-7")
			profile.Spec.CPU.Reserved = &reservedCPUs
			profile.Spec.CPU.Isolated = &isolatedCPUs
			errors := profile.validateCPUs()
			Expect(errors).To(BeEmpty())
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

		It("should reject cpus allocation with overlapping sets", func() {
			reservedCPUs := CPUSet("0-7")
			isolatedCPUs := CPUSet("0-15")
			profile.Spec.CPU.Reserved = &reservedCPUs
			profile.Spec.CPU.Isolated = &isolatedCPUs
			errors := profile.validateCPUs()
			Expect(errors).NotTo(BeEmpty(), "should have validation error when reserved and isolation CPUs have overlap")
			Expect(errors[0].Error()).To(ContainSubstring("reserved and isolated cpus overlap"))
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

	Describe("Hugepages validation", func() {
		It("should reject on incorrect default hugepages size", func() {
			incorrectDefaultSize := HugePageSize("!#@")
			profile.Spec.HugePages.DefaultHugePagesSize = &incorrectDefaultSize

			errors := profile.validateHugePages()
			Expect(errors).NotTo(BeEmpty(), "should have validation error when default huge pages size has invalid value")
			Expect(errors[0].Error()).To(ContainSubstring("hugepages default size should be equal"))
		})

		It("should reject hugepages allocation with unexpected page size", func() {
			profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, HugePage{
				Count: 128,
				Node:  pointer.Int32Ptr(0),
				Size:  "14M",
			})
			errors := profile.validateHugePages()
			Expect(errors).NotTo(BeEmpty(), "should have validation error when page with invalid format presents")
			Expect(errors[0].Error()).To(ContainSubstring(fmt.Sprintf("the page size should be equal to %q or %q", hugepagesSize1G, hugepagesSize2M)))
		})

		When("pages have duplication", func() {
			Context("with specified NUMA node", func() {
				It("should raise the validation error", func() {
					profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, HugePage{
						Count: 128,
						Size:  hugepagesSize1G,
						Node:  pointer.Int32Ptr(0),
					})
					profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, HugePage{
						Count: 64,
						Size:  hugepagesSize1G,
						Node:  pointer.Int32Ptr(0),
					})
					errors := profile.validateHugePages()
					Expect(errors).NotTo(BeEmpty())
					Expect(errors[0].Error()).To(ContainSubstring(fmt.Sprintf("the page with the size %q and with specified NUMA node 0, has duplication", hugepagesSize1G)))
				})
			})

			Context("without specified NUMA node", func() {
				It("should raise the validation error", func() {
					profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, HugePage{
						Count: 128,
						Size:  hugepagesSize1G,
					})
					errors := profile.validateHugePages()
					Expect(errors).NotTo(BeEmpty())
					Expect(errors[0].Error()).To(ContainSubstring(fmt.Sprintf("the page with the size %q and without the specified NUMA node, has duplication", hugepagesSize1G)))
				})
			})

			Context("with not sequentially duplication blocks", func() {
				It("should raise the validation error", func() {
					profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, HugePage{
						Count: 128,
						Size:  hugepagesSize2M,
					})
					profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, HugePage{
						Count: 128,
						Size:  hugepagesSize1G,
					})
					errors := profile.validateHugePages()
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
				profile.Spec.Net.Devices[0].InterfaceName = pointer.StringPtr("")
				profile.Spec.Net.Devices[0].VendorID = pointer.StringPtr(invalidVendor)
				profile.Spec.Net.Devices[0].DeviceID = pointer.StringPtr(invalidDevice)
				errors := profile.validateNet()
				Expect(len(errors)).To(Equal(3))
				Expect(errors[0].Error()).To(ContainSubstring(fmt.Sprintf("device name cannot be empty")))
				Expect(errors[1].Error()).To(ContainSubstring(fmt.Sprintf("device vendor ID %s has an invalid format. Vendor ID should be represented as 0x<4 hexadecimal digits> (16 bit representation)", invalidVendor)))
				Expect(errors[2].Error()).To(ContainSubstring(fmt.Sprintf("device model ID %s has an invalid format. Model ID should be represented as 0x<4 hexadecimal digits> (16 bit representation)", invalidDevice)))

			})
			It("should raise the validation errors for missing fields", func() {
				profile.Spec.Net.Devices[0].VendorID = nil
				profile.Spec.Net.Devices[0].DeviceID = pointer.StringPtr("0x1")
				errors := profile.validateNet()
				Expect(errors).NotTo(BeEmpty())
				Expect(errors[0].Error()).To(ContainSubstring(fmt.Sprintf("device model ID can not be used without specifying the device vendor ID.")))
			})
		})
	})
})

func setValidNodeSelector(profile *PerformanceProfile) {
	selector := make(map[string]string)
	selector["fooDomain/"+NodeSelectorRole] = ""
	profile.Spec.NodeSelector = selector
}
