package e2e

import (
	"log"
	"os"
	"testing"

	"github.com/go-logr/stdr"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

func TestNodeTuningOperatorSecurity(t *testing.T) {
	ctrllog.SetLogger(stdr.New(log.New(os.Stderr, "", log.LstdFlags|log.Lshortfile)))
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "Node Tuning Operator e2e tests: security")
}
