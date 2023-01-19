package __2_performance_update_second

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
)

type checkFunction func(*corev1.Node) (string, error)

var _ = Describe("[rfe_id:28761][performance] Updating parameters in performance profile", func() {
	var profile, initialProfile *performancev2.PerformanceProfile
	var performanceMCP string
	var err error

	chkCmdLine := []string{"cat", "/proc/cmdline"}
	chkIrqbalance := []string{"cat", "/rootfs/etc/sysconfig/irqbalance"}

	nodeLabel := testutils.NodeSelectorLabels
	profile, err = profiles.GetByNodeLabels(nodeLabel)
	Expect(err).ToNot(HaveOccurred())
	initialProfile = profile.DeepCopy()

	BeforeEach(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}

		profile, err = profiles.GetByNodeLabels(nodeLabel)
		Expect(err).ToNot(HaveOccurred())
		performanceMCP, err = mcps.GetByProfile(profile)
		Expect(err).ToNot(HaveOccurred())

		// Verify that worker and performance MCP have updated state equals to true
		for _, mcpName := range []string{testutils.RoleWorker, performanceMCP} {
			mcps.WaitForCondition(mcpName, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
		}
	})

	Context("Updating of nodeSelector parameter and node labels", func() {
		var mcp *machineconfigv1.MachineConfigPool
		var newCnfNode *corev1.Node

		newRole := "worker-test"
		newLabel := fmt.Sprintf("%s/%s", testutils.LabelRole, newRole)
		newNodeSelector := map[string]string{newLabel: ""}

		//fetch the latest profile
		profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		//Expect(err).ToNot(HaveOccurred())
		var oldMcpSelector, oldNodeSelector map[string]string

		testutils.BeforeAll(func() {
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

		removeLabels := func(nodeSelector map[string]string, targetNode *corev1.Node) error {
			for l := range nodeSelector {
				delete(targetNode.Labels, l)
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

		It("[test_id:28440]Verifies that nodeSelector can be updated in performance profile", func() {
			kubeletConfig, err := nodes.GetKubeletConfig(newCnfNode)
			Expect(kubeletConfig.TopologyManagerPolicy).ToNot(BeEmpty())
			cmdline, err := nodes.ExecCommandOnNode(chkCmdLine, newCnfNode)
			Expect(err).ToNot(HaveOccurred(), "failed to execute %s", chkCmdLine)
			Expect(cmdline).To(ContainSubstring("tuned.non_isolcpus"))

			err = removeLabels(profile.Spec.NodeSelector, newCnfNode)
			Expect(err).ToNot(HaveOccurred())

		})

		It("[test_id:27484]Verifies that node is reverted to plain worker when the extra labels are removed", func() {
			By("Deleting cnf labels from the node")
			err = removeLabels(profile.Spec.NodeSelector, newCnfNode)
			Expect(err).ToNot(HaveOccurred())
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
})
