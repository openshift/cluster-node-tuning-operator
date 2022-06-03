//go:build !unittests
// +build !unittests

package __performance_config_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/reporters"
	. "github.com/onsi/gomega"

	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"

	qe_reporters "kubevirt.io/qe-tools/pkg/ginkgo-reporters"
)

func TestPerformanceConfig(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Performance Addon Operator configuration")
}

var _ = BeforeSuite(func() {
	Expect(testclient.ClientsEnabled).To(BeTrue())

})

var _ = ReportAfterSuite("e2e serial suite", func(r Report) {
	if qe_reporters.Polarion.Run {
		reporters.ReportViaDeprecatedReporter(&qe_reporters.Polarion, r)
	}
})
