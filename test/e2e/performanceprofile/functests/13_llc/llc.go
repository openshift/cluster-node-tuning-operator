package __llc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/jaypipes/ghw/pkg/cpu"
	"github.com/jaypipes/ghw/pkg/topology"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/cpuset"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	profilecomponent "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"

	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/images"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
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

var _ = Describe("[rfe_id:77446] LLC-aware cpu pinning", Label(string(label.OpenShift)), Ordered, func() {
	var (
		workerRTNodes      []corev1.Node
		machineDatas       map[string]MachineData            // nodeName -> MachineData
		perfProfile        *performancev2.PerformanceProfile // original perf profile
		performanceMCP     string
		err                error
		profileAnnotations map[string]string
		poolName           string
		llcPolicy          string
		mc                 *machineconfigv1.MachineConfig
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

		By(fmt.Sprintf("collecting machine infos for %d nodes", len(workerRTNodes)))
		machineDatas, hasMachineData, err = collectMachineDatas(ctx, workerRTNodes)
		if !hasMachineData {
			Skip("need machinedata available - please check the image for the presence of the machineinfo tool")
		}
		Expect(err).ToNot(HaveOccurred())

		for node, data := range machineDatas {
			testlog.Infof("node=%q data=%v", node, data.Caches)
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

	AfterAll(func() {
		if perfProfile == nil {
			return //nothing to do!
		}

		// Delete machine config created to enable uncocre cache cpumanager policy option
		// first make sure the profile doesn't have the annotation
		ctx := context.Background()
		prof := perfProfile.DeepCopy()
		prof.Annotations = nil
		By("updating performance profile")
		profiles.UpdateWithRetry(prof)

		By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
		profilesupdate.WaitForTuningUpdating(ctx, prof)

		By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
		profilesupdate.WaitForTuningUpdated(ctx, prof)

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

		It("should align containers which request less than a L3 group size exclusive CPUs", func(ctx context.Context) {
			askingCPUs := expectedMinL3GroupSize

			By(fmt.Sprintf("Creating a test pod asking for %d exclusive CPUs", askingCPUs))
			testPod = makePod(targetNodeName, askingCPUs)
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

		It("cannot align containers which request more than a L3 group size exclusive CPUs", func(ctx context.Context) {
			askingCPUs := targetL3GroupSize + 2 // TODO: to be really safe we should add SMT level cpus

			By(fmt.Sprintf("Creating a test pod asking for %d exclusive CPUs", askingCPUs))
			testPod = makePod(targetNodeName, askingCPUs)
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

func makePod(nodeName string, guaranteedCPUs int) *corev1.Pod {
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
