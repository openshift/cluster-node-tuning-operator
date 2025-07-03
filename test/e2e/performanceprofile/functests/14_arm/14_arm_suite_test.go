package __arm_test

import (
	"log"
	"os"
	"testing"

	"github.com/go-logr/stdr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/onsi/ginkgo/v2/reporters"
	qe_reporters "kubevirt.io/qe-tools/pkg/ginkgo-reporters"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

func TestARM(t *testing.T) {
	ctrllog.SetLogger(stdr.New(log.New(os.Stderr, "", log.LstdFlags|log.Lshortfile)))

	RegisterFailHandler(Fail)
	RunSpecs(t, "Kernel page size suite")
}

var _ = ReportAfterSuite("kernel page size suite", func(r Report) {
	if qe_reporters.Polarion.Run {
		reporters.ReportViaDeprecatedReporter(&qe_reporters.Polarion, r)
	}
})
