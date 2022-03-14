package __performance

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"

	performancev2 "github.com/openshift-kni/performance-addon-operators/api/v2"
	testutils "github.com/openshift-kni/performance-addon-operators/functests/utils"
	testclient "github.com/openshift-kni/performance-addon-operators/functests/utils/client"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/cluster"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/discovery"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/events"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/images"
	testlog "github.com/openshift-kni/performance-addon-operators/functests/utils/log"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/nodes"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/pods"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/profiles"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components"
)

var workerRTNode *corev1.Node
var profile *performancev2.PerformanceProfile

const (
	sysDevicesOnlineCPUs = "/sys/devices/system/cpu/online"
)

var _ = Describe("[rfe_id:27363][performance] CPU Management", func() {
	var balanceIsolated bool
	var reservedCPU, isolatedCPU string
	var listReservedCPU []int
	var reservedCPUSet cpuset.CPUSet
	var onlineCPUSet cpuset.CPUSet

	testutils.BeforeAll(func() {
		isSNO, err := cluster.IsSingleNode()
		Expect(err).ToNot(HaveOccurred())
		RunningOnSingleNode = isSNO
	})

	BeforeEach(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}

		workerRTNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("error looking for the optional selector: %v", err))
		Expect(workerRTNodes).ToNot(BeEmpty())
		workerRTNode = &workerRTNodes[0]
		profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		By(fmt.Sprintf("Checking the profile %s with cpus %s", profile.Name, cpuSpecToString(profile.Spec.CPU)))
		balanceIsolated = true
		if profile.Spec.CPU.BalanceIsolated != nil {
			balanceIsolated = *profile.Spec.CPU.BalanceIsolated
		}

		Expect(profile.Spec.CPU.Isolated).NotTo(BeNil())
		isolatedCPU = string(*profile.Spec.CPU.Isolated)

		Expect(profile.Spec.CPU.Reserved).NotTo(BeNil())
		reservedCPU = string(*profile.Spec.CPU.Reserved)
		reservedCPUSet, err = cpuset.Parse(reservedCPU)
		Expect(err).ToNot(HaveOccurred())
		listReservedCPU = reservedCPUSet.ToSlice()

		onlineCPUSet, err = nodes.GetOnlineCPUsSet(workerRTNode)
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Verification of configuration on the worker node", func() {
		It("[test_id:28528][crit:high][vendor:cnf-qe@redhat.com][level:acceptance] Verify CPU reservation on the node", func() {
			By(fmt.Sprintf("Allocatable CPU should be less than capacity by %d", len(listReservedCPU)))
			capacityCPU, _ := workerRTNode.Status.Capacity.Cpu().AsInt64()
			allocatableCPU, _ := workerRTNode.Status.Allocatable.Cpu().AsInt64()
			differenceCPUGot := capacityCPU - allocatableCPU
			differenceCPUExpected := int64(len(listReservedCPU))
			Expect(differenceCPUGot).To(Equal(differenceCPUExpected), "Allocatable CPU %d should be less than capacity %d by %d; got %d instead", allocatableCPU, capacityCPU, differenceCPUExpected, differenceCPUGot)
		})

		It("[test_id:37862][crit:high][vendor:cnf-qe@redhat.com][level:acceptance] Verify CPU affinity mask, CPU reservation and CPU isolation on worker node", func() {
			By("checking isolated CPU")
			cmd := []string{"cat", "/sys/devices/system/cpu/isolated"}
			sysIsolatedCpus, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
			Expect(err).ToNot(HaveOccurred())
			if balanceIsolated {
				Expect(sysIsolatedCpus).To(BeEmpty())
			} else {
				Expect(sysIsolatedCpus).To(Equal(isolatedCPU))
			}

			By("checking reserved CPU in kubelet config file")
			cmd = []string{"cat", "/rootfs/etc/kubernetes/kubelet.conf"}
			conf, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
			Expect(err).ToNot(HaveOccurred(), "failed to cat kubelet.conf")
			// kubelet.conf changed formatting, there is a space after colons atm. Let's deal with both cases with a regex
			Expect(conf).To(MatchRegexp(fmt.Sprintf(`"reservedSystemCPUs": ?"%s"`, reservedCPU)))

			By("checking CPU affinity mask for kernel scheduler")
			cmd = []string{"/bin/bash", "-c", "taskset -pc 1"}
			sched, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
			Expect(err).ToNot(HaveOccurred(), "failed to execute taskset")
			mask := strings.SplitAfter(sched, " ")
			maskSet, err := cpuset.Parse(mask[len(mask)-1])
			Expect(err).ToNot(HaveOccurred())

			Expect(reservedCPUSet.IsSubsetOf(maskSet)).To(Equal(true), fmt.Sprintf("The init process (pid 1) should have cpu affinity: %s", reservedCPU))
		})

		It("[test_id:34358] Verify rcu_nocbs kernel argument on the node", func() {
			By("checking that cmdline contains rcu_nocbs with right value")
			cmd := []string{"cat", "/proc/cmdline"}
			cmdline, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
			Expect(err).ToNot(HaveOccurred())
			re := regexp.MustCompile(`rcu_nocbs=\S+`)
			rcuNocbsArgument := re.FindString(cmdline)
			Expect(rcuNocbsArgument).To(ContainSubstring("rcu_nocbs="))
			rcuNocbsCpu := strings.Split(rcuNocbsArgument, "=")[1]
			Expect(rcuNocbsCpu).To(Equal(isolatedCPU))

			By("checking that new rcuo processes are running on non_isolated cpu")
			cmd = []string{"pgrep", "rcuo"}
			rcuoList, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
			Expect(err).ToNot(HaveOccurred())
			for _, rcuo := range strings.Split(rcuoList, "\n") {
				// check cpu affinity mask
				cmd = []string{"/bin/bash", "-c", fmt.Sprintf("taskset -pc %s", rcuo)}
				taskset, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				mask := strings.SplitAfter(taskset, " ")
				maskSet, err := cpuset.Parse(mask[len(mask)-1])
				Expect(err).ToNot(HaveOccurred())
				Expect(reservedCPUSet.IsSubsetOf(maskSet)).To(Equal(true), "The process should have cpu affinity: %s", reservedCPU)
			}
		})
	})

	Describe("Verification of cpu manager functionality", func() {
		var testpod *corev1.Pod
		var discoveryFailed bool

		testutils.BeforeAll(func() {
			discoveryFailed = false
			if discovery.Enabled() {
				profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
				Expect(err).ToNot(HaveOccurred())
				isolatedCPU = string(*profile.Spec.CPU.Isolated)
			}
		})

		BeforeEach(func() {
			if discoveryFailed {
				Skip("Skipping tests since there are insufficant isolated cores to create a stress pod")
			}
		})

		AfterEach(func() {
			deleteTestPod(testpod)
		})

		table.DescribeTable("Verify CPU usage by stress PODs", func(guaranteed bool) {
			cpuID := onlineCPUSet.ToSliceNoSort()[0]
			smtLevel := nodes.GetSMTLevel(cpuID, workerRTNode)
			if smtLevel < 2 {
				Skip(fmt.Sprintf("designated worker node %q has SMT level %d - minimum required 2", workerRTNode.Name, smtLevel))
			}

			// note must be a multiple of the smtLevel. Pick the minimum to maximize the chances to run on CI
			cpuRequest := smtLevel
			testpod = getStressPod(workerRTNode.Name, cpuRequest)
			testpod.Namespace = testutils.NamespaceTesting

			listCPU := onlineCPUSet.ToSlice()
			expectedQos := corev1.PodQOSBurstable

			if guaranteed {
				listCPU = onlineCPUSet.Difference(reservedCPUSet).ToSlice()
				expectedQos = corev1.PodQOSGuaranteed
				promotePodToGuaranteed(testpod)
			} else if !balanceIsolated {
				// when balanceIsolated is False - non-guaranteed pod should run on reserved cpu
				listCPU = listReservedCPU
			}

			var err error
			err = testclient.Client.Create(context.TODO(), testpod)
			Expect(err).ToNot(HaveOccurred())

			err = pods.WaitForCondition(testpod, corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
			logEventsForPod(testpod)
			Expect(err).ToNot(HaveOccurred())

			updatedPod := &corev1.Pod{}
			err = testclient.Client.Get(context.TODO(), client.ObjectKeyFromObject(testpod), updatedPod)
			Expect(err).ToNot(HaveOccurred())
			Expect(updatedPod.Status.QOSClass).To(Equal(expectedQos),
				"unexpected QoS Class for %s/%s: %s (looking for %s)",
				updatedPod.Namespace, updatedPod.Name, updatedPod.Status.QOSClass, expectedQos)

			output, err := nodes.ExecCommandOnNode(
				[]string{"/bin/bash", "-c", "ps -o psr $(pgrep -n stress) | tail -1"},
				workerRTNode,
			)
			Expect(err).ToNot(HaveOccurred(), "failed to get cpu of stress process")
			cpu, err := strconv.Atoi(strings.Trim(output, " "))
			Expect(err).ToNot(HaveOccurred())

			Expect(cpu).To(BeElementOf(listCPU))
		},
			table.Entry("[test_id:37860] Non-guaranteed POD can work on any CPU", false),
			table.Entry("[test_id:27492] Guaranteed POD should work on isolated cpu", true),
		)
	})

	When("pod runs with the CPU load balancing runtime class", func() {
		var smtLevel int
		var testpod *corev1.Pod
		var defaultFlags map[int][]int

		getCPUsSchedulingDomainFlags := func() (map[int][]int, error) {
			cmd := []string{"/bin/bash", "-c", "more /proc/sys/kernel/sched_domain/cpu*/domain*/flags | cat"}
			out, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
			if err != nil {
				return nil, err
			}

			re, err := regexp.Compile(`/proc/sys/kernel/sched_domain/cpu(\d+)/domain\d+/flags\n:+\n(\d+)`)
			if err != nil {
				return nil, err
			}

			allSubmatch := re.FindAllStringSubmatch(out, -1)
			cpuToSchedDomains := map[int][]int{}
			for _, submatch := range allSubmatch {
				if len(submatch) != 3 {
					return nil, fmt.Errorf("the sched_domain submatch %v does not have a valid length", submatch)
				}

				cpu, err := strconv.Atoi(submatch[1])
				if err != nil {
					return nil, err
				}

				if _, ok := cpuToSchedDomains[cpu]; !ok {
					cpuToSchedDomains[cpu] = []int{}
				}

				flags, err := strconv.Atoi(submatch[2])
				if err != nil {
					return nil, err
				}

				cpuToSchedDomains[cpu] = append(cpuToSchedDomains[cpu], flags)
			}

			// sort sched_domain
			for cpu := range cpuToSchedDomains {
				sort.Ints(cpuToSchedDomains[cpu])
			}

			testlog.Infof("Scheduler domains: %v", cpuToSchedDomains)
			return cpuToSchedDomains, nil
		}

		BeforeEach(func() {
			var err error
			defaultFlags, err = getCPUsSchedulingDomainFlags()
			Expect(err).ToNot(HaveOccurred())

			annotations := map[string]string{
				"cpu-load-balancing.crio.io": "disable",
			}
			// any random existing cpu is fine
			cpuID := onlineCPUSet.ToSliceNoSort()[0]
			smtLevel = nodes.GetSMTLevel(cpuID, workerRTNode)
			testpod = getTestPodWithAnnotations(annotations, smtLevel)
		})

		AfterEach(func() {
			deleteTestPod(testpod)
		})

		It("[test_id:32646] should disable CPU load balancing for CPU's used by the pod", func() {
			var err error
			By("Starting the pod")
			err = testclient.Client.Create(context.TODO(), testpod)
			Expect(err).ToNot(HaveOccurred())

			err = pods.WaitForCondition(testpod, corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
			logEventsForPod(testpod)
			Expect(err).ToNot(HaveOccurred())

			By("Getting the container cpuset.cpus cgroup")
			containerID, err := pods.GetContainerIDByName(testpod, "test")
			Expect(err).ToNot(HaveOccurred())

			containerCgroup := ""
			Eventually(func() string {
				cmd := []string{"/bin/bash", "-c", fmt.Sprintf("find /rootfs/sys/fs/cgroup/cpuset/ -name *%s*", containerID)}
				containerCgroup, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				return containerCgroup
			}, (cluster.ComputeTestTimeout(30*time.Second, RunningOnSingleNode)), 5*time.Second).ShouldNot(BeEmpty(),
				fmt.Sprintf("cannot find cgroup for container %q", containerID))

			By("Checking what CPU the pod is using")
			cmd := []string{"/bin/bash", "-c", fmt.Sprintf("cat %s/cpuset.cpus", containerCgroup)}
			output, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
			Expect(err).ToNot(HaveOccurred())

			cpus, err := cpuset.Parse(output)
			Expect(err).ToNot(HaveOccurred())

			By("Getting the CPU scheduling flags")
			flags, err := getCPUsSchedulingDomainFlags()
			Expect(err).ToNot(HaveOccurred())

			By("Verifying that the CPU load balancing was disabled")
			for _, cpu := range cpus.ToSlice() {
				Expect(len(flags[cpu])).To(Equal(len(defaultFlags[cpu])))
				// the CPU flags should be almost the same except the LSB that should be disabled
				// see https://github.com/torvalds/linux/blob/0fe5f9ca223573167c4c4156903d751d2c8e160e/include/linux/sched/topology.h#L14
				// for more information regarding the sched domain flags
				for i := range flags[cpu] {
					Expect(flags[cpu][i]).To(Equal(defaultFlags[cpu][i] - 1))
				}
			}

			By("Deleting the pod")
			deleteTestPod(testpod)

			By("Getting the CPU scheduling flags")
			flags, err = getCPUsSchedulingDomainFlags()
			Expect(err).ToNot(HaveOccurred())

			By("Verifying that the CPU load balancing was enabled back")
			for _, cpu := range cpus.ToSlice() {
				Expect(len(flags[cpu])).To(Equal(len(defaultFlags[cpu])))
				// the CPU scheduling flags should be restored to the default values
				for i := range flags[cpu] {
					Expect(flags[cpu][i]).To(Equal(defaultFlags[cpu][i]))
				}
			}
		})
	})

	Describe("Verification that IRQ load balance can be disabled per POD", func() {
		var smtLevel int
		var testpod *corev1.Pod

		BeforeEach(func() {
			Skip("part of interrupts does not support CPU affinity change because of underlying hardware")

			if profile.Spec.GloballyDisableIrqLoadBalancing != nil && *profile.Spec.GloballyDisableIrqLoadBalancing {
				Skip("IRQ load balance should be enabled (GloballyDisableIrqLoadBalancing=false), skipping test")
			}

			cpuID := onlineCPUSet.ToSliceNoSort()[0]
			smtLevel = nodes.GetSMTLevel(cpuID, workerRTNode)
		})

		AfterEach(func() {
			deleteTestPod(testpod)
		})

		It("[test_id:36364] should disable IRQ balance for CPU where POD is running", func() {
			By("checking default smp affinity is equal to all active CPUs")
			defaultSmpAffinitySet, err := nodes.GetDefaultSmpAffinitySet(workerRTNode)
			Expect(err).ToNot(HaveOccurred())

			onlineCPUsSet, err := nodes.GetOnlineCPUsSet(workerRTNode)
			Expect(err).ToNot(HaveOccurred())

			Expect(onlineCPUsSet.IsSubsetOf(defaultSmpAffinitySet)).To(BeTrue(), "All online CPUs %s should be subset of default SMP affinity %s", onlineCPUsSet, defaultSmpAffinitySet)

			By("Running pod with annotations that disable specific CPU from IRQ balancer")
			annotations := map[string]string{
				"irq-load-balancing.crio.io": "disable",
				"cpu-quota.crio.io":          "disable",
			}
			testpod = getTestPodWithAnnotations(annotations, smtLevel)

			err = testclient.Client.Create(context.TODO(), testpod)
			Expect(err).ToNot(HaveOccurred())
			err = pods.WaitForCondition(testpod, corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
			logEventsForPod(testpod)
			Expect(err).ToNot(HaveOccurred())

			By("Checking that the default smp affinity mask was updated and CPU (where POD is running) isolated")
			defaultSmpAffinitySet, err = nodes.GetDefaultSmpAffinitySet(workerRTNode)
			Expect(err).ToNot(HaveOccurred())

			getPsr := []string{"/bin/bash", "-c", "grep Cpus_allowed_list /proc/self/status | awk '{print $2}'"}
			psr, err := pods.WaitForPodOutput(testclient.K8sClient, testpod, getPsr)
			Expect(err).ToNot(HaveOccurred())
			psrSet, err := cpuset.Parse(strings.Trim(string(psr), "\n"))
			Expect(err).ToNot(HaveOccurred())

			Expect(psrSet.IsSubsetOf(defaultSmpAffinitySet)).To(BeFalse(), fmt.Sprintf("Default SMP affinity should not contain isolated CPU %s", psr))

			By("Checking that there are no any active IRQ on isolated CPU")
			// It may takes some time for the system to reschedule active IRQs
			Eventually(func() bool {
				getActiveIrq := []string{"/bin/bash", "-c", "for n in $(find /proc/irq/ -name smp_affinity_list); do echo $(cat $n); done"}
				activeIrq, err := nodes.ExecCommandOnNode(getActiveIrq, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				Expect(activeIrq).ToNot(BeEmpty())
				for _, irq := range strings.Split(activeIrq, "\n") {
					irqAffinity, err := cpuset.Parse(irq)
					Expect(err).ToNot(HaveOccurred())
					if !irqAffinity.Equals(onlineCPUsSet) && psrSet.IsSubsetOf(irqAffinity) {
						return false
					}
				}
				return true
			}, (cluster.ComputeTestTimeout(30*time.Second, RunningOnSingleNode)), 5*time.Second).Should(BeTrue(),
				fmt.Sprintf("IRQ still active on CPU%s", psr))

			By("Checking that after removing POD default smp affinity is returned back to all active CPUs")
			deleteTestPod(testpod)
			defaultSmpAffinitySet, err = nodes.GetDefaultSmpAffinitySet(workerRTNode)
			Expect(err).ToNot(HaveOccurred())

			Expect(onlineCPUsSet.IsSubsetOf(defaultSmpAffinitySet)).To(BeTrue(), "All online CPUs %s should be subset of default SMP affinity %s", onlineCPUsSet, defaultSmpAffinitySet)
		})
	})

	When("reserved CPUs specified", func() {
		var testpod *corev1.Pod

		BeforeEach(func() {
			testpod = pods.GetTestPod()
			testpod.Namespace = testutils.NamespaceTesting
			testpod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNode.Name}
			testpod.Spec.ShareProcessNamespace = pointer.BoolPtr(true)

			err := testclient.Client.Create(context.TODO(), testpod)
			Expect(err).ToNot(HaveOccurred())

			err = pods.WaitForCondition(testpod, corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
			logEventsForPod(testpod)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should run infra containers on reserved CPUs", func() {
			var err error
			// find used because that crictl does not show infra containers, `runc list` shows them
			// but you will need somehow to find infra containers ID's
			podUID := strings.Replace(string(testpod.UID), "-", "_", -1)

			podCgroup := ""
			Eventually(func() string {
				cmd := []string{"/bin/bash", "-c", fmt.Sprintf("find /rootfs/sys/fs/cgroup/cpuset/ -name *%s*", podUID)}
				podCgroup, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				return podCgroup
			}, cluster.ComputeTestTimeout(30*time.Second, RunningOnSingleNode), 5*time.Second).ShouldNot(BeEmpty(),
				fmt.Sprintf("cannot find cgroup for pod %q", podUID))

			containersCgroups := ""
			Eventually(func() string {
				cmd := []string{"/bin/bash", "-c", fmt.Sprintf("find %s -name crio-*", podCgroup)}
				containersCgroups, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				return containersCgroups
			}, cluster.ComputeTestTimeout(30*time.Second, RunningOnSingleNode), 5*time.Second).ShouldNot(BeEmpty(),
				fmt.Sprintf("cannot find containers cgroups from pod cgroup %q", podCgroup))

			containerID, err := pods.GetContainerIDByName(testpod, "test")
			Expect(err).ToNot(HaveOccurred())

			containersCgroups = strings.Trim(containersCgroups, "\n")
			containersCgroupsDirs := strings.Split(containersCgroups, "\n")
			Expect(len(containersCgroupsDirs)).To(Equal(2), "unexpected amount of containers cgroups")

			for _, dir := range containersCgroupsDirs {
				// skip application container cgroup
				if strings.Contains(dir, containerID) {
					continue
				}

				By("Checking what CPU the infra container is using")
				cmd := []string{"/bin/bash", "-c", fmt.Sprintf("cat %s/cpuset.cpus", dir)}
				output, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				cpus, err := cpuset.Parse(output)
				Expect(err).ToNot(HaveOccurred())

				Expect(cpus.ToSlice()).To(Equal(reservedCPUSet.ToSlice()))
			}
		})
	})

	When("strict NUMA aligment is requested", func() {
		var testpod *corev1.Pod

		BeforeEach(func() {
			if profile.Spec.NUMA == nil || profile.Spec.NUMA.TopologyPolicy == nil {
				Skip("Topology Manager Policy is not configured")
			}
			tmPolicy := *profile.Spec.NUMA.TopologyPolicy
			if tmPolicy != "single-numa-node" {
				Skip("Topology Manager Policy is not Single NUMA Node")
			}
		})

		AfterEach(func() {
			if testpod == nil {
				return
			}
			deleteTestPod(testpod)
		})

		It("should reject pods which request integral CPUs not aligned with machine SMT level", func() {
			// any random existing cpu is fine
			cpuID := onlineCPUSet.ToSliceNoSort()[0]
			smtLevel := nodes.GetSMTLevel(cpuID, workerRTNode)
			if smtLevel < 2 {
				Skip(fmt.Sprintf("designated worker node %q has SMT level %d - minimum required 2", workerRTNode.Name, smtLevel))
			}

			cpuCount := 1 // must be intentionally < than the smtLevel to trigger the kubelet validation
			testpod = promotePodToGuaranteed(getStressPod(workerRTNode.Name, cpuCount))
			testpod.Namespace = testutils.NamespaceTesting

			err := testclient.Client.Create(context.TODO(), testpod)
			Expect(err).ToNot(HaveOccurred())

			err = pods.WaitForPredicate(testpod, 10*time.Minute, func(pod *corev1.Pod) (bool, error) {
				if pod.Status.Phase != corev1.PodPending {
					return true, nil
				}
				return false, nil
			})
			Expect(err).ToNot(HaveOccurred())

			updatedPod := &corev1.Pod{}
			err = testclient.Client.Get(context.TODO(), client.ObjectKeyFromObject(testpod), updatedPod)
			Expect(err).ToNot(HaveOccurred())

			Expect(updatedPod.Status.Phase).To(Equal(corev1.PodFailed), "pod %s not failed: %v", updatedPod.Name, updatedPod.Status)
			Expect(isSMTAlignmentError(updatedPod)).To(BeTrue(), "pod %s failed for wrong reason: %q", updatedPod.Name, updatedPod.Status.Reason)
		})
	})

})

func isSMTAlignmentError(pod *corev1.Pod) bool {
	re := regexp.MustCompile(`SMT.*Alignment.*Error`)
	return re.MatchString(pod.Status.Reason)
}

func getStressPod(nodeName string, cpus int) *corev1.Pod {
	cpuCount := fmt.Sprintf("%d", cpus)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-cpu-",
			Labels: map[string]string{
				"test": "",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "stress-test",
					Image: images.Test(),
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(cpuCount),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
					Command: []string{"/usr/bin/stresser"},
					Args:    []string{"-cpus", cpuCount},
				},
			},
			NodeSelector: map[string]string{
				testutils.LabelHostname: nodeName,
			},
		},
	}
}

func promotePodToGuaranteed(pod *corev1.Pod) *corev1.Pod {
	for idx := 0; idx < len(pod.Spec.Containers); idx++ {
		cnt := &pod.Spec.Containers[idx] // shortcut
		if cnt.Resources.Limits == nil {
			cnt.Resources.Limits = make(corev1.ResourceList)
		}
		for resName, resQty := range cnt.Resources.Requests {
			cnt.Resources.Limits[resName] = resQty
		}
	}
	return pod
}

func getTestPodWithAnnotations(annotations map[string]string, cpus int) *corev1.Pod {
	testpod := pods.GetTestPod()
	testpod.Annotations = annotations
	testpod.Namespace = testutils.NamespaceTesting

	cpuCount := fmt.Sprintf("%d", cpus)

	resCpu := resource.MustParse(cpuCount)
	resMem := resource.MustParse("256Mi")

	// change pod resource requirements, to change the pod QoS class to guaranteed
	testpod.Spec.Containers[0].Resources = corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resCpu,
			corev1.ResourceMemory: resMem,
		},
	}

	runtimeClassName := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
	testpod.Spec.RuntimeClassName = &runtimeClassName
	testpod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNode.Name}

	return testpod
}

func deleteTestPod(testpod *corev1.Pod) {
	// it possible that the pod already was deleted as part of the test, in this case we want to skip teardown
	err := testclient.Client.Get(context.TODO(), client.ObjectKeyFromObject(testpod), testpod)
	if errors.IsNotFound(err) {
		return
	}

	err = testclient.Client.Delete(context.TODO(), testpod)
	Expect(err).ToNot(HaveOccurred())

	err = pods.WaitForDeletion(testpod, pods.DefaultDeletionTimeout*time.Second)
	Expect(err).ToNot(HaveOccurred())
}

func cpuSpecToString(cpus *performancev2.CPU) string {
	if cpus == nil {
		return "<nil>"
	}
	sb := strings.Builder{}
	if cpus.Reserved != nil {
		fmt.Fprintf(&sb, "reserved=[%s]", *cpus.Reserved)
	}
	if cpus.Isolated != nil {
		fmt.Fprintf(&sb, " isolated=[%s]", *cpus.Isolated)
	}
	if cpus.BalanceIsolated != nil {
		fmt.Fprintf(&sb, " balanceIsolated=%t", *cpus.BalanceIsolated)
	}
	return sb.String()
}

func logEventsForPod(testPod *corev1.Pod) {
	evs, err := events.GetEventsForObject(testclient.Client, testPod.Namespace, testPod.Name, string(testPod.UID))
	if err != nil {
		testlog.Error(err)
	}
	for _, event := range evs.Items {
		testlog.Warningf("-> %s %s %s", event.Action, event.Reason, event.Message)
	}
}
