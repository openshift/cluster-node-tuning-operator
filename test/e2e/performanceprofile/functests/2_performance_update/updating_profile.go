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
	"k8s.io/utils/cpuset"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	profilecomponent "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
)

type checkFunction func(context.Context, *corev1.Node) (string, error)

var _ = Describe("[rfe_id:28761][performance] Updating parameters in performance profile", func() {
	var workerRTNodes []corev1.Node
	var profile, initialProfile *performancev2.PerformanceProfile
	var performanceMCP string
	var err error

	chkCmdLine := []string{"cat", "/proc/cmdline"}
	chkKubeletConfig := []string{"cat", "/rootfs/etc/kubernetes/kubelet.conf"}
	chkIrqbalance := []string{"cat", "/rootfs/etc/sysconfig/irqbalance"}

	chkCmdLineFn := func(ctx context.Context, node *corev1.Node) (string, error) {
		return nodes.ExecCommandToString(ctx, chkCmdLine, node)
	}
	chkKubeletConfigFn := func(ctx context.Context, node *corev1.Node) (string, error) {
		return nodes.ExecCommandToString(ctx, chkKubeletConfig, node)
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
		performanceMCP, err = mcps.GetByProfile(profile)
		Expect(err).ToNot(HaveOccurred())
		klog.Infof("using performanceMCP: %q", performanceMCP)

		// Verify that worker and performance MCP have updated state equals to true
		for _, mcpName := range []string{testutils.RoleWorker, performanceMCP} {
			mcps.WaitForCondition(mcpName, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
		}
	})

	Context("Verify hugepages count split on two NUMA nodes", Ordered, func() {
		hpSize2M := performancev2.HugePageSize("2M")
		skipTests := false

		testutils.CustomBeforeAll(func() {
			for _, node := range workerRTNodes {
				numaInfo, err := nodes.GetNumaNodes(context.TODO(), &node)
				Expect(err).ToNot(HaveOccurred())
				if len(numaInfo) < 2 {
					skipTests = true
					klog.Infof(fmt.Sprintf("This test need 2 NUMA nodes.The number of NUMA nodes on node %s < 2", node.Name))
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
				BalanceIsolated: pointer.Bool(false),
				Reserved:        &reserved,
				Isolated:        &isolated,
			}
			profile.Spec.HugePages = &performancev2.HugePages{
				DefaultHugePagesSize: &hpSize2M,
				Pages: []performancev2.HugePage{
					{
						Count: hpCntOnNuma0,
						Size:  hpSize2M,
						Node:  pointer.Int32(0),
					},
					{
						Count: hpCntOnNuma1,
						Size:  hpSize2M,
						Node:  pointer.Int32(1),
					},
				},
			}
			profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
				Enabled: pointer.Bool(true),
			}

			By("Verifying that mcp is ready for update")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

			spec, err := json.Marshal(profile.Spec)
			Expect(err).ToNot(HaveOccurred())

			By("Applying changes in performance profile and waiting until mcp will start updating")
			Expect(testclient.Client.Patch(context.TODO(), profile,
				client.RawPatch(
					types.JSONPatchType,
					[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, spec)),
				),
			)).ToNot(HaveOccurred())
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

			By("Waiting when mcp finishes updates")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

			for _, node := range workerRTNodes {
				for i := 0; i < 2; i++ {
					nodeCmd := []string{"cat", hugepagesPathForNode(i, 2)}
					result, err := nodes.ExecCommandToString(context.TODO(), nodeCmd, &node)
					Expect(err).ToNot(HaveOccurred())

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
			// return initial configuration
			spec, err := json.Marshal(initialProfile.Spec)
			Expect(err).ToNot(HaveOccurred())
			Expect(testclient.Client.Patch(context.TODO(), profile,
				client.RawPatch(
					types.JSONPatchType,
					[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, spec)),
				),
			)).ToNot(HaveOccurred())
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
		})
	})

	Context("Verify that all performance profile parameters can be updated", Ordered, func() {
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
				BalanceIsolated: pointer.Bool(false),
				Reserved:        &reserved,
				Isolated:        &isolated,
			}
			profile.Spec.NUMA = &performancev2.NUMA{
				TopologyPolicy: &policy,
			}
			profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
				Enabled: pointer.Bool(false),
			}

			if profile.Spec.AdditionalKernelArgs == nil {
				By("AdditionalKernelArgs is empty. Checking only adding new arguments")
				profile.Spec.AdditionalKernelArgs = append(profile.Spec.AdditionalKernelArgs, "new-argument=test")
			} else {
				removedKernelArgs = profile.Spec.AdditionalKernelArgs[0]
				profile.Spec.AdditionalKernelArgs = append(profile.Spec.AdditionalKernelArgs[1:], "new-argument=test")
			}

			By("Verifying that mcp is ready for update")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

			spec, err := json.Marshal(profile.Spec)
			Expect(err).ToNot(HaveOccurred())

			By("Applying changes in performance profile and waiting until mcp will start updating")
			Expect(testclient.Client.Patch(context.TODO(), profile,
				client.RawPatch(
					types.JSONPatchType,
					[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, spec)),
				),
			)).ToNot(HaveOccurred())
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

			By("Waiting when mcp finishes updates")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
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
			// kubelet.conf changed formatting, there is a space after colons atm. Let's deal with both cases with a regex
			Entry("[test_id:28935] verify that reservedSystemCPUs was updated", context.TODO(), chkKubeletConfigFn, []string{`"reservedSystemCPUs": ?"0,3"`}, true, true),
			Entry("[test_id:28760] verify that topologyManager was updated", context.TODO(), chkKubeletConfigFn, []string{`"topologyManagerPolicy": ?"best-effort"`}, true, true),
		)

		It("[test_id:27738] should succeed to disable the RT kernel", func() {
			for _, node := range workerRTNodes {
				err := nodes.HasPreemptRTKernel(context.TODO(), &node)
				Expect(err).To(HaveOccurred())
			}
		})

		It("[test_id:28612]Verify that Kernel arguments can me updated (added, removed) thru performance profile", func() {
			for _, node := range workerRTNodes {
				cmdline, err := nodes.ExecCommandToString(context.TODO(), chkCmdLine, &node)
				Expect(err).ToNot(HaveOccurred(), "failed to execute %s", chkCmdLine)

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

			if profile.Spec.RealTimeKernel == nil || *profile.Spec.RealTimeKernel.Enabled == true {
				Skip("Skipping test - This test expects RT Kernel to be disabled. Found it to be enabled or nil.")
			}

			By("Applying changes in performance profile")
			profile.Spec.RealTimeKernel = nil
			spec, err := json.Marshal(profile.Spec)
			Expect(err).ToNot(HaveOccurred())

			By("Applying changes in performance profile and waiting until mcp will start updating")
			Expect(testclient.Client.Patch(context.TODO(), profile,
				client.RawPatch(
					types.JSONPatchType,
					[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, spec)),
				),
			)).ToNot(HaveOccurred())

			Expect(profile.Spec.RealTimeKernel).To(BeNil(), "real time kernel setting expected in profile spec but missing")
			By("Checking that the updating MCP status will consistently stay false")
			Consistently(func() corev1.ConditionStatus {
				return mcps.GetConditionStatus(performanceMCP, conditionUpdating)
			}, 30, 5).Should(Equal(corev1.ConditionFalse))

			for _, node := range workerRTNodes {
				err := nodes.HasPreemptRTKernel(context.TODO(), &node)
				Expect(err).To(HaveOccurred())
			}
		})

		AfterAll(func() {
			// return initial configuration
			spec, err := json.Marshal(initialProfile.Spec)
			Expect(err).ToNot(HaveOccurred())
			Expect(testclient.Client.Patch(context.TODO(), profile,
				client.RawPatch(
					types.JSONPatchType,
					[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, spec)),
				),
			)).ToNot(HaveOccurred())
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
		})
	})

	Context("Updating of nodeSelector parameter and node labels", func() {
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

			Expect(testclient.Client.Update(context.TODO(), newCnfNode)).ToNot(HaveOccurred())

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
			Expect(kubeletConfig.TopologyManagerPolicy).ToNot(BeEmpty())
			cmdline, err := nodes.ExecCommandToString(context.TODO(), chkCmdLine, newCnfNode)
			Expect(err).ToNot(HaveOccurred(), "failed to execute %s", chkCmdLine)
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

			cmdline, err := nodes.ExecCommandToString(context.TODO(), chkCmdLine, newCnfNode)
			Expect(err).ToNot(HaveOccurred(), "failed to execute %s", chkCmdLine)
			Expect(cmdline).NotTo(ContainSubstring("tuned.non_isolcpus"))

			kblcfg, err := nodes.GetKubeletConfig(context.TODO(), newCnfNode)
			Expect(kblcfg.ReservedSystemCPUs).NotTo(ContainSubstring("reservedSystemCPUs"))

			Expect(profile.Spec.CPU.Reserved).NotTo(BeNil())
			reservedCPU := string(*profile.Spec.CPU.Reserved)
			cpuMask, err := components.CPUListToHexMask(reservedCPU)
			Expect(err).ToNot(HaveOccurred(), "failed to list in Hex %s", reservedCPU)
			irqBal, err := nodes.ExecCommandToString(context.TODO(), chkIrqbalance, newCnfNode)
			Expect(err).ToNot(HaveOccurred(), "failed to execute %s", chkIrqbalance)
			Expect(irqBal).NotTo(ContainSubstring(cpuMask))
		})

		AfterEach(func() {
			if labelsDeletion == false {
				removeLabels(profile.Spec.NodeSelector, newCnfNode)
			}

			var selectorLabels []string
			for k, v := range oldNodeSelector {
				selectorLabels = append(selectorLabels, fmt.Sprintf(`"%s":"%s"`, k, v))
			}
			nodeSelector := strings.Join(selectorLabels, ",")
			profile.Spec.NodeSelector = oldNodeSelector
			spec, err := json.Marshal(profile.Spec)
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

			performanceMCP, err = mcps.GetByProfile(updatedProfile)
			Expect(err).ToNot(HaveOccurred())
			Expect(testclient.Client.Delete(context.TODO(), mcp)).ToNot(HaveOccurred())
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

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
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

				By("Waiting when mcp finishes updates")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
			}
		})

	})

	Context("Offlined CPU API", Ordered, func() {
		var numaCoreSiblings map[int]map[int][]int
		BeforeAll(func() {
			//Saving the old performance profile
			initialProfile = profile.DeepCopy()

			for _, node := range workerRTNodes {
				onlineCPUCount, err := nodes.ExecCommandToString(context.TODO(), []string{"nproc", "--all"}, &node)
				Expect(err).ToNot(HaveOccurred())

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

			By("Applying changes in performance profile and waiting until mcp will start updating")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

			By("Waiting for MCP being updated")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

			workerRTNodes = getUpdatedNodes()
			//Check offlined cpus are setting correctly
			for _, node := range workerRTNodes {
				offlinedOutput, err := nodes.ExecCommandToString(context.TODO(), []string{"cat", "/sys/devices/system/cpu/offline"}, &node)
				Expect(err).ToNot(HaveOccurred())
				offlinedCPUSet, err := cpuset.Parse(offlinedOutput)
				offlinedCPUSetProfile, err := cpuset.Parse(string(offlined))
				Expect(err).ToNot(HaveOccurred())
				Expect(offlinedCPUSet.Equals(offlinedCPUSetProfile))
			}
		})

		It("[test_id:50964] Offline Higher CPUID's", func() {
			var reserved, isolated, offline []string
			numaTopology := copyNumaCoreSiblings(numaCoreSiblings)
			for numaNode := range numaTopology {
				cores := make([]int, 0)
				for k := range numaTopology[numaNode] {
					cores = append(cores, k)
				}
				sort.Ints(cores)
				// Select the last core id
				higherCoreIds := cores[len(cores)-1]
				// Get cpu siblings from the selected cores and delete the selected cores  from the map
				cpusiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, higherCoreIds)
				offline = append(offline, cpusiblings...)
			}
			offlineCpus := strings.Join(offline, ",")
			// Get reserved core siblings from 0, 1
			for reservedCores := 0; reservedCores < 2; reservedCores++ {
				// Get the cpu siblings from the selected core and delete the siblings
				// from the map. Selected siblings of cores are saved in reservedCpus
				cpusiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, reservedCores)
				reserved = append(reserved, cpusiblings...)
			}
			reservedCpus := strings.Join(reserved, ",")
			// Remaining core siblings available in the
			// numaTopology map is used in isolatedCpus
			for key := range numaTopology {
				for k := range numaTopology[key] {
					cpusiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, k)
					isolated = append(isolated, cpusiblings...)
				}
			}
			isolatedCpus := strings.Join(isolated, ",")
			// Create new performance with offlined
			reservedSet := performancev2.CPUSet(reservedCpus)
			isolatedSet := performancev2.CPUSet(isolatedCpus)
			offlinedSet := performancev2.CPUSet(offlineCpus)
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

			By("Applying changes in performance profile and waiting until mcp will start updating")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

			By("Waiting for MCP being updated")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

			workerRTNodes = getUpdatedNodes()
			// Check offlined cpus are setting correctly
			for _, node := range workerRTNodes {
				offlinedOutput, err := nodes.ExecCommandToString(context.TODO(), []string{"cat", "/sys/devices/system/cpu/offline"}, &node)
				Expect(err).ToNot(HaveOccurred())
				offlinedCPUSet, err := cpuset.Parse(offlinedOutput)
				offlinedCPUSetProfile, err := cpuset.Parse(string(offlinedSet))
				Expect(err).ToNot(HaveOccurred())
				Expect(offlinedCPUSet.Equals(offlinedCPUSetProfile))
			}
		})

		It("[test_id:50965]Offline Middle CPUID's", func() {
			var reserved, isolated, offline []string
			numaTopology := copyNumaCoreSiblings(numaCoreSiblings)
			for key := range numaTopology {
				cores := make([]int, 0)
				for k := range numaTopology[key] {
					cores = append(cores, k)
				}
				sort.Ints(cores)
				middleCoreIds := cores[len(cores)/2]
				siblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, middleCoreIds)
				offline = append(offline, siblings...)
			}
			offlineCpus := strings.Join(offline, ",")
			for reservedCores := 0; reservedCores < 2; reservedCores++ {
				// Get the cpu siblings from the selected core and delete the siblings
				// from the map. Selected siblings of cores are saved in reservedCpus
				siblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, reservedCores)
				reserved = append(reserved, siblings...)
			}
			reservedCpus := strings.Join(reserved, ",")
			// Remaining core siblings available in the
			// numaTopology map is used in isolatedCpus
			for key := range numaTopology {
				for k := range numaTopology[key] {
					siblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, k)
					isolated = append(isolated, siblings...)
				}
			}
			isolatedCpus := strings.Join(isolated, ",")
			// Create new performance with offlined
			reservedSet := performancev2.CPUSet(reservedCpus)
			isolatedSet := performancev2.CPUSet(isolatedCpus)
			offlinedSet := performancev2.CPUSet(offlineCpus)
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

			By("Applying changes in performance profile and waiting until mcp will start updating")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

			By("Waiting for MCP being updated")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

			workerRTNodes = getUpdatedNodes()
			//Check offlined cpus are setting correctly
			for _, node := range workerRTNodes {
				offlinedOutput, err := nodes.ExecCommandToString(context.TODO(), []string{"cat", "/sys/devices/system/cpu/offline"}, &node)
				Expect(err).ToNot(HaveOccurred())
				offlinedCPUSet, err := cpuset.Parse(offlinedOutput)
				offlinedCPUSetProfile, err := cpuset.Parse(string(offlinedSet))
				Expect(err).ToNot(HaveOccurred())
				Expect(offlinedCPUSet.Equals(offlinedCPUSetProfile))
			}
		})

		It("[test_id:50966]verify offlined parameter accepts multiple ranges of cpuid's", func() {
			var reserved, isolated, offlined []string
			numaTopology := copyNumaCoreSiblings(numaCoreSiblings)
			if len(numaCoreSiblings) < 2 {
				Skip(fmt.Sprintf("This test need 2 NUMA nodes, available only %d", len(numaCoreSiblings)))
			}
			if len(numaCoreSiblings[0]) < 20 {
				Skip(fmt.Sprintf("This test needs systems with at least 20 cores per socket"))
			}
			// Get reserved core siblings from 0, 1
			for reservedCores := 0; reservedCores < 2; reservedCores++ {
				// Get the cpu siblings from the selected core and delete the siblings
				// from the map. Selected siblings of cores are saved in reservedCpus
				siblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, reservedCores)
				reserved = append(reserved, siblings...)
			}
			reservedCpus := strings.Join(reserved, ",")
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
					offlined = append(offlined, siblings...)
				}
			}
			offlinedCpus := nodes.GetNumaRanges(strings.Join(offlined, ","))
			// Remaining core siblings available in the numaTopology
			// map is used in isolatedCpus
			for key := range numaTopology {
				for k := range numaTopology[key] {
					siblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, k)
					isolated = append(isolated, siblings...)
				}
			}
			isolatedCpus := strings.Join(isolated, ",")
			// Create new performance with offlined
			reservedSet := performancev2.CPUSet(reservedCpus)
			isolatedSet := performancev2.CPUSet(isolatedCpus)
			offlinedSet := performancev2.CPUSet(offlinedCpus)
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

			By("Applying changes in performance profile and waiting until mcp will start updating")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

			By("Waiting for MCP being updated")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

			workerRTNodes = getUpdatedNodes()
			//Check offlined cpus are setting correctly
			for _, node := range workerRTNodes {
				offlinedOutput, err := nodes.ExecCommandToString(context.TODO(), []string{"cat", "/sys/devices/system/cpu/offline"}, &node)
				Expect(err).ToNot(HaveOccurred())
				offlinedCPUSet, err := cpuset.Parse(offlinedOutput)
				offlinedCPUSetProfile, err := cpuset.Parse(string(offlinedSet))
				Expect(err).ToNot(HaveOccurred())
				Expect(offlinedCPUSet.Equals(offlinedCPUSetProfile))
			}
		})

		It("[test_id:50968]verify cpus mentioned in reserved or isolated cannot be offline", func() {
			var reserved, isolated []string
			numaTopology := copyNumaCoreSiblings(numaCoreSiblings)
			if len(numaCoreSiblings) < 2 {
				Skip(fmt.Sprintf("This test need 2 NUMA nodes, available only %d", len(numaCoreSiblings)))
			}
			// Get reserved core siblings from 0, 1
			for reservedCores := 0; reservedCores < 2; reservedCores++ {
				// Get the cpu siblings from the selected core and delete the siblings
				// from the map. Selected siblings of cores are saved in reservedCpus
				siblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, reservedCores)
				reserved = append(reserved, siblings...)
			}
			reservedCpus := strings.Join(reserved, ",")
			// Remaining core siblings available in the
			// numaTopology map is used in isolatedCpus
			for key := range numaTopology {
				for k := range numaTopology[key] {
					siblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, k)
					isolated = append(isolated, siblings...)
				}
			}
			isolatedCpus := strings.Join(isolated, ",")
			//combine both isolated and reserved
			totalCpus := fmt.Sprintf("%s,%s", reservedCpus, isolatedCpus)
			totalCpuSlice := strings.Split(totalCpus, ",")
			// get partial cpus from the combined cpus
			partialCpulist := totalCpuSlice[:len(totalCpuSlice)/2]
			offlineCpus := strings.Join(partialCpulist, ",")
			// Create new performance with offlined
			reservedSet := performancev2.CPUSet(reservedCpus)
			isolatedSet := performancev2.CPUSet(isolatedCpus)
			offlinedSet := performancev2.CPUSet(offlineCpus)
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
				err := testclient.Client.Update(context.TODO(), profile)
				if err != nil {
					statusErr, _ := err.(*errors.StatusError)
					return statusErr.Status().Message
				}
				return fmt.Sprint("Profile applied successfully")
			}, time.Minute, 5*time.Second).Should(ContainSubstring("isolated and offlined cpus overlap"))
		})

		It("[test_id:50970]Offline CPUID's from multiple numa nodes", func() {
			var reserved, isolated, offlined []string
			numaTopology := copyNumaCoreSiblings(numaCoreSiblings)
			if len(numaCoreSiblings) < 2 {
				Skip(fmt.Sprintf("This test need 2 NUMA nodes, available only %d", len(numaCoreSiblings)))
			}
			// Get reserved core siblings from 0, 1
			for reservedCores := 0; reservedCores < 2; reservedCores++ {
				// Get the cpu siblings from the selected core and delete the siblings
				// from the map. Selected siblings of cores are saved in reservedCpus
				siblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, reservedCores)
				reserved = append(reserved, siblings...)
			}
			reservedCpus := strings.Join(reserved, ",")

			discreteCores := []int{3, 13, 15, 24, 29}
			for _, v := range discreteCores {
				siblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, v)
				offlined = append(offlined, siblings...)
			}
			offlineCpus := strings.Join(offlined, ",")
			for key := range numaTopology {
				for k := range numaTopology[key] {
					cpusiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaTopology, k)
					isolated = append(isolated, cpusiblings...)
				}
			}
			isolatedCpus := strings.Join(isolated, ",")
			// Create new performance with offlined
			reservedSet := performancev2.CPUSet(reservedCpus)
			isolatedSet := performancev2.CPUSet(isolatedCpus)
			offlinedSet := performancev2.CPUSet(offlineCpus)
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

			By("Applying changes in performance profile and waiting until mcp will start updating")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

			By("Waiting for MCP being updated")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

			workerRTNodes = getUpdatedNodes()
			//Check offlined cpus are setting correctly
			for _, node := range workerRTNodes {
				offlinedOutput, err := nodes.ExecCommandToString(context.TODO(), []string{"cat", "/sys/devices/system/cpu/offline"}, &node)
				Expect(err).ToNot(HaveOccurred())
				offlinedCPUSet, err := cpuset.Parse(offlinedOutput)
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
				spec, err := json.Marshal(initialProfile.Spec)
				Expect(err).ToNot(HaveOccurred())
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

			findcmd := `find /sys/devices/system/cpu/cpu* -type f -name online -exec cat {} \;`
			checkCpuStatusCmd := []string{"bash", "-c", findcmd}
			for _, node := range workerRTNodes {
				stdout, err := nodes.ExecCommandToString(context.TODO(), checkCpuStatusCmd, &node)
				Expect(err).NotTo(HaveOccurred())
				v := strings.Split(stdout, "\n")
				for _, val := range v {
					Expect(val).To(Equal("1"))
				}
			}
		})
	})

	Context("[rfe_id:54374][rps_mask] Network Stack Pinning", func() {

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

			By("Applying changes in performance profile and waiting until mcp will start updating")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

			By("Waiting when mcp finishes updates")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
		})

		It("[test_id:56006]Verify systemd unit file gets updated when the reserved cpus are modified", func() {
			var reserved, isolated []string
			var onlineCPUInt int
			for _, node := range workerRTNodes {
				onlineCPUCount, err := nodes.ExecCommandToString(context.TODO(), []string{"nproc", "--all"}, &node)
				Expect(err).ToNot(HaveOccurred())
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
				middleCoreIds := coreids[len(coreids)/2]
				coresiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaCoreSiblings, middleCoreIds)
				reserved = append(reserved, coresiblings...)
			}
			reservedCpus := strings.Join(reserved, ",")
			for numaNode := range numaCoreSiblings {
				for coreids := range numaCoreSiblings[numaNode] {
					coresiblings := nodes.GetAndRemoveCpuSiblingsFromMap(numaCoreSiblings, coreids)
					isolated = append(isolated, coresiblings...)
				}
			}
			isolatedCpus := strings.Join(isolated, ",")
			// Update performance profile
			reservedSet := performancev2.CPUSet(reservedCpus)
			isolatedSet := performancev2.CPUSet(isolatedCpus)

			By("Update reserved, isolated parameters")
			profile.Spec.CPU = &performancev2.CPU{
				Reserved: &reservedSet,
				Isolated: &isolatedSet,
			}

			By("Updating the performance profile")
			profiles.UpdateWithRetry(profile)

			By("Applying changes in performance profile and waiting until mcp will start updating")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

			By("Waiting for MCP being updated")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

			//Check RPS Mask after profile is updated with New reserved Cpus
			expectedRPSCPUs, err := cpuset.Parse(string(*profile.Spec.CPU.Reserved))
			Expect(err).ToNot(HaveOccurred())
			for _, node := range workerRTNodes {
				// Verify the systemd RPS service uses the correct RPS mask
				var maskContent string
				cmd := []string{"sysctl", "-n", "net.core.rps_default_mask"}
				maskContent, err := nodes.ExecCommandToString(context.TODO(), cmd, &node)
				Expect(err).ToNot(HaveOccurred(), "failed to exec command %q on node %q", cmd, node)
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
				devsRPS, err := nodes.ExecCommandToString(context.TODO(), cmd, &node)
				Expect(err).ToNot(HaveOccurred(), "failed to exec command %q on node %q", cmd, node.Name)
				for _, devRPS := range strings.Split(devsRPS, "\n") {
					rpsCPUs, err = components.CPUMaskToCPUSet(devRPS)
					Expect(err).ToNot(HaveOccurred())
					Expect(rpsCPUs.Equals(expectedRPSCPUs)).To(BeTrue(), "a host device rps mask is different from the reserved CPUs")
				}
			}
		})

		It("[test_id:54191]Verify RPS Mask is not applied when RealtimeHint is disabled", func() {
			By("Modifying profile")
			profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
				HighPowerConsumption:  pointer.Bool(false),
				RealTime:              pointer.Bool(false),
				PerPodPowerManagement: pointer.Bool(false),
			}

			profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
				Enabled: pointer.Bool(false),
			}
			By("Updating the performance profile")
			profiles.UpdateWithRetry(profile)

			By("Applying changes in performance profile and waiting until mcp will start updating")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

			By("Waiting for MCP being updated")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

			for _, node := range workerRTNodes {
				// Verify the systemd RPS services were not created
				cmd := []string{"ls", "/rootfs/etc/systemd/system/update-rps@.service"}
				_, err := nodes.ExecCommandToString(context.TODO(), cmd, &node)
				Expect(err).To(HaveOccurred())
			}
		})
	})

	Context("ContainerRuntimeConfig", Ordered, func() {
		var ctrcfg *machineconfigv1.ContainerRuntimeConfig
		const ContainerRuntimeConfigName = "ctrcfg-test"
		mcp := &machineconfigv1.MachineConfigPool{}
		BeforeAll(func() {
			key := types.NamespacedName{
				Name: performanceMCP,
			}
			Expect(testclient.Client.Get(context.TODO(), key, mcp)).ToNot(HaveOccurred(), "cannot get MCP %q", performanceMCP)
			By("checking if ContainerRuntimeConfig object already exists")
			ctrcfg, err = getContainerRuntimeConfigFrom(context.TODO(), profile, mcp)
			Expect(err).ToNot(HaveOccurred(), "failed to get ContainerRuntimeConfig from profile %q mcp %q", profile.Name, mcp.Name)
		})

		When("is not given", func() {
			It("should run high-performance runtimes class with runc as container-runtime", func() {
				if ctrcfg != nil {
					Skip("runc is not the default runtime configuration")
				}
				cmd := []string{"cat", "/rootfs/etc/crio/crio.conf.d/99-runtimes.conf"}
				for i := 0; i < len(workerRTNodes); i++ {
					out, err := nodes.ExecCommandToString(context.TODO(), cmd, &workerRTNodes[i])
					Expect(err).ToNot(HaveOccurred(), "cannot get 99-runtimes.conf from %q", workerRTNodes[i].Name)
					By(fmt.Sprintf("checking node: %q", workerRTNodes[i].Name))
					Expect(out).To(ContainSubstring("/bin/runc"))
					Expect(out).To(ContainSubstring("/run/runc"))
				}
			})
		})
		When("updates the default runtime to crun", func() {
			It("should run high-performance runtimes class with crun as container-runtime", func() {
				if ctrcfg == nil {
					testlog.Infof("ContainerRuntimeConfig not exist")
					ctrcfg = newContainerRuntimeConfig(ContainerRuntimeConfigName, profile, mcp)
					By(fmt.Sprintf("creating ContainerRuntimeConfig %q", ctrcfg.Name))
					Expect(testclient.Client.Create(context.TODO(), ctrcfg)).ToNot(HaveOccurred(), "failed to create ctrcfg %#v", ctrcfg)

					DeferCleanup(func() {
						Expect(testclient.Client.Delete(context.TODO(), ctrcfg)).ToNot(HaveOccurred(), "failed to delete ctfcfg %#v", ctrcfg)
						By(fmt.Sprintf("waiting for mcp %q transition to UPDATING state", performanceMCP))
						mcps.WaitForConditionFunc(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue, getMCPConditionStatus)
						By(fmt.Sprintf("waiting for mcp %q transition to UPDATED state", performanceMCP))
						mcps.WaitForConditionFunc(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue, getMCPConditionStatus)
					})

					By(fmt.Sprintf("waiting for mcp %q transition to UPDATING state", performanceMCP))
					mcps.WaitForConditionFunc(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue, getMCPConditionStatus)
					By(fmt.Sprintf("waiting for mcp %q transition to UPDATED state", performanceMCP))
					mcps.WaitForConditionFunc(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue, getMCPConditionStatus)
				}
				Expect(ctrcfg.Spec.ContainerRuntimeConfig.DefaultRuntime == machineconfigv1.ContainerRuntimeDefaultRuntimeCrun).To(BeTrue())
				cmd := []string{"cat", "/rootfs/etc/crio/crio.conf.d/99-runtimes.conf"}
				for i := 0; i < len(workerRTNodes); i++ {
					out, err := nodes.ExecCommandToString(context.TODO(), cmd, &workerRTNodes[i])
					Expect(err).ToNot(HaveOccurred(), "cannot get 99-runtimes.conf from %q", workerRTNodes[i].Name)
					By(fmt.Sprintf("checking node: %q", workerRTNodes[i].Name))
					Expect(out).To(ContainSubstring("/usr/bin/crun"))
					Expect(out).To(ContainSubstring("/run/crun"))
				}
			})
		})
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
		result, err := nodes.ExecCommandToString(ctx, nodeCmd, node)
		if err != nil {
			return 0, err
		}
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
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: machineconfigv1.ContainerRuntimeConfigSpec{
			MachineConfigPoolSelector: &metav1.LabelSelector{
				MatchLabels: profilecomponent.GetMachineConfigPoolSelector(profile, profileMCP),
			},
			ContainerRuntimeConfig: &machineconfigv1.ContainerRuntimeConfiguration{
				DefaultRuntime: machineconfigv1.ContainerRuntimeDefaultRuntimeCrun,
			},
		},
	}
}

func getContainerRuntimeConfigFrom(ctx context.Context, profile *performancev2.PerformanceProfile, mcp *machineconfigv1.MachineConfigPool) (*machineconfigv1.ContainerRuntimeConfig, error) {
	ctrcfgList := &machineconfigv1.ContainerRuntimeConfigList{}
	if err := testclient.Client.List(ctx, ctrcfgList); err != nil {
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
