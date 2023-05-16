//go:build !unittests
// +build !unittests

package __latency_test

import (
	"context"
	"flag"
	"path"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/reporters"
	"github.com/onsi/ginkgo/v2/types"
	. "github.com/onsi/gomega"
	kniK8sReporter "github.com/openshift-kni/k8sreporter"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/k8sreporter"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/namespaces"
	stringsutil "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/strings"

	"k8s.io/apimachinery/pkg/api/errors"

	qe_reporters "kubevirt.io/qe-tools/pkg/ginkgo-reporters"
)

var (
	reportPath *string
	junitPath  *string
	reporter   *kniK8sReporter.KubernetesReporter
)

func init() {
	junitPath = flag.String("junit", "", "the path for the junit format report")
	reportPath = flag.String("report", "", "the path of the report file containing details for failed tests")
}

var _ = BeforeSuite(func() {
	Expect(testclient.ClientsEnabled).To(BeTrue())
	// create test namespace
	err := testclient.Client.Create(context.TODO(), namespaces.TestingNamespace)
	if errors.IsAlreadyExists(err) {
		testlog.Warning("test namespace already exists, that is unexpected")
		return
	}
	Expect(err).ToNot(HaveOccurred())
})

var _ = AfterSuite(func() {
	err := testclient.Client.Delete(context.TODO(), namespaces.TestingNamespace)
	Expect(err).ToNot(HaveOccurred())
	err = namespaces.WaitForDeletion(testutils.NamespaceTesting, 5*time.Minute)
})

func TestLatency(t *testing.T) {
	RegisterFailHandler(Fail)
	if *reportPath != "" {
		reporter = k8sreporter.New("", *reportPath, namespaces.TestingNamespace.Name)
	}

	RunSpecs(t, "Performance Addon Operator latency e2e tests")
}

var _ = ReportAfterSuite("e2e serial suite", func(r Report) {
	if qe_reporters.Polarion.Run {
		reporters.ReportViaDeprecatedReporter(&qe_reporters.Polarion, r)
	}
	if *junitPath != "" {
		junitFile := path.Join(*junitPath, "latency_junit.xml")
		reporters.GenerateJUnitReportWithConfig(r, junitFile, reporters.JunitReportConfig{
			OmitTimelinesForSpecState: types.SpecStatePassed | types.SpecStateSkipped,
			OmitLeafNodeType:          true,
			OmitSuiteSetupNodes:       true,
		})
	}
})

var _ = ReportAfterEach(func(specReport types.SpecReport) {
	if specReport.Failed() == false {
		return
	}

	if *reportPath != "" {
		dumpSubPath := stringsutil.CleanDirName(specReport.LeafNodeText)
		reporter.Dump(utils.LogsFetchDuration, dumpSubPath)
	}
})
