package e2e

import (
	"fmt"
	"os/exec"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	. "github.com/openshift/cluster-node-tuning-operator/test/e2e/utils"

	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	coreapi "k8s.io/api/core/v1"
)

// Test the MachineConfig labels matching functionality and rollback.
var _ = Describe("[reboots][machine_config_labels] Node Tuning Operator machine config labels", func() {
	const (
		profileRealtime   = "../../../examples/realtime-mc.yaml"
		mcpRealtime       = "../../../examples/realtime-mcp.yaml"
		nodeLabelRealtime = "node-role.kubernetes.io/worker-rt"
		procCmdline       = "/proc/cmdline"
	)

	Context("machine config labels", func() {
		var (
			node *coreapi.Node
		)

		// Cleanup code to roll back cluster changes done by this test even if it fails in the middle of It()
		AfterEach(func() {
			// This cleanup code ignores issues outlined in rhbz#1816239;
			// this can cause a degraded MachineConfigPool
			By("cluster changes rollback")
			if node != nil {
				exec.Command("oc", "label", "node", "--overwrite", node.Name, nodeLabelRealtime+"-").CombinedOutput()
			}
			exec.Command("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileRealtime).CombinedOutput()
			exec.Command("oc", "delete", "-f", mcpRealtime).CombinedOutput()
		})

		It("kernel parameters set", func() {
			By("getting a list of worker nodes")
			nodes, err := GetNodesByRole(cs, "worker")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(nodes)).NotTo(BeZero())

			workerMachinesOrig, err := GetUpdatedMachineCountForPool(cs, "worker")
			Expect(err).NotTo(HaveOccurred())

			node = &nodes[0]
			By(fmt.Sprintf("getting a tuned pod running on node %s", node.Name))
			pod, err := GetTunedForNode(cs, node)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("getting the current %s value in pod %s", procCmdline, pod.Name))
			cmdlineOrig, err := GetFileInPod(pod, procCmdline)
			Expect(err).NotTo(HaveOccurred())
			By(fmt.Sprintf("%s has %s: %s", pod.Name, procCmdline, cmdlineOrig))

			By(fmt.Sprintf("labelling node %s with label %s", node.Name, nodeLabelRealtime))
			_, err = exec.Command("oc", "label", "node", "--overwrite", node.Name, nodeLabelRealtime+"=").CombinedOutput()
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("creating custom realtime profile %s", profileRealtime))
			_, err = exec.Command("oc", "create", "-n", ntoconfig.OperatorNamespace(), "-f", profileRealtime).CombinedOutput()
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("creating custom MachineConfigPool %s", mcpRealtime))
			_, err = exec.Command("oc", "create", "-f", mcpRealtime).CombinedOutput()
			Expect(err).NotTo(HaveOccurred())

			By("waiting for worker-rt MachineConfigPool UpdatedMachineCount == 1")
			err = WaitForPoolUpdatedMachineCount(cs, "worker-rt", 1)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("getting the current %s value in pod %s", procCmdline, pod.Name))
			cmdlineNew, err := GetFileInPod(pod, procCmdline)
			Expect(err).NotTo(HaveOccurred())
			Logf("%s has %s: %s", pod.Name, procCmdline, cmdlineNew)

			// Check the key kernel parameters for the realtime profile to be present in /proc/cmdline
			By("ensuring the custom worker node profile was set")
			for _, v := range []string{"skew_tick", "intel_pstate=disable", "nosoftlockup", "tsc=nowatchdog"} {
				Expect(strings.Contains(cmdlineNew, v)).To(BeTrue(), "missing '%s' in %s: %s", v, procCmdline, cmdlineNew)
			}

			// Node label needs to be removed first, and we also need to wait for the worker pool to complete the update;
			// otherwise worker-rt MachineConfigPool deletion would cause Degraded state of the worker pool.
			// see rhbz#1816239
			By(fmt.Sprintf("removing label %s from node %s", nodeLabelRealtime, node.Name))
			out, err := exec.Command("oc", "label", "node", "--overwrite", node.Name, nodeLabelRealtime+"-").CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), string(out))

			By(fmt.Sprintf("deleting the custom realtime profile %s", profileRealtime))
			out, err = exec.Command("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileRealtime).CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), string(out))

			// Wait for the worker machineCount to go to the original value when the node was not part of worker-rt pool.
			By(fmt.Sprintf("waiting for worker UpdatedMachineCount == %d", workerMachinesOrig))
			err = WaitForPoolUpdatedMachineCount(cs, "worker", workerMachinesOrig)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("getting the current %s value in pod %s", procCmdline, pod.Name))
			cmdlineNew, err = GetFileInPod(pod, procCmdline)
			Logf("%s has %s: %s", pod.Name, procCmdline, cmdlineNew)

			By("ensuring the original kernel command line was restored")
			Expect(cmdlineOrig == cmdlineNew).To(BeTrue(), "kernel parameters as retrieved from %s after profile rollback do not match", procCmdline)

			By(fmt.Sprintf("deleting custom MachineConfigPool %s", mcpRealtime))
			out, err = exec.Command("oc", "delete", "-f", mcpRealtime).CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), string(out))
		})
	})
})
