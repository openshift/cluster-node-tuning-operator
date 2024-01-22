//go:build !unittests
// +build !unittests

package __performance_config_test

import (
	"flag"
	"log"
	"os"
	"path"
	"testing"

	"github.com/go-logr/stdr"
	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/reporters"
	. "github.com/onsi/gomega"

	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	kniK8sReporter "github.com/openshift-kni/k8sreporter"
	qe_reporters "kubevirt.io/qe-tools/pkg/ginkgo-reporters"

	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/k8sreporter"
)

var (
	reportPath *string
	reporter   *kniK8sReporter.KubernetesReporter
)

func init() {
	reportPath = flag.String("report", "", "the path of the report file containing details for failed tests")
}

func TestPerformanceConfig(t *testing.T) {
	ctrllog.SetLogger(stdr.New(log.New(os.Stderr, "", log.LstdFlags|log.Lshortfile)))
	// We want to collect logs before any resource is deleted in AfterEach, so we register the global fail handler
	// in a way such that the reporter's Dump is always called before the default Fail.
	RegisterFailHandler(func(message string, callerSkip ...int) {
		if reporter != nil {
			reporter.Dump(testutils.LogsFetchDuration, CurrentSpecReport().FullText())
		}

		// Ensure failing line location is not affected by this wrapper
		for i := range callerSkip {
			callerSkip[i]++
		}
		Fail(message, callerSkip...)
	})
	if *reportPath != "" {
		reportPath := path.Join(*reportPath, "nto_failure_report.log")
		reporter = k8sreporter.New(reportPath)
	}

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
