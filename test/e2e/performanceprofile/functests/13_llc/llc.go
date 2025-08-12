package __llc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/jaypipes/ghw/pkg/cpu"
	"github.com/jaypipes/ghw/pkg/topology"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/cpuset"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	profilecomponent "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup/controller"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/deployments"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/images"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/namespaces"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/poolname"

	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profilesupdate"
)

const (
	llcEnableFileName            = "/etc/kubernetes/openshift-llc-alignment"
	defaultIgnitionContentSource = "data:text/plain;charset=utf-8;base64"
	defaultIgnitionVersion       = "3.2.0"
	fileMode                     = 0420
	restartCooldownTime          = 2 * time.Minute
	deploymentDeletionTime       = 1 * time.Minute
)

const (
	// random number corresponding to the minimum we need. No known supported hardware has groups so little, they are all way bigger
	expectedMinL3GroupSize = 8
)

type Machine struct {
	CPU      *cpu.Info      `json:"cpu"`
	Topology *topology.Info `json:"topology"`
}

type CacheInfo struct {
	NodeID int
	Level  int
	CPUs   cpuset.CPUSet
}

func (ci CacheInfo) String() string {
	return fmt.Sprintf("NUMANode=%d cacheLevel=%d cpus=<%s>", ci.NodeID, ci.Level, ci.CPUs.String())
}

type MachineData struct {
	Info   Machine
	Caches []CacheInfo
}

var _ = Describe("[rfe_id:77446] LLC-aware cpu pinning", Label(string(label.OpenShift), string(label.UnCoreCache)), Ordered, func() {
	var (
		workerRTNodes               []corev1.Node
		machineDatas                map[string]MachineData // nodeName -> MachineData
		initialProfile, perfProfile *performancev2.PerformanceProfile
		performanceMCP              string
		err                         error
		profileAnnotations          map[string]string
		poolName                    string
		llcPolicy                   string
		mc                          *machineconfigv1.MachineConfig
		getter                      cgroup.ControllersGetter
		cgroupV2                    bool
	)

	BeforeAll(func() {
		var hasMachineData bool
		profileAnnotations = make(map[string]string)
		ctx := context.Background()

		workerRTNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred(), "error looking for the optional selector: %v", err)

		if len(workerRTNodes) < 1 {
			Skip("need at least a worker node")
		}

		perfProfile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		initialProfile = perfProfile.DeepCopy()

		By(fmt.Sprintf("collecting machine infos for %d nodes", len(workerRTNodes)))
		machineDatas, hasMachineData, err = collectMachineDatas(ctx, workerRTNodes)
		if !hasMachineData {
			Skip("need machinedata available - please check the image for the presence of the machineinfo tool")
		}
		Expect(err).ToNot(HaveOccurred())

		for node, data := range machineDatas {
			testlog.Infof("node=%q data=%v", node, data.Caches)
		}

		getter, err = cgroup.BuildGetter(ctx, testclient.DataPlaneClient, testclient.K8sClient)
		Expect(err).ToNot(HaveOccurred())
		cgroupV2, err = cgroup.IsVersion2(ctx, testclient.DataPlaneClient)
		Expect(err).ToNot(HaveOccurred())
		if !cgroupV2 {
			Skip("prefer-align-cpus-by-uncorecache cpumanager policy options is supported in cgroupv2 configuration only")
		}

		performanceMCP, err = mcps.GetByProfile(perfProfile)
		Expect(err).ToNot(HaveOccurred())

		poolName = poolname.GetByProfile(ctx, perfProfile)

		mc, err = createMachineConfig(perfProfile)
		Expect(err).ToNot(HaveOccurred())

		llcPolicy = `{"cpuManagerPolicyOptions":{"prefer-align-cpus-by-uncorecache":"true", "full-pcpus-only":"true"}}`
		profileAnnotations["kubeletconfig.experimental"] = llcPolicy

		// Create machine config to create file /etc/kubernetes/openshift-llc-alignment
		// required to enable align-cpus-by-uncorecache cpumanager policy

		By("Enabling Uncore cache feature")
		Expect(testclient.Client.Create(context.TODO(), mc)).To(Succeed(), "Unable to apply machine config for enabling uncore cache")

		Expect(err).ToNot(HaveOccurred(), "Unable to apply machine config for enabling uncore cache")

		mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
		By("Waiting when mcp finishes updates")

		mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

		// Apply Annotation to enable align-cpu-by-uncorecache cpumanager policy option
		if perfProfile.Annotations == nil || perfProfile.Annotations["kubeletconfig.experimental"] != llcPolicy {
			testlog.Info("Enable align-cpus-by-uncorecache cpumanager policy")
			prof := perfProfile.DeepCopy()
			prof.Annotations = profileAnnotations

			By("updating performance profile")
			profiles.UpdateWithRetry(prof)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(ctx, prof)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(ctx, prof)
		}
	})

	AfterAll(func(ctx context.Context) {

		// Delete machine config created to enable uncocre cache cpumanager policy option
		// first make sure the profile doesn't have the annotation
		By("Reverting Performance Profile")
		perfProfile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		perfProfile.Annotations = nil
		By("updating performance profile")
		profiles.UpdateWithRetry(initialProfile)

		By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
		profilesupdate.WaitForTuningUpdating(ctx, initialProfile)

		By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
		profilesupdate.WaitForTuningUpdated(ctx, initialProfile)

		// delete the machine config pool
		Expect(testclient.Client.Delete(ctx, mc)).To(Succeed())

		mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
		By("Waiting when mcp finishes updates")

		mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
	})

	Context("Configuration Tests", func() {
		When("align-cpus-by-uncorecache cpumanager policy option is enabled", func() {
			It("[test_id:77722] kubelet is configured appropriately", func() {
				ctx := context.Background()
				for _, node := range workerRTNodes {
					kubeletconfig, err := nodes.GetKubeletConfig(ctx, &node)
					Expect(err).ToNot(HaveOccurred())
					Expect(kubeletconfig.CPUManagerPolicyOptions).To(HaveKeyWithValue("prefer-align-cpus-by-uncorecache", "true"))
				}
			})
		})

		When("align-cpus-by-uncorecache annotations is removed", func() {
			It("[test_id:77723] should disable align-cpus-by-uncorecache cpumanager policy option", func() {
				ctx := context.Background()
				// Get latest profile
				perfProfile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
				Expect(err).ToNot(HaveOccurred())
				// Delete the Annotations
				if perfProfile.Annotations != nil {
					perfProfile.Annotations = nil

					By("updating performance profile")
					profiles.UpdateWithRetry(perfProfile)

					By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
					profilesupdate.WaitForTuningUpdating(ctx, perfProfile)

					By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
					profilesupdate.WaitForTuningUpdated(ctx, perfProfile)
				}
				// Use Eventually in case of any delays in updating kubelet
				Eventually(func() bool {
					for _, node := range workerRTNodes {
						kubeletconfig, err := nodes.GetKubeletConfig(ctx, &node)
						if err != nil {
							testlog.Errorf("Failed to get kubelet config from node %s: %v", node.Name, err)
							return false
						}
						if kubeletconfig.CPUManagerPolicyOptions != nil {
							if _, exists := kubeletconfig.CPUManagerPolicyOptions["prefer-align-cpus-by-uncorecache"]; exists {
								testlog.Infof("Node %s still has prefer-align-cpus-by-uncorecache", node.Name)
								return false
							}
						}
					}
					return true
				}, 5*time.Minute, 30*time.Second).Should(BeTrue(), "prefer-align-cpus-by-uncore option should be removed from all nodes")
			})
		})

		When("align-cpus-by-uncorecache cpumanager policy option is disabled", func() {
			It("[test_id:77724] cpumanager Policy option in kubelet is configured appropriately", func() {
				ctx := context.Background()
				llcDisablePolicy := `{"cpuManagerPolicyOptions":{"prefer-align-cpus-by-uncorecache":"false", "full-pcpus-only":"true"}}`
				profileAnnotations["kubeletconfig.experimental"] = llcDisablePolicy
				perfProfile.Annotations = profileAnnotations

				By("updating performance profile")
				profiles.UpdateWithRetry(perfProfile)

				By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
				profilesupdate.WaitForTuningUpdating(ctx, perfProfile)

				By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
				profilesupdate.WaitForTuningUpdated(ctx, perfProfile)

				for _, node := range workerRTNodes {
					kubeletconfig, err := nodes.GetKubeletConfig(ctx, &node)
					Expect(err).ToNot(HaveOccurred())
					Expect(kubeletconfig.CPUManagerPolicyOptions).To(HaveKeyWithValue("prefer-align-cpus-by-uncorecache", "false"))
				}
			})
		})
	})

	Context("Functional Tests with SMT Enabled", func() {
		var (
			L3CacheGroupSize int
			totalOnlineCpus  cpuset.CPUSet
			getCCX           func(cpuid int) (cpuset.CPUSet, error)
		)
		BeforeAll(func() {
			var (
				reserved, isolated cpuset.CPUSet
				policy             = "single-numa-node"
			)
			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			ctx := context.Background()
			for _, cnfnode := range workerRTNodes {
				numaInfo, err := nodes.GetNumaNodes(ctx, &cnfnode)
				Expect(err).ToNot(HaveOccurred(), "Unable to get numa information from the node")
				if len(numaInfo) < 2 {
					Skip(fmt.Sprintf("This test need 2 Numa nodes. The number of numa nodes on node %s < 2", cnfnode.Name))
				}

				getCCX = nodes.GetL3SharedCPUs(&cnfnode)
				reserved, err = getCCX(0)
				Expect(err).ToNot(HaveOccurred())
				totalOnlineCpus, err = nodes.GetOnlineCPUsSet(ctx, &cnfnode)
				Expect(err).ToNot(HaveOccurred())
				L3CacheGroupSize = reserved.Size()
				if len(numaInfo[0]) == L3CacheGroupSize {
					Skip("This test requires systems where L3 cache is shared amount subset of cpus")
				}
			}
			// Modify the profile such that we give 1 whole ccx to reserved cpus
			By("Modifying the profile")
			isolated = totalOnlineCpus.Difference(reserved)
			reservedSet := performancev2.CPUSet(reserved.String())
			isolatedSet := performancev2.CPUSet(isolated.String())
			testlog.Infof("Modifying profile with reserved cpus %s and isolated cpus %s", reserved.String(), isolated.String())
			profile.Spec.CPU = &performancev2.CPU{
				Reserved: &reservedSet,
				Isolated: &isolatedSet,
			}
			profile.Spec.NUMA = &performancev2.NUMA{
				TopologyPolicy: &policy,
			}
			By("Updating Performance profile")
			profiles.UpdateWithRetry(profile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(ctx, profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(ctx, profile)

			testNS := *namespaces.TestingNamespace
			Expect(testclient.DataPlaneClient.Create(ctx, &testNS)).ToNot(HaveOccurred())
			DeferCleanup(func() {
				Expect(testclient.DataPlaneClient.Delete(ctx, &testNS)).ToNot(HaveOccurred())
				Expect(namespaces.WaitForDeletion(testutils.NamespaceTesting, 5*time.Minute)).ToNot(HaveOccurred())
			})
		})

		It("[test_id:77725] Align Guaranteed pod requesting L3Cache group size cpus", func(ctx context.Context) {
			podLabel := make(map[string]string)
			targetNode := workerRTNodes[0]
			cpusetCfg := &controller.CpuSet{}
			deploymentName := "test-deployment1"
			getCCX := nodes.GetL3SharedCPUs(&targetNode)

			rl := &corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(strconv.Itoa(L3CacheGroupSize)),
				corev1.ResourceMemory: resource.MustParse("100Mi"),
			}
			podLabel["test-app"] = "telco1"
			dp, err := createDeployment(ctx, deploymentName, podLabel, &targetNode, rl)
			Expect(err).ToNot(HaveOccurred())
			defer func() {
				testlog.TaggedInfof("Cleanup", "Deleting Deployment %v", deploymentName)
				err := testclient.DataPlaneClient.Delete(ctx, dp)
				Expect(err).ToNot(HaveOccurred(), "Unable to delete deployment %v", deploymentName)
				waitForDeploymentPodsDeletion(ctx, &targetNode, podLabel)
			}()

			podList := &corev1.PodList{}
			listOptions := &client.ListOptions{Namespace: testutils.NamespaceTesting, LabelSelector: labels.SelectorFromSet(dp.Spec.Selector.MatchLabels)}
			Eventually(func() bool {
				isReady, err := deployments.IsReady(ctx, testclient.Client, listOptions, podList, dp)
				Expect(err).ToNot(HaveOccurred())
				return isReady
			}, time.Minute, time.Second).Should(BeTrue())
			Expect(testclient.Client.List(ctx, podList, listOptions)).To(Succeed())
			Expect(len(podList.Items)).To(Equal(1), "Expected exactly one pod in the list")
			testpod := podList.Items[0]
			err = getter.Container(ctx, &testpod, testpod.Spec.Containers[0].Name, cpusetCfg)
			Expect(err).ToNot(HaveOccurred())
			testpodCpuset, err := cpuset.Parse(cpusetCfg.Cpus)
			testlog.TaggedInfof("Pod", "CPUs used by %q are: %q", testpod.Name, testpodCpuset.String())
			Expect(err).ToNot(HaveOccurred())
			// fetch ccx to which cpu used by pod is part of
			cpus, err := getCCX(testpodCpuset.List()[0])
			testlog.TaggedInfof("L3 Cache Group", "CPU Group sharing L3 Cache to which %s is alloted are: %s ", testpod.Name, cpus.String())
			Expect(err).ToNot(HaveOccurred())
			// All the cpus alloted to Pod should be equal to the cpus sharing L3 Cache group
			Expect(testpodCpuset).To(Equal(cpus))
		})

		It("[test_id:77726] Verify guaranteed pod consumes the whole Uncore group after reboot", func(ctx context.Context) {
			var err error
			podLabel := make(map[string]string)
			targetNode := workerRTNodes[0]
			cpusetCfg := &controller.CpuSet{}
			deploymentName := "test-deployment2"
			getCCX := nodes.GetL3SharedCPUs(&targetNode)
			rl := &corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(strconv.Itoa(L3CacheGroupSize)),
				corev1.ResourceMemory: resource.MustParse("100Mi"),
			}
			podLabel["test-app"] = "telco2"
			dp, err := createDeployment(ctx, deploymentName, podLabel, &targetNode, rl)
			Expect(err).ToNot(HaveOccurred())
			testlog.TaggedInfof("Deployment", "Deployment %s created", deploymentName)
			defer func() {
				// delete deployment
				testlog.TaggedInfof("Cleanup", "Deleting Deployment %s", deploymentName)
				err := testclient.DataPlaneClient.Delete(ctx, dp)
				Expect(err).ToNot(HaveOccurred(), "Unable to delete deployment %v", deploymentName)
				waitForDeploymentPodsDeletion(ctx, &targetNode, podLabel)
			}()
			podList := &corev1.PodList{}
			listOptions := &client.ListOptions{Namespace: testutils.NamespaceTesting, LabelSelector: labels.SelectorFromSet(dp.Spec.Selector.MatchLabels)}
			Eventually(func() bool {
				isReady, err := deployments.IsReady(ctx, testclient.Client, listOptions, podList, dp)
				Expect(err).ToNot(HaveOccurred())
				return isReady
			}, time.Minute, time.Second).Should(BeTrue())
			Expect(testclient.Client.List(ctx, podList, listOptions)).To(Succeed())
			Expect(len(podList.Items)).To(Equal(1), "Expected exactly one pod in the list")
			testpod := podList.Items[0]
			err = getter.Container(ctx, &testpod, testpod.Spec.Containers[0].Name, cpusetCfg)
			Expect(err).ToNot(HaveOccurred())
			cgroupCpuset, err := cpuset.Parse(cpusetCfg.Cpus)
			Expect(err).ToNot(HaveOccurred())
			testlog.TaggedInfof("Pod", "pod %s using cpus %q", testpod.Name, cgroupCpuset.String())
			// fetch ccx to which cpu used by pod is part of
			cpus, err := getCCX(cgroupCpuset.List()[0])
			testlog.TaggedInfof("L3 Cache Group", "L3 Cache group associated with Pod %s using cpu %d is %q: ", testpod.Name, cgroupCpuset.List()[0], cpus)
			Expect(err).ToNot(HaveOccurred())
			Expect(cgroupCpuset).To(Equal(cpus))
			// reboot the node
			rebootCmd := "chroot /rootfs systemctl reboot"
			testlog.TaggedInfof("Reboot", "Node %q: Rebooting", targetNode.Name)
			_, _ = nodes.ExecCommand(ctx, &targetNode, []string{"sh", "-c", rebootCmd})
			testlog.Info("Node Rebooted")

			By("Rebooted node manually and waiting for mcp to get Updating status")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

			By("Waiting for mcp to be updated")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

			// After reboot check the deployment
			desiredStatus := appsv1.DeploymentStatus{
				Replicas:          1,
				AvailableReplicas: 1,
			}
			err = deployments.WaitForDesiredDeploymentStatus(ctx, dp, testclient.Client, testutils.NamespaceTesting, dp.Name, desiredStatus)
			Expect(err).ToNot(HaveOccurred())
			testlog.TaggedInfof("Deployment", "Deployment %q is running after reboot", dp.Name)

			listOptions = &client.ListOptions{Namespace: testutils.NamespaceTesting, LabelSelector: labels.SelectorFromSet(dp.Spec.Selector.MatchLabels)}
			Eventually(func() bool {
				isReady, err := deployments.IsReady(ctx, testclient.Client, listOptions, podList, dp)
				Expect(err).ToNot(HaveOccurred())
				return isReady
			}, time.Minute, time.Second).Should(BeTrue())
			Expect(testclient.Client.List(ctx, podList, listOptions)).To(Succeed())
			Expect(len(podList.Items)).To(Equal(1), "Expected exactly one pod in the list")
			testpod = podList.Items[0]

			err = getter.Container(ctx, &testpod, testpod.Spec.Containers[0].Name, cpusetCfg)
			Expect(err).ToNot(HaveOccurred())
			cgroupCpuset, err = cpuset.Parse(cpusetCfg.Cpus)
			testlog.TaggedInfof("Pod", "pod %s using cpus %q", testpod.Name, cgroupCpuset.String())
			Expect(err).ToNot(HaveOccurred())
			// fetch ccx to which cpu used by pod is part of
			cpus, err = getCCX(cgroupCpuset.List()[0])
			Expect(err).ToNot(HaveOccurred())
			Expect(cgroupCpuset).To(Equal(cpus))
			testlog.TaggedInfof("L3 Cache Group", "L3 Cache group associated with Pod %s using cpu %d is %q: ", testpod.Name, cgroupCpuset.List()[0], cpus)
		})

		It("[test_id:81668]  Verify guaranteed pod consumes the whole Uncore group after kubelet restart", func(ctx context.Context) {
			podLabel := make(map[string]string)
			targetNode := workerRTNodes[0]
			getCCX := nodes.GetL3SharedCPUs(&targetNode)
			// fetch size of shared cpus by passing cpuid 1
			cpusetCfg := &controller.CpuSet{}
			deploymentName := "test-deployment3"
			rl := &corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(strconv.Itoa(L3CacheGroupSize)),
				corev1.ResourceMemory: resource.MustParse("100Mi"),
			}
			podLabel["test-app"] = "telco3"
			dp, err := createDeployment(ctx, deploymentName, podLabel, &targetNode, rl)
			Expect(err).ToNot(HaveOccurred())
			defer func() {
				// delete deployment
				testlog.TaggedInfof("Cleanup", "Deleting Deployment %v", deploymentName)
				err := testclient.DataPlaneClient.Delete(ctx, dp)
				Expect(err).ToNot(HaveOccurred(), "Unable to delete deployment %v", deploymentName)
				waitForDeploymentPodsDeletion(ctx, &targetNode, podLabel)
			}()
			podList := &corev1.PodList{}
			listOptions := &client.ListOptions{Namespace: testutils.NamespaceTesting, LabelSelector: labels.SelectorFromSet(dp.Spec.Selector.MatchLabels)}
			Eventually(func() bool {
				isReady, err := deployments.IsReady(ctx, testclient.Client, listOptions, podList, dp)
				Expect(err).ToNot(HaveOccurred())
				return isReady
			}, time.Minute, time.Second).Should(BeTrue())
			Expect(testclient.Client.List(ctx, podList, listOptions)).To(Succeed())
			Expect(len(podList.Items)).To(Equal(1), "Expected exactly one pod in the list")
			testpod := podList.Items[0]
			err = getter.Container(ctx, &testpod, testpod.Spec.Containers[0].Name, cpusetCfg)
			Expect(err).ToNot(HaveOccurred())
			cgroupCpuset, err := cpuset.Parse(cpusetCfg.Cpus)
			Expect(err).ToNot(HaveOccurred())
			// fetch ccx to which cpu used by pod is part of
			cpus, err := getCCX(cgroupCpuset.List()[0])
			Expect(err).ToNot(HaveOccurred())
			Expect(cgroupCpuset).To(Equal(cpus))

			kubeletRestartCmd := []string{
				"chroot",
				"/rootfs",
				"/bin/bash",
				"-c",
				"systemctl restart kubelet",
			}
			_, _ = nodes.ExecCommand(ctx, &targetNode, kubeletRestartCmd)
			nodes.WaitForReadyOrFail("post kubelet restart", targetNode.Name, 20*time.Minute, 3*time.Second)
			// giving kubelet more time to stabilize and initialize itself before
			testlog.Infof("post restart: entering cooldown time: %v", restartCooldownTime)
			time.Sleep(restartCooldownTime)

			testlog.Infof("post restart: finished cooldown time: %v", restartCooldownTime)
			// After restart of kubelet check the deployment
			desiredStatus := appsv1.DeploymentStatus{
				Replicas:          1,
				AvailableReplicas: 1,
			}
			err = deployments.WaitForDesiredDeploymentStatus(ctx, dp, testclient.Client, testutils.NamespaceTesting, dp.Name, desiredStatus)
			Expect(err).ToNot(HaveOccurred())
			testlog.TaggedInfof("Deployment", "Deployment %q is running after reboot", dp.Name)

			listOptions = &client.ListOptions{Namespace: testutils.NamespaceTesting, LabelSelector: labels.SelectorFromSet(dp.Spec.Selector.MatchLabels)}

			Eventually(func() bool {
				isReady, err := deployments.IsReady(ctx, testclient.Client, listOptions, podList, dp)
				Expect(err).ToNot(HaveOccurred())
				return isReady
			}, time.Minute, time.Second).Should(BeTrue())
			Expect(testclient.Client.List(ctx, podList, listOptions)).To(Succeed())
			Expect(len(podList.Items)).To(Equal(1), "Expected exactly one pod in the list")
			testpod = podList.Items[0]
			err = getter.Container(ctx, &testpod, testpod.Spec.Containers[0].Name, cpusetCfg)
			Expect(err).ToNot(HaveOccurred())
			cgroupCpuset, err = cpuset.Parse(cpusetCfg.Cpus)
			testlog.TaggedInfof("Pod", "pod %s using cpus %q", testpod.Name, cgroupCpuset.String())
			Expect(err).ToNot(HaveOccurred())
			// fetch ccx to which cpu used by pod is part of
			cpus, err = getCCX(cgroupCpuset.List()[0])
			Expect(err).ToNot(HaveOccurred())
			Expect(cgroupCpuset).To(Equal(cpus))
			testlog.TaggedInfof("L3 Cache Group", "L3 Cache group associated with Pod %s using cpu %d is: %q", testpod.Name, cgroupCpuset.List()[0], cpus)
		})

		Context("With Multiple Pods", func() {
			type L3UncoreCacheShareMode string
			const (
				L3UncoreCacheShareEqual   L3UncoreCacheShareMode = "equal"
				L3UncoreCacheShareUnequal L3UncoreCacheShareMode = "unequal"
			)
			DescribeTable("Align multiple Guaranteed pods",
				func(mode L3UncoreCacheShareMode) {
					var (
						ctx                   = context.Background()
						targetNode            = workerRTNodes[0]
						cpusetCfg             = &controller.CpuSet{}
						cpusetList            []cpuset.CPUSet
						podCpuRequirementList []string
						dpList                []*appsv1.Deployment
					)

					podLabel := make(map[string]string)
					switch mode {
					case L3UncoreCacheShareEqual:
						podCpuRequirementList = []string{fmt.Sprintf("%d", L3CacheGroupSize/2), fmt.Sprintf("%d", L3CacheGroupSize/2)}
					case L3UncoreCacheShareUnequal:
						podCpuRequirementList = []string{fmt.Sprintf("%d", L3CacheGroupSize), fmt.Sprintf("%d", L3CacheGroupSize/2)}
					}

					// create 2 deployments with pod cpu requirements are different
					// for each test case.
					for i := range 2 {
						deploymentName := fmt.Sprintf("test-deployment%d", i)
						rl := &corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(podCpuRequirementList[i]),
							corev1.ResourceMemory: resource.MustParse("100Mi"),
						}
						podLabel["test-app"] = fmt.Sprintf("telcoApp-%d", i)
						testlog.TaggedInfof("Deployment", "Creating Deployment %v", deploymentName)
						dp, err := createDeployment(ctx, deploymentName, podLabel, &targetNode, rl)
						Expect(err).ToNot(HaveOccurred())
						dpList = append(dpList, dp)
					}

					defer func() {
						// delete deployment
						for _, dp := range dpList {
							testlog.TaggedInfof("Deployment", "Deleting Deployment %v", dp.Name)
							key := client.ObjectKeyFromObject(dp)
							err := testclient.Client.Get(ctx, key, dp)
							Expect(err).ToNot(HaveOccurred(), "Unable to fetch podlabels")
							podLabels := dp.Spec.Template.Labels
							err = testclient.DataPlaneClient.Delete(ctx, dp)
							Expect(err).ToNot(HaveOccurred(), "Unable to delete deployment %v", dp.Name)
							waitForDeploymentPodsDeletion(ctx, &targetNode, podLabels)
						}
					}()

					for i := 0; i < 2; i++ {
						podList := &corev1.PodList{}
						podLabel["test-app"] = fmt.Sprintf("telcoApp-%d", i)
						listOptions := &client.ListOptions{Namespace: testutils.NamespaceTesting, LabelSelector: labels.SelectorFromSet(podLabel)}
						testlog.TaggedInfof("Deployment", "waiting for %q to be ready", dpList[i].Name)
						Eventually(func() bool {
							isReady, err := deployments.IsReady(ctx, testclient.Client, listOptions, podList, dpList[i])
							Expect(err).ToNot(HaveOccurred())
							return isReady
						}, time.Minute, time.Second).Should(BeTrue())
						Expect(testclient.Client.List(ctx, podList, listOptions)).To(Succeed())
						testpod := podList.Items[0]
						err = getter.Container(ctx, &testpod, testpod.Spec.Containers[0].Name, cpusetCfg)
						Expect(err).ToNot(HaveOccurred())
						podCpuset, err := cpuset.Parse(cpusetCfg.Cpus)
						testlog.TaggedInfof("Pod", "Cpus used by pod %v are %v", testpod.Name, podCpuset.String())
						Expect(err).ToNot(HaveOccurred(), "Unable to parse cpus from container")
						// fetch ccx to which cpu used by pod is part of
						cpus, err := getCCX(podCpuset.List()[0])
						testlog.TaggedInfof("L3 Cache Group", "cpu id %d used Pod %s is part of CCX group %s", podCpuset.List()[0], testpod.Name, cpus.String())
						cpusetList = append(cpusetList, cpus)
						Expect(err).ToNot(HaveOccurred())
						Expect(podCpuset.IsSubsetOf(cpus))
						// verify retrieved pod cpus matches with our requirements
						Expect(fmt.Sprintf("%d", podCpuset.Size())).To(Equal(podCpuRequirementList[i]))
					}
					// Here pods of both deployments can take the full core even as it will follow
					// packed allocation
					// From KEP: takeUncoreCache and takePartialUncore will still follow a "packed" allocation principle as the rest of the implementation.
					// Uncore Cache IDs will be scanned in numerical order and assigned as possible. For takePartialUncore, CPUs will be assigned
					// within the uncore cache in numerical order to follow a "packed" methodology.
					if mode == L3UncoreCacheShareEqual {
						Expect(cpusetList[0]).To(Equal(cpusetList[1]))
					} else {
						Expect(cpusetList[0]).ToNot(Equal(cpusetList[1]))
					}
				},

				Entry("[test_id:77728] 2 pods where total number of cpus equal to L3CacheGroup size share same L3CacheSize", L3UncoreCacheShareEqual),
				Entry("[test_id:77729] 2 pods with unequal cpurequests do not share same L3 cache size with 1 Pod requesting 1 full L3Cache Group and other requesting less than L3Cache GroupSize", L3UncoreCacheShareUnequal),
			)
		})

		Context("Multiple Containers", func() {
			type alignment string
			const (
				partialAlignment               alignment = "partial"
				fullAlignment                  alignment = "full"
				partialAlignmentWithContainers alignment = "partialWithContainers"
			)

			DescribeTable("Verify CPU Alignment with multiple containers",
				func(deploymentName string, alignmentType alignment, containerName []string) {
					var (
						ctx           = context.Background()
						containerList []corev1.Container
						cpuSize       int
						cpusetCfg     = &controller.CpuSet{}
						targetNode    = workerRTNodes[0]
					)

					podLabel := make(map[string]string)
					podLabel["test-app"] = "telco"
					nodeSelector := make(map[string]string)

					switch alignmentType {
					case partialAlignmentWithContainers:
						// Main guaranteed container requesting L3CacheGroupSize -2 and other sidecar containers
						// requesting 1.5 and 0.5
						cpuSize = L3CacheGroupSize - 2
						cpuResources := []string{"1.5", "0.5"}
						containerList = createMultipleContainers(containerName, cpuResources)
					case fullAlignment:
						// with 2 guaranteed containers requesting half of L3CacheGroupSize
						cpuSize = L3CacheGroupSize / 2
						cpuResources := []string{fmt.Sprintf("%d", cpuSize)}
						containerList = createMultipleContainers(containerName, cpuResources)
					case partialAlignment:
						// With 2 guaranteed containers, where 1 is requesting half of L3CacheGroupSize
						// and another container requesting 2 less than the remaining cpus
						cpuSize = L3CacheGroupSize / 2
						cpuResources := []string{fmt.Sprintf("%d", cpuSize-2)}
						containerList = createMultipleContainers(containerName, cpuResources)
					}
					rl := &corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(strconv.Itoa(cpuSize)),
						corev1.ResourceMemory: resource.MustParse("100Mi"),
					}

					p := makePod(testutils.NamespaceTesting, withRequests(rl), withLimits(rl))
					nodeSelector["kubernetes.io/hostname"] = targetNode.Name
					dp := deployments.Make(deploymentName, testutils.NamespaceTesting,
						deployments.WithPodTemplate(p),
						deployments.WithNodeSelector(nodeSelector))
					dp.Spec.Selector.MatchLabels = podLabel
					dp.Spec.Template.Labels = podLabel
					dp.Spec.Template.Spec.Containers = append(dp.Spec.Template.Spec.Containers, containerList...)
					err = testclient.Client.Create(ctx, dp)
					Expect(err).ToNot(HaveOccurred())
					defer func() {
						testlog.TaggedInfof("Cleanup", "Deleting Deployment %v", deploymentName)
						err := testclient.DataPlaneClient.Delete(ctx, dp)
						Expect(err).ToNot(HaveOccurred())
						waitForDeploymentPodsDeletion(ctx, &targetNode, podLabel)
					}()
					podList := &corev1.PodList{}
					listOptions := &client.ListOptions{Namespace: testutils.NamespaceTesting, LabelSelector: labels.SelectorFromSet(dp.Spec.Selector.MatchLabels)}
					Eventually(func() bool {
						isReady, err := deployments.IsReady(ctx, testclient.Client, listOptions, podList, dp)
						Expect(err).ToNot(HaveOccurred())
						return isReady
					}, time.Minute, time.Second).Should(BeTrue())
					Expect(testclient.Client.List(ctx, podList, listOptions)).To(Succeed())
					Expect(len(podList.Items)).To(Equal(1), "Expected exactly one pod in the list")
					testpod := podList.Items[0]
					err = getter.Container(ctx, &testpod, "test", cpusetCfg)
					Expect(err).ToNot(HaveOccurred())
					containerWithIntegralCpu, err := cpuset.Parse(cpusetCfg.Cpus)
					Expect(err).ToNot(HaveOccurred())
					// fetch ccx to which cpu used by pod is part of
					L3CacheGroupCpus, err := getCCX(containerWithIntegralCpu.List()[0])
					Expect(err).ToNot(HaveOccurred())
					Expect(containerWithIntegralCpu.IsSubsetOf(L3CacheGroupCpus)).To(BeTrue())
					if alignmentType == partialAlignmentWithContainers {
						expectedBurstablePodCpus := totalOnlineCpus.Difference(containerWithIntegralCpu)
						err = getter.Container(ctx, &testpod, "log1", cpusetCfg)
						Expect(err).ToNot(HaveOccurred(), "Unable to fetch container details from log1 pod")
						containerWithnonIntegralCpu1, err := cpuset.Parse(cpusetCfg.Cpus)
						Expect(err).ToNot(HaveOccurred(), "Unable to parse cpus used by container of log1 pod")
						Expect(containerWithnonIntegralCpu1.Equals(expectedBurstablePodCpus)).To(BeTrue())
						err = getter.Container(ctx, &testpod, "log2", cpusetCfg)
						Expect(err).ToNot(HaveOccurred(), "Unable to fetch container details from log2 pod")
						containerWithnonIntegralCpu2, err := cpuset.Parse(cpusetCfg.Cpus)
						Expect(err).ToNot(HaveOccurred(), "Unable to parse cpus used by container of log2 pod")
						Expect(containerWithnonIntegralCpu2.Equals(expectedBurstablePodCpus)).To(BeTrue())
					} else {
						err = getter.Container(ctx, &testpod, "test2", cpusetCfg)
						Expect(err).ToNot(HaveOccurred(), "unable to get container details from test2 pod")
						containerWithIntegralCpu2, err := cpuset.Parse(cpusetCfg.Cpus)
						Expect(err).ToNot(HaveOccurred(), "unable to parse cpus used by container of test2 pod")
						Expect(containerWithIntegralCpu2.IsSubsetOf(L3CacheGroupCpus)).To(BeTrue())
					}

				},
				Entry("[test-id:77730] With different QoS class containers have guaranteed Container pinned to single CCX", "test-deployment1", partialAlignmentWithContainers, []string{"log1", "log2"}),
				Entry("[test-id:81670] With multiple guaranteed containers where total cpus requested is equal to single ccx size", "test-deployment2", fullAlignment, []string{"test2"}),
				Entry("[test-id:81671] With multiple guaranteed containers where total cpus requested is less than single ccx size", "test-deployment3", partialAlignment, []string{"test2"}),
			)
		})
	})

	Context("Functional Tests with SMT Disabled", func() {
		type podCpuResourceSize string
		var (
			L3CacheGroupSize   int
			totalOnlineCpus    cpuset.CPUSet
			nosmt              map[int][]int
			getCCX             func(cpuid int) (cpuset.CPUSet, error)
			reserved, isolated cpuset.CPUSet
			policy             = "single-numa-node"
		)
		const (
			podCpuResourceEqualtoL3CacheSize  podCpuResourceSize = "equalto"
			podCpuResourceLessthanL3CacheSize podCpuResourceSize = "lessthan"
		)
		BeforeAll(func() {
			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			ctx := context.Background()
			for _, cnfnode := range workerRTNodes {
				numaInfo, err := nodes.GetNumaNodes(ctx, &cnfnode)
				Expect(err).ToNot(HaveOccurred(), "Unable to fetch numa nodes")
				coresiblings, err := nodes.GetCoreSiblings(ctx, &cnfnode)
				Expect(err).ToNot(HaveOccurred(), "Unable to get numa information from the node")
				nosmt = transformToNoSMT(coresiblings)
				if len(numaInfo) < 2 {
					Skip(fmt.Sprintf("This test need 2 Numa nodes. The number of numa nodes on node %s < 2", cnfnode.Name))
				}
				Expect(err).ToNot(HaveOccurred())
			}
			node0 := cpuset.New(nosmt[0]...)
			node1 := cpuset.New(nosmt[1]...)
			reserved = cpuset.New(nosmt[0][:4]...)
			node0remaining := node0.Difference(reserved)
			isolated = (node0remaining.Union(node1))
			// Modify the profile such that we give 1 whole ccx to reserved cpus
			By("Modifying the profile")
			reservedSet := performancev2.CPUSet(reserved.String())
			isolatedSet := performancev2.CPUSet(isolated.String())
			testlog.Infof("Modifying profile with reserved cpus %s and isolated cpus %s", reserved.String(), isolated.String())
			profile.Spec.CPU = &performancev2.CPU{
				Reserved: &reservedSet,
				Isolated: &isolatedSet,
			}
			profile.Spec.NUMA = &performancev2.NUMA{
				TopologyPolicy: &policy,
			}

			profile.Spec.AdditionalKernelArgs = []string{"nosmt", "module_blacklist=irdma"}

			By("Updating Performance profile")
			profiles.UpdateWithRetry(profile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(ctx, profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(ctx, profile)

			// we are good till now but not good enough, we need to move all the
			// reserved cpus to first L3CacheGroup to which cpu0 is part of
			for _, cnfnode := range workerRTNodes {
				getCCX = nodes.GetL3SharedCPUs(&cnfnode)
				reserved, err = getCCX(0)
				Expect(err).ToNot(HaveOccurred(), "Unable to fetch cpus used by L3 cache group 0 from node %q", &cnfnode.Name)
				totalOnlineCpus, err = nodes.GetOnlineCPUsSet(ctx, &cnfnode)
				Expect(err).ToNot(HaveOccurred(), "Unable to fetch total online cpus from the %q", &cnfnode.Name)
				L3CacheGroupSize = reserved.Size()
			}
			// Modify the profile such that we give 1 whole ccx to reserved cpus
			By("Modifying the profile")
			isolated = totalOnlineCpus.Difference(reserved)
			reservedSet = performancev2.CPUSet(reserved.String())
			isolatedSet = performancev2.CPUSet(isolated.String())
			testlog.Infof("Modifying profile with reserved cpus %s and isolated cpus %s", reserved.String(), isolated.String())
			profile.Spec.CPU = &performancev2.CPU{
				Reserved: &reservedSet,
				Isolated: &isolatedSet,
			}
			profile.Spec.NUMA = &performancev2.NUMA{
				TopologyPolicy: &policy,
			}
			By("Updating Performance profile")
			profiles.UpdateWithRetry(profile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(ctx, profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(ctx, profile)

			testNS := *namespaces.TestingNamespace
			Expect(testclient.DataPlaneClient.Create(ctx, &testNS)).ToNot(HaveOccurred())
			DeferCleanup(func() {
				Expect(testclient.DataPlaneClient.Delete(ctx, &testNS)).ToNot(HaveOccurred())
				Expect(namespaces.WaitForDeletion(testutils.NamespaceTesting, 5*time.Minute)).ToNot(HaveOccurred())
			})
		})
		DescribeTable("Align Guaranteed pod",
			func(deploymentName string, resourceSize podCpuResourceSize) {
				var (
					ctx = context.Background()
					rl  *corev1.ResourceList
				)
				podLabel := make(map[string]string)
				targetNode := workerRTNodes[0]
				cpusetCfg := &controller.CpuSet{}
				getCCX := nodes.GetL3SharedCPUs(&targetNode)
				switch resourceSize {
				case podCpuResourceEqualtoL3CacheSize:
					rl = &corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(strconv.Itoa(L3CacheGroupSize)),
						corev1.ResourceMemory: resource.MustParse("100Mi"),
					}
				case podCpuResourceLessthanL3CacheSize:
					rl = &corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(strconv.Itoa(L3CacheGroupSize / 2)),
						corev1.ResourceMemory: resource.MustParse("100Mi"),
					}
				}
				podLabel["test-app"] = "telco1"
				dp, err := createDeployment(ctx, deploymentName, podLabel, &targetNode, rl)
				Expect(err).ToNot(HaveOccurred())
				defer func() {
					testlog.TaggedInfof("Cleanup", "Deleting Deployment %v", deploymentName)
					err := testclient.DataPlaneClient.Delete(ctx, dp)
					Expect(err).ToNot(HaveOccurred(), "Unable to delete deployment %v", deploymentName)
					waitForDeploymentPodsDeletion(ctx, &targetNode, podLabel)
				}()

				podList := &corev1.PodList{}
				listOptions := &client.ListOptions{Namespace: testutils.NamespaceTesting, LabelSelector: labels.SelectorFromSet(dp.Spec.Selector.MatchLabels)}
				Eventually(func() bool {
					isReady, err := deployments.IsReady(ctx, testclient.Client, listOptions, podList, dp)
					Expect(err).ToNot(HaveOccurred())
					return isReady
				}, time.Minute, time.Second).Should(BeTrue())
				Expect(testclient.Client.List(ctx, podList, listOptions)).To(Succeed())
				Expect(len(podList.Items)).To(Equal(1), "Expected exactly one pod in the list")
				testpod := podList.Items[0]
				err = getter.Container(ctx, &testpod, testpod.Spec.Containers[0].Name, cpusetCfg)
				Expect(err).ToNot(HaveOccurred())
				testpodCpuset, err := cpuset.Parse(cpusetCfg.Cpus)
				testlog.TaggedInfof("Pod", "CPUs used by %q are: %q", testpod.Name, testpodCpuset.String())
				Expect(err).ToNot(HaveOccurred())
				// fetch ccx to which cpu used by pod is part of
				cpus, err := getCCX(testpodCpuset.List()[0])
				testlog.TaggedInfof("L3 Cache Group", "CPU Group sharing L3 Cache to which %s is alloted are: %s ", testpod.Name, cpus.String())
				Expect(err).ToNot(HaveOccurred())
				// All the cpus alloted to Pod should be equal to the cpus sharing L3 Cache group
				if resourceSize == podCpuResourceEqualtoL3CacheSize {
					Expect(testpodCpuset).To(Equal(cpus))
				} else {
					Expect(testpodCpuset.IsSubsetOf(cpus)).To(BeTrue())
				}
			},
			Entry("[test_id:81672] requesting equal to L3Cache group size cpus", "test-deployment1", podCpuResourceEqualtoL3CacheSize),
			Entry("[test_id:81673] requesting less than L3Cache group size cpus", "test-deployment2", podCpuResourceLessthanL3CacheSize),
		)
	})

	Context("Runtime Tests", func() {
		var (
			targetNodeName    string      // pick a random node to simplify our testing - e.g. to know ahead of time expected L3 group size
			targetNodeInfo    MachineData // shortcut. Note: **SHALLOW COPY**
			targetL3GroupSize int

			testPod *corev1.Pod
		)

		BeforeEach(func() {
			targetNodeName = workerRTNodes[0].Name // pick random node
			var ok bool
			targetNodeInfo, ok = machineDatas[targetNodeName]
			Expect(ok).To(BeTrue(), "unknown machine data for node %q", targetNodeName)

			targetL3GroupSize = expectedL3GroupSize(targetNodeInfo)
			// random number corresponding to the minimum we need. No known supported hardware has groups so little, they are all way bigger
			Expect(targetL3GroupSize).Should(BeNumerically(">", expectedMinL3GroupSize), "L3 Group size too small: %d", targetL3GroupSize)
		})

		// TODO move to DeferCleanup?
		AfterEach(func() {
			if testPod == nil {
				return
			}
			ctx := context.Background()
			testlog.Infof("deleting pod %q", testPod.Name)
			deleteTestPod(ctx, testPod)
		})

		It("[test_id:77727] should align containers which request less than a L3 group size exclusive CPUs", func(ctx context.Context) {
			askingCPUs := expectedMinL3GroupSize

			By(fmt.Sprintf("Creating a test pod asking for %d exclusive CPUs", askingCPUs))
			testPod = makeLLCPod(targetNodeName, askingCPUs)
			Expect(testclient.Client.Create(ctx, testPod)).To(Succeed())

			By("Waiting for the guaranteed pod to be ready")
			testPod, err = pods.WaitForCondition(ctx, client.ObjectKeyFromObject(testPod), corev1.PodReady, corev1.ConditionTrue, 5*time.Minute)
			Expect(err).ToNot(HaveOccurred(), "Guaranteed pod did not become ready in time")

			logs, err := pods.GetLogs(testclient.K8sClient, testPod)
			Expect(err).ToNot(HaveOccurred(), "Cannot get logs from test pod")

			allocatedCPUs, err := cpuset.Parse(logs)
			Expect(err).ToNot(HaveOccurred(), "Cannot get cpuset for pod %s/%s from logs %q", testPod.Namespace, testPod.Name, logs)
			Expect(allocatedCPUs.Size()).To(Equal(askingCPUs), "asked %d exclusive CPUs got %v", askingCPUs, allocatedCPUs)

			ok, _ := isCPUSetLLCAligned(targetNodeInfo.Caches, allocatedCPUs)
			Expect(ok).To(BeTrue(), "pod has not L3-aligned CPUs") // TODO log what?
		})

		It("[test_id:81669] cannot align containers which request more than a L3 group size exclusive CPUs", func(ctx context.Context) {
			askingCPUs := targetL3GroupSize + 2 // TODO: to be really safe we should add SMT level cpus

			By(fmt.Sprintf("Creating a test pod asking for %d exclusive CPUs", askingCPUs))
			testPod = makeLLCPod(targetNodeName, askingCPUs)
			Expect(testclient.Client.Create(ctx, testPod)).To(Succeed())

			By("Waiting for the guaranteed pod to be ready")
			testPod, err = pods.WaitForCondition(ctx, client.ObjectKeyFromObject(testPod), corev1.PodReady, corev1.ConditionTrue, 5*time.Minute)
			Expect(err).ToNot(HaveOccurred(), "Guaranteed pod did not become ready in time")

			logs, err := pods.GetLogs(testclient.K8sClient, testPod)
			Expect(err).ToNot(HaveOccurred(), "Cannot get logs from test pod")

			allocatedCPUs, err := cpuset.Parse(logs)
			Expect(err).ToNot(HaveOccurred(), "Cannot get cpuset for pod %s/%s from logs %q", testPod.Namespace, testPod.Name, logs)
			Expect(allocatedCPUs.Size()).To(Equal(askingCPUs), "asked %d exclusive CPUs got %v", askingCPUs, allocatedCPUs)

			ok, _ := isCPUSetLLCAligned(targetNodeInfo.Caches, allocatedCPUs)
			Expect(ok).To(BeFalse(), "pod exceeds L3 group capacity so it cannot have L3-aligned CPUs") // TODO log what?
		})
	})
})

// create Machine config to create text file required to enable prefer-align-cpus-by-uncorecache policy option
func createMachineConfig(perfProfile *performancev2.PerformanceProfile) (*machineconfigv1.MachineConfig, error) {
	mcName := "openshift-llc-alignment"
	mc := &machineconfigv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: machineconfigv1.GroupVersion.String(),
			Kind:       "MachineConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:   mcName,
			Labels: profilecomponent.GetMachineConfigLabel(perfProfile),
		},
		Spec: machineconfigv1.MachineConfigSpec{},
	}
	ignitionConfig := &igntypes.Config{
		Ignition: igntypes.Ignition{
			Version: defaultIgnitionVersion,
		},
		Storage: igntypes.Storage{
			Files: []igntypes.File{},
		},
	}
	addContent(ignitionConfig, []byte("enabled"), llcEnableFileName, fileMode)
	rawIgnition, err := json.Marshal(ignitionConfig)
	if err != nil {
		return nil, err
	}
	mc.Spec.Config = runtime.RawExtension{Raw: rawIgnition}
	return mc, nil
}

// creates the ignitionConfig file
func addContent(ignitionConfig *igntypes.Config, content []byte, dst string, mode int) {
	contentBase64 := base64.StdEncoding.EncodeToString(content)
	ignitionConfig.Storage.Files = append(ignitionConfig.Storage.Files, igntypes.File{
		Node: igntypes.Node{
			Path: dst,
		},
		FileEmbedded1: igntypes.FileEmbedded1{
			Contents: igntypes.Resource{
				Source: ptr.To(fmt.Sprintf("%s,%s", defaultIgnitionContentSource, contentBase64)),
			},
			Mode: &mode,
		},
	})
}

func makePod(ns string, opts ...func(pod *corev1.Pod)) *corev1.Pod {
	p := pods.GetTestPod()
	p.Namespace = ns
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func withRequests(rl *corev1.ResourceList) func(p *corev1.Pod) {
	return func(p *corev1.Pod) {
		p.Spec.Containers[0].Resources.Requests = *rl
	}
}

func withLimits(rl *corev1.ResourceList) func(p *corev1.Pod) {
	return func(p *corev1.Pod) {
		p.Spec.Containers[0].Resources.Limits = *rl
	}
}

// create a deployment to deploy pods
func createDeployment(ctx context.Context, deploymentName string, podLabel map[string]string, targetNode *corev1.Node, podResources *corev1.ResourceList) (*appsv1.Deployment, error) {
	testNode := make(map[string]string)
	var err error
	testNode["kubernetes.io/hostname"] = targetNode.Name
	p := makePod(testutils.NamespaceTesting, withRequests(podResources), withLimits(podResources))
	dp := deployments.Make(deploymentName, testutils.NamespaceTesting,
		deployments.WithPodTemplate(p),
		deployments.WithNodeSelector(testNode))
	dp.Spec.Selector.MatchLabels = podLabel
	dp.Spec.Template.Labels = podLabel
	err = testclient.Client.Create(ctx, dp)
	return dp, err
}

// Wait for deployment pods to be deleted
func waitForDeploymentPodsDeletion(ctx context.Context, targetNode *corev1.Node, podLabel map[string]string) {
	listOptions := &client.ListOptions{
		Namespace:     testutils.NamespaceTesting,
		FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": targetNode.Name}),
		LabelSelector: labels.SelectorFromSet(labels.Set(podLabel)),
	}
	podList := &corev1.PodList{}
	Eventually(func() bool {
		if err := testclient.DataPlaneClient.List(ctx, podList, listOptions); err != nil {
			return false
		}
		if len(podList.Items) == 0 {
			return false
		}
		return true
	}).WithTimeout(time.Minute * 5).WithPolling(time.Second * 30).Should(BeTrue())
	for _, pod := range podList.Items {
		testlog.TaggedInfof("cleanup", "Deleting pod %s", pod.Name)
		err := pods.WaitForDeletion(ctx, &pod, pods.DefaultDeletionTimeout*time.Second)
		Expect(err).ToNot(HaveOccurred())
	}
}

func createMultipleContainers(names []string, cpuResources []string) []corev1.Container {
	var containers []corev1.Container
	for i, name := range names {
		cpu := cpuResources[i]
		container := corev1.Container{
			Name:    name,
			Image:   images.Test(),
			Command: []string{"sleep", "10h"},
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(cpu),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			},
		}
		containers = append(containers, container)
	}
	return containers
}

// transformToNoSMT takes a map which contains cores and its siblings
// per numa node to move it to a map containing cpus per numa node
func transformToNoSMT(input map[int]map[int][]int) map[int][]int {
	result := make(map[int][]int)
	for key, innerMap := range input {
		for _, values := range innerMap {
			result[key] = append(result[key], values[0])
		}
		sort.Ints(result[key])
	}
	return result
}

func MachineFromJSON(data string) (Machine, error) {
	ma := Machine{}
	rd := strings.NewReader(data)
	err := json.NewDecoder(rd).Decode(&ma)
	return ma, err
}

func isCPUSetLLCAligned(infos []CacheInfo, cpus cpuset.CPUSet) (bool, *CacheInfo) {
	for idx := range infos {
		info := &infos[idx]
		if cpus.IsSubsetOf(info.CPUs) {
			return true, info
		}
	}
	return false, nil
}

func computeLLCLayout(mi Machine) []CacheInfo {
	ret := []CacheInfo{}
	for _, node := range mi.Topology.Nodes {
		for _, cache := range node.Caches {
			if cache.Level < 3 { // TODO
				continue
			}
			ret = append(ret, CacheInfo{
				NodeID: node.ID,
				Level:  int(cache.Level),
				CPUs:   cpusetFromLogicalProcessors(cache.LogicalProcessors...),
			})
		}
	}
	return ret
}

func cpusetFromLogicalProcessors(procs ...uint32) cpuset.CPUSet {
	cpuList := make([]int, 0, len(procs))
	for _, proc := range procs {
		cpuList = append(cpuList, int(proc))
	}
	return cpuset.New(cpuList...)
}

func expectedL3GroupSize(md MachineData) int {
	// TODO: we assume all L3 Groups are equal in size.
	for idx := range md.Caches {
		cache := &md.Caches[idx]
		if cache.Level != 3 {
			continue
		}
		return cache.CPUs.Size()
	}
	return 0
}

func collectMachineDatas(ctx context.Context, nodeList []corev1.Node) (map[string]MachineData, bool, error) {
	cmd := []string{"/usr/bin/machineinfo"}
	infos := make(map[string]MachineData)
	for idx := range nodeList {
		node := &nodeList[idx]
		out, err := nodes.ExecCommand(ctx, node, cmd)
		if err != nil {
			return infos, false, err
		}

		info, err := MachineFromJSON(string(out))
		if err != nil {
			return infos, true, err
		}

		infos[node.Name] = MachineData{
			Info:   info,
			Caches: computeLLCLayout(info), // precompute
		}
	}
	return infos, true, nil
}

func makeLLCPod(nodeName string, guaranteedCPUs int) *corev1.Pod {
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-",
			Labels: map[string]string{
				"test": "",
			},
			Namespace: testutils.NamespaceTesting,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test",
					Image: images.Test(),
					Command: []string{
						"/bin/sh", "-c", "cat /sys/fs/cgroup/cpuset.cpus.effective && sleep 10h",
					},
				},
			},
			NodeName: nodeName,
			NodeSelector: map[string]string{
				testutils.LabelHostname: nodeName,
			},
		},
	}
	if guaranteedCPUs > 0 {
		testPod.Spec.Containers[0].Resources = corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewQuantity(int64(guaranteedCPUs), resource.DecimalSI),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
		}
	}
	profile, _ := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
	runtimeClass := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
	testPod.Spec.RuntimeClassName = &runtimeClass
	return testPod
}

func deleteTestPod(ctx context.Context, testpod *corev1.Pod) bool {
	GinkgoHelper()

	// it possible that the pod already was deleted as part of the test, in this case we want to skip teardown
	err := testclient.DataPlaneClient.Get(ctx, client.ObjectKeyFromObject(testpod), testpod)
	if apierrors.IsNotFound(err) {
		return false
	}

	err = testclient.DataPlaneClient.Delete(ctx, testpod)
	Expect(err).ToNot(HaveOccurred())

	err = pods.WaitForDeletion(ctx, testpod, pods.DefaultDeletionTimeout*time.Second)
	Expect(err).ToNot(HaveOccurred())

	return true
}
