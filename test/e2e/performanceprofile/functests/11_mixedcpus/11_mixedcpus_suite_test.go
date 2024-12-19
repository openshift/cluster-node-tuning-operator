package __mixedcpus_test

import (
	"context"
	"log"
	"os"
	"testing"

	"github.com/go-logr/stdr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	nodeinspector "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/node_inspector"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

func TestMixedCPUs(t *testing.T) {
	ctrllog.SetLogger(stdr.New(log.New(os.Stderr, "", log.LstdFlags|log.Lshortfile)))
	RegisterFailHandler(Fail)
	RunSpecs(t, "Performance Profile Mixed Cpus")
}

var _ = AfterSuite(func() {
	Expect(nodeinspector.Delete(context.TODO())).To(Succeed())
})
