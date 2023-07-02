package machineconfig

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"k8s.io/utils/pointer"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	configv1 "github.com/openshift/api/config/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"sigs.k8s.io/yaml"

	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	testutils "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/utils/testing"
)

var spaceRegex = regexp.MustCompile(`\s*`)

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

const offlineCPUS = `
      - contents: |
          [Unit]
          Description=Set cpus offline: 6,7
          Before=kubelet.service

          [Service]
          Environment=OFFLINE_CPUS=6,7
          Type=oneshot
          RemainAfterExit=true
          ExecStart=/usr/local/bin/set-cpus-offline.sh

          [Install]
          WantedBy=multi-user.target
        enabled: true
        name: set-cpus-offline.service
`

const clearBannedCPUs = `
      - contents: |
          [Unit]
          Description=Clear the IRQBalance Banned CPU mask early in the boot
          Before=kubelet.service
          Before=irqbalance.service

          [Service]
          Type=oneshot
          RemainAfterExit=true
          ExecStart=/usr/local/bin/clear-irqbalance-banned-cpus.sh

          [Install]
          WantedBy=multi-user.target
        enabled: true
        name: clear-irqbalance-banned-cpus.service
`

var CPUs = []int{1, 2, 3, 4, 5, 6, 7, 8, 9}
var CPUstring = "1,2,3,4,5,6,7,8,9"

var _ = Describe("Machine Config", func() {

	Context("machine config creation ", func() {
		It("should create machine config with valid assets", func() {
			profile := testutils.NewPerformanceProfile("test")
			profile.Spec.HugePages.Pages[0].Node = pointer.Int32Ptr(0)

			_, err := New(profile, nil, "")
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("with hugepages with specified NUMA node and offlinedCPUs", func() {
		var manifest string

		BeforeEach(func() {
			profile := testutils.NewPerformanceProfile("test")
			profile.Spec.HugePages.Pages[0].Node = pointer.Int32Ptr(0)

			labelKey, labelValue := components.GetFirstKeyAndValue(profile.Spec.MachineConfigLabel)
			mc, err := New(profile, nil, "")
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

		It("should add systemd unit to offlineCPUs", func() {
			Expect(manifest).To(ContainSubstring(offlineCPUS))
		})

		// doesn't depend on hugepages or offlined CPUs
		It("should create systemd unit for clearing banned CPUs", func() {
			Expect(manifest).To(ContainSubstring(clearBannedCPUs))
		})
	})

	Context("check listToString ", func() {
		It("should create string from CPUSet", func() {
			res := components.ListToString(CPUs)
			Expect(res).To(Equal(CPUstring))
		})
	})

	Context("check systemd units", func() {
		It("should generate clear-banned-cpus unit", func() {
			unit, err := getSystemdContent(getIRQBalanceBannedCPUsOptions())
			Expect(err).ToNot(HaveOccurred())
			expected := `[Unit]
Description=Clear the IRQBalance Banned CPU mask early in the boot
Before=kubelet.service
Before=irqbalance.service

[Service]
Type=oneshot
RemainAfterExit=true
ExecStart=/usr/local/bin/clear-irqbalance-banned-cpus.sh

[Install]
WantedBy=multi-user.target
`
			Expect(unit).To(Equal(expected))
		})
	})
})

var _ = Describe("Pinning Config", func() {
	type test struct {
		desc        string
		src         string
		cpuSet      *performancev2.CPU
		expected    string
		expectedErr types.GomegaMatcher
	}

	var testCases []test
	testCases = append(testCases, test{
		desc: "pinned workloads infrastructure, should render pinned crio config",
		src:  crioPartitioningConfig,
		cpuSet: &performancev2.CPU{
			Reserved: cpuSetRef("0-1"),
			Isolated: cpuSetRef("2-3"),
		},
		expected: `
			[crio.runtime.workloads.management]
			activation_annotation = "target.workload.openshift.io/management"
			annotation_prefix = "resources.workload.openshift.io"
			resources = { "cpushares" = 0, "cpuset" = "0-1" }
			`,
	})
	testCases = append(testCases, test{
		desc: "pinned workloads infrastructure, should render openshift pinning config",
		src:  ocpPartitioningConfig,
		cpuSet: &performancev2.CPU{
			Reserved: cpuSetRef("0-1"),
			Isolated: cpuSetRef("2-3"),
		},
		expected: `
			{
				"management": {
					"cpuset": "0-1"
				}
			}
			`,
	})
	testCases = append(testCases, test{
		desc:        "should fail when CPUSet is nil",
		src:         ocpPartitioningConfig,
		cpuSet:      nil,
		expectedErr: Equal(errors.New("cpu value is required, skipping generating file")),
	})

	Describe("Pinning Config", func() {
		for _, t := range testCases {
			tc := t
			When(tc.desc, func() {
				It("should match expected", func() {
					f, err := renderManagementCPUPinningConfig(tc.cpuSet, tc.src)
					if tc.expectedErr == nil {
						tc.expectedErr = BeNil()
					}
					Expect(err).Should(tc.expectedErr)

					result := removeAllWhiteSpace(string(f))
					expected := removeAllWhiteSpace(tc.expected)

					Expect(result).To(Equal(expected))
				})
			})
		}
	})
})

var _ = Describe("Bootstrap Pinning Config", func() {

	allNodes := configv1.CPUPartitioningAllNodes
	none := configv1.CPUPartitioningNone
	defaultEmptyCrio := []byte(`
[crio.runtime.workloads.management]
activation_annotation = "target.workload.openshift.io/management"
annotation_prefix = "resources.workload.openshift.io"
resources = { "cpushares" = 0, "cpuset" = "" }
`)
	defaultEmptyKubelet := []byte(`
{
  "management": {
    "cpuset": ""
  }
}
`)

	expected := map[string][]byte{
		"/etc/kubernetes/openshift-workload-pinning":             defaultEmptyKubelet,
		"/etc/crio/crio.conf.d/01-workload-pinning-default.conf": defaultEmptyCrio,
	}

	Context("should generate config", func() {
		It("when cpu partitioning set", func() {
			mc, err := BootstrapWorkloadPinningMC("master", &allNodes)
			Expect(err).ToNot(HaveOccurred())
			Expect(mc).ToNot(BeNil())
		})

		It("with correct ignition configs", func() {
			mc, err := BootstrapWorkloadPinningMC("master", &allNodes)
			Expect(err).ToNot(HaveOccurred())

			result := igntypes.Config{}

			err = json.Unmarshal(mc.Spec.Config.Raw, &result)
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Storage.Files).To(HaveLen(2))

			for _, f := range result.Storage.Files {
				nonEncodedContent, ok := expected[f.Node.Path]
				Expect(ok).To(BeTrue(), "path %s is not present in expected map", f.Node.Path)
				encoded := base64.StdEncoding.EncodeToString(nonEncodedContent)
				Expect(f.Contents.Source).ToNot(BeNil())
				Expect(*f.Contents.Source).To(
					Equal(fmt.Sprintf("%s,%s", defaultIgnitionContentSource, encoded)),
					"path %s contains content mismatch", f.Node.Path)
			}
		})
	})

	Context("should not be generated", func() {
		It("with cpu partitioning set to none", func() {
			mc, err := BootstrapWorkloadPinningMC("master", &none)
			Expect(err).ToNot(HaveOccurred())
			Expect(mc).To(BeNil())
		})

		It("with nil being provided", func() {
			mc, err := BootstrapWorkloadPinningMC("master", nil)
			Expect(err).To(HaveOccurred())
			Expect(mc).To(BeNil())
		})
	})
})

func removeAllWhiteSpace(str string) string {
	return spaceRegex.ReplaceAllString(str, "")
}

func cpuSetRef(set string) *performancev2.CPUSet {
	re := performancev2.CPUSet(set)
	return &re
}
