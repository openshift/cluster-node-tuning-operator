package __mixedcpus_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	nodeinspector "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/node_inspector"
)

func TestMixedCPUs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Performance Profile Mixed Cpus")
}

var _ = AfterSuite(func() {
	nodeinspector.Delete(context.TODO())
})
