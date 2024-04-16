package __performance

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/cpuset"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/utils/schedstat"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup/controller"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cluster"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/events"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/images"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
)

var workerRTNode *corev1.Node
var profile *performancev2.PerformanceProfile

const restartCooldownTime = 1 * time.Minute
const cgroupRoot string = "/sys/fs/cgroup"

var _ = Describe("[rfe_id:27363][performance] CPU Management", Ordered, func() {
	var (
		balanceIsolated          bool
		reservedCPU, isolatedCPU string
		listReservedCPU          []int
		reservedCPUSet           cpuset.CPUSet
		onlineCPUSet             cpuset.CPUSet
		err                      error
		smtLevel                 int
		ctx                      context.Context = context.Background()
		getter                   cgroup.ControllersGetter
		cgroupV2                 bool
	)

	testutils.CustomBeforeAll(func() {
		isSNO, err := cluster.IsSingleNode()
		Expect(err).ToNot(HaveOccurred())
		RunningOnSingleNode = isSNO
		workerRTNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("error looking for the optional selector: %v", err))
		Expect(workerRTNodes).ToNot(BeEmpty())
		workerRTNode = &workerRTNodes[0]

		onlineCPUSet, err = nodes.GetOnlineCPUsSet(ctx, workerRTNode)
		cpuID := onlineCPUSet.UnsortedList()[0]
		smtLevel = nodes.GetSMTLevel(ctx, cpuID, workerRTNode)
		getter, err = cgroup.BuildGetter(ctx, testclient.Client, testclient.K8sClient)
		Expect(err).ToNot(HaveOccurred())
		cgroupV2, err = cgroup.IsVersion2(ctx, testclient.Client)
		Expect(err).ToNot(HaveOccurred())

	})

	BeforeEach(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}
		profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		cpus, err := cpuSpecToString(profile.Spec.CPU)
		Expect(err).ToNot(HaveOccurred(), "failed to parse cpu %v spec to string", cpus)
		By(fmt.Sprintf("Checking the profile %s with cpus %s", profile.Name, cpus))
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
		listReservedCPU = reservedCPUSet.List()

		onlineCPUSet, err = nodes.GetOnlineCPUsSet(context.TODO(), workerRTNode)
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
			sysIsolatedCpus, err := nodes.ExecCommandToString(context.TODO(), cmd, workerRTNode)
			Expect(err).ToNot(HaveOccurred())
			if balanceIsolated {
				Expect(sysIsolatedCpus).To(BeEmpty())
			} else {
				Expect(sysIsolatedCpus).To(Equal(isolatedCPU))
			}

			By("checking reserved CPU in kubelet config file")
			cmd = []string{"cat", "/rootfs/etc/kubernetes/kubelet.conf"}
			conf, err := nodes.ExecCommandToString(context.TODO(), cmd, workerRTNode)
			Expect(err).ToNot(HaveOccurred(), "failed to cat kubelet.conf")
			// kubelet.conf changed formatting, there is a space after colons atm. Let's deal with both cases with a regex
			Expect(conf).To(MatchRegexp(fmt.Sprintf(`"reservedSystemCPUs": ?"%s"`, reservedCPU)))

			By("checking CPU affinity mask for kernel scheduler")
			cmd = []string{"/bin/bash", "-c", "taskset -pc 1"}
			sched, err := nodes.ExecCommandToString(context.TODO(), cmd, workerRTNode)
			Expect(err).ToNot(HaveOccurred(), "failed to execute taskset")
			mask := strings.SplitAfter(sched, " ")
			maskSet, err := cpuset.Parse(mask[len(mask)-1])
			Expect(err).ToNot(HaveOccurred())

			Expect(reservedCPUSet.IsSubsetOf(maskSet)).To(Equal(true), fmt.Sprintf("The init process (pid 1) should have cpu affinity: %s", reservedCPU))
		})

		It("[test_id:34358] Verify rcu_nocbs kernel argument on the node", func() {
			By("checking that cmdline contains rcu_nocbs with right value")
			cmd := []string{"cat", "/proc/cmdline"}
			cmdline, err := nodes.ExecCommandToString(context.TODO(), cmd, workerRTNode)
			Expect(err).ToNot(HaveOccurred())
			re := regexp.MustCompile(`rcu_nocbs=\S+`)
			rcuNocbsArgument := re.FindString(cmdline)
			Expect(rcuNocbsArgument).To(ContainSubstring("rcu_nocbs="))
			rcuNocbsCpu := strings.Split(rcuNocbsArgument, "=")[1]
			Expect(rcuNocbsCpu).To(Equal(isolatedCPU))

			By("checking that new rcuo processes are running on non_isolated cpu")
			cmd = []string{"pgrep", "rcuo"}
			rcuoList, err := nodes.ExecCommandToString(context.TODO(), cmd, workerRTNode)
			Expect(err).ToNot(HaveOccurred())
			for _, rcuo := range strings.Split(rcuoList, "\n") {
				// check cpu affinity mask
				cmd = []string{"/bin/bash", "-c", fmt.Sprintf("taskset -pc %s", rcuo)}
				taskset, err := nodes.ExecCommandToString(context.TODO(), cmd, workerRTNode)
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

		testutils.CustomBeforeAll(func() {
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
			deleteTestPod(context.TODO(), testpod)
		})

		DescribeTable("Verify CPU usage by stress PODs", func(ctx context.Context, guaranteed bool) {
			cpuID := onlineCPUSet.UnsortedList()[0]
			smtLevel := nodes.GetSMTLevel(ctx, cpuID, workerRTNode)
			if smtLevel < 2 {
				Skip(fmt.Sprintf("designated worker node %q has SMT level %d - minimum required 2", workerRTNode.Name, smtLevel))
			}

			// note must be a multiple of the smtLevel. Pick the minimum to maximize the chances to run on CI
			cpuRequest := smtLevel
			testpod = getStressPod(workerRTNode.Name, cpuRequest)
			testpod.Namespace = testutils.NamespaceTesting

			isolatedCPUSet, _ := cpuset.Parse(isolatedCPU)
			// the worker node on which the pod will be scheduled has other pods already scheduled on it, and these also use a
			// portion of the node's isolated cpus, and scheduling a pod requesting all the isolated cpus on the node (hence =)
			// would fail because there is already a base cpu load on the node
			if cpuRequest >= isolatedCPUSet.Size() {
				Skip(fmt.Sprintf("cpus request %d is greater than the isolated cpus %d", cpuRequest, isolatedCPUSet.Size()))
			}

			listCPU := onlineCPUSet.List()
			expectedQos := corev1.PodQOSBurstable

			if guaranteed {
				listCPU = onlineCPUSet.Difference(reservedCPUSet).List()
				expectedQos = corev1.PodQOSGuaranteed
				promotePodToGuaranteed(testpod)
			} else if !balanceIsolated {
				// when balanceIsolated is False - non-guaranteed pod should run on reserved cpu
				listCPU = listReservedCPU
			}

			By(fmt.Sprintf("create a %s QoS stress pod requesting %d cpus", expectedQos, cpuRequest))
			var err error
			err = testclient.Client.Create(ctx, testpod)
			Expect(err).ToNot(HaveOccurred())

			testpod, err = pods.WaitForCondition(ctx, client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
			logEventsForPod(testpod)
			Expect(err).ToNot(HaveOccurred())

			updatedPod := &corev1.Pod{}
			err = testclient.Client.Get(ctx, client.ObjectKeyFromObject(testpod), updatedPod)
			Expect(err).ToNot(HaveOccurred())
			Expect(updatedPod.Status.QOSClass).To(Equal(expectedQos),
				"unexpected QoS Class for %s/%s: %s (looking for %s)",
				updatedPod.Namespace, updatedPod.Name, updatedPod.Status.QOSClass, expectedQos)

			output, err := nodes.ExecCommandToString(ctx,
				[]string{"/bin/bash", "-c", "ps -o psr $(pgrep -n stress) | tail -1"},
				workerRTNode,
			)
			Expect(err).ToNot(HaveOccurred(), "failed to get cpu of stress process")
			cpu, err := strconv.Atoi(strings.Trim(output, " "))
			Expect(err).ToNot(HaveOccurred())

			Expect(cpu).To(BeElementOf(listCPU))
		},
			Entry("[test_id:37860] Non-guaranteed POD can work on any CPU", context.TODO(), false),
			Entry("[test_id:27492] Guaranteed POD should work on isolated cpu", context.TODO(), true),
		)
	})

	Describe("Verification of cpu_manager_state file", func() {
		var testpod *corev1.Pod
		BeforeEach(func() {
			testpod = pods.GetTestPod()
			testpod.Namespace = testutils.NamespaceTesting
			testpod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNode.Name}
			testpod.Spec.Containers[0].Resources = corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("200Mi"),
					corev1.ResourceCPU:    resource.MustParse("2"),
				},
			}
			err := testclient.Client.Create(context.TODO(), testpod)
			testpod, err = pods.WaitForCondition(context.TODO(), client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
			logEventsForPod(testpod)
			Expect(err).ToNot(HaveOccurred())
		})
		AfterEach(func() {
			deleteTestPod(context.TODO(), testpod)
		})
		When("kubelet is restart", func() {
			It("[test_id: 73501] defaultCpuset should not change", func() {
				By("fetch Default cpu set from cpu manager state file before restart")
				cpuManagerCpusetBeforeRestart, err := nodes.CpuManagerCpuSet(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				testlog.Infof("pre kubelet restart default cpuset: %v", cpuManagerCpusetBeforeRestart.String())
				kubeletRestartCmd := []string{
					"chroot",
					"/rootfs",
					"/bin/bash",
					"-c",
					"systemctl restart kubelet",
				}

				_, _ = nodes.ExecCommandToString(ctx, kubeletRestartCmd, workerRTNode)
				nodes.WaitForReadyOrFail("post kubele restart", workerRTNode.Name, 20*time.Minute, 3*time.Second)
				// giving kubelet more time to stabilize and initialize itself before
				testlog.Infof("post restart: entering cooldown time: %v", restartCooldownTime)
				time.Sleep(restartCooldownTime)

				testlog.Infof("post restart: finished cooldown time: %v", restartCooldownTime)

				By("fetch Default cpuset from cpu manager state after restart")
				cpuManagerCpusetAfterRestart, err := nodes.CpuManagerCpuSet(ctx, workerRTNode)
				Expect(cpuManagerCpusetBeforeRestart).To(Equal(cpuManagerCpusetAfterRestart))
			})
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

			cpuID := onlineCPUSet.UnsortedList()[0]
			smtLevel = nodes.GetSMTLevel(context.TODO(), cpuID, workerRTNode)
		})

		AfterEach(func() {
			if testpod != nil {
				deleteTestPod(context.TODO(), testpod)
			}
		})

		It("[test_id:36364] should disable IRQ balance for CPU where POD is running", func() {
			By("checking default smp affinity is equal to all active CPUs")
			defaultSmpAffinitySet, err := nodes.GetDefaultSmpAffinitySet(context.TODO(), workerRTNode)
			Expect(err).ToNot(HaveOccurred())

			onlineCPUsSet, err := nodes.GetOnlineCPUsSet(context.TODO(), workerRTNode)
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
			testpod, err = pods.WaitForCondition(context.TODO(), client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
			logEventsForPod(testpod)
			Expect(err).ToNot(HaveOccurred())

			By("Checking that the default smp affinity mask was updated and CPU (where POD is running) isolated")
			defaultSmpAffinitySet, err = nodes.GetDefaultSmpAffinitySet(context.TODO(), workerRTNode)
			Expect(err).ToNot(HaveOccurred())

			getPsr := []string{"/bin/bash", "-c", "grep Cpus_allowed_list /proc/self/status | awk '{print $2}'"}
			psr, err := pods.WaitForPodOutput(context.TODO(), testclient.K8sClient, testpod, getPsr)
			Expect(err).ToNot(HaveOccurred())
			psrSet, err := cpuset.Parse(strings.Trim(string(psr), "\n"))
			Expect(err).ToNot(HaveOccurred())

			Expect(psrSet.IsSubsetOf(defaultSmpAffinitySet)).To(BeFalse(), fmt.Sprintf("Default SMP affinity should not contain isolated CPU %s", psr))

			By("Checking that there are no any active IRQ on isolated CPU")
			// It may takes some time for the system to reschedule active IRQs
			Eventually(func() bool {
				getActiveIrq := []string{"/bin/bash", "-c", "for n in $(find /proc/irq/ -name smp_affinity_list); do echo $(cat $n); done"}
				activeIrq, err := nodes.ExecCommandToString(context.TODO(), getActiveIrq, workerRTNode)
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
			}).WithTimeout(cluster.ComputeTestTimeout(30*time.Second, RunningOnSingleNode)).WithPolling(5*time.Second).Should(BeTrue(),
				fmt.Sprintf("IRQ still active on CPU%s", psr))

			By("Checking that after removing POD default smp affinity is returned back to all active CPUs")
			deleteTestPod(context.TODO(), testpod)
			defaultSmpAffinitySet, err = nodes.GetDefaultSmpAffinitySet(context.TODO(), workerRTNode)
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
			testpod.Spec.ShareProcessNamespace = pointer.Bool(true)

			err := testclient.Client.Create(context.TODO(), testpod)
			Expect(err).ToNot(HaveOccurred())

			testpod, err = pods.WaitForCondition(context.TODO(), client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
			logEventsForPod(testpod)
			Expect(err).ToNot(HaveOccurred())
		})

		It("[test_id:49147] should run infra containers on reserved CPUs", func() {
			var cpusetPath string
			// find used because that crictl does not show infra containers, `runc list` shows them
			// but you will need somehow to find infra containers ID's
			podUID := strings.Replace(string(testpod.UID), "-", "_", -1)
			podCgroup := ""
			if cgroupV2 {
				cpusetPath = "/rootfs/sys/fs/cgroup/kubepods.slice"
			} else {
				cpusetPath = "/rootfs/sys/fs/cgroup/cpuset"
			}

			Eventually(func() string {
				cmd := []string{"/bin/bash", "-c", fmt.Sprintf("find %s -name *%s*", cpusetPath, podUID)}
				podCgroup, err = nodes.ExecCommandToString(context.TODO(), cmd, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				return podCgroup
			}, cluster.ComputeTestTimeout(30*time.Second, RunningOnSingleNode), 5*time.Second).ShouldNot(BeEmpty(),
				fmt.Sprintf("cannot find cgroup for pod %q", podUID))

			containersCgroups := ""
			Eventually(func() string {
				cmd := []string{"/bin/bash", "-c", fmt.Sprintf("find %s -name crio-*", podCgroup)}
				containersCgroups, err = nodes.ExecCommandToString(context.TODO(), cmd, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				return containersCgroups
			}, cluster.ComputeTestTimeout(30*time.Second, RunningOnSingleNode), 5*time.Second).ShouldNot(BeEmpty(),
				fmt.Sprintf("cannot find containers cgroups from pod cgroup %q", podCgroup))

			containerID, err := pods.GetContainerIDByName(testpod, "test")
			Expect(err).ToNot(HaveOccurred())

			containersCgroups = strings.Trim(containersCgroups, "\n")
			containersCgroupsDirs := strings.Split(containersCgroups, "\n")

			for _, dir := range containersCgroupsDirs {
				// skip application container cgroup
				// skip conmon containers
				if strings.Contains(dir, containerID) || strings.Contains(dir, "conmon") {
					continue
				}

				By("Checking what CPU the infra container is using")
				cmd := []string{"/bin/bash", "-c", fmt.Sprintf("cat %s/cpuset.cpus", dir)}
				output, err := nodes.ExecCommandToString(context.TODO(), cmd, workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				cpus, err := cpuset.Parse(output)
				Expect(err).ToNot(HaveOccurred())

				Expect(cpus.List()).To(Equal(reservedCPUSet.List()))
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
			deleteTestPod(context.TODO(), testpod)
		})

		It("[test_id:49149] should reject pods which request integral CPUs not aligned with machine SMT level", func() {
			// also covers Hyper-thread aware sheduling [test_id:46545] Odd number of isolated CPU threads
			// any random existing cpu is fine
			cpuID := onlineCPUSet.UnsortedList()[0]
			smtLevel := nodes.GetSMTLevel(context.TODO(), cpuID, workerRTNode)
			if smtLevel < 2 {
				Skip(fmt.Sprintf("designated worker node %q has SMT level %d - minimum required 2", workerRTNode.Name, smtLevel))
			}

			cpuCount := 1 // must be intentionally < than the smtLevel to trigger the kubelet validation
			testpod = promotePodToGuaranteed(getStressPod(workerRTNode.Name, cpuCount))
			testpod.Namespace = testutils.NamespaceTesting

			err := testclient.Client.Create(context.TODO(), testpod)
			Expect(err).ToNot(HaveOccurred())

			currentPod, err := pods.WaitForPredicate(context.TODO(), client.ObjectKeyFromObject(testpod), 10*time.Minute, func(pod *corev1.Pod) (bool, error) {
				if pod.Status.Phase != corev1.PodPending {
					return true, nil
				}
				return false, nil
			})
			Expect(err).ToNot(HaveOccurred(), "expected the pod to keep pending, but its current phase is %s", currentPod.Status.Phase)

			updatedPod := &corev1.Pod{}
			err = testclient.Client.Get(context.TODO(), client.ObjectKeyFromObject(testpod), updatedPod)
			Expect(err).ToNot(HaveOccurred())

			Expect(updatedPod.Status.Phase).To(Equal(corev1.PodFailed), "pod %s not failed: %v", updatedPod.Name, updatedPod.Status)
			Expect(isSMTAlignmentError(updatedPod)).To(BeTrue(), "pod %s failed for wrong reason: %q", updatedPod.Name, updatedPod.Status.Reason)
		})
	})
	Describe("Hyper-thread aware scheduling for guaranteed pods", func() {
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
			deleteTestPod(context.TODO(), testpod)
		})

		DescribeTable("Verify Hyper-Thread aware scheduling for guaranteed pods",
			func(ctx context.Context, htDisabled bool, snoCluster bool, snoWP bool) {
				// Check for SMT enabled
				// any random existing cpu is fine
				cpuCounts := make([]int, 0, 2)
				//var testpod *corev1.Pod
				//var err error

				// Check for SMT enabled
				// any random existing cpu is fine
				cpuID := onlineCPUSet.UnsortedList()[0]
				smtLevel := nodes.GetSMTLevel(ctx, cpuID, workerRTNode)
				hasWP := checkForWorkloadPartitioning(ctx)

				// Following checks are required to map test_id scenario correctly to the type of node under test
				if snoCluster && !RunningOnSingleNode {
					Skip("Requires SNO cluster")
				}
				if !snoCluster && RunningOnSingleNode {
					Skip("Requires Non-SNO cluster")
				}
				if (smtLevel < 2) && !htDisabled {
					Skip(fmt.Sprintf("designated worker node %q has SMT level %d - minimum required 2", workerRTNode.Name, smtLevel))
				}
				if (smtLevel > 1) && htDisabled {
					Skip(fmt.Sprintf("designated worker node %q has SMT level %d - requires exactly 1", workerRTNode.Name, smtLevel))
				}

				if (snoCluster && snoWP) && !hasWP {
					Skip("Requires SNO cluster with Workload Partitioning enabled")
				}
				if (snoCluster && !snoWP) && hasWP {
					Skip("Requires SNO cluster without Workload Partitioning enabled")
				}
				cpuCounts = append(cpuCounts, 2)
				if htDisabled {
					cpuCounts = append(cpuCounts, 1)
				}

				for _, cpuCount := range cpuCounts {
					testpod = startHTtestPod(ctx, cpuCount)
					Expect(checkPodHTSiblings(ctx, testpod)).To(BeTrue(), "Pod cpu set does not map to host cpu sibling pairs")
					By("Deleting test pod...")
					deleteTestPod(ctx, testpod)
				}
			},

			Entry("[test_id:46959] Number of CPU requests as multiple of SMT count allowed when HT enabled", context.TODO(), false, false, false),
			Entry("[test_id:46544] Odd number of CPU requests allowed when HT disabled", context.TODO(), true, false, false),
			Entry("[test_id:46538] HT aware scheduling on SNO cluster", context.TODO(), false, true, false),
			Entry("[test_id:46539] HT aware scheduling on SNO cluster and Workload Partitioning enabled", context.TODO(), false, true, true),
		)

	})
	Context("Crio Annotations", func() {
		var testpod *corev1.Pod
		var allTestpods map[types.UID]*corev1.Pod
		var busyCpusImage string
		var targetNode = &corev1.Node{}
		annotations := map[string]string{
			"cpu-load-balancing.crio.io": "disable",
			"cpu-quota.crio.io":          "disable",
		}
		BeforeAll(func() {
			var err error
			allTestpods = make(map[types.UID]*corev1.Pod)
			busyCpusImage = busyCpuImageEnv()
			testpod = getTestPodWithAnnotations(annotations, smtLevel)
			// workaround for https://github.com/kubernetes/kubernetes/issues/107074
			// until https://github.com/kubernetes/kubernetes/pull/120661 lands
			unblockerPod := pods.GetTestPod() // any non-GU pod will suffice ...
			unblockerPod.Namespace = testutils.NamespaceTesting
			unblockerPod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNode.Name}
			err = testclient.Client.Create(context.TODO(), unblockerPod)
			Expect(err).ToNot(HaveOccurred())
			allTestpods[unblockerPod.UID] = unblockerPod
			time.Sleep(30 * time.Second) // let cpumanager reconcile loop catch up

			// It's possible that when this test runs the value of
			// defaultCpuNotInSchedulingDomains is empty if no gu pods are running
			defaultCpuNotInSchedulingDomains, err := getCPUswithLoadBalanceDisabled(ctx, workerRTNode)
			Expect(err).ToNot(HaveOccurred(), "Unable to fetch scheduling domains")
			if len(defaultCpuNotInSchedulingDomains) > 0 {
				pods, err := pods.GetPodsOnNode(context.TODO(), workerRTNode.Name)
				if err != nil {
					testlog.Warningf("Warning cannot list pods on %q: %v", workerRTNode.Name, err)
				} else {
					testlog.Infof("pods on %q BEGIN", workerRTNode.Name)
					for _, pod := range pods {
						testlog.Infof("- %s/%s %s", pod.Namespace, pod.Name, pod.UID)
					}
					testlog.Infof("pods on %q END", workerRTNode.Name)
				}
				Expect(defaultCpuNotInSchedulingDomains).To(BeEmpty(), "the test expects all CPUs within a scheduling domain when starting")
			}

			By("Starting the pod")
			testpod.Spec.NodeSelector = testutils.NodeSelectorLabels
			runtimeClass := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
			testpod.Spec.RuntimeClassName = &runtimeClass
			testpod.Spec.Containers[0].Image = busyCpusImage
			testpod.Spec.Containers[0].Resources.Limits[corev1.ResourceCPU] = resource.MustParse("2")
			err = testclient.Client.Create(context.TODO(), testpod)
			Expect(err).ToNot(HaveOccurred())
			testpod, err = pods.WaitForCondition(ctx, client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
			logEventsForPod(testpod)
			Expect(err).ToNot(HaveOccurred(), "failed to create guaranteed pod %v", testpod)
			allTestpods[testpod.UID] = testpod
			err = testclient.Client.Get(ctx, client.ObjectKey{Name: testpod.Spec.NodeName}, targetNode)
			Expect(err).ToNot(HaveOccurred(), "failed to fetch the node on which pod %v is running", testpod)
		})

		AfterAll(func() {
			for podUID, testpod := range allTestpods {
				testlog.Infof("deleting test pod %s/%s UID=%q", testpod.Namespace, testpod.Name, podUID)
				err := testclient.Client.Get(ctx, client.ObjectKeyFromObject(testpod), testpod)
				Expect(err).ToNot(HaveOccurred())
				err = testclient.Client.Delete(ctx, testpod)
				Expect(err).ToNot(HaveOccurred())

				err = pods.WaitForDeletion(ctx, testpod, pods.DefaultDeletionTimeout*time.Second)
				Expect(err).ToNot(HaveOccurred())
			}
		})

		Describe("cpuset controller", func() {
			It("[test_id:72080] Verify cpu affinity of container process matches with cpuset controller interface file cpuset.cpus", func() {
				cpusetCfg := &controller.CpuSet{}
				err := getter.Container(ctx, testpod, testpod.Spec.Containers[0].Name, cpusetCfg)
				Expect(err).ToNot(HaveOccurred())
				// Get cpus used by the container
				tasksetcmd := []string{"/bin/taskset", "-pc", "1"}
				testpodAffinity, err := pods.ExecCommandOnPod(testclient.K8sClient, testpod, testpod.Spec.Containers[0].Name, tasksetcmd)
				podCpusStr := string(testpodAffinity)
				parts := strings.Split(strings.TrimSpace(podCpusStr), ":")
				testpodCpus := strings.TrimSpace(parts[1])
				testlog.Infof("%v pod is using %v cpus", testpod.Name, testpodCpus)
				podAffinityCpuset, err := cpuset.Parse(testpodCpus)
				Expect(err).ToNot(HaveOccurred(), "Unable to parse cpus %s used by %s pod", testpodCpus, testpod.Name)
				cgroupCpuset, err := cpuset.Parse(cpusetCfg.Cpus)
				Expect(err).ToNot(HaveOccurred(), "Unable to parse cpus from cgroups.cpuset")
				Expect(cgroupCpuset).To(Equal(podAffinityCpuset), "cpuset.cpus not matching the process affinity")
			})
		})

		Describe("Load Balancing Annotation", func() {
			It("[test_id:32646] cpus used by container should not be load balanced", func() {
				output, err := getPodCpus(testpod)
				Expect(err).ToNot(HaveOccurred(), "unable to fetch cpus used by testpod")
				podCpus, err := cpuset.Parse(output)
				Expect(err).ToNot(HaveOccurred(), "unable to parse cpuset used by pod")
				By("Getting the CPU scheduling flags")
				// After the testpod is started get the schedstat and check for cpus
				// not participating in scheduling domains
				checkSchedulingDomains(targetNode, podCpus, func(cpuIDs cpuset.CPUSet) error {
					if !podCpus.IsSubsetOf(cpuIDs) {
						return fmt.Errorf("pod CPUs NOT entirely part of cpus with load balance disabled: %v vs %v", podCpus, cpuIDs)
					}
					return nil
				}, 2*time.Minute, 5*time.Second, "checking scheduling domains with pod running")
			})

			It("[test_id:73382]cpuset.cpus.exclusive of kubepods.slice should be updated", func() {
				if !cgroupV2 {
					Skip("cpuset.cpus.exclusive is part of cgroupv2 interfaces")
				}
				cpusetCfg := &controller.CpuSet{}
				err := getter.Container(ctx, testpod, testpod.Spec.Containers[0].Name, cpusetCfg)
				Expect(err).ToNot(HaveOccurred())
				podCpuset, err := cpuset.Parse(cpusetCfg.Cpus)
				Expect(err).ToNot(HaveOccurred(), "Unable to parse pod cpus")
				kubepodsExclusiveCpus := fmt.Sprintf("%s/kubepods.slice/cpuset.cpus.exclusive", cgroupRoot)
				cmd := []string{"cat", kubepodsExclusiveCpus}
				exclusiveCpus, err := nodes.ExecCommandToString(ctx, cmd, targetNode)
				Expect(err).ToNot(HaveOccurred())
				exclusiveCpuset, err := cpuset.Parse(exclusiveCpus)
				Expect(err).ToNot(HaveOccurred(), "unable to parse cpuset.cpus.exclusive")
				Expect(podCpuset.Equals(exclusiveCpuset)).To(BeTrue())
			})
		})

		Describe("CPU Quota annotation", func() {
			It("[test_id:72079] CPU Quota interface files have correct values", func() {
				cpuCfg := &controller.Cpu{}
				err := getter.Container(ctx, testpod, testpod.Spec.Containers[0].Name, cpuCfg)
				Expect(err).ToNot(HaveOccurred())
				if cgroupV2 {
					Expect(cpuCfg.Quota).To(Equal("max"), "pod=%q, container=%q does not have quota set", client.ObjectKeyFromObject(testpod), testpod.Spec.Containers[0].Name)
				} else {
					Expect(cpuCfg.Quota).To(Equal("-1"), "pod=%q, container=%q does not have quota set", client.ObjectKeyFromObject(testpod), testpod.Spec.Containers[0].Name)
				}
			})

			It("[test_id: 72081] Verify cpu assigned to pod with quota disabled is not throttled", func() {
				cpuCfg := &controller.Cpu{}
				err := getter.Container(ctx, testpod, testpod.Spec.Containers[0].Name, cpuCfg)
				Expect(err).ToNot(HaveOccurred())
				Expect(cpuCfg.Stat["nr_throttled"]).To(Equal("0"), "cpu throttling not disabled on pod=%q, container=%q", client.ObjectKeyFromObject(testpod), testpod.Spec.Containers[0].Name)
			})
		})
	})

})

func checkForWorkloadPartitioning(ctx context.Context) bool {
	// Look for the correct Workload Partition annotation in
	// a crio configuration file on the target node
	By("Check for Workload Partitioning enabled")
	cmd := []string{
		"chroot",
		"/rootfs",
		"/bin/bash",
		"-c",
		"echo CHECK ; /bin/grep -rEo 'activation_annotation.*target\\.workload\\.openshift\\.io/management.*' /etc/crio/crio.conf.d/ || true",
	}
	output, err := nodes.ExecCommandToString(ctx, cmd, workerRTNode)
	Expect(err).ToNot(HaveOccurred(), "Unable to check cluster for Workload Partitioning enabled")
	re := regexp.MustCompile(`activation_annotation.*target\.workload\.openshift\.io/management.*`)
	return re.MatchString(fmt.Sprint(output))
}

func checkPodHTSiblings(ctx context.Context, testpod *corev1.Pod) bool {
	By("Get test pod CPU list")
	containerID, err := pods.GetContainerIDByName(testpod, "test")
	Expect(err).ToNot(HaveOccurred(), "Unable to get pod containerID")

	cmd := []string{
		"chroot",
		"/rootfs",
		"/bin/bash",
		"-c",
		fmt.Sprintf("/bin/crictl inspect %s | /bin/jq -r '.info.runtimeSpec.linux.resources.cpu.cpus'", containerID),
	}
	node, err := nodes.GetByName(testpod.Spec.NodeName)
	Expect(err).ToNot(HaveOccurred(), "failed to get node %q", testpod.Spec.NodeName)
	Expect(testpod.Spec.NodeName).ToNot(BeEmpty(), "testpod %s/%s still pending - no nodeName set", testpod.Namespace, testpod.Name)
	output, err := nodes.ExecCommandToString(ctx, cmd, node)
	Expect(err).ToNot(HaveOccurred(), "Unable to crictl inspect containerID %q", containerID)

	podcpus, err := cpuset.Parse(strings.Trim(output, "\n"))
	Expect(err).ToNot(
		HaveOccurred(), "Unable to cpuset.Parse pod allocated cpu set from output %s", output)
	testlog.Infof("Test pod CPU list: %s", podcpus.String())

	// aggregate cpu sibling paris from the host based on the cpus allocated to the pod
	By("Get host cpu siblings for pod cpuset")
	hostHTSiblingPaths := strings.Builder{}
	for _, cpuNum := range podcpus.List() {
		_, err = hostHTSiblingPaths.WriteString(
			fmt.Sprintf(" /sys/devices/system/cpu/cpu%d/topology/thread_siblings_list", cpuNum),
		)
		Expect(err).ToNot(HaveOccurred(), "Build.Write failed to add dir path string?")
	}
	cmd = []string{
		"chroot",
		"/rootfs",
		"/bin/bash",
		"-c",
		fmt.Sprintf("/bin/cat %s | /bin/sort -u", hostHTSiblingPaths.String()),
	}
	output, err = nodes.ExecCommandToString(ctx, cmd, workerRTNode)
	Expect(err).ToNot(
		HaveOccurred(),
		"Unable to read host thread_siblings_list files",
	)

	// output is newline seperated. Convert to cpulist format by replacing internal "\n" chars with ","
	hostHTSiblings := strings.ReplaceAll(
		strings.Trim(fmt.Sprint(output), "\n"), "\n", ",",
	)

	hostcpus, err := cpuset.Parse(hostHTSiblings)
	Expect(err).ToNot(HaveOccurred(), "Unable to parse host cpu HT siblings: %s", hostHTSiblings)
	By(fmt.Sprintf("Host CPU sibling set from querying for pod cpus: %s", hostcpus.String()))

	// pod cpu list should have the same siblings as the host for the same cpus
	return hostcpus.Equals(podcpus)

}
func startHTtestPod(ctx context.Context, cpuCount int) *corev1.Pod {
	var testpod *corev1.Pod

	annotations := map[string]string{}
	testpod = getTestPodWithAnnotations(annotations, cpuCount)
	testpod.Namespace = testutils.NamespaceTesting

	By(fmt.Sprintf("Creating test pod with %d cpus", cpuCount))
	testlog.Info(pods.DumpResourceRequirements(testpod))
	err := testclient.Client.Create(ctx, testpod)
	Expect(err).ToNot(HaveOccurred())
	testpod, err = pods.WaitForCondition(ctx, client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
	logEventsForPod(testpod)
	Expect(err).ToNot(HaveOccurred(), "Start pod failed")
	// Sanity check for QoS Class == Guaranteed
	Expect(testpod.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed),
		"Test pod does not have QoS class of Guaranteed")
	return testpod
}

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

func getTestPodWithProfileAndAnnotations(perfProf *performancev2.PerformanceProfile, annotations map[string]string, cpus int) *corev1.Pod {
	testpod := pods.GetTestPod()
	if len(annotations) > 0 {
		testpod.Annotations = annotations
	}
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

	if perfProf != nil {
		runtimeClassName := components.GetComponentName(perfProf.Name, components.ComponentNamePrefix)
		testpod.Spec.RuntimeClassName = &runtimeClassName
	}

	return testpod
}

func getTestPodWithAnnotations(annotations map[string]string, cpus int) *corev1.Pod {
	testpod := getTestPodWithProfileAndAnnotations(profile, annotations, cpus)

	testpod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNode.Name}

	return testpod
}

func deleteTestPod(ctx context.Context, testpod *corev1.Pod) (types.UID, bool) {
	// it possible that the pod already was deleted as part of the test, in this case we want to skip teardown
	err := testclient.Client.Get(ctx, client.ObjectKeyFromObject(testpod), testpod)
	if errors.IsNotFound(err) {
		return "", false
	}

	testpodUID := testpod.UID

	err = testclient.Client.Delete(ctx, testpod)
	Expect(err).ToNot(HaveOccurred())

	err = pods.WaitForDeletion(ctx, testpod, pods.DefaultDeletionTimeout*time.Second)
	Expect(err).ToNot(HaveOccurred())

	return testpodUID, true
}

func cpuSpecToString(cpus *performancev2.CPU) (string, error) {
	if cpus == nil {
		return "", fmt.Errorf("performance CPU field is nil")
	}
	sb := strings.Builder{}
	if cpus.Reserved != nil {
		_, err := fmt.Fprintf(&sb, "reserved=[%s]", *cpus.Reserved)
		if err != nil {
			return "", err
		}
	}
	if cpus.Isolated != nil {
		_, err := fmt.Fprintf(&sb, " isolated=[%s]", *cpus.Isolated)
		if err != nil {
			return "", err
		}
	}
	if cpus.BalanceIsolated != nil {
		_, err := fmt.Fprintf(&sb, " balanceIsolated=%t", *cpus.BalanceIsolated)
		if err != nil {
			return "", err
		}
	}
	return sb.String(), nil
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

// getCPUswithLoadBalanceDisabled Return cpus which are not in any scheduling domain
func getCPUswithLoadBalanceDisabled(ctx context.Context, targetNode *corev1.Node) ([]string, error) {
	cmd := []string{"/bin/bash", "-c", "cat /proc/schedstat"}
	schedstatData, err := nodes.ExecCommandToString(ctx, cmd, targetNode)
	if err != nil {
		return nil, err
	}

	info, err := schedstat.ParseData(strings.NewReader(schedstatData))
	if err != nil {
		return nil, err
	}

	cpusWithoutDomain := []string{}
	for _, cpu := range info.GetCPUs() {
		doms, ok := info.GetDomains(cpu)
		if !ok {
			return nil, fmt.Errorf("unknown cpu: %v", cpu)
		}
		if len(doms) > 0 {
			continue
		}
		cpusWithoutDomain = append(cpusWithoutDomain, cpu)
	}

	return cpusWithoutDomain, nil
}

// getPodCpus return cpus used based on taskset
func getPodCpus(testpod *corev1.Pod) (string, error) {
	tasksetcmd := []string{"taskset", "-pc", "1"}
	testpodCpusByte, err := pods.ExecCommandOnPod(testclient.K8sClient, testpod, testpod.Spec.Containers[0].Name, tasksetcmd)
	if err != nil {
		return "", err
	}
	testpodCpusStr := string(testpodCpusByte)
	parts := strings.Split(strings.TrimSpace(testpodCpusStr), ":")
	cpus := strings.TrimSpace(parts[1])
	return cpus, err
}

// checkSchedulingDomains Check cpus are part of any scheduling domain
func checkSchedulingDomains(workerRTNode *corev1.Node, podCpus cpuset.CPUSet, testFunc func(cpuset.CPUSet) error, timeout, polling time.Duration, errMsg string) {
	Eventually(func() error {
		cpusNotInSchedulingDomains, err := getCPUswithLoadBalanceDisabled(context.TODO(), workerRTNode)
		Expect(err).ToNot(HaveOccurred())
		testlog.Infof("cpus with load balancing disabled are: %v", cpusNotInSchedulingDomains)
		Expect(err).ToNot(HaveOccurred(), "unable to fetch cpus with load balancing disabled from /proc/schedstat")
		cpuIDList, err := schedstat.MakeCPUIDListFromCPUList(cpusNotInSchedulingDomains)
		if err != nil {
			return err
		}
		cpuIDs := cpuset.New(cpuIDList...)
		return testFunc(cpuIDs)
	}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).ShouldNot(HaveOccurred(), errMsg)

}

// busyCpuImageEnv return busycpus image used for crio quota annotations test
// This is required for running tests on disconnected environment where images are mirrored
// in private registries.
func busyCpuImageEnv() string {
	qeImageRegistry := os.Getenv("IMAGE_REGISTRY")
	busyCpusImage := os.Getenv("BUSY_CPUS_IMAGE")

	if busyCpusImage == "" {
		busyCpusImage = "busycpus"
	}

	if qeImageRegistry == "" {
		qeImageRegistry = "quay.io/ocp-edge-qe/"
	}
	return fmt.Sprintf("%s%s", qeImageRegistry, busyCpusImage)
}
