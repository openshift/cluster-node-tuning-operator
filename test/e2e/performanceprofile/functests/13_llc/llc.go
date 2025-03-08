package __llc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/utils/cpuset"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	profilecomponent "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"

	//"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup/controller"
	//"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/images"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/deployments"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/namespaces"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/poolname"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profilesupdate"
)

const (
	llcEnableFileName            = "/etc/kubernetes/openshift-llc-alignment"
	defaultIgnitionContentSource = "data:text/plain;charset=utf-8;base64"
	defaultIgnitionVersion       = "3.2.0"
	fileMode                     = 0420
)

var _ = Describe("[rfe_id:77446] LLC-aware cpu pinning", Label(string(label.OpenShift)), Ordered, func() {
	var (
		workerRTNodes      []corev1.Node
		perfProfile        *performancev2.PerformanceProfile
		performanceMCP     string
		err                error
		profileAnnotations map[string]string
		poolName           string
		//llcPolicy          string
		//mc                 *machineconfigv1.MachineConfig
		getter                   cgroup.ControllersGetter
		cgroupV2                 bool
	)

	BeforeAll(func() {
		profileAnnotations = make(map[string]string)
		ctx := context.Background()

		workerRTNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("error looking for the optional selector: %v", err))

		perfProfile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

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

		/*mc, err = createMachineConfig(perfProfile)
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
			perfProfile.Annotations = profileAnnotations

			By("updating performance profile")
			profiles.UpdateWithRetry(perfProfile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(ctx, perfProfile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(ctx, perfProfile)
		}*/

	})

	AfterAll(func() {

		// Delete machine config created to enable uncocre cache cpumanager policy option
		// first make sure the profile doesn't have the annotation
		fmt.Println("We are in afterall and nothing is deleted")
		/*ctx := context.Background()
		perfProfile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		perfProfile.Annotations = nil
		By("updating performance profile")
		profiles.UpdateWithRetry(perfProfile)

		By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
		profilesupdate.WaitForTuningUpdating(ctx, perfProfile)

		By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
		profilesupdate.WaitForTuningUpdated(ctx, perfProfile)

		// delete the machine config pool
		Expect(testclient.Client.Delete(ctx, mc)).To(Succeed())

		mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
		By("Waiting when mcp finishes updates")

		mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)*/
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

				for _, node := range workerRTNodes {
					kubeletconfig, err := nodes.GetKubeletConfig(ctx, &node)
					Expect(err).ToNot(HaveOccurred())
					Expect(kubeletconfig.CPUManagerPolicyOptions).ToNot(HaveKey("prefer-align-cpus-by-uncorecache"))
				}
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
	Context("Functional Tests", func() {
		BeforeAll(func() {
			var cpuGroupSize int
			//var numaCoreSiblings map[int]map[int][]int
			//var reserved, isolated []string
			//var policy = "single-numa-node"

			//profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			//Expect(err).ToNot(HaveOccurred())
			ctx := context.Background()
			for _, cnfnode := range workerRTNodes {
				numaInfo, err := nodes.GetNumaNodes(context.TODO(), &cnfnode)
				Expect(err).ToNot(HaveOccurred(), "Unable to get numa information from the node")
				if len(numaInfo) < 2 {
					Skip(fmt.Sprintf("This test need 2 Numa nodes. The number of numa nodes on node %s < 2", cnfnode.Name))
				}

				getCCX := nodes.GetL3SharedCPUs(&cnfnode)
				cpus, err := getCCX(0)
				Expect(err).ToNot(HaveOccurred())
				cpuGroupSize = cpus.Size()
				if len(numaInfo[0]) == cpuGroupSize {
					Skip("This test requires systems where L3 cache is shared amount subset of cpus")
				}
			}
			/*
			// Modify the profile such that we give 1 whole ccx to reserved cpus
			By("Modifying the profile")
			for _, node := range workerRTNodes {
				numaCoreSiblings, err = nodes.GetCoreSiblings(context.TODO(), &node)
				Expect(err).ToNot(HaveOccurred())
			}
			// Get cpu siblings from core 0-7
			// Reason: The default profile that is applied generally take 4 cpus and those 4 cpus
			// are taken without taking in to account the cpu topology.
			// By reserving the first 8 core siblings to Reserved cpus, we will not have to worry
			// about polluting cacheId used for Reserved cpus
			for reservedCores := 0; reservedCores < 8; reservedCores++ {
				cpusiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaCoreSiblings, reservedCores)
				reserved = append(reserved, cpusiblings...)
			}
			reservedCpus := strings.Join(reserved, ",")
			for key := range numaCoreSiblings {
				for k := range numaCoreSiblings[key] {
					cpusiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaCoreSiblings, k)
					isolated = append(isolated, cpusiblings...)
				}
			}
			isolatedCpus := strings.Join(isolated, ",")
			reservedSet := performancev2.CPUSet(reservedCpus)
			isolatedSet := performancev2.CPUSet(isolatedCpus)
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
			profilesupdate.WaitForTuningUpdated(ctx, profile)*/

			testNS := *namespaces.TestingNamespace
			Expect(testclient.DataPlaneClient.Create(ctx, &testNS)).ToNot(HaveOccurred())
			DeferCleanup(func() {
				Expect(testclient.DataPlaneClient.Delete(ctx, &testNS)).ToNot(HaveOccurred())
				Expect(namespaces.WaitForDeletion(testutils.NamespaceTesting, 5*time.Minute)).ToNot(HaveOccurred())
			})
		})

		It("[test_id:77725] Align Guaranteed pod requesting 16 cpus to the whole CCX if available", Label("llc1"), func() {
			ctx := context.Background()
			podLabel := make(map[string]string)
			targetNode := workerRTNodes[0]
			cpusetCfg := &controller.CpuSet{}
			deploymentName := "test-deployment"
			rl := &corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("16"),
				corev1.ResourceMemory: resource.MustParse("100Mi"),
			}
			podLabel["type"] = "telco"
			dp, err := createDeployment(ctx, deploymentName, podLabel, &targetNode, rl)
			Expect(err).ToNot(HaveOccurred())
			defer func() {
				// delete deployment
				testlog.Infof("Deleting Deployment %v", deploymentName)
				err := testclient.DataPlaneClient.Delete(ctx, dp)
				Expect(err).ToNot(HaveOccurred())
			}()
			podList := &corev1.PodList{}
			listOptions := &client.ListOptions{Namespace: testutils.NamespaceTesting, LabelSelector: labels.SelectorFromSet(dp.Spec.Selector.MatchLabels)}
			Eventually(func() bool {
				isReady, err := deployments.IsReady(ctx, testclient.Client, listOptions, podList, dp)
				Expect(err).ToNot(HaveOccurred())
				return isReady
			},  time.Minute, time.Second).Should(BeTrue())
			Expect(testclient.Client.List(ctx, podList, listOptions)).To(Succeed())
			Expect(len(podList.Items)).To(Equal(1), "Expected exactly one pod in the list")
			testpod := podList.Items[0]
			err = getter.Container(ctx, &testpod, testpod.Spec.Containers[0].Name, cpusetCfg)
			Expect(err).ToNot(HaveOccurred())
			cgroupCpuset, err := cpuset.Parse(cpusetCfg.Cpus)
			Expect(err).ToNot(HaveOccurred())
			getCCX := nodes.GetL3SharedCPUs(&targetNode)
			// fetch ccx to which cpu used by pod is part of
			cpus, err := getCCX(cgroupCpuset.List()[0])
			Expect(err).ToNot(HaveOccurred())
			Expect(cgroupCpuset).To(Equal(cpus))
		})

		It("[test_id:77725] Verify guaranteed pod consumes the whole Uncore group after reboot", Label("llc2"), func() {
			ctx := context.Background()
			podLabel := make(map[string]string)
			targetNode := workerRTNodes[0]
			cpusetCfg := &controller.CpuSet{}
			deploymentName := "test-deployment"
			rl := &corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("16"),
				corev1.ResourceMemory: resource.MustParse("100Mi"),
			}
			podLabel["type"] = "telco"
			dp, err := createDeployment(ctx, deploymentName, podLabel, &targetNode, rl)
			Expect(err).ToNot(HaveOccurred())
			testlog.TaggedInfof("Deployment", "Deployment %s created", deploymentName)
			defer func() {
				// delete deployment
				testlog.Infof("Deleting Deployment %v", deploymentName)
				err := testclient.DataPlaneClient.Delete(ctx, dp)
				Expect(err).ToNot(HaveOccurred())
			}()
			podList := &corev1.PodList{}
			listOptions := &client.ListOptions{Namespace: testutils.NamespaceTesting, LabelSelector: labels.SelectorFromSet(dp.Spec.Selector.MatchLabels)}
			Eventually(func() bool {
				isReady, err := deployments.IsReady(ctx, testclient.Client, listOptions, podList, dp)
				Expect(err).ToNot(HaveOccurred())
				return isReady
			},  time.Minute, time.Second).Should(BeTrue())
			Expect(testclient.Client.List(ctx, podList, listOptions)).To(Succeed())
			Expect(len(podList.Items)).To(Equal(1), "Expected exactly one pod in the list")
			testpod := podList.Items[0]
			err = getter.Container(ctx, &testpod, testpod.Spec.Containers[0].Name, cpusetCfg)
			Expect(err).ToNot(HaveOccurred())
			cgroupCpuset, err := cpuset.Parse(cpusetCfg.Cpus)
			Expect(err).ToNot(HaveOccurred())
			testlog.TaggedInfof("Pod","pod %s using cpus %q", testpod.Name, cgroupCpuset.String())
			getCCX := nodes.GetL3SharedCPUs(&targetNode)
			// fetch ccx to which cpu used by pod is part of
			cpus, err := getCCX(cgroupCpuset.List()[0])
			testlog.TaggedInfof("L3 Cache Group", "L3 Cache group associated with Pod %s using cpu %d is %q: ", testpod.Name, cgroupCpuset.List()[0], cpus)
			Expect(err).ToNot(HaveOccurred())
			Expect(cgroupCpuset).To(Equal(cpus))
			// reboot the node
			rebootCmd := "chroot /rootfs systemctl reboot"
			testlog.TaggedInfof("Reboot", "Node %q: Rebooting", targetNode.Name)

			out, err := nodes.ExecCommand(ctx, &targetNode, []string{"sh", "-c", rebootCmd})
			testlog.Infof("Node Rebooted: %s", string(out))

			By("Rebooted node manually and waiting for mcp to get Updating status")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

			By("Waiting for mcp to be updated")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

			// After reboot check the deployment
			desiredStatus := appsv1.DeploymentStatus{
				Replicas: 1,
				AvailableReplicas: 1,
			}
			err = deployments.WaitForCondition(ctx, dp, testclient.Client, testutils.NamespaceTesting, dp.Name, desiredStatus)
			Expect(err).ToNot(HaveOccurred())
			testlog.TaggedInfof("Deployment", "Deployment %q is running after reboot", dp.Name)

			listOptions = &client.ListOptions{Namespace: testutils.NamespaceTesting, LabelSelector: labels.SelectorFromSet(dp.Spec.Selector.MatchLabels)}
			Eventually(func() bool {
				isReady, err := deployments.IsReady(ctx, testclient.Client, listOptions, podList, dp)
				Expect(err).ToNot(HaveOccurred())
				return isReady
			},  time.Minute, time.Second).Should(BeTrue())
			Expect(testclient.Client.List(ctx, podList, listOptions)).To(Succeed())
			Expect(len(podList.Items)).To(Equal(1), "Expected exactly one pod in the list")
			testpod = podList.Items[0]

			err = getter.Container(ctx, &testpod, testpod.Spec.Containers[0].Name, cpusetCfg)
			Expect(err).ToNot(HaveOccurred())
			cgroupCpuset, err = cpuset.Parse(cpusetCfg.Cpus)
			testlog.TaggedInfof("Pod","pod %s using cpus %q", testpod.Name, cgroupCpuset.String())
			Expect(err).ToNot(HaveOccurred())
			getCCX = nodes.GetL3SharedCPUs(&targetNode)
			// fetch ccx to which cpu used by pod is part of
			cpus, err = getCCX(cgroupCpuset.List()[0])
			Expect(err).ToNot(HaveOccurred())
			Expect(cgroupCpuset).To(Equal(cpus))
			testlog.TaggedInfof("L3 Cache Group", "L3 Cache group associated with Pod %s using cpu %d is %q: ", testpod.Name, cgroupCpuset.List()[0], cpus)

		})

		It("[test_id:77726] Multiple Pods are not sharing same L3 cache", Label("llc3"), func() {
			ctx := context.Background()
			targetNode := workerRTNodes[0]
			// create 2 deployments creating 2 gu pods asking for 8 cpus each
			cpusetCfg := &controller.CpuSet{}
			var cpusetList []cpuset.CPUSet
			var dpList []*appsv1.Deployment
			podLabel := make(map[string]string)
			for i :=0; i < 2; i++ {
				deploymentName := fmt.Sprintf("test-deployment%d", i)
				rl := &corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("8"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				}
				podLabel := make(map[string]string)
				podLabel["type"] = fmt.Sprintf("telco%d", i)
				testlog.TaggedInfof("Deployment", "Creating Deployment %v", deploymentName)
				dp, err := createDeployment(ctx, deploymentName, podLabel, &targetNode, rl)
				Expect(err).ToNot(HaveOccurred())
				dpList = append(dpList, dp)
			}
			defer func() {
				// delete deployment
				for _, dp := range dpList {
					testlog.TaggedInfof("Deployment", "Deleting Deployment %v", dp.Name)
					err := testclient.DataPlaneClient.Delete(ctx, dp)
					Expect(err).ToNot(HaveOccurred())
				}
			}()
			for i:=0; i < 2; i++ {
				podList := &corev1.PodList{}
				podLabel["type"] = fmt.Sprintf("telco%d", i)
				listOptions := &client.ListOptions{Namespace: testutils.NamespaceTesting, LabelSelector: labels.SelectorFromSet(podLabel)}
				Eventually(func() bool {
					isReady, err := deployments.IsReady(ctx, testclient.Client, listOptions, podList, dpList[i])
					Expect(err).ToNot(HaveOccurred())
					return isReady
				},  time.Minute, time.Second).Should(BeTrue())
				Expect(testclient.Client.List(ctx, podList, listOptions)).To(Succeed())
				testpod := podList.Items[0]
				err = getter.Container(ctx, &testpod, testpod.Spec.Containers[0].Name, cpusetCfg)
				Expect(err).ToNot(HaveOccurred())
				podCpuset, err := cpuset.Parse(cpusetCfg.Cpus)
				testlog.TaggedInfof("Pod","Cpus used by pod %v are %v", testpod.Name, podCpuset.String())
				Expect(err).ToNot(HaveOccurred())
				getCCX := nodes.GetL3SharedCPUs(&targetNode)
				// fetch ccx to which cpu used by pod is part of
				cpus, err := getCCX(podCpuset.List()[0])
				testlog.TaggedInfof("L3 Cache Group", "cpu id %d used Pod %s is part of CCX group %s", podCpuset.List()[0], testpod.Name,  cpus.String())
				cpusetList = append(cpusetList, cpus)
				Expect(err).ToNot(HaveOccurred())
				Expect(podCpuset.IsSubsetOf(cpus))
				cpusetList = append(cpusetList, cpus)
			}
			// Here pods of both deployments can take the full core even as it will follow
			// packed allocation
			// From KEP: takeUncoreCache and takePartialUncore will still follow a "packed" allocation principle as the rest of the implementation.
			// Uncore Cache IDs will be scanned in numerical order and assigned as possible. For takePartialUncore, CPUs will be assigned
			// within the uncore cache in numerical order to follow a "packed" methodology.
			Expect(cpusetList[0]).To(Equal(cpusetList[1]))
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
	p := makePod(testutils.NamespaceTesting, withRequests(podResources),withLimits(podResources))
	dp := deployments.Make(deploymentName, testutils.NamespaceTesting,
		deployments.WithPodTemplate(p),
		deployments.WithNodeSelector(testNode))
	dp.Spec.Selector.MatchLabels = podLabel
	dp.Spec.Template.ObjectMeta.Labels= podLabel
	err = testclient.Client.Create(ctx, dp)
	return dp, err
}

