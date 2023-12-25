package __performance_kubelet_node_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/cpuset"
	"sigs.k8s.io/controller-runtime/pkg/client"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/images"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

const minRequiredCPUs = 8

var _ = Describe("[performance] Cgroups and affinity", Ordered, func() {
	const (
		activation_file string = "/rootfs/var/lib/ovn-ic/etc/enable_dynamic_cpu_affinity"
	)
	var (
		onlineCPUSet            cpuset.CPUSet
		workerRTNode            *corev1.Node
		workerRTNodes           []corev1.Node
		profile, initialProfile *performancev2.PerformanceProfile
		performanceMCP          string
		ovsSliceCgroup          string
	)

	BeforeAll(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}
		workerRTNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("error looking for the optional selector: %v", err))
		workerRTNode = &workerRTNodes[0]

		profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		performanceMCP, err = mcps.GetByProfile(profile)
		Expect(err).ToNot(HaveOccurred())

		// TODO: This path is not compatible with cgroupv2.
		ovsSliceCgroup = "/rootfs/sys/fs/cgroup/cpuset/ovs.slice/"
	})

	BeforeEach(func() {
		var err error
		By(fmt.Sprintf("Checking the profile %s with cpus %s", profile.Name, cpuSpecToString(profile.Spec.CPU)))

		Expect(profile.Spec.CPU.Isolated).NotTo(BeNil())
		Expect(profile.Spec.CPU.Reserved).NotTo(BeNil())

		onlineCPUSet, err = nodes.GetOnlineCPUsSet(context.TODO(), workerRTNode)
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("[rfe_id: 64006][Dynamic OVS Pinning]", Ordered, func() {
		Context("[Performance Profile applied]", func() {
			It("[test_id:64097] Activation file is created", func() {
				cmd := []string{"ls", activation_file}
				for _, node := range workerRTNodes {
					out, err := nodes.ExecCommandOnNode(context.TODO(), cmd, &node)
					Expect(err).ToNot(HaveOccurred(), "file %s doesn't exist ", activation_file)
					Expect(out).To(Equal(activation_file))
				}
			})
		})
		Context("[Performance Profile Modified]", func() {
			BeforeEach(func() {
				initialProfile = profile.DeepCopy()
			})
			It("[test_id:64099] Activation file doesn't get deleted", func() {
				performanceMCP, err := mcps.GetByProfile(profile)
				Expect(err).ToNot(HaveOccurred())
				policy := "best-effort"
				// Need to make some changes to pp , causing system reboot
				// and check if activation files is modified or deleted
				profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
				Expect(err).ToNot(HaveOccurred(), "Unable to fetch latest performance profile")
				currentPolicy := profile.Spec.NUMA.TopologyPolicy
				if *currentPolicy == "best-effort" {
					policy = "restricted"
				}
				profile.Spec.NUMA = &performancev2.NUMA{
					TopologyPolicy: &policy,
				}
				By("Updating the performance profile")
				profiles.UpdateWithRetry(profile)
				By("Applying changes in performance profile and waiting until mcp will start updating")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
				By("Waiting for MCP being updated")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
				By("Checking Activation file")
				cmd := []string{"ls", activation_file}
				for _, node := range workerRTNodes {
					out, err := nodes.ExecCommandOnNode(context.TODO(), cmd, &node)
					Expect(err).ToNot(HaveOccurred(), "file %s doesn't exist ", activation_file)
					Expect(out).To(Equal(activation_file))
				}
			})
			AfterEach(func() {
				By("Reverting the Profile")
				profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
				Expect(err).ToNot(HaveOccurred())
				currentSpec, _ := json.Marshal(profile.Spec)
				spec, _ := json.Marshal(initialProfile.Spec)
				performanceMCP, err := mcps.GetByProfile(profile)
				Expect(err).ToNot(HaveOccurred())
				// revert only if the profile changes.
				if !bytes.Equal(currentSpec, spec) {
					Expect(testclient.Client.Patch(context.TODO(), profile,
						client.RawPatch(
							types.JSONPatchType,
							[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, spec)),
						),
					)).ToNot(HaveOccurred())

					By("Applying changes in performance profile and waiting until mcp will start updating")
					mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

					By("Waiting when mcp finishes updates")
					mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
				}
			})
		})
	})

	Context("Verification of cgroup layout on the worker node", func() {
		chkOvsCgrpProcs := func(node *corev1.Node) (string, error) {
			testlog.Info("Verify cgroup.procs is not empty")
			// TODO: This path is not compatible with cgroupv2.
			ovsCgroupPath := filepath.Join(ovsSliceCgroup, "cgroup.procs")
			cmd := []string{"cat", ovsCgroupPath}
			return nodes.ExecCommandOnNode(context.TODO(), cmd, node)
		}
		chkOvsCgrpCpuset := func(node *corev1.Node) (string, error) {
			// TODO: This path is not compatible with cgroupv2.
			ovsCgroupPath := filepath.Join(ovsSliceCgroup, "cpuset.cpus")
			cmd := []string{"cat", ovsCgroupPath}
			return nodes.ExecCommandOnNode(context.TODO(), cmd, node)
		}

		chkOvsCgroupLoadBalance := func(node *corev1.Node) (string, error) {
			// TODO: This path is not compatible with cgroupv2.
			// cpuset.sched_load_balance is not available under cgroupv2
			ovsCgroupPath := filepath.Join(ovsSliceCgroup, "cpuset.sched_load_balance")
			cmd := []string{"cat", ovsCgroupPath}
			return nodes.ExecCommandOnNode(context.TODO(), cmd, node)
		}

		It("[test_id:64098] Verify cgroup layout on worker node", func() {
			// check cgroups.procs
			result, err := chkOvsCgrpProcs(workerRTNode)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Not(BeEmpty()))

			result, err = chkOvsCgrpCpuset(workerRTNode)
			Expect(err).ToNot(HaveOccurred(), "Unable to read cpuset.cpus")
			ovsCPUSet, err := cpuset.Parse(result)
			Expect(err).ToNot(HaveOccurred())
			Expect(ovsCPUSet).To(Equal(onlineCPUSet))

			result, err = chkOvsCgroupLoadBalance(workerRTNode)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal("0"))
		})
	})

	Describe("Affinity", func() {
		Context("ovn-kubenode Pods affinity ", func() {
			testutils.CustomBeforeAll(func() {
				initialProfile = profile.DeepCopy()
			})
			It("[test_id:64100] matches with ovs process affinity", func() {
				ovnPod, err := getOvnPod(context.TODO(), workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to get ovnPod")

				ovnContainersids, err := getOvnPodContainers(&ovnPod)
				Expect(err).ToNot(HaveOccurred())

				// Generally there are many containers inside a kubenode pods
				// we don't need to check cpus used by all the containers
				// we take first container
				cpus := getCpusUsedByOvnContainer(context.TODO(), workerRTNode, ovnContainersids[0])
				testlog.Infof("Cpus used by ovn Containers are %s", cpus)
				pidList, err := getOVSServicesPid(context.TODO(), workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				cpumaskList, err := getCPUMaskForPids(context.TODO(), pidList, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, cpumask := range cpumaskList {
					Expect(cpus).To(Equal(cpumask), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", cpus, cpumask)
				}
			})

			It("[test_id:64101] Creating gu pods modifies affinity of ovs", func() {
				var testpod *corev1.Pod
				var err error
				testpod = pods.GetTestPod()
				testpod.Namespace = testutils.NamespaceTesting
				testpod.Spec.Containers[0].Resources = corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("200Mi"),
					},
				}
				testpod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNode.Name}
				err = testclient.Client.Create(context.TODO(), testpod)
				Expect(err).ToNot(HaveOccurred())
				testpod, err = pods.WaitForCondition(context.TODO(), client.ObjectKeyFromObject(testpod), corev1.PodConditionType(corev1.PodReady), corev1.ConditionTrue, 5*time.Minute)
				Expect(err).ToNot(HaveOccurred())
				Expect(testpod.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed))

				cmd := []string{"taskset", "-pc", "1"}
				testpodCpus, err := pods.ExecCommandOnPod(testclient.K8sClient, testpod, cmd)
				testlog.Infof("%v pod is using %v cpus", testpod.Name, string(testpodCpus))

				By("Get ovnpods running on the worker cnf node")
				ovnPod, err := getOvnPod(context.TODO(), workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to get ovnPod")

				By("Get cpu used by ovn pod containers")
				ovnContainers, err := getOvnPodContainers(&ovnPod)
				Expect(err).ToNot(HaveOccurred())
				cpus := getCpusUsedByOvnContainer(context.TODO(), workerRTNode, ovnContainers[0])

				pidList, err := getOVSServicesPid(context.TODO(), workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				cpumaskList, err := getCPUMaskForPids(context.TODO(), pidList, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, cpumask := range cpumaskList {
					Expect(cpus).To(Equal(cpumask), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", cpus, cpumask)
				}
				deleteTestPod(context.TODO(), testpod)
			})

			It("[test_id:64102] Create and remove gu pods to verify affinity of ovs are changed appropriately", func() {
				var testpod1, testpod2 *corev1.Pod
				var err error
				checkCpuCount(context.TODO(), workerRTNode)
				// Create testpod1
				testpod1 = pods.GetTestPod()
				testpod1.Namespace = testutils.NamespaceTesting
				testpod1.Spec.Containers[0].Resources = corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("200Mi"),
					},
				}

				testpod1.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNode.Name}
				err = testclient.Client.Create(context.TODO(), testpod1)
				Expect(err).ToNot(HaveOccurred())
				testpod1, err = pods.WaitForCondition(context.TODO(), client.ObjectKeyFromObject(testpod1), corev1.PodConditionType(corev1.PodReady), corev1.ConditionTrue, 5*time.Minute)
				Expect(err).ToNot(HaveOccurred())
				Expect(testpod1.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed))

				tasksetcmd := []string{"taskset", "-pc", "1"}
				testpod1Cpus, err := pods.ExecCommandOnPod(testclient.K8sClient, testpod1, tasksetcmd)
				testlog.Infof("%v pod is using %v cpus", testpod1.Name, string(testpod1Cpus))

				// Create testpod2
				testpod2 = pods.GetTestPod()
				testpod2.Namespace = testutils.NamespaceTesting
				testpod2.Spec.Containers[0].Resources = corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("200Mi"),
					},
				}
				testpod2.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNode.Name}
				err = testclient.Client.Create(context.TODO(), testpod2)
				Expect(err).ToNot(HaveOccurred())
				testpod2, err = pods.WaitForCondition(context.TODO(), client.ObjectKeyFromObject(testpod2), corev1.PodConditionType(corev1.PodReady), corev1.ConditionTrue, 5*time.Minute)
				Expect(err).ToNot(HaveOccurred())
				Expect(testpod1.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed))

				By("Getting the container cpuset.cpus cgroup")
				testpod2Cpus, err := pods.ExecCommandOnPod(testclient.K8sClient, testpod2, tasksetcmd)
				testlog.Infof("%v pod is using %v cpus", testpod2.Name, string(testpod2Cpus))

				// Get cpus used by the ovnkubenode-pods containers
				// Each kubenode pods have many containers, we check cpus of only 1 container
				ovnContainerCpus, err := getOvnContainerCpus(context.TODO(), workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to fetch cpus of the containers inside ovn kubenode pods")
				testlog.Infof("cpus used by ovn kube node pods %v", ovnContainerCpus)
				pidList, err := getOVSServicesPid(context.TODO(), workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				// Verify ovs-vswitchd and ovsdb-server process affinity is updated
				cpumaskList1, err := getCPUMaskForPids(context.TODO(), pidList, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, cpumask := range cpumaskList1 {
					Expect(ovnContainerCpus).To(Equal(cpumask), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", ovnContainerCpus, cpumask)
				}
				// Delete testpod1
				testlog.Infof("Deleting pod %v", testpod1.Name)
				deleteTestPod(context.TODO(), testpod1)

				// Check the cpus of ovnkubenode pods
				ovnContainerCpus, err = getOvnContainerCpus(context.TODO(), workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to fetch cpus of the containers inside ovn kubenode pods")
				testlog.Infof("cpus used by ovn kube node pods after deleting pod %v is %v", testpod1.Name, ovnContainerCpus)
				pidList, err = getOVSServicesPid(context.TODO(), workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				// Verify ovs-vswitchd and ovsdb-server process affinity is updated
				cpumaskList2, err := getCPUMaskForPids(context.TODO(), pidList, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, cpumask := range cpumaskList2 {
					Expect(ovnContainerCpus).To(Equal(cpumask), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", ovnContainerCpus, cpumask)
				}
				// Delete testpod2
				deleteTestPod(context.TODO(), testpod2)
			})

			It("[test_id:64103] ovs process affinity still excludes guaranteed pods after reboot", func() {
				checkCpuCount(context.TODO(), workerRTNode)
				var dp *appsv1.Deployment
				// create a deployment to deploy gu pods
				dp = newDeployment()
				testNode := make(map[string]string)
				testNode["kubernetes.io/hostname"] = workerRTNode.Name
				dp.Spec.Template.Spec.NodeSelector = testNode
				err := testclient.Client.Create(context.TODO(), dp)
				Expect(err).ToNot(HaveOccurred(), "Unable to create Deployment")

				defer func() {
					// delete deployment
					testlog.Infof("Deleting Deployment %v", dp.Name)
					err := testclient.Client.Delete(context.TODO(), dp)
					Expect(err).ToNot(HaveOccurred())
				}()

				// Check the cpus of ovn kube node pods and ovs services
				ovnContainerCpus, err := getOvnContainerCpus(context.TODO(), workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to fetch cpus of the containers inside ovn kubenode pods")
				testlog.Infof("cpus used by ovn kube node pods %v", ovnContainerCpus)
				pidList, err := getOVSServicesPid(context.TODO(), workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				// Verify ovs-vswitchd and ovsdb-server process affinity is updated
				cpumaskList1, err := getCPUMaskForPids(context.TODO(), pidList, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, cpumask := range cpumaskList1 {
					Expect(ovnContainerCpus).To(Equal(cpumask), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", ovnContainerCpus, cpumask)
				}

				testlog.Info("Rebooting the node")
				// reboot the node, for that we change the numa policy to best-effort
				// Note: this is used only to trigger reboot
				policy := "best-effort"
				// Need to make some changes to pp , causing system reboot
				// and check if activation files is modified or deleted
				profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
				Expect(err).ToNot(HaveOccurred(), "Unable to fetch latest performance profile")
				currentPolicy := profile.Spec.NUMA.TopologyPolicy
				if *currentPolicy == "best-effort" {
					policy = "restricted"
				}
				profile.Spec.NUMA = &performancev2.NUMA{
					TopologyPolicy: &policy,
				}

				By("Updating the performance profile")
				profiles.UpdateWithRetry(profile)
				By("Applying changes in performance profile and waiting until mcp will start updating")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
				By("Waiting for MCP being updated")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

				// After reboot we want the deployment to be ready before moving forward
				desiredStatus := appsv1.DeploymentStatus{
					Replicas:          3,
					AvailableReplicas: 3,
				}

				err = waitForCondition(dp, desiredStatus)

				// Check the cpus of ovn kube node pods and ovs services
				ovnContainerCpus, err = getOvnContainerCpus(context.TODO(), workerRTNode)
				Expect(err).ToNot(HaveOccurred(), "Unable to fetch cpus of the containers inside ovn kubenode pods")
				testlog.Infof("cpus used by ovn kube node pods %v", ovnContainerCpus)
				pidList, err = getOVSServicesPid(context.TODO(), workerRTNode)
				Expect(err).ToNot(HaveOccurred())

				// Verify ovs-vswitchd and ovsdb-server process affinity is updated
				cpumaskList2, err := getCPUMaskForPids(context.TODO(), pidList, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				for _, cpumask := range cpumaskList2 {
					Expect(ovnContainerCpus).To(Equal(cpumask), "affinity of ovn kube node pods(%s) do not match with ovservices(%s)", ovnContainerCpus, cpumask)
				}
			})
			AfterAll(func() {
				By("Reverting the Profile")
				profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
				Expect(err).ToNot(HaveOccurred())
				currentSpec, _ := json.Marshal(profile.Spec)
				spec, _ := json.Marshal(initialProfile.Spec)
				performanceMCP, err := mcps.GetByProfile(profile)
				Expect(err).ToNot(HaveOccurred())
				// revert only if the profile changes.
				if !bytes.Equal(currentSpec, spec) {
					Expect(testclient.Client.Patch(context.TODO(), profile,
						client.RawPatch(
							types.JSONPatchType,
							[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, spec)),
						),
					)).ToNot(HaveOccurred())

					By("Applying changes in performance profile and waiting until mcp will start updating")
					mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

					By("Waiting when mcp finishes updates")
					mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
				}
			})
		})

	})
})

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

// checkCpuCount check if the node has sufficient cpus
func checkCpuCount(ctx context.Context, workerNode *corev1.Node) {
	onlineCPUCount, err := nodes.ExecCommandOnNode(ctx, []string{"nproc", "--all"}, workerNode)
	if err != nil {
		Fail(fmt.Sprintf("Failed to fetch online CPUs: %v", err))
	}
	onlineCPUInt, err := strconv.Atoi(onlineCPUCount)
	if err != nil {
		Fail(fmt.Sprintf("failed to convert online CPU count to integer: %v", err))
	}
	if onlineCPUInt <= minRequiredCPUs {
		Skip(fmt.Sprintf("This test requires more than %d isolated CPUs, current available CPUs: %s", minRequiredCPUs, onlineCPUCount))
	}
}

// deleteTestPod removes guaranteed pod
func deleteTestPod(ctx context.Context, testpod *corev1.Pod) {
	// it possible that the pod already was deleted as part of the test, in this case we want to skip teardown
	err := testclient.Client.Get(ctx, client.ObjectKeyFromObject(testpod), testpod)
	if errors.IsNotFound(err) {
		return
	}

	err = testclient.Client.Delete(ctx, testpod)
	Expect(err).ToNot(HaveOccurred())

	err = pods.WaitForDeletion(ctx, testpod, pods.DefaultDeletionTimeout*time.Second)
	Expect(err).ToNot(HaveOccurred())
}

// getOvnPod Get OVN Kubenode pods running on the worker cnf node
func getOvnPod(ctx context.Context, workerNode *corev1.Node) (corev1.Pod, error) {
	var ovnKubeNodePod corev1.Pod
	ovnpods := &corev1.PodList{}
	options := &client.ListOptions{
		Namespace: "openshift-ovn-kubernetes",
	}
	err := testclient.Client.List(ctx, ovnpods, options)
	if err != nil {
		return ovnKubeNodePod, err
	}
	for _, pod := range ovnpods.Items {
		if strings.Contains(pod.Name, "node") && pod.Spec.NodeName == workerNode.Name {
			ovnKubeNodePod = pod
			break
		}
	}
	testlog.Infof("ovn kube node pod running on %s is %s", workerNode.Name, ovnKubeNodePod.Name)
	return ovnKubeNodePod, err
}

// getOvnPodContainers returns containerids of all containers running inside ovn kube node pod
func getOvnPodContainers(ovnKubeNodePod *corev1.Pod) ([]string, error) {
	var ovnKubeNodePodContainerids []string
	var err error
	for _, ovnctn := range ovnKubeNodePod.Spec.Containers {
		ctnName, err := pods.GetContainerIDByName(ovnKubeNodePod, ovnctn.Name)
		if err != nil {
			err = fmt.Errorf("unable to fetch container id of %v", ovnctn)
		}
		ovnKubeNodePodContainerids = append(ovnKubeNodePodContainerids, ctnName)
	}
	return ovnKubeNodePodContainerids, err
}

// getCpusUsedByOvnContainer returns cpus used by the ovn kube node container
func getCpusUsedByOvnContainer(ctx context.Context, workerRTNode *corev1.Node, ovnKubeNodePodCtnid string) string {
	var cpus string
	var err error
	var containerCgroup = ""
	Eventually(func() string {
		// TODO: This path is not compatible with cgroupv2.
		cmd := []string{"/bin/bash", "-c", fmt.Sprintf("find /rootfs/sys/fs/cgroup/cpuset/ -name '*%s*'", ovnKubeNodePodCtnid)}
		containerCgroup, err = nodes.ExecCommandOnNode(ctx, cmd, workerRTNode)
		Expect(err).ToNot(HaveOccurred(), "failed to run %s cmd", cmd)
		return containerCgroup
	}, 10*time.Second, 5*time.Second).ShouldNot(BeEmpty())
	cmd := []string{"/bin/bash", "-c", fmt.Sprintf("cat %s/cpuset.cpus", containerCgroup)}
	cpus, err = nodes.ExecCommandOnNode(ctx, cmd, workerRTNode)
	Expect(err).ToNot(HaveOccurred())

	// Wait for a period of time as it takes some time(10s) for cpu manager
	// to update the cpu usage when gu pod is deleted or created
	time.Sleep(30 * time.Second)
	cmd = []string{"/bin/bash", "-c", fmt.Sprintf("cat %s/cpuset.cpus", containerCgroup)}
	cpus, err = nodes.ExecCommandOnNode(ctx, cmd, workerRTNode)
	Expect(err).ToNot(HaveOccurred())
	return cpus
}

// getOvnContain erCpus returns the cpus used by the container inside the ovn kubenode pods
func getOvnContainerCpus(ctx context.Context, workerRTNode *corev1.Node) (string, error) {
	var ovnContainerCpus string
	ovnPod, err := getOvnPod(ctx, workerRTNode)
	if err != nil {
		return "", err
	}
	ovnContainers, err := getOvnPodContainers(&ovnPod)
	if err != nil {
		return "", err
	}
	ovnContainerCpus = getCpusUsedByOvnContainer(ctx, workerRTNode, ovnContainers[0])
	testlog.Infof("cpus used by ovn kube node pods %v", ovnContainerCpus)
	return ovnContainerCpus, nil
}

// getOVSServicesPid returns the pid of ovs-vswitchd and ovsdb-server
func getOVSServicesPid(ctx context.Context, workerNode *corev1.Node) ([]string, error) {
	var pids []string
	// TODO: This path is not compatible with cgroupv2.
	cmd := []string{"cat", "/rootfs/sys/fs/cgroup/cpuset/ovs.slice/cgroup.procs"}
	output, err := nodes.ExecCommandOnNode(ctx, cmd, workerNode)
	pids = strings.Split(string(output), "\n")
	return pids, err
}

// getCPUMaskForPids returns a slice containing cpu affinity of ovs services
func getCPUMaskForPids(ctx context.Context, pidList []string, targetNode *corev1.Node) ([]string, error) {
	var cpumaskList []string

	for _, pid := range pidList {
		cmd := []string{"taskset", "-pc", pid}
		cpumask, err := nodes.ExecCommandOnNode(ctx, cmd, targetNode)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch cpus of %s: %s", pid, err)
		}

		mask := strings.SplitAfter(cpumask, " ")
		maskSet, err := cpuset.Parse(mask[len(mask)-1])
		if err != nil {
			return nil, fmt.Errorf("failed to parse cpuset: %s", err)
		}

		cpumaskList = append(cpumaskList, maskSet.String())
	}

	return cpumaskList, nil
}

func newDeployment() *appsv1.Deployment {
	var replicas int32 = 2
	dp := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-",
			Labels: map[string]string{
				"testDeployment": "",
			},
			Namespace: testutils.NamespaceTesting,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"type": "telco",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"type": "telco",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:    "test",
							Image:   images.Test(),
							Command: []string{"sleep", "inf"},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("200Mi"),
									corev1.ResourceCPU:    resource.MustParse("2"),
								},
							},
						},
					},
				},
			},
		},
	}
	return dp
}

// waitForCondition wait for deployment to be ready
func waitForCondition(deployment *appsv1.Deployment, status appsv1.DeploymentStatus) error {
	var err error
	var val bool
	err = wait.PollUntilContextTimeout(context.TODO(), 5*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		if err != nil {
			if errors.IsNotFound(err) {
				return false, fmt.Errorf("deployment not found")
			}
			return false, err
		}
		val = (deployment.Status.Replicas == status.Replicas && deployment.Status.AvailableReplicas == status.AvailableReplicas)
		return val, err
	})

	return err
}
