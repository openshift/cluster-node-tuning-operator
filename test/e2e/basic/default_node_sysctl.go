package e2e

import (
	"fmt"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"

	coreapi "k8s.io/api/core/v1"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
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

		node := &nodes[0]
		ginkgo.By(fmt.Sprintf("getting a TuneD Pod running on node %s", node.Name))
		pod, err := util.GetTunedForNode(cs, node)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		// Expect the default worker node profile applied prior to getting any current values.
		ginkgo.By(fmt.Sprintf("waiting for TuneD profile %s on node %s", util.GetDefaultWorkerProfile(node), node.Name))
		err = util.WaitForProfileConditionStatus(cs, pollInterval, waitDuration, node.Name, util.GetDefaultWorkerProfile(node), tunedv1.TunedProfileApplied, coreapi.ConditionTrue)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		ginkgo.By("ensuring the default worker node profile was set")
		_, err = util.WaitForSysctlValueInPod(pollInterval, waitDuration, pod, sysctlVar, "8192")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	})
})
