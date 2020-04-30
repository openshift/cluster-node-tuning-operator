package e2e

import (
	"fmt"
	"os/exec"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	. "github.com/openshift/cluster-node-tuning-operator/test/e2e/utils"

	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	coreapi "k8s.io/api/core/v1"
)

// Test the application (and rollback) of a custom profile via node labelling.
var _ = Describe("Node Tuning Operator: custom profile: node labels", func() {
	const (
		profileHugepages   = "../../../examples/hugepages.yaml"
		nodeLabelHugepages = "tuned.openshift.io/hugepages"
		sysctlVar          = "vm.nr_hugepages"
	)

	Context("custom profile: node labels", func() {
		var (
			node *coreapi.Node
		)

		// Cleanup code to roll back cluster changes done by this test even if it fails in the middle of It()
		AfterEach(func() {
			By("cluster changes rollback")
			if node != nil {
				exec.Command("oc", "label", "node", "--overwrite", node.Name, nodeLabelHugepages+"-").CombinedOutput()
			}
			exec.Command("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileHugepages).CombinedOutput()
		})

		It(fmt.Sprintf("%s set", sysctlVar), func() {
			By("getting a list of worker nodes")
			nodes, err := GetNodesByRole(cs, "worker")
			Expect(err).NotTo(HaveOccurred())
			Expect(len(nodes)).NotTo(BeZero())

			node = &nodes[0]
			By(fmt.Sprintf("getting a tuned pod running on node %s", node.Name))
			pod, err := GetTunedForNode(cs, node)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("getting the current value of %s in pod %s", sysctlVar, pod.Name))
			valOrig, err := GetSysctl(sysctlVar, pod)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("labelling node %s with label %s", node.Name, nodeLabelHugepages))
			_, err = exec.Command("oc", "label", "node", "--overwrite", node.Name, nodeLabelHugepages+"=").CombinedOutput()
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("creating the custom hugepages profile %s", profileHugepages))
			_, err = exec.Command("oc", "create", "-n", ntoconfig.OperatorNamespace(), "-f", profileHugepages).CombinedOutput()
			Expect(err).NotTo(HaveOccurred())

			By("ensuring the custom worker node profile was set")
			err = EnsureSysctl(pod, sysctlVar, "16")
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("deleting the custom hugepages profile %s", profileHugepages))
			_, err = exec.Command("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileHugepages).CombinedOutput()
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("ensuring the original %s value (%s) is set in pod %s", sysctlVar, valOrig, pod.Name))
			err = EnsureSysctl(pod, sysctlVar, valOrig)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("removing label %s from node %s", nodeLabelHugepages, node.Name))
			_, err = exec.Command("oc", "label", "node", "--overwrite", node.Name, nodeLabelHugepages+"-").CombinedOutput()
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
