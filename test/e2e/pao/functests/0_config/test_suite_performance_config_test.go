//go:build !unittests
// +build !unittests

package __performance_config_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/pao/functests/utils/junit"

	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/pao/functests/utils/client"
	ginkgo_reporters "kubevirt.io/qe-tools/pkg/ginkgo-reporters"
)

func TestPerformanceConfig(t *testing.T) {
	RegisterFailHandler(Fail)

	rr := []Reporter{}
	if ginkgo_reporters.Polarion.Run {
		rr = append(rr, &ginkgo_reporters.Polarion)
	}
	rr = append(rr, junit.NewJUnitReporter("performance_config"))
	RunSpecsWithDefaultAndCustomReporters(t, "Performance Addon Operator configuration", rr)
}

var _ = BeforeSuite(func() {
	Expect(testclient.ClientsEnabled).To(BeTrue())

})
