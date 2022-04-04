package __performance_profile_creator_test

import (
	"testing"

	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/junit"
	ginkgo_reporters "kubevirt.io/qe-tools/pkg/ginkgo-reporters"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestPerformanceProfileCreator(t *testing.T) {
	RegisterFailHandler(Fail)

	rr := []Reporter{}
	if ginkgo_reporters.Polarion.Run {
		rr = append(rr, &ginkgo_reporters.Polarion)
	}
	rr = append(rr, junit.NewJUnitReporter("performance_profile_creator"))
	RunSpecsWithDefaultAndCustomReporters(t, "Performance Profile Creator tests", rr)
}
