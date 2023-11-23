package __mixedcpus_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
)

func TestMixedCPUs(t *testing.T) {
	BeforeSuite(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("discovery mode enabled, performance profile not found")
		}
	})
	RegisterFailHandler(Fail)
	RunSpecs(t, "Performance Profile Mixed Cpus")
}
