package e2e

import (
	"fmt"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"

	util "github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

// Test the basic functionality of NTO and its operands.  The default sysctl(s)
// need(s) to be set across the nodes.
var _ = ginkgo.Describe("[basic][default_node_sysctl] Node Tuning Operator default profile set", func() {
	sysctlVar := "net.ipv4.neigh.default.gc_thresh1"

	ginkgo.It(fmt.Sprintf("%s set", sysctlVar), func() {
		const (
			pollInterval = 5 * time.Second
			waitDuration = 5 * time.Minute
		)
		ginkgo.By("getting a list of worker nodes")
		nodes, err := util.GetNodesByRole(cs, "worker")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(len(nodes)).NotTo(gomega.BeZero(), "number of worker nodes is 0")

		node := nodes[0]
		ginkgo.By(fmt.Sprintf("getting a Tuned Pod running on node %s", node.Name))
		pod, err := util.GetTunedForNode(cs, &node)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		ginkgo.By("ensuring the default worker node profile was set")
		_, err = util.WaitForSysctlValueInPod(pollInterval, waitDuration, pod, sysctlVar, "8192")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	})
})
