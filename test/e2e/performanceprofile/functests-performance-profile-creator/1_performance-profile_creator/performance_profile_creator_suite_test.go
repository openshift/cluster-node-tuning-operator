package __performance_profile_creator_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/reporters"
	. "github.com/onsi/gomega"
	qe_reporters "kubevirt.io/qe-tools/pkg/ginkgo-reporters"
)

func TestPerformanceProfileCreator(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Performance Profile Creator tests")
}

var _ = ReportAfterSuite("ppc suite", func(r Report) {
	if qe_reporters.Polarion.Run {
		reporters.ReportViaDeprecatedReporter(&qe_reporters.Polarion, r)
	}
})
