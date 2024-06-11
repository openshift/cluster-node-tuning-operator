package e2e

import (
	"fmt"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	coreapi "k8s.io/api/core/v1"

	util "github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

// Test the core functionality by reporting host, container OS and cluster version.
var _ = ginkgo.Describe("[core][cluster_version] Node Tuning Operator host, container OS and cluster version", func() {
	var (
		node *coreapi.Node
	)

	ginkgo.It("container OS and cluster version retrievable", func() {
		ginkgo.By("getting a list of worker nodes")
		nodes, err := util.GetNodesByRole(cs, "worker")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(len(nodes)).NotTo(gomega.BeZero(), "number of worker nodes is 0")

		node = &nodes[0]
		ginkgo.By(fmt.Sprintf("getting a TuneD Pod running on node %s", node.Name))
		pod, err := util.GetTunedForNode(cs, node)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		ginkgo.By("getting the TuneD container OS version")
		out, err := util.ExecCmdInPod(pod, "cat", "/etc/os-release")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		util.Logf("%s", out)

		ginkgo.By("getting the cluster version")
		_, _, err = util.ExecAndLogCommand("oc", "get", "clusterversion", "version", "-o", "jsonpath='{.status.desired.version}'")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	})
})
