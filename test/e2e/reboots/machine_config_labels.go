package e2e

import (
	"fmt"
	"strings"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"

	coreapi "k8s.io/api/core/v1"

	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

// Test the MachineConfig labels matching functionality and rollback.
var _ = ginkgo.Describe("[reboots][machine_config_labels] Node Tuning Operator machine config labels", func() {
	const (
		profileRealtime   = "../../../examples/realtime-mc.yaml"
		mcpRealtime       = "../../../examples/realtime-mcp.yaml"
		nodeLabelRealtime = "node-role.kubernetes.io/worker-rt"
		procCmdline       = "/proc/cmdline"
	)

	ginkgo.Context("machine config labels", func() {
		var (
			node *coreapi.Node
		)

		// Cleanup code to roll back cluster changes done by this test even if it fails in the middle of ginkgo.It()
		ginkgo.AfterEach(func() {
			// This cleanup code ignores issues outlined in rhbz#1816239;
			// this can cause a degraded MachineConfigPool
			ginkgo.By("cluster changes rollback")
			if node != nil {
				util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelRealtime+"-")
			}
			util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileRealtime)
			util.ExecAndLogCommand("oc", "delete", "-f", mcpRealtime)
		})

		ginkgo.It("kernel parameters set", func() {
			const (
				pollInterval = 5 * time.Second
				waitDuration = 5 * time.Minute
			)
			cmdCatCmdline := []string{"cat", procCmdline}

			ginkgo.By("getting a list of worker nodes")
			nodes, err := util.GetNodesByRole(cs, "worker")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(len(nodes)).NotTo(gomega.BeZero(), "number of worker nodes is 0")

			workerMachinesOrig, err := util.GetUpdatedMachineCountForPool(cs, "worker")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			node = &nodes[0]
			ginkgo.By(fmt.Sprintf("getting a TuneD Pod running on node %s", node.Name))
			pod, err := util.GetTunedForNode(cs, node)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("getting the current %s value in Pod %s", procCmdline, pod.Name))
			cmdlineOrig, err := util.WaitForCmdInPod(pollInterval, waitDuration, pod, cmdCatCmdline...)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			util.Logf(fmt.Sprintf("%s has %s: %s", pod.Name, procCmdline, cmdlineOrig))

			ginkgo.By(fmt.Sprintf("labelling node %s with label %s", node.Name, nodeLabelRealtime))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelRealtime+"=")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("creating custom realtime profile %s", profileRealtime))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-n", ntoconfig.OperatorNamespace(), "-f", profileRealtime)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("creating custom MachineConfigPool %s", mcpRealtime))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-f", mcpRealtime)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By("waiting for worker-rt MachineConfigPool UpdatedMachineCount == 1")
			err = util.WaitForPoolUpdatedMachineCount(cs, "worker-rt", 1)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("getting the current %s value in Pod %s", procCmdline, pod.Name))
			cmdlineNew, err := util.WaitForCmdInPod(pollInterval, waitDuration, pod, cmdCatCmdline...)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			util.Logf("%s has %s: %s", pod.Name, procCmdline, cmdlineNew)

			// Check the key kernel parameters for the realtime profile to be present in /proc/cmdline
			ginkgo.By("ensuring the custom worker node profile was set")
			for _, v := range []string{"skew_tick", "intel_pstate=disable", "nosoftlockup", "tsc=nowatchdog"} {
				gomega.Expect(strings.Contains(cmdlineNew, v)).To(gomega.BeTrue(), "missing '%s' in %s: %s", v, procCmdline, cmdlineNew)
			}

			// Node label needs to be removed first, and we also need to wait for the worker pool to complete the update;
			// otherwise worker-rt MachineConfigPool deletion would cause Degraded state of the worker pool.
			// see rhbz#1816239
			ginkgo.By(fmt.Sprintf("removing label %s from node %s", nodeLabelRealtime, node.Name))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelRealtime+"-")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting the custom realtime profile %s", profileRealtime))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileRealtime)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Wait for the worker machineCount to go to the original value when the node was not part of worker-rt pool.
			ginkgo.By(fmt.Sprintf("waiting for worker UpdatedMachineCount == %d", workerMachinesOrig))
			err = util.WaitForPoolUpdatedMachineCount(cs, "worker", workerMachinesOrig)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting custom MachineConfigPool %s", mcpRealtime))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-f", mcpRealtime)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})
	})
})
