package __performance_update

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
	"k8s.io/utils/cpuset"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	profilecomponent "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	manifestsutil "github.com/openshift/cluster-node-tuning-operator/pkg/util"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup/runtime"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/hypershift"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodepools"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/poolname"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profilesupdate"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/tuned"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

type checkFunction func(context.Context, *corev1.Node) (string, error)

var _ = Describe("[rfe_id:28761][performance] Updating parameters in performance profile", func() {
	var workerRTNodes []corev1.Node
	var profile, initialProfile *performancev2.PerformanceProfile
	var poolName string
	var np *hypershiftv1beta1.NodePool
	var err error

	chkCmdLine := []string{"cat", "/proc/cmdline"}
	chkKubeletConfig := []string{"cat", "/rootfs/etc/kubernetes/kubelet.conf"}

	chkCmdLineFn := func(ctx context.Context, node *corev1.Node) (string, error) {
		out, err := nodes.ExecCommand(ctx, node, chkCmdLine)
		if err != nil {
			return "", err
		}
		output := testutils.ToString(out)
		return output, nil
	}
	chkKubeletConfigFn := func(ctx context.Context, node *corev1.Node) (string, error) {
		out, err := nodes.ExecCommand(ctx, node, chkKubeletConfig)
		if err != nil {
			return "", err
		}
		output := testutils.ToString(out)
		return output, nil
	}

	chkHugepages2MFn := func(ctx context.Context, node *corev1.Node) (string, error) {
		count, err := countHugepagesOnNode(ctx, node, 2)
		if err != nil {
			return "", err
		}
		return strconv.Itoa(count), nil
	}

	chkHugepages1GFn := func(ctx context.Context, node *corev1.Node) (string, error) {
		count, err := countHugepagesOnNode(ctx, node, 1024)
		if err != nil {
			return "", err
		}
		return strconv.Itoa(count), nil
	}

	nodeLabel := testutils.NodeSelectorLabels

	BeforeEach(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}

		workerRTNodes = getUpdatedNodes()
		profile, err = profiles.GetByNodeLabels(nodeLabel)
		Expect(err).ToNot(HaveOccurred())
		klog.Infof("using profile: %q", profile.Name)
		poolName = poolname.GetByProfile(context.TODO(), profile)
		profilesupdate.WaitForTuningUpdated(context.TODO(), profile)
		if !hypershift.IsHypershiftCluster() {
			klog.Infof("using performanceMCP: %q", poolName)
			// Verify that worker have updated state equals to true
			// the performanceMCP already checked as part of profilesupdate.WaitForTuningUpdated()
			mcps.WaitForCondition(testutils.RoleWorker, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
		}
	})

	Context("Verify hugepages count split on two NUMA nodes", Ordered, Label(string(label.Tier2)), func() {
		hpSize2M := performancev2.HugePageSize("2M")
		skipTests := false

		testutils.CustomBeforeAll(func() {
			for _, node := range workerRTNodes {
				numaInfo, err := nodes.GetNumaNodes(context.TODO(), &node)
				Expect(err).ToNot(HaveOccurred())
				if len(numaInfo) < 2 {
					skipTests = true
					klog.Infof("This test need 2 NUMA nodes. The number of NUMA nodes on node %s < 2", node.Name)
					return
				}
			}
			initialProfile = profile.DeepCopy()
		})

		DescribeTable("Verify that profile parameters were updated", func(hpCntOnNuma0 int32, hpCntOnNuma1 int32) {
			if skipTests {
				Skip("Insufficient NUMA nodes. This test needs 2 NUMA nodes for all CNF enabled test nodes.")
			}

			By("Verifying cluster configuration matches the requirement")
			//have total of 4 cpus so VMs can handle running the configuration
			numaInfo, _ := nodes.GetNumaNodes(context.TODO(), &workerRTNodes[0])
			cpuSlice := numaInfo[0][0:4]
			isolated := performancev2.CPUSet(fmt.Sprintf("%d-%d", cpuSlice[2], cpuSlice[3]))
			reserved := performancev2.CPUSet(fmt.Sprintf("%d-%d", cpuSlice[0], cpuSlice[1]))

			By("Modifying profile")
			profile.Spec.CPU = &performancev2.CPU{
				BalanceIsolated: ptr.To(false),
				Reserved:        &reserved,
				Isolated:        &isolated,
			}
			profile.Spec.HugePages = &performancev2.HugePages{
				DefaultHugePagesSize: &hpSize2M,
				Pages: []performancev2.HugePage{
					{
						Count: hpCntOnNuma0,
						Size:  hpSize2M,
						Node:  ptr.To(int32(0)),
					},
					{
						Count: hpCntOnNuma1,
						Size:  hpSize2M,
						Node:  ptr.To(int32(1)),
					},
				},
			}
			profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
				Enabled: ptr.To(true),
			}

			By("Updating the performance profile")
			profiles.UpdateWithRetry(profile)
			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(context.TODO(), profile)

			for _, node := range workerRTNodes {
				for i := 0; i < 2; i++ {
					nodeCmd := []string{"cat", hugepagesPathForNode(i, 2)}
					out, err := nodes.ExecCommand(context.TODO(), &node, nodeCmd)
					Expect(err).ToNot(HaveOccurred())
					result := testutils.ToString(out)

					t, err := strconv.Atoi(result)
					Expect(err).ToNot(HaveOccurred())

					if i == 0 {
						Expect(int32(t)).To(Equal(hpCntOnNuma0))
					} else {
						Expect(int32(t)).To(Equal(hpCntOnNuma1))
					}
				}
			}
		},
			Entry("[test_id:45023] verify uneven split of hugepages between 2 numa nodes", int32(2), int32(1)),
			Entry("[test_id:45024] verify even split between 2 numa nodes", int32(1), int32(1)),
		)

		AfterAll(func() {
			if skipTests {
				return
			}
			By("Updating the performance profile")
			profiles.UpdateWithRetry(initialProfile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(context.TODO(), profile)
		})
	})

	Context("Verify that all performance profile parameters can be updated", Ordered, Label(string(label.Tier2)), func() {
		var removedKernelArgs string

		hpSize2M := performancev2.HugePageSize("2M")
		hpSize1G := performancev2.HugePageSize("1G")
		isolated := performancev2.CPUSet("1-2")
		reserved := performancev2.CPUSet("0,3")
		policy := "best-effort"

		// Modify profile and verify that MCO successfully updated the node
		testutils.CustomBeforeAll(func() {
			By(fmt.Sprintf("Modifying profile to nodes=%#v MCPs=%#v", profile.Spec.NodeSelector, profile.Spec.MachineConfigPoolSelector))
			initialProfile = profile.DeepCopy()

			profile.Spec.HugePages = &performancev2.HugePages{
				DefaultHugePagesSize: &hpSize2M,
				Pages: []performancev2.HugePage{
					{
						Count: 256,
						Size:  hpSize2M,
					},
					{
						Count: 3,
						Size:  hpSize1G,
					},
				},
			}
			profile.Spec.CPU = &performancev2.CPU{
				BalanceIsolated: ptr.To(false),
				Reserved:        &reserved,
				Isolated:        &isolated,
			}
			profile.Spec.NUMA = &performancev2.NUMA{
				TopologyPolicy: &policy,
			}
			profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
				Enabled: ptr.To(false),
			}

			if profile.Spec.AdditionalKernelArgs == nil {
				By("AdditionalKernelArgs is empty. Checking only adding new arguments")
				profile.Spec.AdditionalKernelArgs = append(profile.Spec.AdditionalKernelArgs, "new-argument=test")
			} else {
				removedKernelArgs = profile.Spec.AdditionalKernelArgs[0]
				profile.Spec.AdditionalKernelArgs = append(profile.Spec.AdditionalKernelArgs[1:], "new-argument=test")
			}

			By("Updating the performance profile")
			profiles.UpdateWithRetry(profile)
			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(context.TODO(), profile)
		})

		DescribeTable("Verify that profile parameters were updated", func(ctx context.Context, cmdFn checkFunction, parameter []string, shouldContain bool, useRegex bool) {
			for _, node := range workerRTNodes {
				for _, param := range parameter {
					result, err := cmdFn(ctx, &node)
					Expect(err).ToNot(HaveOccurred())
					matcher := ContainSubstring(param)
					if useRegex {
						matcher = MatchRegexp(param)
					}

					if shouldContain {
						Expect(result).To(matcher)
					} else {
						Expect(result).NotTo(matcher)
					}
				}
			}
		},
			Entry("[test_id:34081] verify that hugepages size and count updated", context.TODO(), chkCmdLineFn, []string{"default_hugepagesz=2M", "hugepagesz=1G", "hugepages=3"}, true, false),
			Entry("[test_id:28070] verify that hugepages updated (NUMA node unspecified)", context.TODO(), chkCmdLineFn, []string{"hugepagesz=2M"}, true, false),
			Entry("verify that the right number of hugepages 1G is available on the system", context.TODO(), chkHugepages1GFn, []string{"3"}, true, false),
			Entry("verify that the right number of hugepages 2M is available on the system", context.TODO(), chkHugepages2MFn, []string{"256"}, true, false),
			Entry("[test_id:28025] verify that cpu affinity mask was updated", context.TODO(), chkCmdLineFn, []string{"tuned.non_isolcpus=.*9"}, true, true),
			Entry("[test_id:28071] verify that cpu balancer disabled", context.TODO(), chkCmdLineFn, []string{"isolcpus=domain,managed_irq,1-2"}, true, false),
			Entry("[test_id:28071] verify that cpu balancer disabled", context.TODO(), chkCmdLineFn, []string{"systemd.cpu_affinity=0,3"}, true, false),
		)

		DescribeTable("Verify that kubelet parameters were updated", func(ctx context.Context, cmdFn checkFunction, getterFn func(kubeletCfg *kubeletconfigv1beta1.KubeletConfiguration) string, wantedValue string) {
			for _, node := range workerRTNodes {
				result, err := cmdFn(ctx, &node)
				Expect(err).ToNot(HaveOccurred())
				obj, err := manifestsutil.DeserializeObjectFromData([]byte(result), kubeletconfigv1beta1.AddToScheme)
				Expect(err).ToNot(HaveOccurred())

				kc, ok := obj.(*kubeletconfigv1beta1.KubeletConfiguration)
				Expect(ok).To(BeTrue(), "wrong type %T", obj)
				Expect(getterFn(kc)).To(Equal(wantedValue))
			}
		},
			Entry("[test_id:28935] verify that reservedSystemCPUs was updated", context.TODO(), chkKubeletConfigFn, func(k *kubeletconfigv1beta1.KubeletConfiguration) string { return k.ReservedSystemCPUs }, "0,3"),
			Entry("[test_id:28760] verify that topologyManager was updated", context.TODO(), chkKubeletConfigFn, func(k *kubeletconfigv1beta1.KubeletConfiguration) string { return k.TopologyManagerPolicy }, "best-effort"),
		)

		It("[test_id:27738] should succeed to disable the RT kernel", func() {
			for _, node := range workerRTNodes {
				err := nodes.HasPreemptRTKernel(context.TODO(), &node)
				Expect(err).To(HaveOccurred())
			}
		})

		It("[test_id:28612]Verify that Kernel arguments can me updated (added, removed) thru performance profile", func() {
			for _, node := range workerRTNodes {
				out, err := nodes.ExecCommand(context.TODO(), &node, chkCmdLine)
				Expect(err).ToNot(HaveOccurred(), "failed to execute %s", chkCmdLine)
				cmdline := testutils.ToString(out)

				// Verifying that new argument was added
				Expect(cmdline).To(ContainSubstring("new-argument=test"))

				// Verifying that one of old arguments was removed
				if removedKernelArgs != "" {
					Expect(cmdline).NotTo(ContainSubstring(removedKernelArgs), "%s should be removed from /proc/cmdline", removedKernelArgs)
				}
			}
		})

		It("[test_id:22764] verify that by default RT kernel is disabled", func() {
			conditionUpdating := machineconfigv1.MachineConfigPoolUpdating
			if profile.Spec.RealTimeKernel == nil || *profile.Spec.RealTimeKernel.Enabled {
				Skip("Skipping test - This test expects RT Kernel to be disabled. Found it to be enabled or nil.")
			}
			profile.Spec.RealTimeKernel = nil
			By("Applying changes in performance profile")
			profiles.UpdateWithRetry(profile)
			Expect(profile.Spec.RealTimeKernel).To(BeNil(), "real time kernel setting expected in profile spec but missing")

			if hypershift.IsHypershiftCluster() {
				hostedClusterName, err := hypershift.GetHostedClusterName()
				Expect(err).ToNot(HaveOccurred())
				np, err := nodepools.GetByClusterName(context.TODO(), testclient.ControlPlaneClient, hostedClusterName)
				Expect(err).ToNot(HaveOccurred())
				By("Checking that the nodepool status will stay false")
				Consistently(func() error {
					return nodepools.WaitForConfigToBeReady(context.TODO(), testclient.ControlPlaneClient, np.Name, np.Namespace)
				}, 30, 5).Should(Succeed())
			} else {
				By("Checking that the updating MCP status will consistently stay false")
				Consistently(func() corev1.ConditionStatus {
					return mcps.GetConditionStatus(poolName, conditionUpdating)
				}, 30, 5).Should(Equal(corev1.ConditionFalse))
			}

			for _, node := range workerRTNodes {
				err := nodes.HasPreemptRTKernel(context.TODO(), &node)
				Expect(err).To(HaveOccurred())
			}
		})

		AfterAll(func() {
			// return initial configuration
			By("Updating the performance profile")
			profiles.UpdateWithRetry(initialProfile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(context.TODO(), profile)
		})
	})

	// TODO - This test case is highly specific to MCP.
	// We should workout on ideas for a similar test applicable to the Hypershift environment.
	Context("Updating of nodeSelector parameter and node labels", Label(string(label.Tier2), string(label.OpenShift)), func() {
		var mcp *machineconfigv1.MachineConfigPool
		var newCnfNode *corev1.Node
		newRole := "worker-test"
		newLabel := fmt.Sprintf("%s/%s", testutils.LabelRole, newRole)
		labelsDeletion := false
		newNodeSelector := map[string]string{newLabel: ""}

		//fetch existing MCP Selector if exists in profile
		var oldMcpSelector, oldNodeSelector map[string]string

		BeforeEach(func() {
			// initialize on every run
			labelsDeletion = false
			//fetch the latest profile
			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())

			nonPerformancesWorkers, err := nodes.GetNonPerformancesWorkers(profile.Spec.NodeSelector)
			Expect(err).ToNot(HaveOccurred())
			// we need at least 2 non-performance worker nodes to satisfy pod distribution budget
			if len(nonPerformancesWorkers) > 1 {
				newCnfNode = &nonPerformancesWorkers[0]
			}
			if newCnfNode == nil {
				Skip("Skipping the test - cluster does not have another available worker node ")
			}

			//fetch existing MCP Selector if exists in profile
			if profile.Spec.MachineConfigPoolSelector != nil {
				oldMcpSelector = profile.Spec.DeepCopy().MachineConfigPoolSelector
			}

			//fetch existing Node Selector
			if profile.Spec.NodeSelector != nil {
				oldNodeSelector = profile.Spec.DeepCopy().NodeSelector
			}
			nodeLabel = newNodeSelector
			newCnfNode.Labels[newLabel] = ""

			Expect(testclient.ControlPlaneClient.Update(context.TODO(), newCnfNode)).ToNot(HaveOccurred())

			//Remove the MCP Selector if exists
			if profile.Spec.MachineConfigPoolSelector != nil {
				By("Removing Machine Config Selector")
				Expect(testclient.Client.Patch(context.TODO(), profile,
					client.RawPatch(
						types.JSONPatchType,
						[]byte(fmt.Sprintf(`[{"op": "remove", "path": "/spec/%s"}]`, "machineConfigPoolSelector")),
					),
				)).ToNot(HaveOccurred())
			}

			By("Creating new MachineConfigPool")
			mcp = mcps.New(newRole, newNodeSelector)
			err = testclient.Client.Create(context.TODO(), mcp)
			Expect(err).ToNot(HaveOccurred())

			By("Updating Node Selector performance profile")
			profile.Spec.NodeSelector = newNodeSelector
			spec, err := json.Marshal(profile.Spec)
			Expect(err).ToNot(HaveOccurred())

			By("Applying changes in performance profile and waiting until mcp will start updating")
			Expect(testclient.Client.Patch(context.TODO(), profile,
				client.RawPatch(
					types.JSONPatchType,
					[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, spec)),
				),
			)).ToNot(HaveOccurred())
			mcps.WaitForCondition(newRole, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

			By("Waiting when MCP finishes updates and verifying new node has MCP Selector removed")
			mcps.WaitForCondition(newRole, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
		})

		It("[test_id:28440]Verifies that nodeSelector can be updated in performance profile", func() {
			kubeletConfig, err := nodes.GetKubeletConfig(context.TODO(), newCnfNode)
			Expect(err).ToNot(HaveOccurred())
			Expect(kubeletConfig.TopologyManagerPolicy).ToNot(BeEmpty())
			out, err := nodes.ExecCommand(context.TODO(), newCnfNode, chkCmdLine)
			Expect(err).ToNot(HaveOccurred(), "failed to execute %s", chkCmdLine)
			cmdline := testutils.ToString(out)
			Expect(cmdline).To(ContainSubstring("tuned.non_isolcpus"))

		})

		It("[test_id:27484]Verifies that node is reverted to plain worker when the extra labels are removed", func() {
			By("Deleting cnf labels from the node")
			removeLabels(profile.Spec.NodeSelector, newCnfNode)
			labelsDeletion = true
			// Check if node is Ready
			for i := range newCnfNode.Status.Conditions {
				if newCnfNode.Status.Conditions[i].Type == corev1.NodeReady {
					Expect(newCnfNode.Status.Conditions[i].Status).To(Equal(corev1.ConditionTrue))
				}
			}

			// check that the configs reverted
			err = nodes.HasPreemptRTKernel(context.TODO(), newCnfNode)
			Expect(err).To(HaveOccurred())

			out, err := nodes.ExecCommand(context.TODO(), newCnfNode, chkCmdLine)
			Expect(err).ToNot(HaveOccurred(), "failed to execute %s", chkCmdLine)
			cmdline := testutils.ToString(out)
			Expect(cmdline).NotTo(ContainSubstring("tuned.non_isolcpus"))

			kblcfg, err := nodes.GetKubeletConfig(context.TODO(), newCnfNode)
			Expect(err).ToNot(HaveOccurred())
			Expect(kblcfg.ReservedSystemCPUs).NotTo(ContainSubstring("reservedSystemCPUs"))
		})

		AfterEach(func() {
			if !labelsDeletion {
				removeLabels(profile.Spec.NodeSelector, newCnfNode)
			}

			var selectorLabels []string
			for k, v := range oldNodeSelector {
				selectorLabels = append(selectorLabels, fmt.Sprintf(`"%s":"%s"`, k, v))
			}
			nodeSelector := strings.Join(selectorLabels, ",")
			profile.Spec.NodeSelector = oldNodeSelector
			spec, err := json.Marshal(profile.Spec)
			Expect(err).ToNot(HaveOccurred())
			Expect(testclient.Client.Patch(context.TODO(), profile,
				client.RawPatch(
					types.JSONPatchType,
					[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, spec)),
				),
			)).ToNot(HaveOccurred())

			updatedProfile := &performancev2.PerformanceProfile{}
			Eventually(func() string {
				key := types.NamespacedName{
					Name:      profile.Name,
					Namespace: profile.Namespace,
				}
				Expect(testclient.Client.Get(context.TODO(), key, updatedProfile)).ToNot(HaveOccurred())
				var updatedSelectorLabels []string
				for k, v := range updatedProfile.Spec.NodeSelector {
					updatedSelectorLabels = append(updatedSelectorLabels, fmt.Sprintf(`"%s":"%s"`, k, v))
				}
				updatedNodeSelector := strings.Join(updatedSelectorLabels, ",")
				return updatedNodeSelector
			}, 2*time.Minute, 15*time.Second).Should(Equal(nodeSelector))

			poolName, err = mcps.GetByProfile(updatedProfile)
			Expect(err).ToNot(HaveOccurred())
			Expect(testclient.Client.Delete(context.TODO(), mcp)).ToNot(HaveOccurred())
			mcps.WaitForCondition(poolName, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

			// revert node label to have the expected value
			nodeLabel = testutils.NodeSelectorLabels

			//check the saved existingMcpSelector is not nil. If it's nil that means profile did not had
			// any MCP Selector defined so nothing to restore . Else we restore the saved MCP selector
			if oldMcpSelector != nil {
				By("Restoring Machine config selector")
				profile.Spec.MachineConfigPoolSelector = oldMcpSelector
				//check we were able to marshal the old MCP Selector
				spec, err := json.Marshal(profile.Spec)
				Expect(err).ToNot(HaveOccurred())

				Expect(testclient.Client.Patch(context.TODO(), profile,
					client.RawPatch(
						types.JSONPatchType,
						[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, spec)),
					),
				)).ToNot(HaveOccurred())

				By("Applying changes in performance profile and waiting until mcp will start updating")
				mcps.WaitForCondition(poolName, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

				By("Waiting when mcp finishes updates")
				mcps.WaitForCondition(poolName, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
			}
		})

	})

	Context("Offlined CPU API", Ordered, Label(string(label.OfflineCPUs), string(label.Tier2)), func() {
		var numaCoreSiblings map[int]map[int][]int
		BeforeAll(func() {
			//Saving the old performance profile
			initialProfile = profile.DeepCopy()

			for _, node := range workerRTNodes {
				out, err := nodes.ExecCommand(context.TODO(), &node, []string{"nproc", "--all"})
				Expect(err).ToNot(HaveOccurred())
				onlineCPUCount := testutils.ToString(out)

				onlineCPUInt, err := strconv.Atoi(onlineCPUCount)
				Expect(err).ToNot(HaveOccurred())

				Expect(onlineCPUInt).Should(BeNumerically(">=", 3))
				if onlineCPUInt <= 8 {
					Skip(fmt.Sprintf("Offlined CPU API tests need more than 8 CPUs online to work correctly, current online CPUs are %s", onlineCPUCount))
				}
			}
			for _, node := range workerRTNodes {
				numaCoreSiblings, err = nodes.GetCoreSiblings(context.TODO(), &node)
				Expect(err).ToNot(HaveOccurred())
			}

		})

		It("[disruptive] should set offline cpus after deploy PAO", func() {
			// Create new performance with offlined
			reserved := performancev2.CPUSet("0")
			isolated := performancev2.CPUSet("1")
			offlined := performancev2.CPUSet("2-3")
			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())

			By("Enable UserLevelNetworking and add Devices in Profile")
			profile.Spec.CPU = &performancev2.CPU{
				Reserved: &reserved,
				Isolated: &isolated,
				Offlined: &offlined,
			}
			By("Updating the performance profile")
			profiles.UpdateWithRetry(profile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(context.TODO(), profile)

			workerRTNodes = getUpdatedNodes()
			//Check offlined cpus are setting correctly
			for _, node := range workerRTNodes {
				out, err := nodes.ExecCommand(context.TODO(), &node, []string{"cat", "/sys/devices/system/cpu/offline"})
				Expect(err).ToNot(HaveOccurred())
				offlinedOutput := testutils.ToString(out)
				offlinedCPUSet, err := cpuset.Parse(offlinedOutput)
				Expect(err).ToNot(HaveOccurred())
				offlinedCPUSetProfile, err := cpuset.Parse(string(offlined))
				Expect(err).ToNot(HaveOccurred())
				Expect(offlinedCPUSet.Equals(offlinedCPUSetProfile))
			}
		})

		It("[test_id:50964] Offline Higher CPUID's", func() {
			var reserved, isolated, offline cpuset.CPUSet
			numaTopology := copyNumaCoreSiblings(numaCoreSiblings)
			for numaNode := range numaTopology {
				cores := make([]int, 0)
				for k := range numaTopology[numaNode] {
					cores = append(cores, k)
				}
				sort.Ints(cores)
				// Select the last core id
				higherCoreIds := cores[len(cores)-1]
				// Get cpu siblings from the selected cores and delete the selected cores from the map
				cpusiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, higherCoreIds)
				//offline = append(offline, cpusiblings...)
				offline = offline.Union(cpusiblings)
			}
			// Get reserved core siblings from 0, 1
			for reservedCores := 0; reservedCores < 2; reservedCores++ {
				// Get the cpu siblings from the selected core and delete the siblings
				// from the map. Selected siblings of cores are saved in reservedCpus
				cpusiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, reservedCores)
				//reserved = append(reserved, cpusiblings...)
				reserved = reserved.Union(cpusiblings)
			}

			// Remaining core siblings available in the
			// numaTopology map is used in isolatedCpus
			for key := range numaTopology {
				for k := range numaTopology[key] {
					cpusiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, k)
					//isolated = append(isolated, cpusiblings...)
					isolated = isolated.Union(cpusiblings)
				}
			}
			reservedSet := performancev2.CPUSet(reserved.String())
			isolatedSet := performancev2.CPUSet(isolated.String())
			offlinedSet := performancev2.CPUSet(offline.String())
			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())

			By("Enable reserved , isolated and offlined parameters")
			profile.Spec.CPU = &performancev2.CPU{
				Reserved: &reservedSet,
				Isolated: &isolatedSet,
				Offlined: &offlinedSet,
			}

			By("Updating the performance profile")
			profiles.UpdateWithRetry(profile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(context.TODO(), profile)

			workerRTNodes = getUpdatedNodes()
			// Check offlined cpus are setting correctly
			for _, node := range workerRTNodes {
				out, err := nodes.ExecCommand(context.TODO(), &node, []string{"cat", "/sys/devices/system/cpu/offline"})
				Expect(err).ToNot(HaveOccurred())
				offlinedOutput := testutils.ToString(out)
				offlinedCPUSet, err := cpuset.Parse(offlinedOutput)
				Expect(err).ToNot(HaveOccurred())
				offlinedCPUSetProfile, err := cpuset.Parse(string(offlinedSet))
				Expect(err).ToNot(HaveOccurred())
				Expect(offlinedCPUSet.Equals(offlinedCPUSetProfile))
			}
		})

		It("[test_id:50965]Offline Middle CPUID's", func() {
			var reserved, isolated, offline cpuset.CPUSet
			numaTopology := copyNumaCoreSiblings(numaCoreSiblings)
			for key := range numaTopology {
				cores := make([]int, 0)
				for k := range numaTopology[key] {
					cores = append(cores, k)
				}
				sort.Ints(cores)
				middleCoreIds := cores[len(cores)/2]
				siblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, middleCoreIds)
				offline = offline.Union(siblings)
			}
			for reservedCores := 0; reservedCores < 2; reservedCores++ {
				// Get the cpu siblings from the selected core and delete the siblings
				// from the map. Selected siblings of cores are saved in reservedCpus
				siblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, reservedCores)
				reserved = reserved.Union(siblings)
			}
			// Remaining core siblings available in the
			// numaTopology map is used in isolatedCpus
			for key := range numaTopology {
				for k := range numaTopology[key] {
					siblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, k)
					isolated = isolated.Union(siblings)
				}
			}
			// Create new performance with offlined
			reservedSet := performancev2.CPUSet(reserved.String())
			isolatedSet := performancev2.CPUSet(isolated.String())
			offlinedSet := performancev2.CPUSet(offline.String())
			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())

			By("Enable reserved, isolated and offlined parameters")
			profile.Spec.CPU = &performancev2.CPU{
				Reserved: &reservedSet,
				Isolated: &isolatedSet,
				Offlined: &offlinedSet,
			}
			By("Updating the performance profile")
			profiles.UpdateWithRetry(profile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(context.TODO(), profile)

			workerRTNodes = getUpdatedNodes()
			//Check offlined cpus are setting correctly
			for _, node := range workerRTNodes {
				out, err := nodes.ExecCommand(context.TODO(), &node, []string{"cat", "/sys/devices/system/cpu/offline"})
				Expect(err).ToNot(HaveOccurred())
				offlinedOutput := testutils.ToString(out)
				offlinedCPUSet, err := cpuset.Parse(offlinedOutput)
				Expect(err).ToNot(HaveOccurred())
				offlinedCPUSetProfile, err := cpuset.Parse(string(offlinedSet))
				Expect(err).ToNot(HaveOccurred())
				Expect(offlinedCPUSet.Equals(offlinedCPUSetProfile))
			}
		})

		It("[test_id:50966]verify offlined parameter accepts multiple ranges of cpuid's", func() {
			var reserved, isolated, offlined cpuset.CPUSet
			numaTopology := copyNumaCoreSiblings(numaCoreSiblings)
			if len(numaCoreSiblings) < 2 {
				Skip(fmt.Sprintf("This test need 2 NUMA nodes, available only %d", len(numaCoreSiblings)))
			}
			if len(numaCoreSiblings[0]) < 20 {
				Skip("This test needs systems with at least 20 cores per socket")
			}
			// Get reserved core siblings from 0, 1
			for reservedCores := 0; reservedCores < 2; reservedCores++ {
				// Get the cpu siblings from the selected core and delete the siblings
				// from the map. Selected siblings of cores are saved in reservedCpus
				siblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, reservedCores)
				reserved = reserved.Union(siblings)
			}
			//Get Offline Core siblings . We take the total cores and
			//from the middle we take core ids for calculating the ranges.
			for key := range numaTopology {
				cores := make([]int, 0)
				for k := range numaTopology[key] {
					cores = append(cores, k)
				}
				sort.Ints(cores)
				for i := 0; i < len(cores)/2; i++ {
					siblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, cores[i])
					offlined = offlined.Union(siblings)
				}
			}
			// Remaining core siblings available in the numaTopology
			// map is used in isolatedCpus
			for key := range numaTopology {
				for k := range numaTopology[key] {
					siblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, k)
					isolated = isolated.Union(siblings)
				}
			}
			// Create new performance with offlined
			reservedSet := performancev2.CPUSet(reserved.String())
			isolatedSet := performancev2.CPUSet(isolated.String())
			offlinedSet := performancev2.CPUSet(offlined.String())
			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())

			By("Enable reserved, isolated and offlined parameters")
			profile.Spec.CPU = &performancev2.CPU{
				Reserved: &reservedSet,
				Isolated: &isolatedSet,
				Offlined: &offlinedSet,
			}
			By("Updating the performance profile")
			profiles.UpdateWithRetry(profile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(context.TODO(), profile)

			workerRTNodes = getUpdatedNodes()
			//Check offlined cpus are setting correctly
			for _, node := range workerRTNodes {
				out, err := nodes.ExecCommand(context.TODO(), &node, []string{"cat", "/sys/devices/system/cpu/offline"})
				Expect(err).ToNot(HaveOccurred())
				offlinedOutput := testutils.ToString(out)
				offlinedCPUSet, err := cpuset.Parse(offlinedOutput)
				Expect(err).ToNot(HaveOccurred())
				offlinedCPUSetProfile, err := cpuset.Parse(string(offlinedSet))
				Expect(err).ToNot(HaveOccurred())
				Expect(offlinedCPUSet.Equals(offlinedCPUSetProfile))
			}
		})

		It("[test_id:50968]verify cpus mentioned in reserved or isolated cannot be offline", func() {
			var reserved, isolated, offlined cpuset.CPUSet
			numaTopology := copyNumaCoreSiblings(numaCoreSiblings)
			if len(numaCoreSiblings) < 2 {
				Skip(fmt.Sprintf("This test need 2 NUMA nodes, available only %d", len(numaCoreSiblings)))
			}
			// Get reserved core siblings from 0, 1
			for reservedCores := 0; reservedCores < 2; reservedCores++ {
				// Get the cpu siblings from the selected core and delete the siblings
				// from the map. Selected siblings of cores are saved in reservedCpus
				siblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, reservedCores)
				reserved = reserved.Union(siblings)
			}
			// Remaining core siblings available in the
			// numaTopology map is used in isolatedCpus
			for key := range numaTopology {
				for k := range numaTopology[key] {
					siblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, k)
					isolated = isolated.Union(siblings)
				}
			}
			//combine both isolated and reserved
			totalCpuSlice := cpuset.CPUSet.Union(reserved, isolated)
			// get partial cpus from the combined cpus
			totalList := totalCpuSlice.List()
			offlineCpuList := totalList[:totalCpuSlice.Size()/2]
			offlined = cpuset.New(offlineCpuList...)
			// Create new performance with offlined
			reservedSet := performancev2.CPUSet(reserved.String())
			isolatedSet := performancev2.CPUSet(isolated.String())
			offlinedSet := performancev2.CPUSet(offlined.String())
			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())

			By("Enable reserved, isolated and offlined parameters")
			profile.Spec.CPU = &performancev2.CPU{
				Reserved: &reservedSet,
				Isolated: &isolatedSet,
				Offlined: &offlinedSet,
			}
			By("Updating the performance profile")
			EventuallyWithOffset(1, func() string {
				err := testclient.ControlPlaneClient.Update(context.TODO(), profile)
				if err != nil {
					statusErr, _ := err.(*errors.StatusError)
					return statusErr.Status().Message
				}
				hostedClusterName, err := hypershift.GetHostedClusterName()
				Expect(err).ToNot(HaveOccurred())
				testlog.Infof("HostedCluster Name: %q ", hostedClusterName)
				np, err := nodepools.GetByClusterName(context.TODO(), testclient.ControlPlaneClient, hostedClusterName)
				Expect(err).ToNot(HaveOccurred())
				testlog.Infof("Nodepool Name: %q ", np.Name)
				for _, condition := range np.Status.Conditions {
					if condition.Type == hypershiftv1beta1.NodePoolValidTuningConfigConditionType && condition.Reason == hypershiftv1beta1.NodePoolValidationFailedReason {
						if strings.Contains(condition.Message, "isolated and offlined cpus overlap") {
							return condition.Message
						}
					}
				}
				return "Profile applied successfully"
			}, 10*time.Minute, 5*time.Second).Should(ContainSubstring("isolated and offlined cpus overlap"))

		})

		It("[test_id:50970]Offline CPUID's from multiple numa nodes", func() {
			var reserved, isolated, offlined cpuset.CPUSet
			numaTopology := copyNumaCoreSiblings(numaCoreSiblings)
			if len(numaCoreSiblings) < 2 {
				Skip(fmt.Sprintf("This test need 2 NUMA nodes, available only %d", len(numaCoreSiblings)))
			}
			// Get reserved core siblings from 0, 1
			for reservedCores := 0; reservedCores < 2; reservedCores++ {
				// Get the cpu siblings from the selected core and delete the siblings
				// from the map. Selected siblings of cores are saved in reservedCpus
				siblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, reservedCores)
				reserved = reserved.Union(siblings)
			}

			discreteCores := []int{3, 13, 15, 24, 29}
			for _, v := range discreteCores {
				siblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, v)
				offlined = offlined.Union(siblings)
			}
			for key := range numaTopology {
				for k := range numaTopology[key] {
					cpusiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, k)
					isolated = isolated.Union(cpusiblings)
				}
			}
			// Create new performance with offlined
			reservedSet := performancev2.CPUSet(reserved.String())
			isolatedSet := performancev2.CPUSet(isolated.String())
			offlinedSet := performancev2.CPUSet(offlined.String())
			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())

			By("Enable reserved, isolated and offlined parameters")
			profile.Spec.CPU = &performancev2.CPU{
				Reserved: &reservedSet,
				Isolated: &isolatedSet,
				Offlined: &offlinedSet,
			}

			By("Updating the performance profile")
			profiles.UpdateWithRetry(profile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(context.TODO(), profile)

			workerRTNodes = getUpdatedNodes()
			//Check offlined cpus are setting correctly
			for _, node := range workerRTNodes {
				out, err := nodes.ExecCommand(context.TODO(), &node, []string{"cat", "/sys/devices/system/cpu/offline"})
				Expect(err).ToNot(HaveOccurred())
				offlinedOutput := testutils.ToString(out)
				offlinedCPUSet, err := cpuset.Parse(offlinedOutput)
				Expect(err).ToNot(HaveOccurred())
				offlinedCPUSetProfile, err := cpuset.Parse(string(offlinedSet))
				Expect(err).ToNot(HaveOccurred())
				Expect(offlinedCPUSet.Equals(offlinedCPUSetProfile))
			}
		})

		AfterAll(func() {
			By("Reverting the Profile")
			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			currentSpec, _ := json.Marshal(profile.Spec)
			spec, _ := json.Marshal(initialProfile.Spec)
			// revert only if the profile changes.
			if !bytes.Equal(currentSpec, spec) {
				By("Updating the performance profile")
				profiles.UpdateWithRetry(initialProfile)

				By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
				profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

				By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
				profilesupdate.WaitForTuningUpdated(context.TODO(), profile)
			}

			findcmd := `find /sys/devices/system/cpu/cpu* -type f -name online -exec cat {} \;`
			checkCpuStatusCmd := []string{"bash", "-c", findcmd}
			for _, node := range workerRTNodes {
				out, err := nodes.ExecCommand(context.TODO(), &node, checkCpuStatusCmd)
				Expect(err).NotTo(HaveOccurred())
				stdout := testutils.ToString(out)
				v := strings.Split(stdout, "\n")
				for _, val := range v {
					Expect(val).To(Equal("1"))
				}
			}
		})
	})

	Context("[rfe_id:54374][rps_mask] Network Stack Pinning", Label(string(label.RPSMask), string(label.Tier1)), func() {

		BeforeEach(func() {
			//Get Latest profile
			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())

			By("Backing up the profile")
			initialProfile = profile.DeepCopy()
		})

		AfterEach(func() {
			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())

			currentSpec, _ := json.Marshal(profile.Spec)
			spec, _ := json.Marshal(initialProfile.Spec)
			// revert only if the profile changes.
			if bytes.Equal(currentSpec, spec) {
				testlog.Infof("profile hasn't change, avoiding revert")
				return
			}
			By("Reverting the Profile")
			profiles.UpdateWithRetry(initialProfile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(context.TODO(), profile)
		})

		It("[test_id:56006]Verify systemd unit file gets updated when the reserved cpus are modified", func() {
			// Enable RPS for this test since we're specifically testing RPS functionality
			if profile.Annotations == nil {
				profile.Annotations = make(map[string]string)
			}
			profile.Annotations[performancev2.PerformanceProfileEnableRpsAnnotation] = "enable"

			var reserved, isolated cpuset.CPUSet
			var onlineCPUInt int
			for _, node := range workerRTNodes {
				out, err := nodes.ExecCommand(context.TODO(), &node, []string{"nproc", "--all"})
				Expect(err).ToNot(HaveOccurred())
				onlineCPUCount := testutils.ToString(out)
				onlineCPUInt, err = strconv.Atoi(onlineCPUCount)
				Expect(err).ToNot(HaveOccurred())
				if onlineCPUInt <= 8 {
					Skip(fmt.Sprintf("This test needs more than 8 CPUs online to work correctly, current online CPUs are %s", onlineCPUCount))
				}
			}
			//numaNode[node][coreId][core-siblings]
			var numaCoreSiblings map[int]map[int][]int
			for _, node := range workerRTNodes {
				numaCoreSiblings, err = nodes.GetCoreSiblings(context.TODO(), &node)
			}
			//Lets take reserved cpus from the middle of the cpu list
			for numaNode := range numaCoreSiblings {
				coreids := make([]int, 0)
				for cores := range numaCoreSiblings[numaNode] {
					coreids = append(coreids, cores)
				}
				sort.Ints(coreids)
				Expect(len(coreids)).ToNot(Equal(0))
				middleIndex := len(coreids) / 2
				// we need at least 4 cpus for reserved cpus
				middleCoreIds := coreids[middleIndex-2 : middleIndex+2]
				for _, cores := range middleCoreIds {
					coresiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaCoreSiblings, cores)
					reserved = reserved.Union(coresiblings)
				}
			}
			for numaNode := range numaCoreSiblings {
				for coreids := range numaCoreSiblings[numaNode] {
					coresiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaCoreSiblings, coreids)
					isolated = isolated.Union(coresiblings)
				}
			}
			// Update performance profile
			reservedSet := performancev2.CPUSet(reserved.String())
			isolatedSet := performancev2.CPUSet(isolated.String())

			By("Update reserved, isolated parameters")
			profile.Spec.CPU = &performancev2.CPU{
				Reserved: &reservedSet,
				Isolated: &isolatedSet,
			}

			By("Updating the performance profile")
			profiles.UpdateWithRetry(profile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(context.TODO(), profile)

			//Check RPS Mask after profile is updated with New reserved Cpus
			expectedRPSCPUs, err := cpuset.Parse(string(*profile.Spec.CPU.Reserved))
			Expect(err).ToNot(HaveOccurred())
			for _, node := range workerRTNodes {
				// Verify the systemd RPS service uses the correct RPS mask
				var maskContent string
				cmd := []string{"sysctl", "-n", "net.core.rps_default_mask"}
				out, err := nodes.ExecCommand(context.TODO(), &node, cmd)
				Expect(err).ToNot(HaveOccurred(), "failed to exec command %q on node %q", cmd, node)
				maskContent = testutils.ToString(out)
				rpsMaskContent := strings.Trim(maskContent, "\n")
				rpsCPUs, err := components.CPUMaskToCPUSet(rpsMaskContent)
				Expect(err).ToNot(HaveOccurred(), "failed to parse RPS mask %q", rpsMaskContent)
				Expect(rpsCPUs.Equals(expectedRPSCPUs)).To(BeTrue(), "the default rps mask is different from the reserved CPUs")

				// Verify all host network devices have the correct RPS mask
				cmd = []string{
					"find", "/rootfs/sys/devices/virtual/net",
					"-path", "/rootfs/sys/devices/virtual/net/lo",
					"-prune", "-o",
					"-type", "f",
					"-name", "rps_cpus",
					"-exec", "cat", "{}", ";",
				}
				out, err = nodes.ExecCommand(context.TODO(), &node, cmd)
				Expect(err).ToNot(HaveOccurred(), "failed to exec command %q on node %q", cmd, node.Name)
				devsRPS := testutils.ToString(out)
				for _, devRPS := range strings.Split(devsRPS, "\n") {
					rpsCPUs, err = components.CPUMaskToCPUSet(devRPS)
					Expect(err).ToNot(HaveOccurred())
					Expect(rpsCPUs.Equals(expectedRPSCPUs)).To(BeTrue(), "a host device rps mask is different from the reserved CPUs")
				}
			}
		})

		It("[test_id:54191]Verify RPS Mask is not applied by default", func() {
			// This test verifies that RPS is disabled by default
			// No need to modify workload hints since RPS is disabled by default now

			// Ensure the profile doesn't have RPS enabled
			if profile.Annotations != nil {
				if val, ok := profile.Annotations[performancev2.PerformanceProfileEnableRpsAnnotation]; ok && val == "enable" {
					delete(profile.Annotations, performancev2.PerformanceProfileEnableRpsAnnotation)

					By("Updating the performance profile to ensure RPS is disabled")
					profiles.UpdateWithRetry(profile)

					By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
					profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

					By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
					profilesupdate.WaitForTuningUpdated(context.TODO(), profile)
				}
			}

			for _, node := range workerRTNodes {
				// Verify the systemd RPS services were not created
				cmd := []string{"ls", "/rootfs/etc/systemd/system/update-rps@.service"}
				_, err := nodes.ExecCommand(context.TODO(), &node, cmd)
				Expect(err).To(HaveOccurred())
			}
		})
	})

	Context("ContainerRuntimeConfig", Ordered, Label(string(label.Tier2)), func() {
		var ctrcfg *machineconfigv1.ContainerRuntimeConfig
		const ContainerRuntimeConfigName = "ctrcfg-test"
		mcp := &machineconfigv1.MachineConfigPool{}
		var testpodTemplate *corev1.Pod
		BeforeAll(func() {
			key := types.NamespacedName{
				Name: poolName,
			}
			By("checking if ContainerRuntimeConfig object already exists")
			if !hypershift.IsHypershiftCluster() {
				Expect(testclient.ControlPlaneClient.Get(context.TODO(), key, mcp)).ToNot(HaveOccurred(), "cannot get MCP %q", poolName)
				ctrcfg, err = getContainerRuntimeConfigFrom(context.TODO(), profile, mcp)
				Expect(err).ToNot(HaveOccurred(), "failed to get ContainerRuntimeConfig from mcp %q", mcp.Name)
				Expect(ctrcfg).To(BeNil(), "ContainerRuntimeConfig should not exist for MCP %q", mcp.Name)
			} else {
				ctrcfg, err = getContainerRuntimeConfigFrom(context.TODO(), profile, mcp)
				Expect(err).ToNot(HaveOccurred(), "failed to get ContainerRuntimeConfig from profile %q", profile.Name)
				Expect(ctrcfg).To(BeNil(), "ContainerRuntimeConfig should not exist for profile %q", profile.Name)
			}
			testpodTemplate = pods.GetTestPod()
			testpodTemplate.Namespace = testutils.NamespaceTesting
			runtimeClass := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
			testpodTemplate.Spec.RuntimeClassName = &runtimeClass
		})
		DescribeTable("verifies container runtime behavior",
			func(withCTRCfg bool) {
				var expectedRuntime string
				if withCTRCfg {
					ctrcfg = newContainerRuntimeConfig(ContainerRuntimeConfigName, profile, mcp)
					if hypershift.IsHypershiftCluster() {
						By(fmt.Sprintf("creating ContainerRuntimeConfig configmap %q", ctrcfg.Name))
						Expect(testclient.ControlPlaneClient.Create(context.TODO(), ctrcfg)).ToNot(HaveOccurred(), "failed to create ctrcfg configmap %#v", ctrcfg.Name)

						hostedClusterName, err := hypershift.GetHostedClusterName()
						Expect(err).ToNot(HaveOccurred())
						np, err = nodepools.GetByClusterName(context.TODO(), testclient.ControlPlaneClient, hostedClusterName)
						Expect(err).ToNot(HaveOccurred())

						By("Attaching the Config object to the nodepool")
						Expect(nodepools.AttachConfigObject(context.TODO(), testclient.ControlPlaneClient, ctrcfg)).To(Succeed())

						By("Waiting for the nodepool configuration to start updating")
						err = nodepools.WaitForUpdatingConfig(context.TODO(), testclient.ControlPlaneClient, np.Name, np.Namespace)
						Expect(err).ToNot(HaveOccurred())

						By("Waiting for the nodepool configuration to be ready")
						err = nodepools.WaitForConfigToBeReady(context.TODO(), testclient.ControlPlaneClient, np.Name, np.Namespace)
						Expect(err).ToNot(HaveOccurred())
					} else {
						By(fmt.Sprintf("creating ContainerRuntimeConfig %q", ctrcfg.Name))
						Expect(testclient.ControlPlaneClient.Create(context.TODO(), ctrcfg)).ToNot(HaveOccurred(), "failed to create ctrcfg %#v", ctrcfg)

						By(fmt.Sprintf("waiting for MCP %q transition to UPDATING state", poolName))
						mcps.WaitForConditionFunc(poolName, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue, getMCPConditionStatus)
						By(fmt.Sprintf("waiting for MCP %q transition to UPDATED state", poolName))
						mcps.WaitForConditionFunc(poolName, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue, getMCPConditionStatus)
					}
					DeferCleanup(func() {
						if hypershift.IsHypershiftCluster() {
							By("Deattaching the Config object from the nodepool")
							Expect(nodepools.DeattachConfigObject(context.TODO(), testclient.ControlPlaneClient, ctrcfg)).To(Succeed())

							Expect(testclient.ControlPlaneClient.Delete(context.TODO(), ctrcfg)).ToNot(HaveOccurred(), "failed to delete ctrcfg configmap %#v", ctrcfg)

							By("Waiting for the nodepool configuration to start updating")
							err = nodepools.WaitForUpdatingConfig(context.TODO(), testclient.ControlPlaneClient, np.Name, np.Namespace)
							Expect(err).ToNot(HaveOccurred())

							By("Waiting for the nodepool configuration to be ready")
							err = nodepools.WaitForConfigToBeReady(context.TODO(), testclient.ControlPlaneClient, np.Name, np.Namespace)
							Expect(err).ToNot(HaveOccurred())
						} else {
							Expect(testclient.ControlPlaneClient.Delete(context.TODO(), ctrcfg)).ToNot(HaveOccurred(), "failed to delete ctrcfg %#v", ctrcfg)
							By(fmt.Sprintf("waiting for MCP %q transition to UPDATING state", poolName))
							mcps.WaitForConditionFunc(poolName, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue, getMCPConditionStatus)
							By(fmt.Sprintf("waiting for MCP %q transition to UPDATED state", poolName))
							mcps.WaitForConditionFunc(poolName, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue, getMCPConditionStatus)
						}
					})
				}

				for i := 0; i < len(workerRTNodes); i++ {
					By("Determining the default container runtime used in the node")
					tunedPod, err := tuned.GetPod(context.TODO(), &workerRTNodes[i])
					Expect(err).ToNot(HaveOccurred())
					expectedRuntime, err = runtime.GetContainerRuntimeTypeFor(context.TODO(), testclient.DataPlaneClient, tunedPod)
					Expect(err).ToNot(HaveOccurred())
					testlog.Infof("Container runtime used for the node: %s", expectedRuntime)

					By("verifying pod using high-performance runtime class handled by the default container runtime aswell")
					Expect(err).ToNot(HaveOccurred())
					testpod := testpodTemplate.DeepCopy()
					testpod.Spec.NodeName = workerRTNodes[i].Name
					testpod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNodes[i].Name}
					By(fmt.Sprintf("creating a test pod using high-performance runtime class on node %s", workerRTNodes[i].Name))
					Expect(testclient.DataPlaneClient.Create(context.TODO(), testpod)).ToNot(HaveOccurred())
					DeferCleanup(func() {
						By(fmt.Sprintf("deleting the test pod from node %s", workerRTNodes[i].Name))
						Expect(testclient.DataPlaneClient.Delete(context.TODO(), testpod)).ToNot(HaveOccurred())
						Expect(pods.WaitForDeletion(context.TODO(), testpod, pods.DefaultDeletionTimeout*time.Second)).ToNot(HaveOccurred())
					})
					testpod, err = pods.WaitForCondition(context.TODO(), client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
					Expect(err).ToNot(HaveOccurred())
					runtimeType, err := runtime.GetContainerRuntimeTypeFor(context.TODO(), testclient.DataPlaneClient, testpod)
					Expect(err).ToNot(HaveOccurred())
					testlog.Infof("Container runtime used for the test pod: %s", runtimeType)
					Expect(runtimeType).To(Equal(expectedRuntime))
				}
			},
			Entry("test without ContainerRuntimeConfig", false),
			Entry("create and test with ContainerRuntimeConfig", true),
		)
	})
})

func getMCPConditionStatus(mcpName string, conditionType machineconfigv1.MachineConfigPoolConditionType) corev1.ConditionStatus {
	mcp, err := mcps.GetByNameNoRetry(mcpName)
	if err != nil {
		// In case of any error we just retry, as in case of single node cluster
		// the only node may be rebooting
		testlog.Infof("MCP %q not found -> unknown", mcpName)
		return corev1.ConditionUnknown
	}
	for _, condition := range mcp.Status.Conditions {
		if condition.Type == conditionType {
			testlog.Infof("MCP %q condition %q -> %q", mcpName, conditionType, condition.Status)
			return condition.Status
		}
	}
	testlog.Infof("MCP %q condition %q not found -> unknown", mcpName, conditionType)
	return corev1.ConditionUnknown
}

func hugepagesPathForNode(nodeID, sizeINMb int) string {
	return fmt.Sprintf("/sys/devices/system/node/node%d/hugepages/hugepages-%dkB/nr_hugepages", nodeID, sizeINMb*1024)
}

func countHugepagesOnNode(ctx context.Context, node *corev1.Node, sizeInMb int) (int, error) {
	numaInfo, err := nodes.GetNumaNodes(ctx, node)
	if err != nil {
		return 0, err
	}
	count := 0
	for i := 0; i < len(numaInfo); i++ {
		nodeCmd := []string{"cat", hugepagesPathForNode(i, sizeInMb)}
		out, err := nodes.ExecCommand(ctx, node, nodeCmd)
		if err != nil {
			return 0, err
		}
		result := testutils.ToString(out)
		t, err := strconv.Atoi(result)
		if err != nil {
			return 0, err
		}
		count += t
	}
	return count, nil
}

func getUpdatedNodes() []corev1.Node {
	workerRTNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
	Expect(err).ToNot(HaveOccurred())
	klog.Infof("updated nodes from %#v: %v", testutils.NodeSelectorLabels, getNodeNames(workerRTNodes))
	workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
	klog.Infof("updated nodes matching optional selector: %v", getNodeNames(workerRTNodes))
	Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("error looking for the optional selector: %v", err))
	Expect(workerRTNodes).ToNot(BeEmpty(), "cannot find RT enabled worker nodes")
	return workerRTNodes
}

func getNodeNames(nodes []corev1.Node) []string {
	names := []string{}
	for _, node := range nodes {
		names = append(names, node.Name)
	}
	return names
}

func removeLabels(nodeSelector map[string]string, targetNode *corev1.Node) {
	ExpectWithOffset(1, testclient.Client.Get(context.TODO(), client.ObjectKeyFromObject(targetNode), targetNode)).ToNot(HaveOccurred())
	patchNode := false
	for l := range nodeSelector {
		if _, ok := targetNode.Labels[l]; ok {
			patchNode = true
			testlog.Infof("found key: %q in targetNode.Labels, deleting it", l)
			delete(targetNode.Labels, l)
		}
	}
	if !patchNode {
		testlog.Warningf("node %q does not contain nodeSelector %v", targetNode.Name, nodeSelector)
		return
	}
	label, err := json.Marshal(targetNode.Labels)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, testclient.Client.Patch(context.TODO(), targetNode,
		client.RawPatch(
			types.JSONPatchType,
			[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/metadata/labels", "value": %s }]`, label)),
		),
	)).ToNot(HaveOccurred())
	By(fmt.Sprintf("Waiting for MCP %q to start updating", testutils.RoleWorker))
	mcps.WaitForCondition(testutils.RoleWorker, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

	By(fmt.Sprintf("Waiting when MCP %q complete updates and verifying that node reverted back configuration", testutils.RoleWorker))
	mcps.WaitForCondition(testutils.RoleWorker, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
}

func newContainerRuntimeConfig(name string, profile *performancev2.PerformanceProfile, profileMCP *machineconfigv1.MachineConfigPool) *machineconfigv1.ContainerRuntimeConfig {
	return &machineconfigv1.ContainerRuntimeConfig{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ContainerRuntimeConfig",
			APIVersion: machineconfigv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: machineconfigv1.ContainerRuntimeConfigSpec{
			MachineConfigPoolSelector: &metav1.LabelSelector{
				MatchLabels: profilecomponent.GetMachineConfigPoolSelector(profile, profileMCP),
			},
			ContainerRuntimeConfig: &machineconfigv1.ContainerRuntimeConfiguration{
				DefaultRuntime: machineconfigv1.ContainerRuntimeDefaultRuntimeRunc,
			},
		},
	}
}

func getContainerRuntimeConfigFrom(ctx context.Context, profile *performancev2.PerformanceProfile, mcp *machineconfigv1.MachineConfigPool) (*machineconfigv1.ContainerRuntimeConfig, error) {
	ctrcfgList := &machineconfigv1.ContainerRuntimeConfigList{}
	if err := testclient.ControlPlaneClient.List(ctx, ctrcfgList); err != nil {
		return nil, err
	}

	if len(ctrcfgList.Items) == 0 {
		testlog.Infof("no ContainerRuntimeConfig object found on the cluster")
		return nil, nil
	}

	var ctrcfgs []*machineconfigv1.ContainerRuntimeConfig
	mcpLabels := labels.Set(mcp.Labels)
	for i := 0; i < len(ctrcfgList.Items); i++ {
		ctrcfg := &ctrcfgList.Items[i]
		ctrcfgSelector, err := metav1.LabelSelectorAsSelector(ctrcfg.Spec.MachineConfigPoolSelector)
		if err != nil {
			return nil, err
		}
		if ctrcfgSelector.Matches(mcpLabels) {
			ctrcfgs = append(ctrcfgs, ctrcfg)
		}
	}

	if len(ctrcfgs) == 0 {
		testlog.Infof("no ContainerRuntimeConfig found that matches MCP labels %s that associated with performance profile %q", mcpLabels.String(), profile.Name)
		return nil, nil
	}

	if len(ctrcfgs) > 1 {
		return nil, fmt.Errorf("more than one ContainerRuntimeConfig found that matches MCP labels %s that associated with performance profile %q", mcpLabels.String(), profile.Name)
	}
	return ctrcfgs[0], nil
}

// copyNumaCoreSiblings copies the existing numa topology to another
// map required for offline tests.
func copyNumaCoreSiblings(src map[int]map[int][]int) map[int]map[int][]int {
	dst := make(map[int]map[int][]int)
	for k, v := range src {
		coresiblings := make(map[int][]int)
		for core, cpus := range v {
			newslice := make([]int, len(cpus))
			copy(newslice, cpus)
			coresiblings[core] = newslice
		}
		dst[k] = coresiblings
	}
	return dst
}
