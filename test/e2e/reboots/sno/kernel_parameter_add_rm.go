package e2e

import (
	"fmt"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	coreapi "k8s.io/api/core/v1"

	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

// Test the removal of a kernel parameter defined by a parent profile.
var _ = ginkgo.Describe("[reboots][kernel_parameter_add_rm] Node Tuning Operator parent profile kernel parameter removal", func() {
	const (
		profileParent = "../../testing_manifests/sno/kernel_parameter_add_rm-parent.yaml"
		profileChild  = "../../testing_manifests/sno/kernel_parameter_add_rm-child.yaml"
		procCmdline   = "/proc/cmdline"
	)

	ginkgo.Context("kernel parameter add/remove", func() {
		var (
			node *coreapi.Node
		)

		// Cleanup code to roll back cluster changes done by this test even if it fails in the middle of ginkgo.It()
		ginkgo.AfterEach(func() {
			// The cleanup will not work during the time API server is unavailable, e.g. during SNO reboot.
			ginkgo.By("cluster changes rollback")
			util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", profileParent)
			util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", profileChild)
		})

		ginkgo.It("kernel parameters set", func() {
			const (
				pollInterval = 5 * time.Second
				waitDuration = 5 * time.Minute
				paramAdd1    = "nto.e2e.child.add1"
				paramAdd2    = "nto.e2e.child.add2"
				paramRemove  = "nto.e2e.parent.remove"
				paramKeep    = "nto.e2e.parent.keep"
			)
			cmdCatCmdline := []string{"cat", procCmdline}

			ginkgo.By("getting a list of master nodes")
			nodes, err := util.GetNodesByRole(cs, "master")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(len(nodes)).To(gomega.Equal(1), "number of master nodes: %d", len(nodes))

			masterMachinesOrig, err := util.GetUpdatedMachineCountForPool(cs, "master")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			node = &nodes[0]
			ginkgo.By(fmt.Sprintf("getting a TuneD Pod running on node %s", node.Name))
			pod, err := util.GetTunedForNode(cs, node)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("getting the current %s value in Pod %s", procCmdline, pod.Name))
			cmdlineOrig, err := util.WaitForCmdInPod(pollInterval, waitDuration, pod, cmdCatCmdline...)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			util.Logf(fmt.Sprintf("%s has %s: %s", pod.Name, procCmdline, cmdlineOrig))

			ginkgo.By(fmt.Sprintf("creating custom parent profile %s", profileParent))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-n", ntoconfig.WatchNamespace(), "-f", profileParent)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			waitForMCPFlip()

			ginkgo.By(fmt.Sprintf("getting the current %s value in Pod %s", procCmdline, pod.Name))
			cmdlineNew, err := util.WaitForCmdInPod(pollInterval, waitDuration, pod, cmdCatCmdline...)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			util.Logf("%s has %s: %s", pod.Name, procCmdline, cmdlineNew)

			// Check the key kernel parameters for the parent profile to be present in /proc/cmdline
			ginkgo.By("ensuring the custom master parent profile was set")
			for _, v := range []string{paramKeep, paramRemove} {
				gomega.Expect(strings.Contains(cmdlineNew, v)).To(gomega.BeTrue(), "missing '%s' in %s: %s", v, procCmdline, cmdlineNew)
			}

			ginkgo.By(fmt.Sprintf("creating custom child profile %s", profileChild))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-n", ntoconfig.WatchNamespace(), "-f", profileChild)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			waitForMCPFlip()

			ginkgo.By(fmt.Sprintf("getting the current %s value in Pod %s", procCmdline, pod.Name))
			cmdlineNew, err = util.WaitForCmdInPod(pollInterval, waitDuration, pod, cmdCatCmdline...)
			util.Logf("%s has %s: %s", pod.Name, procCmdline, cmdlineNew)

			ginkgo.By("ensuring the custom master child profile was set")
			gomega.Expect(strings.Contains(cmdlineNew, paramRemove)).NotTo(gomega.BeTrue(), "found '%s' in %s: %s", paramRemove, procCmdline, cmdlineNew)
			for _, v := range []string{paramAdd1, paramAdd2, paramKeep} {
				gomega.Expect(strings.Contains(cmdlineNew, v)).To(gomega.BeTrue(), "missing '%s' in %s: %s", v, procCmdline, cmdlineNew)
			}

			ginkgo.By(fmt.Sprintf("deleting the custom parent profile %s", profileParent))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", profileParent)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting the custom child profile %s", profileChild))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", profileChild)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Wait for the master machineCount to go to the original value.
			ginkgo.By(fmt.Sprintf("waiting for master UpdatedMachineCount == %d", masterMachinesOrig))
			err = util.WaitForPoolUpdatedMachineCount(cs, "master", masterMachinesOrig)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})
	})
})
