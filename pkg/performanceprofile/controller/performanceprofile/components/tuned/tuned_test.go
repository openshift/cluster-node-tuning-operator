package tuned

import (
	"regexp"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	testutils "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/utils/testing"
	"gopkg.in/ini.v1"
	"sigs.k8s.io/yaml"

	cpuset "k8s.io/utils/cpuset"
	"k8s.io/utils/pointer"
)

const expectedMatchSelector = `
  - machineConfigLabels:
      mcKey: mcValue
`

var (
	cmdlineAdditionalArgs            = "+audit=0 processor.max_cstate=1 idle=poll intel_idle.max_cstate=0"
	cmdlineAmdHighPowerConsumption   = "processor.max_cstate=1"
	cmdlineAmdPstateActive           = "amd_pstate=active"
	cmdlineAmdPstateAutomatic        = "amd_pstate=guided"
	cmdlineAmdPstatePassive          = "amd_pstate=passive"
	cmdlineCPUsPartitioning          = "+nohz=on rcu_nocbs=${isolated_cores} tuned.non_isolcpus=${not_isolated_cpumask} systemd.cpu_affinity=${not_isolated_cores_expanded}"
	cmdlineDummy2MHugePages          = "+ default_hugepagesz=1G   hugepagesz=1G hugepages=4 hugepagesz=2M hugepages=0"
	cmdlineHugepages                 = "+ default_hugepagesz=1G   hugepagesz=1G hugepages=4"
	cmdlineIdlePoll                  = "idle=poll"
	cmdlineIntelHighPowerConsumption = "processor.max_cstate=1 intel_idle.max_cstate=0"
	cmdlineIntelPstateActive         = "intel_pstate=active"
	cmdlineIntelPstateAutomatic      = "intel_pstate=${f:intel_recommended_pstate}"
	cmdlineIntelPstatePassive        = "intel_pstate=passive"
	cmdlineMultipleHugePages         = "+ default_hugepagesz=1G   hugepagesz=1G hugepages=4 hugepagesz=2M hugepages=128"
	cmdlineRealtime                  = "+nohz_full=${isolated_cores} nosoftlockup skew_tick=1 rcutree.kthread_prio=11"
	cmdlineWithoutStaticIsolation    = "+isolcpus=managed_irq,${isolated_cores}"
	cmdlineWithStaticIsolation       = "+isolcpus=domain,managed_irq,${isolated_cores}"
)

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

	getTunedStructuredData := func(profile *performancev2.PerformanceProfile, profileName string) *ini.File {
		tuned, err := NewNodePerformance(profile)
		Expect(err).ToNot(HaveOccurred())
		var profileIndex int
		// The index order here should match how they are defined in tuned.go
		switch profileName {
		case components.ProfileNamePerformance:
			profileIndex = 0
		case components.ProfileNamePerformanceRT:
			profileIndex = 1
		case components.ProfileNameAmdX86:
			profileIndex = 2
		case components.ProfileNameArmAarch64:
			profileIndex = 3
		case components.ProfileNameIntelX86:
			profileIndex = 4
		default:
			profileIndex = 0
		}
		tunedData := []byte(*tuned.Spec.Profile[profileIndex].Data)
		cfg, err := ini.Load(tunedData)
		print(cfg)
		print(err)
		Expect(err).ToNot(HaveOccurred())
		return cfg
	}

	Context("with worker performance profile", func() {
		It("should generate yaml with expected parameters", func() {
			manifest := getTunedManifest(profile)
			tunedData := getTunedStructuredData(profile, components.ProfileNamePerformance)
			isolated, err := tunedData.GetSection("variables")
			Expect(err).ToNot(HaveOccurred())

			Expect(manifest).To(ContainSubstring(expectedMatchSelector))
			Expect(isolated.Key("isolated_cores").String()).To(Equal("4-5"))

			cpuSection, err := tunedData.GetSection("cpu")
			Expect(err).ToNot(HaveOccurred())
			Expect((cpuSection.Key("force_latency").String())).To(Equal("cstate.id:1|3"))
			Expect((cpuSection.Key("governor").String())).To(Equal("performance"))
			Expect((cpuSection.Key("energy_perf_bias").String())).To(Equal("performance"))
			Expect((cpuSection.Key("min_perf_pct").String())).To(Equal("100"))

			serviceSection, err := tunedData.GetSection("service")
			Expect(err).ToNot(HaveOccurred())
			Expect(serviceSection.Key("service.stalld").String()).To(Equal("start,enable"))

			schedulerSection, err := tunedData.GetSection("scheduler")
			Expect(err).ToNot(HaveOccurred())
			Expect(schedulerSection.Key("group.ksoftirqd").String()).To(Equal("0:f:11:*:ksoftirqd.*"))
			Expect(schedulerSection.Key("group.rcuc").String()).To(Equal("0:f:11:*:rcuc.*"))
			Expect(schedulerSection.Key("group.ktimers").String()).To(Equal("0:f:11:*:ktimers.*"))

			sysctlSection, err := tunedData.GetSection("sysctl")
			Expect(err).ToNot(HaveOccurred())
			Expect(sysctlSection.Key("kernel.hung_task_timeout_secs").String()).To(Equal("600"))
			Expect(sysctlSection.Key("kernel.sched_rt_runtime_us").String()).To(Equal("-1"))

			bootLoaderSection, err := tunedData.GetSection("bootloader")
			Expect(err).ToNot(HaveOccurred())
			Expect(bootLoaderSection.Key("cmdline_cpu_part").String()).To(Equal(cmdlineCPUsPartitioning))
			Expect(bootLoaderSection.Key("cmdline_isolation").String()).To(Equal(cmdlineWithoutStaticIsolation))
			Expect(bootLoaderSection.Key("cmdline_hugepages").String()).To(Equal(cmdlineHugepages))
			Expect(bootLoaderSection.Key("cmdline_additionalArg").String()).To(Equal(cmdlineAdditionalArgs))
			Expect(bootLoaderSection.Key("cmdline_realtime").String()).To(Equal(cmdlineRealtime))
		})

		Context("default profile default tuned", func() {
			It("should [cpu] section in tuned", func() {
				tunedData := getTunedStructuredData(profile, components.ProfileNamePerformance)
				cpuSection, err := tunedData.GetSection("cpu")
				Expect(err).ToNot(HaveOccurred())
				Expect((cpuSection.Key("force_latency").String())).To(Equal("cstate.id:1|3"))
				Expect((cpuSection.Key("governor").String())).To(Equal("performance"))
				Expect((cpuSection.Key("energy_perf_bias").String())).To(Equal("performance"))
				Expect((cpuSection.Key("min_perf_pct").String())).To(Equal("100"))
			})
		})

		When("realtime hint disabled", func() {
			It("should not contain realtime related parameters", func() {
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{RealTime: pointer.Bool(false)}
				tunedData := getTunedStructuredData(profile, components.ProfileNamePerformance)
				service, err := tunedData.GetSection("service")
				Expect(err).ToNot(HaveOccurred())
				Expect(service.Key("service.stalld").String()).To(Equal("stop,disable"))

				schedulerSection, err := tunedData.GetSection("scheduler")
				Expect(err).ToNot(HaveOccurred())
				Expect(schedulerSection.Key("sched_rt_runtime_us").String()).ToNot(Equal("-1"))

				sysctlSection, err := tunedData.GetSection("sysctl")
				Expect(err).ToNot(HaveOccurred())
				Expect(sysctlSection.Key("kernel.hung_task_timeout_secs").String()).ToNot(Equal("600"))
				Expect(sysctlSection.Key("kernel.sched_rt_runtime_us").String()).ToNot(Equal("-1"))

				bootLoaderSection, err := tunedData.GetSection("bootloader")
				Expect(err).ToNot(HaveOccurred())
				Expect(bootLoaderSection.Key("cmdline_realtime").String()).ToNot(Equal(cmdlineRealtime))
			})
		})

		When("realtime hint enabled", func() {
			It("should contain realtime related parameters", func() {
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{RealTime: pointer.Bool(true)}
				tunedData := getTunedStructuredData(profile, components.ProfileNamePerformance)
				service, err := tunedData.GetSection("service")
				Expect(err).ToNot(HaveOccurred())
				Expect(service.Key("service.stalld").String()).To(Equal("start,enable"))
				sysctl, err := tunedData.GetSection("sysctl")
				Expect(err).ToNot(HaveOccurred())
				Expect(sysctl.Key("kernel.hung_task_timeout_secs").String()).To(Equal("600"))
				Expect(sysctl.Key("kernel.sched_rt_runtime_us").String()).To(Equal("-1"))
				bootLoader, err := tunedData.GetSection("bootloader")
				Expect(err).ToNot(HaveOccurred())
				Expect(bootLoader.Key("cmdline_realtime").String()).To(Equal(cmdlineRealtime))
			})
		})

		Context("high power consumption hint enabled", func() {
			When("default realtime workload settings", func() {
				It("should not contain high power consumption related parameters", func() {
					profile.Spec.WorkloadHints = &performancev2.WorkloadHints{HighPowerConsumption: pointer.Bool(true)}
					tunedData := getTunedStructuredData(profile, components.ProfileNamePerformance)
					bootLoader, err := tunedData.GetSection("bootloader")
					Expect(err).ToNot(HaveOccurred())
					Expect(bootLoader.Key("cmdline_power_performance").String()).To(Equal(""))
				})
			})

			When("realtime workload enabled", func() {
				It("should not contain idle=poll cmdline", func() {
					profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
						HighPowerConsumption: pointer.Bool(true),
						RealTime:             pointer.Bool(true),
					}
					tunedData := getTunedStructuredData(profile, components.ProfileNamePerformance)
					bootLoader, err := tunedData.GetSection("bootloader")
					Expect(err).ToNot(HaveOccurred())
					Expect(bootLoader.Key("cmdline_idle_poll").String()).To(Equal(""))
				})
			})

			When("perPodPowerManagement Hint to true", func() {
				It("should fail as PerPodPowerManagement and HighPowerConsumption can not be set to true", func() {
					profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
						HighPowerConsumption:  pointer.Bool(true),
						PerPodPowerManagement: pointer.Bool(true),
					}
					_, err := NewNodePerformance(profile)
					Expect(err.Error()).To(ContainSubstring("Invalid WorkloadHints configuration: HighPowerConsumption is true and PerPodPowerManagement is true"))
				})
			})
		})

		When("perPodPowerManagement Hint is false realTime Hint false", func() {
			It("should not contain perPodPowerManagement related parameters", func() {
				profile.Spec.WorkloadHints.PerPodPowerManagement = pointer.Bool(false)
				profile.Spec.WorkloadHints.RealTime = pointer.Bool(false)
				tunedData := getTunedStructuredData(profile, components.ProfileNamePerformance)
				cpuSection, err := tunedData.GetSection("cpu")
				Expect(err).ToNot(HaveOccurred())
				Expect(cpuSection.Key("enabled").String()).ToNot(Equal("false"))
				_, err = tunedData.GetSection("bootloader")
				Expect(err).ToNot(HaveOccurred())
			})
		})

		When("perPodPowerManagement Hint is false realTime Hint true", func() {
			It("should not contain perPodPowerManagement related parameters", func() {
				profile.Spec.WorkloadHints.PerPodPowerManagement = pointer.Bool(false)
				profile.Spec.WorkloadHints.RealTime = pointer.Bool(true)
				tunedData := getTunedStructuredData(profile, components.ProfileNamePerformance)
				cpuSection, err := tunedData.GetSection("cpu")
				Expect(err).ToNot(HaveOccurred())
				Expect(cpuSection.Key("enabled").String()).ToNot(Equal("false"))
				_, err = tunedData.GetSection("bootloader")
				Expect(err).ToNot(HaveOccurred())
			})
		})

		When("perPodPowerManagement Hint to true", func() {
			It("should contain perPodPowerManagement related parameters", func() {
				profile.Spec.WorkloadHints.PerPodPowerManagement = pointer.Bool(true)
				tunedData := getTunedStructuredData(profile, components.ProfileNamePerformance)
				cpuSection, err := tunedData.GetSection("cpu")
				Expect(err).ToNot(HaveOccurred())
				Expect(cpuSection.Key("enabled").String()).To(Equal("false"))
				_, err = tunedData.GetSection("bootloader")
				Expect(err).ToNot(HaveOccurred())
			})
		})

		It("should generate yaml with expected parameters for Isolated balancing disabled", func() {
			profile.Spec.CPU.BalanceIsolated = pointer.Bool(false)
			tunedData := getTunedStructuredData(profile, components.ProfileNamePerformance)
			bootLoader, err := tunedData.GetSection("bootloader")
			Expect(err).ToNot(HaveOccurred())
			Expect(bootLoader.Key("cmdline_isolation").String()).To(Equal(cmdlineWithStaticIsolation))
		})

		It("should generate yaml with expected parameters for Isolated balancing enabled", func() {
			profile.Spec.CPU.BalanceIsolated = pointer.Bool(true)
			tunedData := getTunedStructuredData(profile, components.ProfileNamePerformance)
			bootLoader, err := tunedData.GetSection("bootloader")
			Expect(err).ToNot(HaveOccurred())
			Expect(bootLoader.Key("cmdline_isolation").String()).To(Equal(cmdlineWithoutStaticIsolation))
		})

		// This tests checking Additional arguments is an example of how additional kernel args could look like
		// they have been selected randomly with no concrete purpose
		It("should contain additional additional parameters", func() {
			tunedData := getTunedStructuredData(profile, components.ProfileNamePerformance)
			bootLoader, err := tunedData.GetSection("bootloader")
			Expect(err).ToNot(HaveOccurred())
			Expect(bootLoader.Key("cmdline_additionalArg").String()).To(Equal(cmdlineAdditionalArgs))
		})

		It("should not contain additional additional parameters", func() {
			profile.Spec.AdditionalKernelArgs = nil
			tunedData := getTunedStructuredData(profile, components.ProfileNamePerformance)
			bootLoader, err := tunedData.GetSection("bootloader")
			Expect(err).ToNot(HaveOccurred())
			Expect(bootLoader.Key("cmdline_additionalArg").String()).ToNot(Equal(cmdlineAdditionalArgs))
		})

		It("should not allocate hugepages on the specific NUMA node via kernel arguments", func() {
			manifest := getTunedManifest(profile)
			Expect(strings.Count(manifest, "hugepagesz=")).To(BeNumerically("==", 2))
			Expect(strings.Count(manifest, "hugepages=")).To(BeNumerically("==", 3))

			profile.Spec.HugePages.Pages[0].Node = pointer.Int32(1)
			manifest = getTunedManifest(profile)
			Expect(strings.Count(manifest, "hugepagesz=")).To(BeNumerically("==", 1))
			Expect(strings.Count(manifest, "hugepages=")).To(BeNumerically("==", 2))
		})

		Context("with 1G default huge pages", func() {
			Context("with requested 2M huge pages allocation on the specified node", func() {
				It("should append the dummy 2M huge pages kernel arguments", func() {
					profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, performancev2.HugePage{
						Size:  components.HugepagesSize2M,
						Count: 128,
						Node:  pointer.Int32(0),
					})

					tunedData := getTunedStructuredData(profile, components.ProfileNamePerformance)
					bootLoader, err := tunedData.GetSection("bootloader")
					Expect(err).ToNot(HaveOccurred())
					Expect(bootLoader.Key("cmdline_hugepages").String()).To(Equal(cmdlineDummy2MHugePages))
				})
			})

			Context("with requested 2M huge pages allocation via kernel arguments", func() {
				It("should not append the dummy 2M kernel arguments", func() {
					profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, performancev2.HugePage{
						Size:  components.HugepagesSize2M,
						Count: 128,
					})

					tunedData := getTunedStructuredData(profile, components.ProfileNamePerformance)
					bootLoader, err := tunedData.GetSection("bootloader")
					Expect(err).ToNot(HaveOccurred())
					Expect(bootLoader.Key("cmdline_hugepages").String()).ToNot(Equal(cmdlineDummy2MHugePages))
					Expect(bootLoader.Key("cmdline_hugepages").String()).To(Equal(cmdlineMultipleHugePages))
				})
			})

			Context("without requested 2M hugepages", func() {
				It("should not append dummy 2M huge pages kernel arguments", func() {
					tunedData := getTunedStructuredData(profile, components.ProfileNamePerformance)
					bootLoader, err := tunedData.GetSection("bootloader")
					Expect(err).ToNot(HaveOccurred())
					Expect(bootLoader.Key("cmdline_hugepages").String()).ToNot(Equal(cmdlineDummy2MHugePages))
				})
			})

			Context("with requested 2M huge pages allocation on the specified node and via kernel arguments", func() {
				It("should not append the dummy 2M kernel arguments", func() {
					profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, performancev2.HugePage{
						Size:  components.HugepagesSize2M,
						Count: 128,
						Node:  pointer.Int32(0),
					})
					profile.Spec.HugePages.Pages = append(profile.Spec.HugePages.Pages, performancev2.HugePage{
						Size:  components.HugepagesSize2M,
						Count: 128,
					})

					tunedData := getTunedStructuredData(profile, components.ProfileNamePerformance)
					bootLoader, err := tunedData.GetSection("bootloader")
					Expect(err).ToNot(HaveOccurred())
					Expect(bootLoader.Key("cmdline_hugepages").String()).ToNot(Equal(cmdlineDummy2MHugePages))
					Expect(bootLoader.Key("cmdline_hugepages").String()).To(Equal(cmdlineMultipleHugePages))
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
						Node:  pointer.Int32(0),
					})

					tunedData := getTunedStructuredData(profile, components.ProfileNamePerformance)
					bootLoader, err := tunedData.GetSection("bootloader")
					Expect(err).ToNot(HaveOccurred())
					Expect(bootLoader.Key("cmdline_hugepages").String()).ToNot(Equal(cmdlineDummy2MHugePages))
					Expect(bootLoader.Key("cmdline_hugepages").String()).ToNot(Equal(cmdlineMultipleHugePages))
				})
			})
		})

		Context("with user level networking enabled", func() {
			Context("with default net device queues (all devices set)", func() {
				It("should set the default netqueues count to reserved CPUs count", func() {
					profile.Spec.Net = &performancev2.Net{
						UserLevelNetworking: pointer.Bool(true),
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
						UserLevelNetworking: pointer.Bool(true),
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
					netDeviceName := "ens5"
					netDeviceNameInverted := "!" + netDeviceName

					//regex field should be: devices_udev_regex=^INTERFACE=(?!ens5)
					devicesUdevRegex := "\\^INTERFACE=\\(\\?!" + strings.Replace(netDeviceName, "*", "\\.\\*", -1) + "\\)"

					profile.Spec.Net = &performancev2.Net{
						UserLevelNetworking: pointer.Bool(true),
						Devices: []performancev2.Device{
							{
								InterfaceName: &netDeviceNameInverted,
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
						UserLevelNetworking: pointer.Bool(true),
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
						UserLevelNetworking: pointer.Bool(true),
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
						UserLevelNetworking: pointer.Bool(true),
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
					netDeviceName := "ens5"
					netDeviceNameInverted := "!ens5"
					netDeviceVendorID := "0x1af4"
					netDeviceModelID := "0x1000"
					//regex field should be: devices_udev_regex=^ID_MODEL_ID=0x1000[\\s\\S]*^ID_VENDOR_ID=0x1af4[\\s\\S]*^INTERFACE=(?!ens5)
					devicesUdevRegex := `\^ID_MODEL_ID=` + netDeviceModelID + `\[\\\\s\\\\S]\*\^ID_VENDOR_ID=` + netDeviceVendorID + `\[\\\\s\\\\S]\*\^INTERFACE=\(\?!` + netDeviceName + `\)`

					profile.Spec.Net = &performancev2.Net{
						UserLevelNetworking: pointer.Bool(true),
						Devices: []performancev2.Device{
							{
								InterfaceName: &netDeviceNameInverted,
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

			Context("with user level networking nil pointer", func() {
				It("should create a manifest", func() {
					profile.Spec.Net = &performancev2.Net{
						UserLevelNetworking: nil,
					}
					manifest := getTunedManifest(profile)
					Expect(len(manifest)).ToNot(Equal(0))
				})
			})
		})
	})

	Context("with amd x86 performance profile", func() {
		When("perPodPowerManagement Hint is false and realTime hint is false", func() {
			It("should contain amd_pstate set to automatic", func() {
				profile.Spec.WorkloadHints.PerPodPowerManagement = pointer.Bool(false)
				tunedData := getTunedStructuredData(profile, components.ProfileNameAmdX86)
				bootloaderSection, err := tunedData.GetSection("bootloader")
				Expect(err).ToNot(HaveOccurred())
				Expect(bootloaderSection.Key("cmdline_pstate").String()).To(Equal(cmdlineAmdPstateAutomatic))
			})
		})
		When("perPodPowerManagement Hint is false and realTime hint is true", func() {
			It("should contain amd_pstate set to automatic", func() {
				profile.Spec.WorkloadHints.PerPodPowerManagement = pointer.Bool(false)
				profile.Spec.WorkloadHints.RealTime = pointer.Bool(true)
				tunedData := getTunedStructuredData(profile, components.ProfileNameAmdX86)
				bootloaderSection, err := tunedData.GetSection("bootloader")
				Expect(err).ToNot(HaveOccurred())
				Expect(bootloaderSection.Key("cmdline_pstate").String()).To(Equal(cmdlineAmdPstateAutomatic))
			})
		})
		When("perPodPowerManagement Hint is true", func() {
			It("should contain amd_pstate set to passive", func() {
				profile.Spec.WorkloadHints.PerPodPowerManagement = pointer.Bool(true)
				tunedData := getTunedStructuredData(profile, components.ProfileNameAmdX86)
				bootloaderSection, err := tunedData.GetSection("bootloader")
				Expect(err).ToNot(HaveOccurred())
				Expect(bootloaderSection.Key("cmdline_pstate").String()).To(Equal(cmdlineAmdPstatePassive))
			})
		})
		When("realtime workload enabled and high power consumption is enabled", func() {
			It("should contain idle=poll cmdline", func() {
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption: pointer.Bool(true),
					RealTime:             pointer.Bool(true),
				}
				tunedData := getTunedStructuredData(profile, components.ProfileNameAmdX86)
				bootloaderSection, err := tunedData.GetSection("bootloader")
				Expect(err).ToNot(HaveOccurred())
				Expect(bootloaderSection.Key("cmdline_idle_poll_amd").String()).To(Equal(cmdlineIdlePoll))
			})
		})
		When("realtime workload disabled and high power consumption is disabled", func() {
			It("should not contain idle=poll cmdline", func() {
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption: pointer.Bool(true),
					RealTime:             pointer.Bool(false),
				}
				tunedData := getTunedStructuredData(profile, components.ProfileNameAmdX86)
				bootloaderSection, err := tunedData.GetSection("bootloader")
				Expect(err).ToNot(HaveOccurred())
				Expect(bootloaderSection.Key("cmdline_idle_poll_amd").String()).To(Equal(""))
			})
		})
		When("high power consumption is enabled", func() {
			It("should contain high power consumption related parameters", func() {
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{HighPowerConsumption: pointer.Bool(true)}
				tunedData := getTunedStructuredData(profile, components.ProfileNameAmdX86)
				bootLoader, err := tunedData.GetSection("bootloader")
				Expect(err).ToNot(HaveOccurred())
				Expect(bootLoader.Key("cmdline_power_performance_amd").String()).To(Equal(cmdlineAmdHighPowerConsumption))
			})
		})
	})

	Context("with arm aarch64 performance profile", func() {
		When("regardless of perPodPowerManagement hint", func() {
			It("should not set pstate", func() {
				tunedData := getTunedStructuredData(profile, components.ProfileNameArmAarch64)
				bootloaderSection, err := tunedData.GetSection("bootloader")
				Expect(err).ToNot(HaveOccurred())
				Expect(bootloaderSection.Key("cmdline_pstate").String()).To(Equal(""))
			})
			It("should set iommu passthrough", func() {
				tunedData := getTunedStructuredData(profile, components.ProfileNameArmAarch64)
				bootloaderSection, err := tunedData.GetSection("bootloader")
				Expect(err).ToNot(HaveOccurred())
				Expect(bootloaderSection.Key("cmdline_iommu_arm").String()).To(Equal("iommu.passthrough=1"))
			})
		})
	})

	Context("with intel x86 performance profile", func() {
		When("perPodPowerManagement Hint is false and realTime hint is false", func() {
			It("should contain intel_pstate set to automatic", func() {
				profile.Spec.WorkloadHints.PerPodPowerManagement = pointer.Bool(false)
				tunedData := getTunedStructuredData(profile, components.ProfileNameIntelX86)
				bootloaderSection, err := tunedData.GetSection("bootloader")
				Expect(err).ToNot(HaveOccurred())
				Expect(bootloaderSection.Key("cmdline_pstate").String()).To(Equal(cmdlineIntelPstateAutomatic))
			})
		})
		When("perPodPowerManagement Hint is false and realTime hint is true", func() {
			It("should contain intel_pstate set to automatic", func() {
				profile.Spec.WorkloadHints.PerPodPowerManagement = pointer.Bool(false)
				profile.Spec.WorkloadHints.RealTime = pointer.Bool(true)
				tunedData := getTunedStructuredData(profile, components.ProfileNameIntelX86)
				bootloaderSection, err := tunedData.GetSection("bootloader")
				Expect(err).ToNot(HaveOccurred())
				Expect(bootloaderSection.Key("cmdline_pstate").String()).To(Equal(cmdlineIntelPstateAutomatic))
			})
		})
		When("perPodPowerManagement Hint is true", func() {
			It("should contain intel_pstate set to passive", func() {
				profile.Spec.WorkloadHints.PerPodPowerManagement = pointer.Bool(true)
				tunedData := getTunedStructuredData(profile, components.ProfileNameIntelX86)
				bootloaderSection, err := tunedData.GetSection("bootloader")
				Expect(err).ToNot(HaveOccurred())
				Expect(bootloaderSection.Key("cmdline_pstate").String()).To(Equal(cmdlineIntelPstatePassive))
			})
		})
		When("realtime workload enabled and high power consumption is enabled", func() {
			It("should contain idle=poll cmdline", func() {
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption: pointer.Bool(true),
					RealTime:             pointer.Bool(true),
				}
				tunedData := getTunedStructuredData(profile, components.ProfileNameIntelX86)
				bootloaderSection, err := tunedData.GetSection("bootloader")
				Expect(err).ToNot(HaveOccurred())
				Expect(bootloaderSection.Key("cmdline_idle_poll_intel").String()).To(Equal(cmdlineIdlePoll))
			})
		})
		When("realtime workload disabled and high power consumption is disabled", func() {
			It("should not contain idle=poll cmdline", func() {
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption: pointer.Bool(true),
					RealTime:             pointer.Bool(false),
				}
				tunedData := getTunedStructuredData(profile, components.ProfileNameIntelX86)
				bootloaderSection, err := tunedData.GetSection("bootloader")
				Expect(err).ToNot(HaveOccurred())
				Expect(bootloaderSection.Key("cmdline_idle_poll_intel").String()).To(Equal(""))
			})
		})
		When("high power consumption is enabled", func() {
			It("should contain high power consumption related parameters", func() {
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{HighPowerConsumption: pointer.Bool(true)}
				tunedData := getTunedStructuredData(profile, components.ProfileNameIntelX86)
				bootLoader, err := tunedData.GetSection("bootloader")
				Expect(err).ToNot(HaveOccurred())
				Expect(bootLoader.Key("cmdline_power_performance_intel").String()).To(Equal(cmdlineIntelHighPowerConsumption))
			})
		})
	})
})
