package e2e

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"

	coreapi "k8s.io/api/core/v1"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	util "github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

// Test the application (and rollback) of [scheduler]'s default_irq_smp_affinity option.
// See: https://github.com/redhat-performance/tuned/pull/306
var _ = ginkgo.Describe("[basic][default_irq_smp_affinity] Node Tuning Operator set irq default smp affinity", func() {
	const (
		profileAffinity0          = "../testing_manifests/default_irq_smp_affinity0.yaml"
		profileAffinity1          = "../testing_manifests/default_irq_smp_affinity1.yaml"
		nodeLabelAffinity         = "tuned.openshift.io/default-irq-smp-affinity" // make sure this matches the value in profileAffinity[01] files
		procIrqDefaultSmpAffinity = "/proc/irq/default_smp_affinity"
		maskExp1                  = "2" // Mask to restrict IRQs to CPU1 (2^1);  make sure this matches the value in profileAffinity1 file
	)

	ginkgo.Context("irq default smp affinity", func() {
		var (
			maskExp0 string
			node     *coreapi.Node
		)

		// Cleanup code to roll back cluster changes done by this test even if it fails in the middle of ginkgo.It()
		ginkgo.AfterEach(func() {
			ginkgo.By("cluster changes rollback")
			if node != nil {
				util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelAffinity+"-")
			}
			util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", profileAffinity0)
		})

		ginkgo.It(fmt.Sprintf("default_irq_smp_affinity: %s set", procIrqDefaultSmpAffinity), func() {
			const (
				pollInterval = 5 * time.Second
				waitDuration = 5 * time.Minute
			)
			cmdGrepAffinity := []string{"grep", "-o", ".$", procIrqDefaultSmpAffinity}

			ginkgo.By("getting a list of worker nodes")
			nodes, err := util.GetNodesByRole(cs, "worker")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(len(nodes)).NotTo(gomega.BeZero(), "number of worker nodes is 0")

			node = &nodes[0]
			ginkgo.By(fmt.Sprintf("getting a TuneD Pod running on node %s", node.Name))
			pod, err := util.GetTunedForNode(cs, node)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("getting the number of CPUs on %s", node.Name))
			nCPUs, err := util.ExecCmdInPod(pod, "nproc", "--all")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			nCPUs = strings.TrimSpace(nCPUs)
			cpus, err := strconv.ParseUint(nCPUs, 10, 0)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Expect the default worker node profile applied prior to getting any current values.
			ginkgo.By(fmt.Sprintf("waiting for TuneD profile %s on node %s", util.GetDefaultWorkerProfile(node), node.Name))
			err = util.WaitForProfileConditionStatus(cs, pollInterval, waitDuration, node.Name, util.GetDefaultWorkerProfile(node), tunedv1.TunedProfileApplied, coreapi.ConditionTrue)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("getting the original value of %s", procIrqDefaultSmpAffinity))
			valOrig, err := util.ExecCmdInPod(pod, cmdGrepAffinity...)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			valOrig = strings.TrimSpace(valOrig)
			util.Logf("%s has the last nibble of %s: %s", pod.Name, procIrqDefaultSmpAffinity, valOrig)

			ginkgo.By(fmt.Sprintf("labelling node %s with label %s", node.Name, nodeLabelAffinity))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelAffinity+"=")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("creating the custom affinity profile %s", profileAffinity0))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-n", ntoconfig.WatchNamespace(), "-f", profileAffinity0)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Check only the last nibble of the /proc/irq/default_smp_affinity mask.  Some virtualized (Xen) systems have
			// a four nibbles affinity mask even though they have only 4 vCPUs.
			n, err := strconv.ParseUint(valOrig[len(valOrig)-1:], 16, 4)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			// Mask to leave CPU1 alone wrt. IRQs, e.g. (f & ~(2^1)); make sure this matches the value in profileAffinity0 file.
			n &= ^(uint64(1 << 1)) & ((1 << cpus) - 1)
			maskExp0 = strconv.FormatUint(n, 16)
			ginkgo.By(fmt.Sprintf("ensuring the correct value of %s was set in the last nibble of %s", maskExp0, procIrqDefaultSmpAffinity))
			_, err = util.WaitForCmdOutputInPod(pollInterval, waitDuration, pod, maskExp0, true, cmdGrepAffinity...)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("applying the custom affinity profile %s", profileAffinity1))
			_, _, err = util.ExecAndLogCommand("oc", "apply", "-n", ntoconfig.WatchNamespace(), "-f", profileAffinity1)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("ensuring the correct value of %s was set in the last nibble of %s", maskExp1, procIrqDefaultSmpAffinity))
			_, err = util.WaitForCmdOutputInPod(pollInterval, waitDuration, pod, maskExp1, true, cmdGrepAffinity...)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting the custom affinity profile %s", profileAffinity0))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", profileAffinity0)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("ensuring the original value of %s was set in the last nibble of %s", valOrig, procIrqDefaultSmpAffinity))
			_, err = util.WaitForCmdOutputInPod(pollInterval, waitDuration, pod, valOrig, true, cmdGrepAffinity...)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("removing label %s from node %s", nodeLabelAffinity, node.Name))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelAffinity+"-")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})
	})
})
