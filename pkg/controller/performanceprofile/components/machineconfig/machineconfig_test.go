package machineconfig

import (
	"fmt"

	"k8s.io/utils/pointer"

	"github.com/ghodss/yaml"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components"
	testutils "github.com/openshift-kni/performance-addon-operators/pkg/utils/testing"
)

const hugepagesAllocationService = `
      - contents: |
          [Unit]
          Description=Hugepages-1048576kB allocation on the node 0
          Before=kubelet.service

          [Service]
          Environment=HUGEPAGES_COUNT=4
          Environment=HUGEPAGES_SIZE=1048576
          Environment=NUMA_NODE=0
          Type=oneshot
          RemainAfterExit=true
          ExecStart=/usr/local/bin/hugepages-allocation.sh

          [Install]
          WantedBy=multi-user.target
        enabled: true
        name: hugepages-allocation-1048576kB-NUMA0.service
`

var _ = Describe("Machine Config", func() {

	Context("machine config creation ", func() {
		It("should create machine config with valid assets", func() {
			profile := testutils.NewPerformanceProfile("test")
			profile.Spec.HugePages.Pages[0].Node = pointer.Int32Ptr(0)

			_, err := New(profile)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("with hugepages with specified NUMA node", func() {
		var manifest string

		BeforeEach(func() {
			profile := testutils.NewPerformanceProfile("test")
			profile.Spec.HugePages.Pages[0].Node = pointer.Int32Ptr(0)

			labelKey, labelValue := components.GetFirstKeyAndValue(profile.Spec.MachineConfigLabel)
			mc, err := New(profile)
			Expect(err).ToNot(HaveOccurred())
			Expect(mc.Spec.KernelType).To(Equal(MCKernelRT))

			y, err := yaml.Marshal(mc)
			Expect(err).ToNot(HaveOccurred())

			manifest = string(y)
			Expect(manifest).To(ContainSubstring(fmt.Sprintf("%s: %s", labelKey, labelValue)))
		})

		It("should not add hugepages kernel boot parameters", func() {
			Expect(manifest).ToNot(ContainSubstring("- hugepagesz=1G"))
			Expect(manifest).ToNot(ContainSubstring("- hugepages=4"))
		})

		It("should add systemd unit to allocate hugepages", func() {
			Expect(manifest).To(ContainSubstring(hugepagesAllocationService))
		})

	})
})
