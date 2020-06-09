package e2e

import (
	"fmt"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"

	util "github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

// Test the basic functionality of NTO and its operands.  The default sysctl(s)
// need(s) to be set across the nodes.
var _ = ginkgo.Describe("[basic][default_node_sysctl] Node Tuning Operator default profile set", func() {
	sysctlVar := "net.ipv4.neigh.default.gc_thresh1"

	ginkgo.It(fmt.Sprintf("%s set", sysctlVar), func() {
		ginkgo.By("getting a list of worker nodes")
		nodes, err := util.GetNodesByRole(cs, "worker")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(len(nodes)).NotTo(gomega.BeZero(), "number of worker nodes is 0")

		node := nodes[0]
		ginkgo.By(fmt.Sprintf("getting a tuned pod running on node %s", node.Name))
		pod, err := util.GetTunedForNode(cs, &node)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		ginkgo.By("ensuring the default worker node profile was set")
		err = util.EnsureSysctl(pod, sysctlVar, "8192")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	})
})
