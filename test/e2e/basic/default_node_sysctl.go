package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	. "github.com/openshift/cluster-node-tuning-operator/test/e2e/utils"
)

// Test the basic functionality of NTO and its operands.  The default sysctl(s)
// need(s) to be set across the nodes.
var _ = Describe("Node Tuning Operator: default profile set", func() {
	sysctlVar := "net.ipv4.neigh.default.gc_thresh1"

	It(fmt.Sprintf("%s set", sysctlVar), func() {
		By("getting a list of worker nodes")
		nodes, err := GetNodesByRole(cs, "worker")
		Expect(err).NotTo(HaveOccurred())
		Expect(len(nodes)).NotTo(BeZero())

		node := nodes[0]
		By(fmt.Sprintf("getting a tuned pod running on node %s", node.Name))
		pod, err := GetTunedForNode(cs, &node)
		Expect(err).NotTo(HaveOccurred())

		By("ensuring the default worker node profile was set")
		err = EnsureSysctl(pod, sysctlVar, "8192")
		Expect(err).NotTo(HaveOccurred())
	})
})
