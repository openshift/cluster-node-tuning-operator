package e2e

import (
	"testing"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"

	"github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
	"github.com/openshift/cluster-node-tuning-operator/test/framework"
)

var (
	cs = framework.NewClientSet()
)

// Node Tuning Operator e2e tests causing node reboots
func TestNodeTuningOperator(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)

	util.Logf("getting cluster ControlPlaneTopology")
	controlPlaneTopology, err := util.GetClusterControlPlaneTopology(cs)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	nodeCount, err := util.GetClusterNodes(cs)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	if nodeCount == 1 && controlPlaneTopology == configv1.SingleReplicaTopologyMode {
		// This looks like an SNO cluster.  For the "reboots" tests to work, "master" MCP needs to be targeted.
		util.Logf("seeing only %d node and control plane topology is %v, skipping test suite", nodeCount, controlPlaneTopology)
		return
	}

	ginkgo.RunSpecs(t, "Node Tuning Operator e2e tests: reboots")
}

func waitForMCPFlip() {
	// By creating the custom child profile, we will first see worker-rt MachineConfigPool UpdatedMachineCount drop to 0 first...
	ginkgo.By("waiting for worker-rt MachineConfigPool UpdatedMachineCount == 0")
	err := util.WaitForPoolUpdatedMachineCount(cs, "worker-rt", 0)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	// ...and go up to 1 again next.
	ginkgo.By("waiting for worker-rt MachineConfigPool UpdatedMachineCount == 1")
	err = util.WaitForPoolUpdatedMachineCount(cs, "worker-rt", 1)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
}
