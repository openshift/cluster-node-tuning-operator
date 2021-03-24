package e2e

import (
	"testing"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"

	"github.com/openshift/cluster-node-tuning-operator/test/framework"
)

var (
	cs = framework.NewClientSet()
)

func TestNodeTuningOperator(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "Node Tuning Operator e2e tests: core")
}
