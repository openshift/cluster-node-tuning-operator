package tuned

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/ghodss/yaml"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	testutils "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/utils/testing"

	cpuset "k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
	"k8s.io/utils/pointer"
)

const expectedMatchSelector = `
  - machineConfigLabels:
      mcKey: mcValue
`

var (
	cmdlineCPUsPartitioning                     = regexp.MustCompile(`\s*cmdline_cpu_part=\+\s*nohz=on\s+rcu_nocbs=\${isolated_cores}\s+tuned.non_isolcpus=\${not_isolated_cpumask}\s+systemd.cpu_affinity=\${not_isolated_cores_expanded}\s+intel_iommu=on\s+iommu=pt\s*`)
	cmdlineWithStaticIsolation                  = regexp.MustCompile(`\s*cmdline_isolation=\+\s*isolcpus=managed_irq,\${isolated_cores}\s*`)
	cmdlineWithoutStaticIsolation               = regexp.MustCompile(`\s*cmdline_isolation=\+\s*isolcpus=domain,managed_irq,\${isolated_cores}\s*`)
	cmdlineWithRealtimeHint                     = regexp.MustCompile(`\s*cmdline_realtime=\+\s*nohz_full=\${isolated_cores}\s+tsc=nowatchdog\s+nosoftlockup\s+nmi_watchdog=0\s+mce=off\s+skew_tick=1\s*`)
	cmdlineHighPowerConsumption                 = regexp.MustCompile(`\s*cmdline_power_performance=\+\s*processor.max_cstate=1\s+intel_idle.max_cstate=0\s+intel_pstate=disable\s*`)
	cmdlineHighPowerConsumptionWithRealtimeHint = regexp.MustCompile(`\s*cmdline_idle_poll=\+\s*idle=poll\s*`)
	cmdlineHugepages                            = regexp.MustCompile(`\s*cmdline_hugepages=\+\s*default_hugepagesz=1G\s+hugepagesz=1G\s+hugepages=4\s*`)
	cmdlineAdditionalArg                        = regexp.MustCompile(`\s*cmdline_additionalArg=\+\s*test1=val1\s+test2=val2\s*`)
	cmdlineDummy2MHugePages                     = regexp.MustCompile(`\s*cmdline_hugepages=\+\s*default_hugepagesz=1G\s+hugepagesz=1G\s+hugepages=4\s+hugepagesz=2M\s+hugepages=0\s*`)
	cmdlineMultipleHugePages                    = regexp.MustCompile(`\s*cmdline_hugepages=\+\s*default_hugepagesz=1G\s+hugepagesz=1G\s+hugepages=4\s+hugepagesz=2M\s+hugepages=128\s*`)
)

var additionalArgs = []string{"test1=val1", "test2=val2"}

var _ = Describe("Tuned", func() {
	var profile *performancev2.PerformanceProfile

	BeforeEach(func() {
		profile = testutils.NewPerformanceProfile("test")
	})

	getTunedManifest := func(profile *performancev2.PerformanceProfile) string {
		tuned, err := NewNodePerformance(profile)
		Expect(err).ToNot(HaveOccurred())
		y, err := yaml.Marshal(tuned)
		Expect(err).ToNot(HaveOccurred())
		return string(y)
	}

	Context("with worker performance profile", func() {
		It("should generate yaml with expected parameters", func() {
			manifest := getTunedManifest(profile)

			Expect(manifest).To(ContainSubstring(expectedMatchSelector))
			Expect(manifest).To(ContainSubstring("isolated_cores=4-5"))
			Expect(manifest).To(ContainSubstring("governor=performance"))
			By("Populating CPU partitioning cmdline")
			Expect(cmdlineCPUsPartitioning.MatchString(manifest)).To(BeTrue())
			By("Populating static isolation cmdline")
			Expect(cmdlineWithStaticIsolation.MatchString(manifest)).To(BeTrue())
			By("Populating hugepages cmdline")
			Expect(cmdlineHugepages.MatchString(manifest)).To(BeTrue())
			By("Populating empty additional kernel arguments cmdline")
			Expect(manifest).To(ContainSubstring("cmdline_additionalArg="))
		})

		When("realtime Hint disabled", func() {
			It("should not contain realtime related parameters", func() {
				profile.Spec.WorkloadHints.RealTime = pointer.BoolPtr(false)

				manifest := getTunedManifest(profile)
				By("Populating realtime cmdline")
				Expect(cmdlineWithRealtimeHint.MatchString(manifest)).ToNot(BeTrue())
			})
		})

		When("HighPowerConsumption Hint disabled", func() {
			It("should not contain realtime related parameters", func() {
				profile.Spec.WorkloadHints.HighPowerConsumption = pointer.BoolPtr(false)

				manifest := getTunedManifest(profile)
				Expect(manifest).ToNot(ContainSubstring("processor.max_cstate=1"))
				Expect(manifest).ToNot(ContainSubstring("intel_idle.max_cstate=0"))
				Expect(manifest).ToNot(ContainSubstring("intel_pstate = disable"))
				By("Populating realtime cmdline")
				Expect(cmdlineHighPowerConsumption.MatchString(manifest)).ToNot(BeTrue())
			})
		})

		When("HighPowerConsumption Hint enable, realtime disabled", func() {
			It("should not contain idle=poll cmdline", func() {
				profile.Spec.WorkloadHints.HighPowerConsumption = pointer.BoolPtr(true)
				profile.Spec.WorkloadHints.RealTime = pointer.BoolPtr(false)

				manifest := getTunedManifest(profile)
				By("Populating idle poll cmdline")
				Expect(cmdlineHighPowerConsumption.MatchString(manifest)).To(BeTrue())
			})
		})

		When("HighPowerConsumption Hint enable, realtime enabled", func() {
			BeforeEach(func() {
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption: pointer.BoolPtr(true),
					RealTime:             pointer.BoolPtr(true),
				}
			})

			It("should contain HighPowerConsumption and RealTime Hints related parameters", func() {
				manifest := getTunedManifest(profile)
				By("Populating high power consumption cmdline")
				Expect(cmdlineHighPowerConsumption.MatchString(manifest)).To(BeTrue())
				By("Populating idle poll cmdline")
				Expect(cmdlineHighPowerConsumptionWithRealtimeHint.MatchString(manifest)).To(BeTrue())
			})
		})

		It("should generate yaml with expected parameters for Isolated balancing disabled", func() {
			profile.Spec.CPU.BalanceIsolated = pointer.BoolPtr(false)
			manifest := getTunedManifest(profile)

			Expect(cmdlineWithoutStaticIsolation.MatchString(manifest)).To(BeTrue())
		})

		It("should generate yaml with expected parameters for additional kernel arguments", func() {
			profile.Spec.AdditionalKernelArgs = additionalArgs
			manifest := getTunedManifest(profile)

			Expect(cmdlineAdditionalArg.MatchString(manifest)).To(BeTrue())
		})

		It("should not allocate hugepages on the specific NUMA node via kernel arguments", func() {
			manifest := getTunedManifest(profile)
			Expect(strings.Count(manifest, "hugepagesz=")).Should(BeNumerically("==", 2))
			Expect(strings.Count(manifest, "hugepages=")).Should(BeNumerically("==", 3))

			profile.Spec.HugePages.Pages[0].Node = pointer.Int32Ptr(1)
			manifest = getTunedManifest(profile)
			Expect(strings.Count(manifest, "hugepagesz=")).Should(BeNumerically("==", 1))
			Expect(strings.Count(manifest, "hugepages=")).Should(BeNumerically("==", 2))
		})

		Context("with 1G default huge pages", func() {
			Context("with requested 2M huge pages allocation on the specified node", func() {
				It("should append the dummy 2M huge pages kernel arguments", func() {
					profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, performancev2.HugePage{
						Size:  components.HugepagesSize2M,
						Count: 128,
						Node:  pointer.Int32Ptr(0),
					})

					manifest := getTunedManifest(profile)
					Expect(cmdlineDummy2MHugePages.MatchString(manifest)).To(BeTrue())
				})
			})

			Context("with requested 2M huge pages allocation via kernel arguments", func() {
				It("should not append the dummy 2M kernel arguments", func() {
					profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, performancev2.HugePage{
						Size:  components.HugepagesSize2M,
						Count: 128,
					})

					manifest := getTunedManifest(profile)
					Expect(cmdlineDummy2MHugePages.MatchString(manifest)).To(BeFalse())
					Expect(cmdlineMultipleHugePages.MatchString(manifest)).To(BeTrue())
				})
			})

			Context("without requested 2M hugepages", func() {
				It("should not append dummy 2M huge pages kernel arguments", func() {
					manifest := getTunedManifest(profile)
					Expect(cmdlineDummy2MHugePages.MatchString(manifest)).To(BeFalse())
				})
			})

			Context("with requested 2M huge pages allocation on the specified node and via kernel arguments", func() {
				It("should not append the dummy 2M kernel arguments", func() {
					profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, performancev2.HugePage{
						Size:  components.HugepagesSize2M,
						Count: 128,
						Node:  pointer.Int32Ptr(0),
					})
					profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, performancev2.HugePage{
						Size:  components.HugepagesSize2M,
						Count: 128,
					})

					manifest := getTunedManifest(profile)
					Expect(cmdlineDummy2MHugePages.MatchString(manifest)).To(BeFalse())
					Expect(cmdlineMultipleHugePages.MatchString(manifest)).To(BeTrue())
				})
			})
		})

		Context("with 2M default huge pages", func() {
			Context("with requested 2M huge pages allocation on the specified node", func() {
				It("should not append the dummy 2M huge pages kernel arguments", func() {
					defaultSize := performancev2.HugePageSize(components.HugepagesSize2M)
					profile.Spec.HugePages.DefaultHugePagesSize = &defaultSize
					profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, performancev2.HugePage{
						Size:  components.HugepagesSize2M,
						Count: 128,
						Node:  pointer.Int32Ptr(0),
					})

					manifest := getTunedManifest(profile)
					Expect(cmdlineDummy2MHugePages.MatchString(manifest)).To(BeFalse())
					Expect(cmdlineMultipleHugePages.MatchString(manifest)).To(BeFalse())
				})
			})
		})

		Context("with user level networking enabled", func() {
			Context("with default net device queues (all devices set)", func() {
				It("should set the default netqueues count to reserved CPUs count", func() {
					profile.Spec.Net = &performancev2.Net{
						UserLevelNetworking: pointer.BoolPtr(true),
					}
					manifest := getTunedManifest(profile)
					reservedSet, err := cpuset.Parse(string(*profile.Spec.CPU.Reserved))
					Expect(err).ToNot(HaveOccurred())
					reserveCPUcount := reservedSet.Size()
					channelsRegex := regexp.MustCompile(`\s*channels=combined\s*` + strconv.Itoa(reserveCPUcount) + `\s*`)
					Expect(channelsRegex.MatchString(manifest)).To(BeTrue())
				})
				It("should set by interface name with reserved CPUs count", func() {
					netDeviceName := "eth*"
					//regex field should be: devices_udev_regex=^INTERFACE=eth.*
					devicesUdevRegex := "\\^INTERFACE=" + strings.Replace(netDeviceName, "*", "\\.\\*", -1)

					profile.Spec.Net = &performancev2.Net{
						UserLevelNetworking: pointer.BoolPtr(true),
						Devices: []performancev2.Device{
							{
								InterfaceName: &netDeviceName,
							},
						}}
					manifest := getTunedManifest(profile)
					reservedSet, err := cpuset.Parse(string(*profile.Spec.CPU.Reserved))
					Expect(err).ToNot(HaveOccurred())
					reserveCPUcount := reservedSet.Size()
					channelsRegex := regexp.MustCompile(`\s*\[net\]\\ntype=net\\ndevices_udev_regex=` + devicesUdevRegex + `\\nchannels=combined\s*` + strconv.Itoa(reserveCPUcount) + `\s*`)
					Expect(channelsRegex.MatchString(manifest)).To(BeTrue())
				})
				It("should set by negative interface name with reserved CPUs count", func() {
					netDeviceName := "!ens5"
					//regex field should be: devices_udev_regex=^INTERFACE=(?!ens5)
					devicesUdevRegex := "\\^INTERFACE=\\(\\?!" + strings.Replace(netDeviceName, "*", "\\.\\*", -1) + "\\)"

					profile.Spec.Net = &performancev2.Net{
						UserLevelNetworking: pointer.BoolPtr(true),
						Devices: []performancev2.Device{
							{
								InterfaceName: &netDeviceName,
							},
						}}
					manifest := getTunedManifest(profile)
					reservedSet, err := cpuset.Parse(string(*profile.Spec.CPU.Reserved))
					Expect(err).ToNot(HaveOccurred())
					reserveCPUcount := reservedSet.Size()
					channelsRegex := regexp.MustCompile(`\s*\[net\]\\ntype=net\\ndevices_udev_regex=` + devicesUdevRegex + `\\nchannels=combined\s*` + strconv.Itoa(reserveCPUcount) + `\s*`)
					Expect(channelsRegex.MatchString(manifest)).To(BeTrue())
				})
				It("should set by specific vendor with reserved CPUs count", func() {
					netDeviceVendorID := "0x1af4"
					//regex field should be: devices_udev_regex=^ID_VENDOR_ID=0x1af4
					devicesUdevRegex := "\\^ID_VENDOR_ID=" + netDeviceVendorID

					profile.Spec.Net = &performancev2.Net{
						UserLevelNetworking: pointer.BoolPtr(true),
						Devices: []performancev2.Device{
							{
								VendorID: &netDeviceVendorID,
							},
						}}
					manifest := getTunedManifest(profile)
					reservedSet, err := cpuset.Parse(string(*profile.Spec.CPU.Reserved))
					Expect(err).ToNot(HaveOccurred())
					reserveCPUcount := reservedSet.Size()
					channelsRegex := regexp.MustCompile(`\s*\[net\]\\ntype=net\\ndevices_udev_regex=` + devicesUdevRegex + `\\nchannels=combined\s*` + strconv.Itoa(reserveCPUcount) + `\s*`)
					Expect(channelsRegex.MatchString(manifest)).To(BeTrue())
				})
				It("should set by specific vendor and model with reserved CPUs count", func() {
					netDeviceVendorID := "0x1af4"
					netDeviceModelID := "0x1000"
					//regex field should be: devices_udev_regex=^ID_MODEL_ID=0x1000[\s\S]*^ID_VENDOR_ID=0x1af4
					devicesUdevRegex := `\^ID_MODEL_ID=` + netDeviceModelID + `\[\\\\s\\\\S]\*\^ID_VENDOR_ID=` + netDeviceVendorID

					profile.Spec.Net = &performancev2.Net{
						UserLevelNetworking: pointer.BoolPtr(true),
						Devices: []performancev2.Device{
							{
								DeviceID: &netDeviceModelID,
								VendorID: &netDeviceVendorID,
							},
						}}
					manifest := getTunedManifest(profile)
					reservedSet, err := cpuset.Parse(string(*profile.Spec.CPU.Reserved))
					Expect(err).ToNot(HaveOccurred())
					reserveCPUcount := reservedSet.Size()
					channelsRegex := regexp.MustCompile(`\s*\[net\]\\ntype=net\\ndevices_udev_regex=` + devicesUdevRegex + `\\nchannels=combined\s*` + strconv.Itoa(reserveCPUcount) + `\s*`)
					Expect(channelsRegex.MatchString(manifest)).To(BeTrue())
				})
				It("should set by specific vendor,model and interface name with reserved CPUs count", func() {
					netDeviceName := "ens5"
					netDeviceVendorID := "0x1af4"
					netDeviceModelID := "0x1000"
					//regex field should be: devices_udev_regex=^ID_MODEL_ID=0x1000[\s\S]*^ID_VENDOR_ID=0x1af4[\s\S]*^INTERFACE=ens5
					devicesUdevRegex := `\^ID_MODEL_ID=` + netDeviceModelID + `\[\\\\s\\\\S]\*\^ID_VENDOR_ID=` + netDeviceVendorID + `\[\\\\s\\\\S]\*\^INTERFACE=` + netDeviceName

					profile.Spec.Net = &performancev2.Net{
						UserLevelNetworking: pointer.BoolPtr(true),
						Devices: []performancev2.Device{
							{
								InterfaceName: &netDeviceName,
								DeviceID:      &netDeviceModelID,
								VendorID:      &netDeviceVendorID,
							},
						}}
					manifest := getTunedManifest(profile)
					reservedSet, err := cpuset.Parse(string(*profile.Spec.CPU.Reserved))
					Expect(err).ToNot(HaveOccurred())
					reserveCPUcount := reservedSet.Size()
					channelsRegex := regexp.MustCompile(`\s*\[net\]\\ntype=net\\ndevices_udev_regex=` + devicesUdevRegex + `\\nchannels=combined\s*` + strconv.Itoa(reserveCPUcount) + `\s*`)
					Expect(channelsRegex.MatchString(manifest)).To(BeTrue())
				})
				It("should set by specific vendor,model and negative interface name with reserved CPUs count", func() {
					netDeviceName := "!ens5"
					netDeviceVendorID := "0x1af4"
					netDeviceModelID := "0x1000"
					//regex field should be: devices_udev_regex=^ID_MODEL_ID=0x1000[\\s\\S]*^ID_VENDOR_ID=0x1af4[\\s\\S]*^INTERFACE=(?!ens5)
					devicesUdevRegex := `\^ID_MODEL_ID=` + netDeviceModelID + `\[\\\\s\\\\S]\*\^ID_VENDOR_ID=` + netDeviceVendorID + `\[\\\\s\\\\S]\*\^INTERFACE=\(\?!` + netDeviceName + `\)`

					profile.Spec.Net = &performancev2.Net{
						UserLevelNetworking: pointer.BoolPtr(true),
						Devices: []performancev2.Device{
							{
								InterfaceName: &netDeviceName,
								DeviceID:      &netDeviceModelID,
								VendorID:      &netDeviceVendorID,
							},
						}}
					manifest := getTunedManifest(profile)
					reservedSet, err := cpuset.Parse(string(*profile.Spec.CPU.Reserved))
					Expect(err).ToNot(HaveOccurred())
					reserveCPUcount := reservedSet.Size()
					channelsRegex := regexp.MustCompile(`\s*\[net\]\\ntype=net\\ndevices_udev_regex=` + devicesUdevRegex + `\\nchannels=combined\s*` + strconv.Itoa(reserveCPUcount) + `\s*`)
					Expect(channelsRegex.MatchString(manifest)).To(BeTrue())
				})
			})
		})
	})
})
