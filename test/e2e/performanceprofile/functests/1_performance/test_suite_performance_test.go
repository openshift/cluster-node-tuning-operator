//go:build !unittests
// +build !unittests

package __performance_test

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/reporters"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/errors"

	ginkgo_reporters "kubevirt.io/qe-tools/pkg/ginkgo-reporters"

	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/namespaces"
)

var _ = BeforeSuite(func() {
	Expect(testclient.ClientsEnabled).To(BeTrue(), "package client not enabled")
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

func TestPerformance(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Performance Test")
}

var _ = ReportAfterSuite("e2e serial suite", func(r Report) {
	if ginkgo_reporters.Polarion.Run {
		reporters.ReportViaDeprecatedReporter(&ginkgo_reporters.Polarion, r)
	}
	reporters.ReportViaDeprecatedReporter(reporters.NewJUnitReporter("performance"), r)

})
