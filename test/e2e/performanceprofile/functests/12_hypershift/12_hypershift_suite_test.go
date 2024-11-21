package __hypershift_test

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/go-logr/stdr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/errors"

	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/namespaces"
	nodeinspector "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/node_inspector"
)

var _ = BeforeSuite(func() {
	Expect(testclient.ClientsEnabled).To(BeTrue())
	err := testclient.DataPlaneClient.Create(context.TODO(), namespaces.TestingNamespace)
	if errors.IsAlreadyExists(err) {
		testlog.Warning("test namespace already exists, that is unexpected")
		return
	}
	Expect(err).ToNot(HaveOccurred())
})

var _ = AfterSuite(func() {
	err := testclient.DataPlaneClient.Delete(context.TODO(), namespaces.TestingNamespace)
	Expect(err).ToNot(HaveOccurred())
	Expect(namespaces.WaitForDeletion(testutils.NamespaceTesting, 5*time.Minute)).To(Succeed())
	Expect(nodeinspector.Delete(context.TODO())).To(Succeed())
})

func TestHypershift(t *testing.T) {
	ctrllog.SetLogger(stdr.New(log.New(os.Stderr, "", log.LstdFlags|log.Lshortfile)))

	RegisterFailHandler(Fail)

	RunSpecs(t, "PAO Hypershift tests")
}
