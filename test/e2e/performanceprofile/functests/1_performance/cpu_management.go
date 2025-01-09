package __performance

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/utils/schedstat"
	manifestsutil "github.com/openshift/cluster-node-tuning-operator/pkg/util"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
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

var _ = Describe("[rfe_id:27363][performance] CPU Management", Ordered, func() {
	var balanceIsolated bool
	var reservedCPU, isolatedCPU string
	var listReservedCPU []int
	var reservedCPUSet cpuset.CPUSet
	var onlineCPUSet cpuset.CPUSet

	testutils.CustomBeforeAll(func() {
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
			obj, err := manifestsutil.DeserializeObjectFromData([]byte(conf), kubeletconfigv1beta1.AddToScheme)
			Expect(err).ToNot(HaveOccurred())
			kc, ok := obj.(*kubeletconfigv1beta1.KubeletConfiguration)
			Expect(ok).To(BeTrue(), "wrong type %T", obj)
			Expect(kc.ReservedSystemCPUs).To(Equal(reservedCPU))

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
			deleteTestPod(testpod)
		})

		DescribeTable("Verify CPU usage by stress PODs", func(guaranteed bool) {
			cpuID := onlineCPUSet.UnsortedList()[0]
			smtLevel := nodes.GetSMTLevel(cpuID, workerRTNode)
			if smtLevel < 2 {
				Skip(fmt.Sprintf("designated worker node %q has SMT level %d - minimum required 2", workerRTNode.Name, smtLevel))
			}

			// note must be a multiple of the smtLevel. Pick the minimum to maximize the chances to run on CI
			cpuRequest := smtLevel
			testpod = getStressPod(workerRTNode.Name, cpuRequest)
			testpod.Namespace = testutils.NamespaceTesting

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

			var err error
			err = testclient.Client.Create(context.TODO(), testpod)
			Expect(err).ToNot(HaveOccurred())

			testpod, err = pods.WaitForCondition(client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
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
			Entry("[test_id:37860] Non-guaranteed POD can work on any CPU", false),
			Entry("[test_id:27492] Guaranteed POD should work on isolated cpu", true),
		)
	})

	When("pod runs with the CPU load balancing runtime class", func() {
		var smtLevel int
		var allTestpods map[types.UID]*corev1.Pod
		var testpod *corev1.Pod

		BeforeEach(func() {
			var err error
			allTestpods = make(map[types.UID]*corev1.Pod)

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
			defaultCpuNotInSchedulingDomains, err := getCPUswithLoadBalanceDisabled(workerRTNode)
			Expect(err).ToNot(HaveOccurred(), "Unable to fetch scheduling domains")

			if len(defaultCpuNotInSchedulingDomains) > 0 {
				pods, err := pods.GetPodsOnNode(context.TODO(), workerRTNode.Name)
				if err != nil {
					testlog.Warningf("cannot list pods on %q: %v", workerRTNode.Name, err)
				} else {
					testlog.Infof("pods on %q BEGIN", workerRTNode.Name)
					for _, pod := range pods {
						testlog.Infof("- %s/%s %s", pod.Namespace, pod.Name, pod.UID)
					}
					testlog.Infof("pods on %q END", workerRTNode.Name)
				}

				Expect(defaultCpuNotInSchedulingDomains).To(BeEmpty(), "the test expects all CPUs within a scheduling domain when starting")
			}

			annotations := map[string]string{
				"cpu-load-balancing.crio.io": "disable",
			}
			// any random existing cpu is fine
			cpuID := onlineCPUSet.UnsortedList()[0]
			smtLevel = nodes.GetSMTLevel(cpuID, workerRTNode)
			testpod = getTestPodWithAnnotations(annotations, smtLevel)
		})

		AfterEach(func() {
			for podUID, testpod := range allTestpods {
				testlog.Infof("deleting test pod %s/%s UID=%q", testpod.Namespace, testpod.Name, podUID)
				deleteTestPod(testpod)
			}
		})

		It("[test_id:32646] should disable CPU load balancing for CPU's used by the pod", func() {
			var err error
			By("Starting the pod")
			err = testclient.Client.Create(context.TODO(), testpod)
			Expect(err).ToNot(HaveOccurred())

			testpod, err = pods.WaitForCondition(client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
			logEventsForPod(testpod)
			Expect(err).ToNot(HaveOccurred(), "failed to create guaranteed pod %v", testpod)
			allTestpods[testpod.UID] = testpod

			// workaround for https://github.com/kubernetes/kubernetes/issues/107074
			// until https://github.com/kubernetes/kubernetes/pull/120661 lands
			unblockerPod := pods.GetTestPod() // any non-GU pod will suffice ...
			unblockerPod.Namespace = testpod.Namespace
			unblockerPod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: testpod.Spec.NodeName} // ... as long as it hits the same kubelet

			err = testclient.Client.Create(context.TODO(), unblockerPod)
			Expect(err).ToNot(HaveOccurred())
			allTestpods[unblockerPod.UID] = unblockerPod

			By("Getting the container cpuset.cpus cgroup")
			containerID, err := pods.GetContainerIDByName(testpod, "test")
			Expect(err).ToNot(HaveOccurred(), "unable to fetch containerID")

			containerCgroup := ""
			Eventually(func() string {
				cmd := []string{"/bin/bash", "-c", fmt.Sprintf("find /rootfs/sys/fs/cgroup/cpuset/ -name *%s*", containerID)}
				containerCgroup, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "failed to execute %v", cmd)
				return containerCgroup
			}).WithTimeout(cluster.ComputeTestTimeout(30*time.Second, RunningOnSingleNode)).WithPolling(5*time.Second).ShouldNot(BeEmpty(),
				fmt.Sprintf("cannot find cgroup for container %q", containerID))

			By("Checking what CPU the pod is using")
			cmd := []string{"/bin/bash", "-c", fmt.Sprintf("cat %s/cpuset.cpus", containerCgroup)}
			output, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
			Expect(err).ToNot(HaveOccurred(), "failed to execute %v", cmd)
			testlog.Infof("cpus used by test pod are: %s", output)

			podCpus, err := cpuset.Parse(output)
			Expect(err).ToNot(HaveOccurred(), "unable to parse cpuset used by pod")

			By("Getting the CPU scheduling flags")
			// After the testpod is started get the schedstat and check for cpus
			// not participating in scheduling domains
			Eventually(func() error {
				cpusNotInSchedulingDomains, err := getCPUswithLoadBalanceDisabled(workerRTNode)
				testlog.Infof("cpus with load balancing disabled are: %v", cpusNotInSchedulingDomains)
				Expect(err).ToNot(HaveOccurred(), "unable to fetch cpus with load balancing disabled from /proc/schedstat")
				cpuIDList, err := schedstat.MakeCPUIDListFromCPUList(cpusNotInSchedulingDomains)
				if err != nil {
					return err
				}
				cpuIDs := cpuset.New(cpuIDList...)
				if !podCpus.IsSubsetOf(cpuIDs) {
					return fmt.Errorf("pod CPUs NOT entirely part of cpus with load balance disabled: %v vs %v", podCpus, cpuIDs)
				}
				return nil
			}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).ShouldNot(HaveOccurred(), "checking scheduling domains with pod running")

			By("Deleting the pod")
			deletedUID, ok := deleteTestPod(testpod)
			if ok {
				delete(allTestpods, deletedUID)
			}

			// The below loop takes time because kernel takes time
			// to update /proc/schedstat with cpuid of deleted pods
			// to be under scheduling domains
			Eventually(func() error {
				By("Getting the CPU scheduling flags")
				cpusNotInSchedulingDomains, err := getCPUswithLoadBalanceDisabled(workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				testlog.Infof("cpus with load balancing disabled are: %v", cpusNotInSchedulingDomains)
				if len(cpusNotInSchedulingDomains) == 0 {
					return nil
				}
				cpuIDList, err := schedstat.MakeCPUIDListFromCPUList(cpusNotInSchedulingDomains)
				if err != nil {
					return err
				}
				cpuIDs := cpuset.New(cpuIDList...)
				if podCpus.IsSubsetOf(cpuIDs) {
					return fmt.Errorf("pod CPUs still part of cpus with load balance disabled: %v vs %v", podCpus, cpuIDs)
				}
				return nil
			}).WithTimeout(15*time.Minute).WithPolling(10*time.Second).ShouldNot(HaveOccurred(), "checking the scheduling domains are restored after pod deletion")
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
			smtLevel = nodes.GetSMTLevel(cpuID, workerRTNode)
		})

		AfterEach(func() {
			if testpod != nil {
				deleteTestPod(testpod)
			}
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
			testpod, err = pods.WaitForCondition(client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
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
			}).WithTimeout(cluster.ComputeTestTimeout(30*time.Second, RunningOnSingleNode)).WithPolling(5*time.Second).Should(BeTrue(),
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
			testpod.Spec.ShareProcessNamespace = pointer.Bool(true)

			err := testclient.Client.Create(context.TODO(), testpod)
			Expect(err).ToNot(HaveOccurred())

			testpod, err = pods.WaitForCondition(client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
			logEventsForPod(testpod)
			Expect(err).ToNot(HaveOccurred())
		})

		It("[test_id:49147] should run infra containers on reserved CPUs", func() {
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
			deleteTestPod(testpod)
		})

		It("[test_id:49149] should reject pods which request integral CPUs not aligned with machine SMT level", func() {
			// also covers Hyper-thread aware sheduling [test_id:46545] Odd number of isolated CPU threads
			// any random existing cpu is fine
			cpuID := onlineCPUSet.UnsortedList()[0]
			smtLevel := nodes.GetSMTLevel(cpuID, workerRTNode)
			if smtLevel < 2 {
				Skip(fmt.Sprintf("designated worker node %q has SMT level %d - minimum required 2", workerRTNode.Name, smtLevel))
			}

			cpuCount := 1 // must be intentionally < than the smtLevel to trigger the kubelet validation
			testpod = promotePodToGuaranteed(getStressPod(workerRTNode.Name, cpuCount))
			testpod.Namespace = testutils.NamespaceTesting

			err := testclient.Client.Create(context.TODO(), testpod)
			Expect(err).ToNot(HaveOccurred())

			currentPod, err := pods.WaitForPredicate(client.ObjectKeyFromObject(testpod), 10*time.Minute, func(pod *corev1.Pod) (bool, error) {
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
			deleteTestPod(testpod)
		})

		DescribeTable("Verify Hyper-Thread aware scheduling for guaranteed pods",
			func(htDisabled bool, snoCluster bool, snoWP bool) {
				// Check for SMT enabled
				// any random existing cpu is fine
				cpuCounts := make([]int, 0, 2)
				//var testpod *corev1.Pod
				//var err error

				// Check for SMT enabled
				// any random existing cpu is fine
				cpuID := onlineCPUSet.UnsortedList()[0]
				smtLevel := nodes.GetSMTLevel(cpuID, workerRTNode)
				hasWP := checkForWorkloadPartitioning()

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
					testpod = startHTtestPod(cpuCount)
					Expect(checkPodHTSiblings(testpod)).To(BeTrue(), "Pod cpu set does not map to host cpu sibling pairs")
					By("Deleting test pod...")
					deleteTestPod(testpod)
				}
			},

			Entry("[test_id:46959] Number of CPU requests as multiple of SMT count allowed when HT enabled", false, false, false),
			Entry("[test_id:46544] Odd number of CPU requests allowed when HT disabled", true, false, false),
			Entry("[test_id:46538] HT aware scheduling on SNO cluster", false, true, false),
			Entry("[test_id:46539] HT aware scheduling on SNO cluster and Workload Partitioning enabled", false, true, true),
		)

	})

})

func checkForWorkloadPartitioning() bool {
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
	output, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
	Expect(err).ToNot(HaveOccurred(), "Unable to check cluster for Workload Partitioning enabled")
	re := regexp.MustCompile(`activation_annotation.*target\.workload\.openshift\.io/management.*`)
	return re.MatchString(fmt.Sprint(output))
}

func checkPodHTSiblings(testpod *corev1.Pod) bool {
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
	output, err := nodes.ExecCommandOnNode(cmd, node)
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
	output, err = nodes.ExecCommandOnNode(cmd, workerRTNode)
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
func startHTtestPod(cpuCount int) *corev1.Pod {
	var testpod *corev1.Pod

	annotations := map[string]string{}
	testpod = getTestPodWithAnnotations(annotations, cpuCount)
	testpod.Namespace = testutils.NamespaceTesting

	By(fmt.Sprintf("Creating test pod with %d cpus", cpuCount))
	testlog.Info(pods.DumpResourceRequirements(testpod))
	err := testclient.Client.Create(context.TODO(), testpod)
	Expect(err).ToNot(HaveOccurred())
	testpod, err = pods.WaitForCondition(client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
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

func deleteTestPod(testpod *corev1.Pod) (types.UID, bool) {
	// it possible that the pod already was deleted as part of the test, in this case we want to skip teardown
	err := testclient.Client.Get(context.TODO(), client.ObjectKeyFromObject(testpod), testpod)
	if errors.IsNotFound(err) {
		return types.UID(""), false
	}

	testpodUID := testpod.UID

	err = testclient.Client.Delete(context.TODO(), testpod)
	Expect(err).ToNot(HaveOccurred())

	err = pods.WaitForDeletion(testpod, pods.DefaultDeletionTimeout*time.Second)
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
func getCPUswithLoadBalanceDisabled(targetNode *corev1.Node) ([]string, error) {
	cmd := []string{"/bin/bash", "-c", "cat /proc/schedstat"}
	schedstatData, err := nodes.ExecCommandOnNode(cmd, targetNode)
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
