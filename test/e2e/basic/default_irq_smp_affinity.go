package e2e

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"

	coreapi "k8s.io/api/core/v1"

	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	util "github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

// Test the application (and rollback) of [scheduler]'s default_irq_smp_affinity option.
// See: https://github.com/redhat-performance/tuned/pull/306
var _ = ginkgo.Describe("[basic][default_irq_smp_affinity] Node Tuning Operator set irq default smp affinity", func() {
	const (
		profileAffinity0          = "../testing_manifests/default_irq_smp_affinity0.yaml"
		profileAffinity1          = "../testing_manifests/default_irq_smp_affinity1.yaml"
		nodeLabelAffinity         = "tuned.openshift.io/default-irq-smp-affinity" // make sure this matches the value in profileAffinity file
		procIrqDefaultSmpAffinity = "/proc/irq/default_smp_affinity"
		maskExp                   = "2" // Mask to restrict IRQs to CPU1 (2^1);  make sure this matches the value in profileAffinity file
	)

	ginkgo.Context("irq default smp affinity", func() {
		var (
			valExp string
			node   *coreapi.Node
		)

		// Cleanup code to roll back cluster changes done by this test even if it fails in the middle of ginkgo.It()
		ginkgo.AfterEach(func() {
			ginkgo.By("cluster changes rollback")
			if node != nil {
				util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelAffinity+"-")
			}
			util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileAffinity0)
		})

		ginkgo.It(fmt.Sprintf("default_irq_smp_affinity: %s set", procIrqDefaultSmpAffinity), func() {
			cmdCatAffinity := []string{"cat", procIrqDefaultSmpAffinity}
			ginkgo.By("getting a list of worker nodes")
			nodes, err := util.GetNodesByRole(cs, "worker")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(len(nodes)).NotTo(gomega.BeZero(), "number of worker nodes is 0")

			node = &nodes[0]
			ginkgo.By(fmt.Sprintf("getting a tuned pod running on node %s", node.Name))
			pod, err := util.GetTunedForNode(cs, node)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("getting the original value of %s", procIrqDefaultSmpAffinity))
			valOrig, err := util.ExecCmdInPod(pod, cmdCatAffinity...)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			valOrig = strings.TrimSpace(valOrig)
			util.Logf("%s has %s: %s", pod.Name, procIrqDefaultSmpAffinity, valOrig)

			ginkgo.By(fmt.Sprintf("labelling node %s with label %s", node.Name, nodeLabelAffinity))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelAffinity+"=")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("creating the custom affinity profile %s", profileAffinity0))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-n", ntoconfig.OperatorNamespace(), "-f", profileAffinity0)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("ensuring the correct value of %s was set in %s", procIrqDefaultSmpAffinity, procIrqDefaultSmpAffinity))
			n, err := strconv.ParseUint(valOrig[len(valOrig)-1:], 16, 4)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			n &= ^(uint64(1 << 1)) // Mask to leave CPU1 alone wrt. IRQs, e.g. (f & ~(2^1)); make sure this matches the value in profileAffinity file
			valExp = valOrig[0:len(valOrig)-1] + strconv.FormatUint(n, 16)
			util.Logf("calculated expected IRQ mask: %s", valExp)
			_, err = util.EnsureCmdOutputInPod(5*time.Second, 5*time.Minute, valExp, pod, cmdCatAffinity...)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("applying the custom affinity profile %s", profileAffinity1))
			_, _, err = util.ExecAndLogCommand("oc", "apply", "-n", ntoconfig.OperatorNamespace(), "-f", profileAffinity1)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("ensuring the correct value of %s was set in %s", procIrqDefaultSmpAffinity, procIrqDefaultSmpAffinity))
			valExp = valOrig[0:len(valOrig)-1] + maskExp
			_, err = util.EnsureCmdOutputInPod(5*time.Second, 5*time.Minute, valExp, pod, cmdCatAffinity...)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting the custom affinity profile %s", profileAffinity0))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileAffinity0)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("ensuring the original value of %s was set in %s", procIrqDefaultSmpAffinity, procIrqDefaultSmpAffinity))
			_, err = util.EnsureCmdOutputInPod(5*time.Second, 5*time.Minute, valOrig, pod, cmdCatAffinity...)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("removing label %s from node %s", nodeLabelAffinity, node.Name))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelAffinity+"-")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})
	})
})
