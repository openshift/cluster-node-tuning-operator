package __performance_kubelet_node_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"

	profilecomponent "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/utils/schedstat"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/events"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

var (
	profile      *performancev2.PerformanceProfile
	workerRTNode *corev1.Node
)
var _ = Describe("[performance] Test crio annotaions on container runtimes", Ordered, func() {
	var performanceMCP string
	var onlineCPUSet cpuset.CPUSet
	var smtLevel int

	testutils.CustomBeforeAll(func() {
		var workerRTNodes []corev1.Node
		workerRTNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred())

		workerRTNode = &workerRTNodes[0]

		profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		performanceMCP, err = mcps.GetByProfile(profile)
		Expect(err).ToNot(HaveOccurred())

		onlineCPUSet, err = nodes.GetOnlineCPUsSet(workerRTNode)
		Expect(err).ToNot(HaveOccurred())

		for _, mcpName := range []string{testutils.RoleWorker, performanceMCP} {
			mcps.WaitForCondition(mcpName, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
		}

		cpuID := onlineCPUSet.UnsortedList()[0]
		smtLevel = nodes.GetSMTLevel(cpuID, workerRTNode)

	})

	Context("with crun as runtime", func() {
		var testpod *corev1.Pod
		var ctrcfg *machineconfigv1.ContainerRuntimeConfig
		var allTestpods map[types.UID]*corev1.Pod
		annotations := map[string]string{
			"cpu-load-balancing.crio.io": "disable",
			"cpu-quota.crio.io":          "disable",
		}
		BeforeAll(func() {
			mcpLabel := profilecomponent.GetMachineConfigLabel(profile)
			key, value := components.GetFirstKeyAndValue(mcpLabel)
			mcp, err := mcps.GetByLabel(key, value)
			Expect(err).ToNot(HaveOccurred())
			fmt.Println("mcp=", mcp)
			ctrcfg = mcps.EnableCrun("cnf-crun", profile, &mcp[0])
			testclient.Client.Create(context.TODO(), ctrcfg)

			By("Applying changes in performance profile and waiting until mcp will start updating")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

			By("Waiting when mcp finishes updates")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
		})

		AfterAll(func() {
			By("Disabling crun")
			testclient.Client.Delete(context.TODO(), ctrcfg)

			By("Applying changes in performance profile and waiting until mcp will start updating")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

			By("Waiting when mcp finishes updates")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
		})
		BeforeEach(func() {
			var err error
			allTestpods = make(map[types.UID]*corev1.Pod)

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

			By("Starting the pod")
			testpod.Spec.NodeSelector = testutils.NodeSelectorLabels
			err = testclient.Client.Create(context.TODO(), testpod)
			Expect(err).ToNot(HaveOccurred())
			testpod, err = pods.WaitForCondition(client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
			logEventsForPod(testpod)
			Expect(err).ToNot(HaveOccurred(), "failed to create guaranteed pod %v", testpod)
			allTestpods[testpod.UID] = testpod
		})

		AfterEach(func() {
			for podUID, testpod := range allTestpods {
				testlog.Infof("deleting test pod %s/%s UID=%q", testpod.Namespace, testpod.Name, podUID)
				err := testclient.Client.Get(context.TODO(), client.ObjectKeyFromObject(testpod), testpod)
				Expect(err).ToNot(HaveOccurred())
				//testpodUID := testpod.UID

				err = testclient.Client.Delete(context.TODO(), testpod)
				Expect(err).ToNot(HaveOccurred())

				err = pods.WaitForDeletion(testpod, pods.DefaultDeletionTimeout*time.Second)
				Expect(err).ToNot(HaveOccurred())
			}
		})
		Describe("cpuset controller", func() {
			It("Verify cpuset controller interface files have right values", func() {
				var containerCgroup string
				containerID, err := pods.GetContainerIDByName(testpod, testpod.Spec.Containers[0].Name)
				Expect(err).ToNot(HaveOccurred())
				containerCgroup = nodes.GetCgroupv1ControllerPath(workerRTNode, "cpuset", containerID, false)
				// check cpuset.cpus inside the container subdirectory directory  of containers cgroup
				cmd := []string{"/bin/bash", "-c", fmt.Sprintf("cat %s/container/cpuset.cpus", containerCgroup)}
				containerCpus, err := nodes.ExecCommandOnNode(cmd, workerRTNode)

				//check the cpuset.cpus the containers cgroup
				cmd = []string{"/bin/bash", "-c", fmt.Sprintf("cat %s/cpuset.cpus", containerCgroup)}
				containerScopeCpus, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				// Get cpus used by the container
				tasksetcmd := []string{"taskset", "-pc", "1"}
				testpodAffinity, err := pods.ExecCommandOnPod(testclient.K8sClient, testpod, tasksetcmd)
				podCpusStr := string(testpodAffinity)
				parts := strings.Split(strings.TrimSpace(podCpusStr), ":")
				testpodCpus := strings.TrimSpace(parts[1])
				testlog.Infof("%v pod is using %v cpus", testpod.Name, string(testpodCpus))
				Expect(containerCpus).To(Equal(testpodCpus), "cpuset.cpus not matching the process affinity")
				Expect(containerScopeCpus).To(Equal(testpodCpus), "container scope cpuset.cpus not matching with process affinity")
			})
		})

		Describe("Load Balancing Annotation", func() {
			It("cpus used by container should not be load balanced", func() {
				output, err := getPodCpus(testpod)
				Expect(err).ToNot(HaveOccurred(), "unable to fetch cpus used by testpod")
				podCpus, err := cpuset.Parse(output)
				Expect(err).ToNot(HaveOccurred(), "unable to parse cpuset used by pod")
				By("Getting the CPU scheduling flags")
				// After the testpod is started get the schedstat and check for cpus
				// not participating in scheduling domains
				checkSchedulingDomains(workerRTNode, podCpus, func(cpuIDs cpuset.CPUSet) error {
					if !podCpus.IsSubsetOf(cpuIDs) {
						return fmt.Errorf("pod CPUs NOT entirely part of cpus with load balance disabled: %v vs %v", podCpus, cpuIDs)
					}
					return nil
				}, 2*time.Minute, 5*time.Second, "checking scheduling domains with pod running")
			})
		})

		Describe("CPU Quota Annotation", func() {
			It("cpu controller interface files have correct values", func() {
				var containerCgroup string
				containerID, err := pods.GetContainerIDByName(testpod, testpod.Spec.Containers[0].Name)
				Expect(err).ToNot(HaveOccurred())
				output := nodes.GetCgroupv1ControllerPath(workerRTNode, "cpu,cpuacct", containerID, false)

				lines := strings.Split(output, "\n")
				for _, line := range lines {
					if !strings.Contains(line, "/crio-conmon-") {
						containerCgroup = line
					}
				}
				podDir := filepath.Dir(containerCgroup)
				cmd := []string{"/bin/bash", "-c", fmt.Sprintf("cat %s/container/cpu.cfs_quota_us", containerCgroup)}
				quotaValue, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
				Expect(quotaValue).To(Equal("-1"))
				cmd = []string{"/bin/bash", "-c", fmt.Sprintf("cat %s/cpu.cfs_quota_us", podDir)}
				podScopeQUotaValue, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
				Expect(podScopeQUotaValue).To(Equal("-1"))
			})

		})

	})
})

// logEventsForPod log pod events
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

// getTestPodWithAnnotations Add node selector to test pod
func getTestPodWithAnnotations(annotations map[string]string, cpus int) *corev1.Pod {
	testpod := getTestPodWithProfileAndAnnotations(profile, annotations, cpus)

	testpod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNode.Name}

	return testpod
}

// getTestPodWithProfileAndAnnotations Create pod with appropriate resources and annotations
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

// getPodCpus return cpus used based on taskset
func getPodCpus(testpod *corev1.Pod) (string, error) {
	tasksetcmd := []string{"taskset", "-pc", "1"}
	testpodCpusByte, err := pods.ExecCommandOnPod(testclient.K8sClient, testpod, tasksetcmd)
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
		cpusNotInSchedulingDomains, err := getCPUswithLoadBalanceDisabled(workerRTNode)
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
