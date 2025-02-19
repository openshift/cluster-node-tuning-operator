package __llc

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

func TestLLC(t *testing.T) {
	ctrllog.SetLogger(stdr.New(log.New(os.Stderr, "", log.LstdFlags|log.Lshortfile)))

	RegisterFailHandler(Fail)
	RunSpecs(t, "LLC Suite")
}

var _ = ReportAfterSuite("e2e serial suite", func(r Report) {
	if qe_reporters.Polarion.Run {
		reporters.ReportViaDeprecatedReporter(&qe_reporters.Polarion, r)
	}
})
