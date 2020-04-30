package e2e

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/openshift/cluster-node-tuning-operator/test/framework"
)

var (
	cs = framework.NewClientSet()
)

// Node Tuning Operator e2e tests causing node reboots
func TestNodeTuningOperator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Node Tuning Operator e2e tests: reboots")
}
