package __performance_update

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"gopkg.in/ini.v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/tuned"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cluster"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
)

type checkFunction func(*corev1.Node) (string, error)

var _ = Describe("[rfe_id:28761][performance] Updating parameters in performance profile", func() {
	var workerRTNodes []corev1.Node
	var profile, initialProfile *performancev2.PerformanceProfile
	var performanceMCP string
	var err error

	chkCmdLine := []string{"cat", "/proc/cmdline"}
	chkKubeletConfig := []string{"cat", "/rootfs/etc/kubernetes/kubelet.conf"}
	chkIrqbalance := []string{"cat", "/rootfs/etc/sysconfig/irqbalance"}

	chkCmdLineFn := func(node *corev1.Node) (string, error) {
		return nodes.ExecCommandOnNode(chkCmdLine, node)
	}
	chkKubeletConfigFn := func(node *corev1.Node) (string, error) {
		return nodes.ExecCommandOnNode(chkKubeletConfig, node)
	}

	chkHugepages2MFn := func(node *corev1.Node) (string, error) {
		count, err := countHugepagesOnNode(node, 2)
		if err != nil {
			return "", err
		}
		return strconv.Itoa(count), nil
	}

	chkHugepages1GFn := func(node *corev1.Node) (string, error) {
		count, err := countHugepagesOnNode(node, 1024)
		if err != nil {
			return "", err
		}
		return strconv.Itoa(count), nil
	}

	nodeLabel := testutils.NodeSelectorLabels

	var RunningOnSingleNode bool

	testutils.CustomBeforeAll(func() {
		isSNO, err := cluster.IsSingleNode()
		Expect(err).ToNot(HaveOccurred())
		RunningOnSingleNode = isSNO
	})

	BeforeEach(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}

		workerRTNodes = getUpdatedNodes()
		profile, err = profiles.GetByNodeLabels(nodeLabel)
		Expect(err).ToNot(HaveOccurred())
		performanceMCP, err = mcps.GetByProfile(profile)
		Expect(err).ToNot(HaveOccurred())

		// Verify that worker and performance MCP have updated state equals to true
		for _, mcpName := range []string{testutils.RoleWorker, performanceMCP} {
			mcps.WaitForCondition(mcpName, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
		}
	})

	Context("Verify hugepages count split on two NUMA nodes", Ordered, func() {
		hpSize2M := performancev2.HugePageSize("2M")

		testutils.CustomBeforeAll(func() {
			By("Modifying profile")
			initialProfile = profile.DeepCopy()
		})

		DescribeTable("Verify that profile parameters were updated", func(hpCntOnNuma0 int32, hpCntOnNuma1 int32) {
			By("Verifying cluster configuration matches the requirement")
			for _, node := range workerRTNodes {
				numaInfo, err := nodes.GetNumaNodes(&node)
				Expect(err).ToNot(HaveOccurred())
				if len(numaInfo) < 2 {
					Skip(fmt.Sprintf("This test need 2 NUMA nodes.The number of NUMA nodes on node %s < 2", node.Name))
				}
			}
			//have total of 4 cpus so VMs can handle running the configuration
			numaInfo, _ := nodes.GetNumaNodes(&workerRTNodes[0])
			cpuSlice := numaInfo[0][0:4]
			isolated := performancev2.CPUSet(fmt.Sprintf("%d-%d", cpuSlice[2], cpuSlice[3]))
			reserved := performancev2.CPUSet(fmt.Sprintf("%d-%d", cpuSlice[0], cpuSlice[1]))

			By("Modifying profile")
			profile.Spec.CPU = &performancev2.CPU{
				BalanceIsolated: pointer.BoolPtr(false),
				Reserved:        &reserved,
				Isolated:        &isolated,
			}
			profile.Spec.HugePages = &performancev2.HugePages{
				DefaultHugePagesSize: &hpSize2M,
				Pages: []performancev2.HugePage{
					{
						Count: hpCntOnNuma0,
						Size:  hpSize2M,
						Node:  pointer.Int32Ptr(0),
					},
					{
						Count: hpCntOnNuma1,
						Size:  hpSize2M,
						Node:  pointer.Int32Ptr(1),
					},
				},
			}
			profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
				Enabled: pointer.BoolPtr(true),
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
					result, err := nodes.ExecCommandOnNode(nodeCmd, &node)
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
			By("Modifying profile")
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
				BalanceIsolated: pointer.BoolPtr(false),
				Reserved:        &reserved,
				Isolated:        &isolated,
			}
			profile.Spec.NUMA = &performancev2.NUMA{
				TopologyPolicy: &policy,
			}
			profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
				Enabled: pointer.BoolPtr(false),
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

		DescribeTable("Verify that profile parameters were updated", func(cmdFn checkFunction, parameter []string, shouldContain bool, useRegex bool) {
			for _, node := range workerRTNodes {
				for _, param := range parameter {
					result, err := cmdFn(&node)
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
			Entry("[test_id:34081] verify that hugepages size and count updated", chkCmdLineFn, []string{"default_hugepagesz=2M", "hugepagesz=1G", "hugepages=3"}, true, false),
			Entry("[test_id:28070] verify that hugepages updated (NUMA node unspecified)", chkCmdLineFn, []string{"hugepagesz=2M"}, true, false),
			Entry("verify that the right number of hugepages 1G is available on the system", chkHugepages1GFn, []string{"3"}, true, false),
			Entry("verify that the right number of hugepages 2M is available on the system", chkHugepages2MFn, []string{"256"}, true, false),
			Entry("[test_id:28025] verify that cpu affinity mask was updated", chkCmdLineFn, []string{"tuned.non_isolcpus=.*9"}, true, true),
			Entry("[test_id:28071] verify that cpu balancer disabled", chkCmdLineFn, []string{"isolcpus=domain,managed_irq,1-2"}, true, false),
			Entry("[test_id:28071] verify that cpu balancer disabled", chkCmdLineFn, []string{"systemd.cpu_affinity=0,3"}, true, false),
			// kubelet.conf changed formatting, there is a space after colons atm. Let's deal with both cases with a regex
			Entry("[test_id:28935] verify that reservedSystemCPUs was updated", chkKubeletConfigFn, []string{`"reservedSystemCPUs": ?"0,3"`}, true, true),
			Entry("[test_id:28760] verify that topologyManager was updated", chkKubeletConfigFn, []string{`"topologyManagerPolicy": ?"best-effort"`}, true, true),
		)

		It("[test_id:27738] should succeed to disable the RT kernel", func() {
			for _, node := range workerRTNodes {
				err := nodes.HasPreemptRTKernel(&node)
				Expect(err).To(HaveOccurred())
			}
		})

		It("[test_id:28612]Verify that Kernel arguments can me updated (added, removed) thru performance profile", func() {
			for _, node := range workerRTNodes {
				cmdline, err := nodes.ExecCommandOnNode(chkCmdLine, &node)
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
				err := nodes.HasPreemptRTKernel(&node)
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
		var labelsDeletion = false
		newNodeSelector := map[string]string{newLabel: ""}

		//fetch the latest profile
		profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		//Expect(err).ToNot(HaveOccurred())
		var oldMcpSelector, oldNodeSelector map[string]string
		var removeLabels func(map[string]string, *corev1.Node) error

		//fetch existing MCP Selector if exists in profile
		BeforeEach(func() {

			//fetch existing MCP Selector if exists in profile
			if profile.Spec.MachineConfigPoolSelector != nil {
				oldMcpSelector = profile.Spec.DeepCopy().MachineConfigPoolSelector
			}

			//fetch existing Node Selector
			if profile.Spec.NodeSelector != nil {
				oldNodeSelector = profile.Spec.DeepCopy().NodeSelector
			}
			removeLabels = func(nodeSelector map[string]string, targetNode *corev1.Node) error {
				patchNode := false
				for l := range nodeSelector {
					if _, ok := targetNode.Labels[l]; ok {
						patchNode = true
						delete(targetNode.Labels, l)
					}
				}
				if !patchNode {
					return nil
				}
				label, err := json.Marshal(targetNode.Labels)
				Expect(err).ToNot(HaveOccurred())
				Expect(testclient.Client.Patch(context.TODO(), targetNode,
					client.RawPatch(
						types.JSONPatchType,
						[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/metadata/labels", "value": %s }]`, label)),
					),
				)).ToNot(HaveOccurred())
				mcps.WaitForCondition(testutils.RoleWorker, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

				By("Waiting when MCP Worker complete updates and verifying that node reverted back configuration")
				mcps.WaitForCondition(testutils.RoleWorker, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
				return nil
			}

			nonPerformancesWorkers, err := nodes.GetNonPerformancesWorkers(profile.Spec.NodeSelector)
			Expect(err).ToNot(HaveOccurred())
			// we need atleast 2 non performance worker nodes to satisfy pod distribution budget
			if len(nonPerformancesWorkers) > 1 {
				newCnfNode = &nonPerformancesWorkers[0]
			}
			if newCnfNode == nil {
				Skip("Skipping the test - cluster does not have another available worker node ")
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
			kubeletConfig, err := nodes.GetKubeletConfig(newCnfNode)
			Expect(kubeletConfig.TopologyManagerPolicy).ToNot(BeEmpty())
			cmdline, err := nodes.ExecCommandOnNode(chkCmdLine, newCnfNode)
			Expect(err).ToNot(HaveOccurred(), "failed to execute %s", chkCmdLine)
			Expect(cmdline).To(ContainSubstring("tuned.non_isolcpus"))

		})

		It("[test_id:27484]Verifies that node is reverted to plain worker when the extra labels are removed", func() {
			By("Deleting cnf labels from the node")
			err = removeLabels(profile.Spec.NodeSelector, newCnfNode)
			Expect(err).ToNot(HaveOccurred())
			labelsDeletion = true
			// Check if node is Ready
			for i := range newCnfNode.Status.Conditions {
				if newCnfNode.Status.Conditions[i].Type == corev1.NodeReady {
					Expect(newCnfNode.Status.Conditions[i].Status).To(Equal(corev1.ConditionTrue))
				}
			}

			// check that the configs reverted
			err = nodes.HasPreemptRTKernel(newCnfNode)
			Expect(err).To(HaveOccurred())

			cmdline, err := nodes.ExecCommandOnNode(chkCmdLine, newCnfNode)
			Expect(err).ToNot(HaveOccurred(), "failed to execute %s", chkCmdLine)
			Expect(cmdline).NotTo(ContainSubstring("tuned.non_isolcpus"))

			kblcfg, err := nodes.GetKubeletConfig(newCnfNode)
			Expect(kblcfg.ReservedSystemCPUs).NotTo(ContainSubstring("reservedSystemCPUs"))

			Expect(profile.Spec.CPU.Reserved).NotTo(BeNil())
			reservedCPU := string(*profile.Spec.CPU.Reserved)
			cpuMask, err := components.CPUListToHexMask(reservedCPU)
			Expect(err).ToNot(HaveOccurred(), "failed to list in Hex %s", reservedCPU)
			irqBal, err := nodes.ExecCommandOnNode(chkIrqbalance, newCnfNode)
			Expect(err).ToNot(HaveOccurred(), "failed to execute %s", chkIrqbalance)
			Expect(irqBal).NotTo(ContainSubstring(cpuMask))
		})

		AfterEach(func() {

			if labelsDeletion == false {
				err = removeLabels(profile.Spec.NodeSelector, newCnfNode)
				Expect(err).ToNot(HaveOccurred())
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

	Context("WorkloadHints", func() {
		var testpod *corev1.Pod
		BeforeEach(func() {
			By("Saving the old performance profile")
			initialProfile = profile.DeepCopy()
		})
		When("workloadHint RealTime is disabled", func() {
			It("should update kernel arguments and tuned accordingly to realTime Hint enabled by default", func() {
				By("Modifying profile")
				profile.Spec.WorkloadHints = nil

				profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
					Enabled: pointer.BoolPtr(false),
				}

				By("Updating the performance profile")
				profiles.UpdateWithRetry(profile)

				By("Applying changes in performance profile and waiting until mcp will start updating")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

				By("Waiting for MCP being updated")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

				stalldEnabled, rtKernel := true, false
				noHzParam := fmt.Sprintf("nohz_full=%s", *profile.Spec.CPU.Isolated)
				sysctlMap := map[string]string{
					"kernel.hung_task_timeout_secs": "600",
					"kernel.nmi_watchdog":           "0",
					"kernel.sched_rt_runtime_us":    "-1",
					"vm.stat_interval":              "10",
				}
				kernelParameters := []string{noHzParam, "tsc=nowatchdog", "nosoftlockup", "nmi_watchdog=0", "mce=off", "skew_tick=1"}
				checkTunedParameters(workerRTNodes, stalldEnabled, sysctlMap, kernelParameters, rtKernel)
			})
		})

		When("RealTime Workload with RealTime Kernel set to false", func() {
			It("[test_id:50991][crit:high][vendor:cnf-qe@redhat.com][level:acceptance]should update kernel arguments and tuned accordingly", func() {
				By("Modifying profile")
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption: pointer.Bool(false),
					RealTime:             pointer.Bool(true),
				}

				profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
					Enabled: pointer.BoolPtr(false),
				}

				By("Updating the performance profile")
				profiles.UpdateWithRetry(profile)

				By("Applying changes in performance profile and waiting until mcp will start updating")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

				By("Waiting for MCP being updated")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

				stalldEnabled, rtKernel := true, false
				noHzParam := fmt.Sprintf("nohz_full=%s", *profile.Spec.CPU.Isolated)
				sysctlMap := map[string]string{
					"kernel.hung_task_timeout_secs": "600",
					"kernel.nmi_watchdog":           "0",
					"kernel.sched_rt_runtime_us":    "-1",
					"vm.stat_interval":              "10",
				}
				kernelParameters := []string{noHzParam, "tsc=nowatchdog", "nosoftlockup", "nmi_watchdog=0", "mce=off", "skew_tick=1"}
				checkTunedParameters(workerRTNodes, stalldEnabled, sysctlMap, kernelParameters, rtKernel)
			})
		})
		When("HighPower Consumption workload enabled", func() {
			It("[test_id:50992][crit:high][vendor:cnf-qe@redhat.com][level:acceptance]should update kernel arguments and tuned accordingly", func() {
				By("Modifying profile")
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption: pointer.Bool(true),
					RealTime:             pointer.Bool(false),
				}

				profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
					Enabled: pointer.BoolPtr(false),
				}

				By("Updating the performance profile")
				profiles.UpdateWithRetry(profile)

				By("Applying changes in performance profile and waiting until mcp will start updating")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

				By("Waiting for MCP being updated")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

				stalldEnabled, rtKernel := false, false
				sysctlMap := map[string]string{
					"kernel.hung_task_timeout_secs": "600",
					"kernel.nmi_watchdog":           "0",
					"kernel.sched_rt_runtime_us":    "950000",
					"vm.stat_interval":              "10",
				}
				kernelParameters := []string{"processor.max_cstate=1", "intel_idle.max_cstate=0"}
				checkTunedParameters(workerRTNodes, stalldEnabled, sysctlMap, kernelParameters, rtKernel)
			})
		})

		When("realtime and high power consumption enabled", func() {
			It("[test_id:50993][crit:high][vendor:cnf-qe@redhat.com][level:acceptance]should update kernel arguments and tuned accordingly", func() {
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption:  pointer.BoolPtr(true),
					RealTime:              pointer.BoolPtr(true),
					PerPodPowerManagement: pointer.BoolPtr(false),
				}
				By("Patching the performance profile with workload hints")
				workloadHints, err := json.Marshal(profile.Spec.WorkloadHints)
				Expect(err).ToNot(HaveOccurred())

				Expect(testclient.Client.Patch(context.TODO(), profile,
					client.RawPatch(
						types.JSONPatchType,
						[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec/workloadHints", "value": %s }]`, workloadHints)),
					),
				)).ToNot(HaveOccurred())

				By("Applying changes in performance profile and waiting until mcp will start updating")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

				By("Waiting when mcp finishes updates")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

				stalldEnabled, rtKernel := true, true
				noHzParam := fmt.Sprintf("nohz_full=%s", *profile.Spec.CPU.Isolated)
				sysctlMap := map[string]string{
					"kernel.hung_task_timeout_secs": "600",
					"kernel.nmi_watchdog":           "0",
					"kernel.sched_rt_runtime_us":    "-1",
					"vm.stat_interval":              "10",
				}
				kernelParameters := []string{noHzParam, "tsc=nowatchdog", "nosoftlockup", "nmi_watchdog=0", "mce=off", "skew_tick=1",
					"processor.max_cstate=1", "intel_idle.max_cstate=0", "intel_pstate=disable", "idle=poll"}
				checkTunedParameters(workerRTNodes, stalldEnabled, sysctlMap, kernelParameters, rtKernel)
			})
		})

		When("perPodPowerManagent enabled", func() {
			It("[test_id:54177]should update kernel arguments and tuned accordingly", func() {
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					PerPodPowerManagement: pointer.BoolPtr(true),
					HighPowerConsumption:  pointer.BoolPtr(false),
					RealTime:              pointer.BoolPtr(true),
				}
				By("Patching the performance profile with workload hints")
				workloadHints, err := json.Marshal(profile.Spec.WorkloadHints)
				Expect(err).ToNot(HaveOccurred())

				Expect(testclient.Client.Patch(context.TODO(), profile,
					client.RawPatch(
						types.JSONPatchType,
						[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec/workloadHints", "value": %s }]`, workloadHints)),
					),
				)).ToNot(HaveOccurred())

				By("Applying changes in performance profile and waiting until mcp will start updating")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

				By("Waiting when mcp finishes updates")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

				By("Verifying node kernel arguments")
				cmdline, err := nodes.ExecCommandOnMachineConfigDaemon(&workerRTNodes[0], []string{"cat", "/proc/cmdline"})
				Expect(err).ToNot(HaveOccurred())
				Expect(cmdline).To(ContainSubstring("intel_pstate=passive"))
				Expect(cmdline).ToNot(ContainSubstring("intel_pstate=disable"))

				By("Verifying tuned profile")
				key := types.NamespacedName{
					Name:      components.GetComponentName(profile.Name, components.ProfileNamePerformance),
					Namespace: components.NamespaceNodeTuningOperator,
				}
				tuned := &tunedv1.Tuned{}
				err = testclient.Client.Get(context.TODO(), key, tuned)
				Expect(err).ToNot(HaveOccurred(), "cannot find the Cluster Node Tuning Operator object")
				tunedData := getTunedStructuredData(profile)
				cpuSection, err := tunedData.GetSection("cpu")
				Expect(err).ToNot(HaveOccurred())
				Expect(cpuSection.Key("enabled").String()).To(Equal("false"))
			})

			It("[test_id:54178]Verify System is tuned when updating from HighPowerConsumption to PerPodPowermanagment", func() {

				// This test requires real hardware with powermanagement settings done on BIOS
				// Using numa nodes to check if we are running on real hardware.
				checkHardwareCapability(workerRTNodes)
				// First enable HighPowerConsumption
				By("Modifying profile")
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption:  pointer.Bool(true),
					RealTime:              pointer.Bool(true),
					PerPodPowerManagement: pointer.BoolPtr(false),
				}
				if !*profile.Spec.RealTimeKernel.Enabled {
					profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
						Enabled: pointer.BoolPtr(true),
					}
				}

				By("Patching the performance profile with workload hints")
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

				stalldEnabled, rtKernel := true, true
				noHzParam := fmt.Sprintf("nohz_full=%s", *profile.Spec.CPU.Isolated)
				sysctlMap := map[string]string{
					"kernel.hung_task_timeout_secs": "600",
					"kernel.nmi_watchdog":           "0",
					"kernel.sched_rt_runtime_us":    "-1",
					"vm.stat_interval":              "10",
				}

				kernelParameters := []string{noHzParam, "tsc=nowatchdog", "nosoftlockup", "nmi_watchdog=0", "mce=off", "skew_tick=1",
					"processor.max_cstate=1", "intel_idle.max_cstate=0", "intel_pstate=disable", "idle=poll"}
				checkTunedParameters(workerRTNodes, stalldEnabled, sysctlMap, kernelParameters, rtKernel)

				//Update the profile to disable HighPowerConsumption and enable PerPodPowerManagment
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption:  pointer.Bool(false),
					RealTime:              pointer.Bool(true),
					PerPodPowerManagement: pointer.BoolPtr(true),
				}
				if !*profile.Spec.RealTimeKernel.Enabled {
					profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
						Enabled: pointer.BoolPtr(true),
					}
				}

				By("Patching the performance profile with workload hints")
				newspec, err := json.Marshal(profile.Spec)
				Expect(err).ToNot(HaveOccurred())

				Expect(testclient.Client.Patch(context.TODO(), profile,
					client.RawPatch(
						types.JSONPatchType,
						[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, newspec)),
					),
				)).ToNot(HaveOccurred())

				By("Applying changes in performance profile and waiting until mcp will start updating")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

				By("Waiting when mcp finishes updates")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

				stalldEnabled, rtKernel = true, true
				noHzParam = fmt.Sprintf("nohz_full=%s", *profile.Spec.CPU.Isolated)
				sysctlMap = map[string]string{
					"kernel.hung_task_timeout_secs": "600",
					"kernel.nmi_watchdog":           "0",
					"kernel.sched_rt_runtime_us":    "-1",
					"vm.stat_interval":              "10",
				}

				kernelParameters = []string{noHzParam, "tsc=nowatchdog", "nosoftlockup", "nmi_watchdog=0", "mce=off", "skew_tick=1", "intel_pstate=passive"}
				checkTunedParameters(workerRTNodes, stalldEnabled, sysctlMap, kernelParameters, rtKernel)

			})

			It("[test_id:54179]Verify System is tuned when reverting from PerPodPowerManagement to HighPowerConsumption", func() {

				// This test requires real hardware with powermanagement settings done on BIOS
				// Using numa nodes to check if we are running on real hardware.
				checkHardwareCapability(workerRTNodes)
				// First enable HighPowerConsumption
				By("Modifying profile")
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption:  pointer.Bool(false),
					RealTime:              pointer.Bool(true),
					PerPodPowerManagement: pointer.BoolPtr(true),
				}
				if !*profile.Spec.RealTimeKernel.Enabled {
					profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
						Enabled: pointer.BoolPtr(true),
					}
				}

				By("Patching the performance profile with workload hints")
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

				stalldEnabled, rtKernel := true, true
				noHzParam := fmt.Sprintf("nohz_full=%s", *profile.Spec.CPU.Isolated)
				sysctlMap := map[string]string{
					"kernel.hung_task_timeout_secs": "600",
					"kernel.nmi_watchdog":           "0",
					"kernel.sched_rt_runtime_us":    "-1",
					"vm.stat_interval":              "10",
				}

				kernelParameters := []string{noHzParam, "tsc=nowatchdog", "nosoftlockup", "nmi_watchdog=0", "mce=off", "skew_tick=1", "intel_pstate=passive"}
				checkTunedParameters(workerRTNodes, stalldEnabled, sysctlMap, kernelParameters, rtKernel)

				//Update the profile to disable HighPowerConsumption and enable PerPodPowerManagment
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption:  pointer.Bool(true),
					RealTime:              pointer.Bool(true),
					PerPodPowerManagement: pointer.BoolPtr(false),
				}
				if !*profile.Spec.RealTimeKernel.Enabled {
					profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
						Enabled: pointer.BoolPtr(true),
					}
				}

				By("Patching the performance profile with workload hints")
				newspec, err := json.Marshal(profile.Spec)
				Expect(err).ToNot(HaveOccurred())

				Expect(testclient.Client.Patch(context.TODO(), profile,
					client.RawPatch(
						types.JSONPatchType,
						[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, newspec)),
					),
				)).ToNot(HaveOccurred())

				By("Applying changes in performance profile and waiting until mcp will start updating")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

				By("Waiting when mcp finishes updates")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

				stalldEnabled, rtKernel = true, true
				noHzParam = fmt.Sprintf("nohz_full=%s", *profile.Spec.CPU.Isolated)
				sysctlMap = map[string]string{
					"kernel.hung_task_timeout_secs": "600",
					"kernel.nmi_watchdog":           "0",
					"kernel.sched_rt_runtime_us":    "-1",
					"vm.stat_interval":              "10",
				}

				kernelParameters = []string{noHzParam, "tsc=nowatchdog", "nosoftlockup", "nmi_watchdog=0", "mce=off", "skew_tick=1",
					"processor.max_cstate=1", "intel_idle.max_cstate=0", "intel_pstate=disable", "idle=poll"}
				checkTunedParameters(workerRTNodes, stalldEnabled, sysctlMap, kernelParameters, rtKernel)
			})

			It("[test_id:54184]Verify enabling both HighPowerConsumption and PerPodPowerManagment fails", func() {

				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					PerPodPowerManagement: pointer.BoolPtr(true),
					HighPowerConsumption:  pointer.BoolPtr(true),
					RealTime:              pointer.BoolPtr(true),
				}
				EventuallyWithOffset(1, func() string {
					err := testclient.Client.Update(context.TODO(), profile)
					if err != nil {
						statusErr, _ := err.(*errors.StatusError)
						return statusErr.Status().Message
					}
					return fmt.Sprint("Profile applied successfully")
				}, time.Minute, 5*time.Second).Should(ContainSubstring("HighPowerConsumption and PerPodPowerManagement can not be both enabled"))
			})

			It("[test_id:54185] Verify sysfs parameters of guaranteed pod with powersave annotations", func() {

				// This test requires real hardware with powermanagement settings done on BIOS
				// Using numa nodes to check if we are running on real hardware.
				checkHardwareCapability(workerRTNodes)
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					PerPodPowerManagement: pointer.BoolPtr(true),
					HighPowerConsumption:  pointer.BoolPtr(false),
					RealTime:              pointer.BoolPtr(true),
				}
				By("Patching the performance profile with workload hints")
				workloadHints, err := json.Marshal(profile.Spec.WorkloadHints)
				Expect(err).ToNot(HaveOccurred())

				Expect(testclient.Client.Patch(context.TODO(), profile,
					client.RawPatch(
						types.JSONPatchType,
						[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec/workloadHints", "value": %s }]`, workloadHints)),
					),
				)).ToNot(HaveOccurred())
				By("Applying changes in performance profile and waiting until mcp will start updating")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

				By("Waiting when mcp finishes updates")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

				annotations := map[string]string{
					"cpu-c-states.crio.io":      "enable",
					"cpu-freq-governor.crio.io": "schedutil",
				}

				cpuCount := "2"
				resCpu := resource.MustParse(cpuCount)
				resMem := resource.MustParse("100Mi")
				testpod = pods.GetTestPod()
				testpod.Namespace = testutils.NamespaceTesting
				testpod.Spec.Containers[0].Resources = corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resCpu,
						corev1.ResourceMemory: resMem,
					},
				}
				testpod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNodes[0].Name}
				testpod.Annotations = annotations
				runtimeClass := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
				testpod.Spec.RuntimeClassName = &runtimeClass

				By("creating test pod")
				err = testclient.Client.Create(context.TODO(), testpod)
				Expect(err).ToNot(HaveOccurred())
				err = pods.WaitForCondition(testpod, corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
				Expect(err).ToNot(HaveOccurred())
				Expect(testpod.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed), "Test pod does not have QoS class of Guaranteed")

				By("Getting the container cpuset.cpus cgroup")
				containerID, err := pods.GetContainerIDByName(testpod, "test")
				Expect(err).ToNot(HaveOccurred())

				containerCgroup := ""
				Eventually(func() string {
					cmd := []string{"/bin/bash", "-c", fmt.Sprintf("find /rootfs/sys/fs/cgroup/cpuset/ -name *%s*", containerID)}
					containerCgroup, err = nodes.ExecCommandOnNode(cmd, &workerRTNodes[0])
					Expect(err).ToNot(HaveOccurred())
					return containerCgroup
				}, (cluster.ComputeTestTimeout(30*time.Second, RunningOnSingleNode)), 5*time.Second).ShouldNot(BeEmpty(),
					fmt.Sprintf("cannot find cgroup for container %q", containerID))

				By("Verify powersetting of cpus used by the pod")
				cmd := []string{"/bin/bash", "-c", fmt.Sprintf("cat %s/cpuset.cpus", containerCgroup)}
				output, err := nodes.ExecCommandOnNode(cmd, &workerRTNodes[0])
				Expect(err).ToNot(HaveOccurred())
				cpus, err := cpuset.Parse(output)
				targetCpus := cpus.ToSlice()
				err = checkCpuGovernorsAndResumeLatency(targetCpus, &workerRTNodes[0], "0", "schedutil")
				Expect(err).ToNot(HaveOccurred())
				//verify the rest of the cpus do not have powersave cpu governors
				By("Verify the rest of the cpus donot haver powersave settings")
				numaInfo, err := nodes.GetNumaNodes(&workerRTNodes[0])
				Expect(err).ToNot(HaveOccurred())
				var otherCpus []int
				for _, cpusiblings := range numaInfo {
					for _, cpu := range cpusiblings {
						if cpu != targetCpus[0] && cpu != targetCpus[1] {
							otherCpus = append(otherCpus, cpu)
						}
					}
				}
				err = checkCpuGovernorsAndResumeLatency(otherCpus, &workerRTNodes[0], "0", "performance")
				deleteTestPod(testpod)
				//Verify after the pod is deleted the cpus assigned to container have default powersave settings
				By("Verify after pod is delete cpus assigned to container have default powersave settings")
				err = checkCpuGovernorsAndResumeLatency(targetCpus, &workerRTNodes[0], "0", "performance")
			})

			It("[test_id:54186] Verify sysfs paramters of guaranteed pod with performance annotiations", func() {

				// This test requires real hardware with powermanagement settings done on BIOS
				// Using numa nodes to check if we are running on real hardware
				checkHardwareCapability(workerRTNodes)
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					PerPodPowerManagement: pointer.BoolPtr(false),
					HighPowerConsumption:  pointer.BoolPtr(true),
					RealTime:              pointer.BoolPtr(true),
				}
				By("Patching the performance profile with workload hints")
				workloadHints, err := json.Marshal(profile.Spec.WorkloadHints)
				Expect(err).ToNot(HaveOccurred())

				Expect(testclient.Client.Patch(context.TODO(), profile,
					client.RawPatch(
						types.JSONPatchType,
						[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec/workloadHints", "value": %s }]`, workloadHints)),
					),
				)).ToNot(HaveOccurred())
				By("Applying changes in performance profile and waiting until mcp will start updating")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

				By("Waiting when mcp finishes updates")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

				annotations := map[string]string{
					"cpu-load-balancing.crio.io": "disable",
					"cpu-quota.crio.io":          "disable",
					"irq-load-balancing.crio.io": "disable",
					"cpu-c-states.crio.io":       "disable",
					"cpu-freq-governor.crio.io":  "performance",
				}

				cpuCount := "2"
				resCpu := resource.MustParse(cpuCount)
				resMem := resource.MustParse("100Mi")
				testpod = pods.GetTestPod()
				testpod.Namespace = testutils.NamespaceTesting
				testpod.Spec.Containers[0].Resources = corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resCpu,
						corev1.ResourceMemory: resMem,
					},
				}
				testpod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNodes[0].Name}
				testpod.Annotations = annotations
				runtimeClass := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
				testpod.Spec.RuntimeClassName = &runtimeClass

				By("creating test pod")
				err = testclient.Client.Create(context.TODO(), testpod)
				Expect(err).ToNot(HaveOccurred())
				err = pods.WaitForCondition(testpod, corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
				Expect(err).ToNot(HaveOccurred())
				Expect(testpod.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed), "Test pod does not have QoS class of Guaranteed")

				By("Getting the container cpuset.cpus cgroup")
				containerID, err := pods.GetContainerIDByName(testpod, "test")
				Expect(err).ToNot(HaveOccurred())

				containerCgroup := ""
				Eventually(func() string {
					cmd := []string{"/bin/bash", "-c", fmt.Sprintf("find /rootfs/sys/fs/cgroup/cpuset/ -name *%s*", containerID)}
					containerCgroup, err = nodes.ExecCommandOnNode(cmd, &workerRTNodes[0])
					Expect(err).ToNot(HaveOccurred())
					return containerCgroup
				}, (cluster.ComputeTestTimeout(30*time.Second, RunningOnSingleNode)), 5*time.Second).ShouldNot(BeEmpty(),
					fmt.Sprintf("cannot find cgroup for container %q", containerID))

				By("Verify powersetting of cpus used by the pod")
				cmd := []string{"/bin/bash", "-c", fmt.Sprintf("cat %s/cpuset.cpus", containerCgroup)}
				output, err := nodes.ExecCommandOnNode(cmd, &workerRTNodes[0])
				Expect(err).ToNot(HaveOccurred())
				cpus, err := cpuset.Parse(output)
				targetCpus := cpus.ToSlice()
				err = checkCpuGovernorsAndResumeLatency(targetCpus, &workerRTNodes[0], "n/a", "performance")
				Expect(err).ToNot(HaveOccurred())
				By("Verify the rest of cpus have default power setting")
				var otherCpus []int
				numaInfo, err := nodes.GetNumaNodes(&workerRTNodes[0])
				for _, cpusiblings := range numaInfo {
					for _, cpu := range cpusiblings {
						if cpu != targetCpus[0] && cpu != targetCpus[1] {
							otherCpus = append(otherCpus, cpu)
						}
					}
				}
				//Verify cpus not assigned to the pod have default power settings
				err = checkCpuGovernorsAndResumeLatency(otherCpus, &workerRTNodes[0], "0", "performance")
				deleteTestPod(testpod)
				//Test after pod is deleted the governors are set back to default for the cpus that were alloted to containers.
				By("Verify after pod is delete cpus assigned to container have default powersave settings")
				err = checkCpuGovernorsAndResumeLatency(targetCpus, &workerRTNodes[0], "0", "performance")
			})
		})

		AfterEach(func() {
			currentProfile := &performancev2.PerformanceProfile{}
			if err := testclient.Client.Get(context.TODO(), client.ObjectKeyFromObject(initialProfile), currentProfile); err != nil {
				klog.Errorf("failed to get performance profile %q", initialProfile.Name)
				return
			}

			if reflect.DeepEqual(currentProfile.Spec, initialProfile.Spec) {
				return
			}

			By("Restoring the old performance profile")
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

		})
	})

	Context("Offlined CPU API", func() {
		BeforeEach(func() {
			//Saving the old performance profile
			initialProfile = profile.DeepCopy()
		})

		It("[disruptive] should set offline cpus after deploy PAO", func() {
			for _, node := range workerRTNodes {
				onlineCPUCount, err := nodes.ExecCommandOnNode([]string{"nproc", "--all"}, &node)
				Expect(err).ToNot(HaveOccurred())
				onlineCPUInt, err := strconv.Atoi(onlineCPUCount)
				Expect(err).ToNot(HaveOccurred())
				Expect(onlineCPUInt).Should(BeNumerically(">=", 3))
				if onlineCPUInt <= 8 {
					Skip(fmt.Sprintf("This test needs more than 8 CPUs online to work correctly, current online CPUs are %s", onlineCPUCount))
				}
			}

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
				offlinedOutput, err := nodes.ExecCommandOnNode([]string{"cat", "/sys/devices/system/cpu/offline"}, &node)
				Expect(err).ToNot(HaveOccurred())
				offlinedCPUSet, err := cpuset.Parse(offlinedOutput)
				offlinedCPUSetProfile, err := cpuset.Parse(string(offlined))
				Expect(err).ToNot(HaveOccurred())
				Expect(offlinedCPUSet.Equals(offlinedCPUSetProfile))
			}
		})

		It("[test_id:50964] Offline Higher CPUID's", func() {
			var reserved, isolated, offline []string
			// This map is of the form numaNode[core][cpu-siblings]
			var numaCoreSiblings map[int]map[int][]int
			var onlineCPUInt int
			for _, node := range workerRTNodes {
				onlineCPUCount, err := nodes.ExecCommandOnNode([]string{"nproc", "--all"}, &node)
				Expect(err).ToNot(HaveOccurred())
				onlineCPUInt, err = strconv.Atoi(onlineCPUCount)
				Expect(err).ToNot(HaveOccurred())
				if onlineCPUInt <= 8 {
					Skip(fmt.Sprintf("This test needs more than 8 CPUs online to work correctly, current online CPUs are %s", onlineCPUCount))
				}
			}
			// Get Per Numa Per core siblings
			for _, node := range workerRTNodes {
				numaCoreSiblings, err = nodes.GetCoreSiblings(&node)
			}
			for numaNode := range numaCoreSiblings {
				cores := make([]int, 0)
				for k := range numaCoreSiblings[numaNode] {
					cores = append(cores, k)
				}
				sort.Ints(cores)
				// Select the last core id
				higherCoreIds := cores[len(cores)-1]
				// Get cpu siblings from the selected cores and delete the selected cores  from the map
				cpusiblings := nodes.GetCpuSiblings(numaCoreSiblings, higherCoreIds)
				offline = append(offline, cpusiblings...)
			}
			offlineCpus := strings.Join(offline, ",")
			// Get reserved core siblings from 0, 1
			for reservedCores := 0; reservedCores < 2; reservedCores++ {
				// Get the cpu siblings from the selected core and delete the siblings
				// from the map. Selected siblings of cores are saved in reservedCpus
				cpusiblings := nodes.GetCpuSiblings(numaCoreSiblings, reservedCores)
				reserved = append(reserved, cpusiblings...)
			}
			reservedCpus := strings.Join(reserved, ",")
			// Remaining core siblings available in the
			// numaCoreSiblings map is used in isolatedCpus
			for key := range numaCoreSiblings {
				for k := range numaCoreSiblings[key] {
					cpusiblings := nodes.GetCpuSiblings(numaCoreSiblings, k)
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
				offlinedOutput, err := nodes.ExecCommandOnNode([]string{"cat", "/sys/devices/system/cpu/offline"}, &node)
				Expect(err).ToNot(HaveOccurred())
				offlinedCPUSet, err := cpuset.Parse(offlinedOutput)
				offlinedCPUSetProfile, err := cpuset.Parse(string(offlinedSet))
				Expect(err).ToNot(HaveOccurred())
				Expect(offlinedCPUSet.Equals(offlinedCPUSetProfile))
			}
		})

		It("[test_id:50965]Offline Middle CPUID's", func() {
			var reserved, isolated, offline []string
			// This map is of the form numaNode[core][cpu-siblings]
			var numaCoreSiblings map[int]map[int][]int
			var onlineCPUInt int
			for _, node := range workerRTNodes {
				onlineCPUCount, err := nodes.ExecCommandOnNode([]string{"nproc", "--all"}, &node)
				Expect(err).ToNot(HaveOccurred())
				onlineCPUInt, err = strconv.Atoi(onlineCPUCount)
				Expect(err).ToNot(HaveOccurred())
				if onlineCPUInt <= 8 {
					Skip(fmt.Sprintf("This test needs more than 8 CPUs online to work correctly, current online CPUs are %s", onlineCPUCount))
				}
			}
			for _, node := range workerRTNodes {
				numaCoreSiblings, err = nodes.GetCoreSiblings(&node)
			}
			for key := range numaCoreSiblings {
				cores := make([]int, 0)
				for k := range numaCoreSiblings[key] {
					cores = append(cores, k)
				}
				sort.Ints(cores)
				middleCoreIds := cores[len(cores)/2]
				siblings := nodes.GetCpuSiblings(numaCoreSiblings, middleCoreIds)
				offline = append(offline, siblings...)
			}
			offlineCpus := strings.Join(offline, ",")
			for reservedCores := 0; reservedCores < 2; reservedCores++ {
				// Get the cpu siblings from the selected core and delete the siblings
				// from the map. Selected siblings of cores are saved in reservedCpus
				siblings := nodes.GetCpuSiblings(numaCoreSiblings, reservedCores)
				reserved = append(reserved, siblings...)
			}
			reservedCpus := strings.Join(reserved, ",")
			// Remaining core siblings available in the
			// numaCoreSiblings map is used in isolatedCpus
			for key := range numaCoreSiblings {
				for k := range numaCoreSiblings[key] {
					siblings := nodes.GetCpuSiblings(numaCoreSiblings, k)
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
				offlinedOutput, err := nodes.ExecCommandOnNode([]string{"cat", "/sys/devices/system/cpu/offline"}, &node)
				Expect(err).ToNot(HaveOccurred())
				offlinedCPUSet, err := cpuset.Parse(offlinedOutput)
				offlinedCPUSetProfile, err := cpuset.Parse(string(offlinedSet))
				Expect(err).ToNot(HaveOccurred())
				Expect(offlinedCPUSet.Equals(offlinedCPUSetProfile))
			}
		})
		It("[test_id:50966]verify offlined parameter accepts multiple ranges of cpuid's", func() {
			var reserved, isolated, offlined []string
			//This map is of the form numaNode[core][cpu-siblings]
			var numaCoreSiblings map[int]map[int][]int
			for _, node := range workerRTNodes {
				numaInfo, err := nodes.GetNumaNodes(&node)
				Expect(err).ToNot(HaveOccurred())
				if len(numaInfo) < 2 {
					Skip(fmt.Sprintf("This test need 2 NUMA nodes.The number of NUMA nodes on node %s < 2", node.Name))
				}
			}
			for _, node := range workerRTNodes {
				onlineCPUCount, err := nodes.ExecCommandOnNode([]string{"nproc", "--all"}, &node)
				Expect(err).ToNot(HaveOccurred())
				onlineCPUInt, err := strconv.Atoi(onlineCPUCount)
				Expect(err).ToNot(HaveOccurred())
				Expect(onlineCPUInt).Should(BeNumerically(">=", 3))
				if onlineCPUInt <= 8 {
					Skip(fmt.Sprintf("This test needs more than 8 CPUs online to work correctly, current online CPUs are %s", onlineCPUCount))
				}
			}
			for _, node := range workerRTNodes {
				numaCoreSiblings, err = nodes.GetCoreSiblings(&node)
			}
			// Get reserved core siblings from 0, 1
			for reservedCores := 0; reservedCores < 2; reservedCores++ {
				// Get the cpu siblings from the selected core and delete the siblings
				// from the map. Selected siblings of cores are saved in reservedCpus
				siblings := nodes.GetCpuSiblings(numaCoreSiblings, reservedCores)
				reserved = append(reserved, siblings...)
			}
			reservedCpus := strings.Join(reserved, ",")
			//Get Offline Core siblings . We take the total cores and
			//from the middle we take core ids for calculating the ranges.
			for key := range numaCoreSiblings {
				cores := make([]int, 0)
				for k := range numaCoreSiblings[key] {
					cores = append(cores, k)
				}
				sort.Ints(cores)
				if len(cores) < 20 {
					Skip(fmt.Sprintf("This test needs systems with at least 20 cores per socket"))
				}
				for i := 0; i < len(cores)/2; i++ {
					siblings := nodes.GetCpuSiblings(numaCoreSiblings, cores[i])
					offlined = append(offlined, siblings...)
				}
			}
			offlinedCpus := nodes.GetNumaRanges(strings.Join(offlined, ","))
			// Remaining core siblings available in the numaCoreSiblings
			// map is used in isolatedCpus
			for key := range numaCoreSiblings {
				for k := range numaCoreSiblings[key] {
					siblings := nodes.GetCpuSiblings(numaCoreSiblings, k)
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
				offlinedOutput, err := nodes.ExecCommandOnNode([]string{"cat", "/sys/devices/system/cpu/offline"}, &node)
				Expect(err).ToNot(HaveOccurred())
				offlinedCPUSet, err := cpuset.Parse(offlinedOutput)
				offlinedCPUSetProfile, err := cpuset.Parse(string(offlinedSet))
				Expect(err).ToNot(HaveOccurred())
				Expect(offlinedCPUSet.Equals(offlinedCPUSetProfile))
			}
		})

		It("[test_id:50968]verify cpus mentioned in reserved or isolated cannot be offline", func() {
			var reserved, isolated []string
			//This map is of the form numaNode[core][cpu-siblings]
			var numaCoreSiblings map[int]map[int][]int
			for _, node := range workerRTNodes {
				numaInfo, err := nodes.GetNumaNodes(&node)
				Expect(err).ToNot(HaveOccurred())
				if len(numaInfo) < 2 {
					Skip(fmt.Sprintf("This test need 2 NUMA nodes.The number of NUMA nodes on node %s < 2", node.Name))
				}
			}
			for _, node := range workerRTNodes {
				onlineCPUCount, err := nodes.ExecCommandOnNode([]string{"nproc", "--all"}, &node)
				Expect(err).ToNot(HaveOccurred())
				onlineCPUInt, err := strconv.Atoi(onlineCPUCount)
				Expect(err).ToNot(HaveOccurred())
				Expect(onlineCPUInt).Should(BeNumerically(">=", 3))
				if onlineCPUInt <= 8 {
					Skip(fmt.Sprintf("This test needs more than 8 CPUs online to work correctly, current online CPUs are %s", onlineCPUCount))
				}

			}

			for _, node := range workerRTNodes {
				numaCoreSiblings, err = nodes.GetCoreSiblings(&node)
			}
			// Get reserved core siblings from 0, 1
			for reservedCores := 0; reservedCores < 2; reservedCores++ {
				// Get the cpu siblings from the selected core and delete the siblings
				// from the map. Selected siblings of cores are saved in reservedCpus
				siblings := nodes.GetCpuSiblings(numaCoreSiblings, reservedCores)
				reserved = append(reserved, siblings...)
			}
			reservedCpus := strings.Join(reserved, ",")
			// Remaining core siblings available in the
			// numaCoreSiblings map is used in isolatedCpus
			for key := range numaCoreSiblings {
				for k := range numaCoreSiblings[key] {
					siblings := nodes.GetCpuSiblings(numaCoreSiblings, k)
					isolated = append(isolated, siblings...)
				}
			}
			isolatedCpus := strings.Join(isolated, ",")
			//combine both isolated and reserved
			totalCpus := fmt.Sprintf("%s,%s", reservedCpus, isolatedCpus)
			totalCpuSlice := strings.Split(totalCpus, ",")
			// get partial cpus from the combined cpus
			partialCpulist := (totalCpuSlice[:len(totalCpuSlice)/2])
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
			//var offlineCPUs, reservedCpus, isolatedCpus string = "", "", ""
			//This map is of the form numaNode[core][cpu-siblings]
			var numaCoreSiblings map[int]map[int][]int
			for _, node := range workerRTNodes {
				numaInfo, err := nodes.GetNumaNodes(&node)
				Expect(err).ToNot(HaveOccurred())
				if len(numaInfo) < 2 {
					Skip(fmt.Sprintf("This test need 2 NUMA nodes.The number of NUMA nodes on node %s < 2", node.Name))
				}
			}
			for _, node := range workerRTNodes {
				onlineCPUCount, err := nodes.ExecCommandOnNode([]string{"nproc", "--all"}, &node)
				Expect(err).ToNot(HaveOccurred())
				onlineCPUInt, err := strconv.Atoi(onlineCPUCount)
				Expect(err).ToNot(HaveOccurred())
				Expect(onlineCPUInt).Should(BeNumerically(">=", 3))
				if onlineCPUInt <= 8 {
					Skip(fmt.Sprintf("This test needs more than 8 CPUs online to work correctly, current online CPUs are %s", onlineCPUCount))
				}

			}

			for _, node := range workerRTNodes {
				numaCoreSiblings, err = nodes.GetCoreSiblings(&node)
			}

			// Get reserved core siblings from 0, 1
			for reservedCores := 0; reservedCores < 2; reservedCores++ {
				// Get the cpu siblings from the selected core and delete the siblings
				// from the map. Selected siblings of cores are saved in reservedCpus
				siblings := nodes.GetCpuSiblings(numaCoreSiblings, reservedCores)
				reserved = append(reserved, siblings...)
			}
			reservedCpus := strings.Join(reserved, ",")

			discreteCores := []int{3, 13, 15, 24, 29}
			for _, v := range discreteCores {
				siblings := nodes.GetCpuSiblings(numaCoreSiblings, v)
				offlined = append(offlined, siblings...)
			}
			offlineCpus := strings.Join(offlined, ",")
			for key := range numaCoreSiblings {
				for k := range numaCoreSiblings[key] {
					cpusiblings := nodes.GetCpuSiblings(numaCoreSiblings, k)
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
				offlinedOutput, err := nodes.ExecCommandOnNode([]string{"cat", "/sys/devices/system/cpu/offline"}, &node)
				Expect(err).ToNot(HaveOccurred())
				offlinedCPUSet, err := cpuset.Parse(offlinedOutput)
				offlinedCPUSetProfile, err := cpuset.Parse(string(offlinedSet))
				Expect(err).ToNot(HaveOccurred())
				Expect(offlinedCPUSet.Equals(offlinedCPUSetProfile))
			}
		})

		AfterEach(func() {
			By("Reverting the Profile")
			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			currentSpec, _ := json.Marshal(profile.Spec)
			spec, _ := json.Marshal(initialProfile.Spec)
			// revert only if the profile changes.
			if !bytes.Equal(currentSpec, spec) {
				var numaCoreSiblings map[int]map[int][]int
				var allCpus = []int{}
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

				// Verify cpus are back online when the offline parameters is removed
				for _, node := range workerRTNodes {
					numaCoreSiblings, err = nodes.GetCoreSiblings(&node)
				}
				for _, cores := range numaCoreSiblings {
					for _, cpuSiblings := range cores {
						for _, cpus := range cpuSiblings {
							allCpus = append(allCpus, cpus)
						}
					}
				}
				for _, node := range workerRTNodes {
					for _, v := range allCpus {
						checkCpuStatusCmd := []string{"bash", "-c",
							fmt.Sprintf("cat /sys/devices/system/cpu/cpu%d/online", v)}
						fmt.Printf("Checking cpu%d is online\n", v)
						stdout, err := nodes.ExecCommandOnNode(checkCpuStatusCmd, &node)
						Expect(err).NotTo(HaveOccurred())
						Expect(stdout).Should(Equal("1"))
					}
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
			//Revert the profile
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
				onlineCPUCount, err := nodes.ExecCommandOnNode([]string{"nproc", "--all"}, &node)
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
				numaCoreSiblings, err = nodes.GetCoreSiblings(&node)
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
				coresiblings := nodes.GetCpuSiblings(numaCoreSiblings, middleCoreIds)
				reserved = append(reserved, coresiblings...)
			}
			reservedCpus := strings.Join(reserved, ",")
			for numaNode := range numaCoreSiblings {
				for coreids := range numaCoreSiblings[numaNode] {
					coresiblings := nodes.GetCpuSiblings(numaCoreSiblings, coreids)
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
				cmd := []string{"cat", "/rootfs/etc/systemd/system/update-rps@.service"}
				unitFileContents, err := nodes.ExecCommandOnNode(cmd, &node)
				Expect(err).ToNot(HaveOccurred())
				for _, line := range strings.Split(unitFileContents, "\n") {
					if strings.Contains(line, "ExecStart=/usr/local/bin/set-rps-mask.sh") {
						maskContent = line
					}
				}
				rpsMaskContent := strings.TrimSuffix(maskContent, "\r")
				Expect(len(strings.Split(rpsMaskContent, " "))).To(Equal(3), "systemd unit file doesn't have proper rpsmask")
				serviceRPSCPUs := strings.Split(rpsMaskContent, " ")[2]
				rpsCPUs, err := components.CPUMaskToCPUSet(serviceRPSCPUs)
				Expect(err).ToNot(HaveOccurred())
				Expect(rpsCPUs).To(Equal(expectedRPSCPUs), "the service rps mask is different from the reserved CPUs")

				// Verify all host network devices have the correct RPS mask
				cmd = []string{"find", "/rootfs/sys/devices/virtual", "-type", "f", "-name", "rps_cpus", "-exec", "cat", "{}", ";"}
				devsRPS, err := nodes.ExecCommandOnNode(cmd, &node)
				Expect(err).ToNot(HaveOccurred())
				for _, devRPS := range strings.Split(devsRPS, "\n") {
					rpsCPUs, err = components.CPUMaskToCPUSet(devRPS)
					Expect(err).ToNot(HaveOccurred())
					if rpsCPUs.String() != string(*profile.Spec.CPU.Reserved) {
						testlog.Info("Applying RPS Mask can be skipped due to race conditions")
						testlog.Info("This is a known issue, Refer OCPBUGS-4194")
					}
					//Once the OCPBUGS-4194 is fixed Remove the If condition and uncomment the below assertion
					//Expect(rpsCPUs).To(Equal(expectedRPSCPUs), "a host device rps mask is different from the reserved CPUs")
				}
			}
		})

		It("[test_id:54191]Verify RPS Mask is not applied when RealtimeHint is disabled", func() {
			By("Modifying profile")
			profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
				HighPowerConsumption:  pointer.BoolPtr(false),
				RealTime:              pointer.BoolPtr(false),
				PerPodPowerManagement: pointer.BoolPtr(false),
			}

			profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
				Enabled: pointer.BoolPtr(false),
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
				_, err := nodes.ExecCommandOnNode(cmd, &node)
				Expect(err).To(HaveOccurred())
			}
		})
	})
})

func hugepagesPathForNode(nodeID, sizeINMb int) string {
	return fmt.Sprintf("/sys/devices/system/node/node%d/hugepages/hugepages-%dkB/nr_hugepages", nodeID, sizeINMb*1024)
}

func countHugepagesOnNode(node *corev1.Node, sizeInMb int) (int, error) {
	numaInfo, err := nodes.GetNumaNodes(node)
	if err != nil {
		return 0, err
	}
	count := 0
	for i := 0; i < len(numaInfo); i++ {
		nodeCmd := []string{"cat", hugepagesPathForNode(i, sizeInMb)}
		result, err := nodes.ExecCommandOnNode(nodeCmd, node)
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
	workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
	Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("error looking for the optional selector: %v", err))
	Expect(workerRTNodes).ToNot(BeEmpty(), "cannot find RT enabled worker nodes")
	return workerRTNodes
}

// Check All tunables and kernel paramters for workloadHint
func checkTunedParameters(workerRTNodes []corev1.Node, stalld bool, sysctlMap map[string]string, kernelParameters []string, rtkernel bool) {
	for _, node := range workerRTNodes {
		stalld_pid, err := nodes.ExecCommandOnNode([]string{"pidof", "stalld"}, &node)
		if stalld {
			Expect(err).ToNot(HaveOccurred())
			Expect(stalld_pid).ToNot(BeEmpty())
		} else {
			Expect(err).To(HaveOccurred())
			Expect(stalld_pid).To(BeEmpty())
		}
	}

	key := types.NamespacedName{
		Name:      components.GetComponentName(testutils.PerformanceProfileName, components.ProfileNamePerformance),
		Namespace: components.NamespaceNodeTuningOperator,
	}
	tuned := &tunedv1.Tuned{}
	err := testclient.Client.Get(context.TODO(), key, tuned)
	Expect(err).ToNot(HaveOccurred(), "Cannot find the cluster Node Tuning Operator object "+key.String())
	if stalld {
		Expect(*tuned.Spec.Profile[0].Data).To(ContainSubstring("stalld"))
	} else {
		Expect(*tuned.Spec.Profile[0].Data).ToNot(ContainSubstring("stalld"))
	}

	for _, node := range workerRTNodes {
		for param, expected := range sysctlMap {
			By(fmt.Sprintf("Executing he command \"sysctl -n %s\"", param))
			out, err := nodes.ExecCommandOnMachineConfigDaemon(&node, []string{"sysctl", "-n", param})
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.TrimSpace(string(out))).Should(Equal(expected), "parameter %s value is not %s. ", param, expected)
		}
	}
	for _, node := range workerRTNodes {
		cmdline, err := nodes.ExecCommandOnMachineConfigDaemon(&node, []string{"cat", "/proc/cmdline"})
		Expect(err).ToNot(HaveOccurred())
		for _, paramter := range kernelParameters {
			Expect(string(cmdline)).To(ContainSubstring(paramter))
		}
	}

	if !rtkernel {
		for _, node := range workerRTNodes {
			cmd := []string{"uname", "-a"}
			kernel, err := nodes.ExecCommandOnNode(cmd, &node)
			Expect(err).ToNot(HaveOccurred(), "failed to execute uname")
			Expect(kernel).To(ContainSubstring("Linux"), "Kernel should report itself as Linux")

			err = nodes.HasPreemptRTKernel(&node)
			Expect(err).To(HaveOccurred(), "Node should have non-RT kernel")
		}
	}
}

func getTunedStructuredData(profile *performancev2.PerformanceProfile) *ini.File {
	tuned, err := tuned.NewNodePerformance(profile)
	Expect(err).ToNot(HaveOccurred())
	tunedData := []byte(*tuned.Spec.Profile[0].Data)
	cfg, err := ini.Load(tunedData)
	Expect(err).ToNot(HaveOccurred())
	return cfg
}

// deleteTestPod removes guaranteed pod
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

// checkCpuGovernorsAndResumeLatency  Checks power and latency settings of the cpus
func checkCpuGovernorsAndResumeLatency(cpus []int, targetNode *corev1.Node, pm_qos string, governor string) error {
	for _, cpu := range cpus {
		cmd := []string{"/bin/bash", "-c", fmt.Sprintf("cat /sys/devices/system/cpu/cpu%d/power/pm_qos_resume_latency_us", cpu)}
		output, err := nodes.ExecCommandOnNode(cmd, targetNode)
		if err != nil {
			return err
		}
		Expect(output).To(Equal(pm_qos))
		cmd = []string{"/bin/bash", "-c", fmt.Sprintf("cat /sys/devices/system/cpu/cpu%d/cpufreq/scaling_governor", cpu)}
		output, err = nodes.ExecCommandOnNode(cmd, targetNode)
		if err != nil {
			return err
		}
		Expect(output).To(Equal(governor))
	}
	return nil
}

// checkHardwareCapability Checks if test is run on baremetal worker
func checkHardwareCapability(workerRTNodes []corev1.Node) {
	const totalCpus = 32
	for _, node := range workerRTNodes {
		numaInfo, err := nodes.GetNumaNodes(&node)
		Expect(err).ToNot(HaveOccurred())
		if len(numaInfo) < 2 {
			Skip(fmt.Sprintf("This test need 2 NUMA nodes.The number of NUMA nodes on node %s < 2", node.Name))
		}
		// Additional check so that test gets skipped on vm with fake numa
		onlineCPUCount, err := nodes.ExecCommandOnNode([]string{"nproc", "--all"}, &node)
		Expect(err).ToNot(HaveOccurred())
		onlineCPUInt, err := strconv.Atoi(onlineCPUCount)
		Expect(err).ToNot(HaveOccurred())
		if onlineCPUInt < totalCpus {
			Skip(fmt.Sprintf("This test needs system with %d CPUs to work correctly, current CPUs are %s", totalCpus, onlineCPUCount))
		}
	}
}
