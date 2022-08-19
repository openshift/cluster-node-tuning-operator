package e2e

import (
	"testing"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"

	"github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
	"github.com/openshift/cluster-node-tuning-operator/test/framework"
)

var (
	cs = framework.NewClientSet()
)

// Node Tuning Operator e2e tests causing node reboots
func TestNodeTuningOperator(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)
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
