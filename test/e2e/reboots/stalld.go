package e2e

import (
	"fmt"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"

	coreapi "k8s.io/api/core/v1"

	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

// Test the installation of systemd units with the stall daemon.
var _ = ginkgo.Describe("[reboots][stalld] Node Tuning Operator installing systemd units and stalld", func() {
	const (
		profileStalldOn   = "../testing_manifests/stalld.yaml"
		profileStalldOff  = "../testing_manifests/stalld-disable.yaml"
		mcpRealtime       = "../../../examples/realtime-mcp.yaml"
		nodeLabelRealtime = "node-role.kubernetes.io/worker-rt"
	)

	ginkgo.Context("stalld", func() {
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
			util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileStalldOn)
			util.ExecAndLogCommand("oc", "delete", "-f", mcpRealtime)
		})

		ginkgo.It("stalld process started/stopped", func() {
			const (
				pollInterval = 5 * time.Second
				waitDuration = 5 * time.Minute
			)

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

			ginkgo.By(fmt.Sprintf("labelling node %s with label %s", node.Name, nodeLabelRealtime))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelRealtime+"=")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// BZ1926903: check for the systemd/TuneD [service] plug-in race
			ginkgo.By(fmt.Sprintf("creating custom realtime profile %s with stalld service stopped,disabled", profileStalldOff))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-n", ntoconfig.OperatorNamespace(), "-f", profileStalldOff)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("creating custom MachineConfigPool %s", mcpRealtime))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-f", mcpRealtime)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			waitForMCPFlip()

			waitForTuneD := func() {
				ginkgo.By(fmt.Sprintf("waiting for the TuneD daemon running on node %s", node.Name))
				_, err := util.WaitForCmdInPod(pollInterval, waitDuration, pod, "test", "-e", "/run/tuned/tuned.pid")
				gomega.Expect(err).NotTo(gomega.HaveOccurred())

				// 10s is very generous to allow for stalld service starting (if any)
				ginkgo.By("sleeping 10s to allow for stalld service starting (if any)")
				time.Sleep(10 * time.Second)
			}
			waitForTuneD()

			ginkgo.By(fmt.Sprintf("checking the stalld daemon is not running on node %s", node.Name))
			out, err := util.ExecCmdInPod(pod, "pidof", "stalld")
			gomega.Expect(out).To(gomega.Equal(""))
			gomega.Expect(err).To(gomega.HaveOccurred()) // pidof exits 1 when there is no running process found

			ginkgo.By(fmt.Sprintf("applying custom realtime profile %s with stalld service", profileStalldOn))
			_, _, err = util.ExecAndLogCommand("oc", "apply", "-n", ntoconfig.OperatorNamespace(), "-f", profileStalldOn)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("checking the stalld daemon is running on node %s", node.Name))
			out, err = util.WaitForCmdInPod(pollInterval, 20*time.Minute, pod, "pidof", "stalld")
			util.Logf(fmt.Sprintf("stalld process running on node %s with PID %s", node.Name, out))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Node label needs to be removed first, and we also need to wait for the worker pool to complete the update;
			// otherwise worker-rt MachineConfigPool deletion would cause Degraded state of the worker pool.
			// see rhbz#1816239
			ginkgo.By(fmt.Sprintf("removing label %s from node %s", nodeLabelRealtime, node.Name))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelRealtime+"-")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting the custom realtime profile %s with stalld service", profileStalldOn))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileStalldOn)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Wait for the worker machineCount to go to the original value when the node was not part of worker-rt pool.
			ginkgo.By(fmt.Sprintf("waiting for worker UpdatedMachineCount == %d", workerMachinesOrig))
			err = util.WaitForPoolUpdatedMachineCount(cs, "worker", workerMachinesOrig)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("checking the stalld daemon is not running on node %s", node.Name))
			_, err = util.ExecCmdInPod(pod, "pidof", "stalld")
			gomega.Expect(err).To(gomega.HaveOccurred()) // pidof exits 1 when there is no running process found

			ginkgo.By(fmt.Sprintf("deleting custom MachineConfigPool %s", mcpRealtime))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-f", mcpRealtime)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})
	})
})
