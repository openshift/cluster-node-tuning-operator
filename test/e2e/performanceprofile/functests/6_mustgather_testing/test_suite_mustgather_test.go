package pao_mustgather

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/reporters"
	. "github.com/onsi/gomega"

	qe_reporters "kubevirt.io/qe-tools/pkg/ginkgo-reporters"
)

var _ = BeforeSuite(func() {
	By("Looking for oc tool")
	ocExec, err := exec.LookPath("oc")
	if err != nil {
		fmt.Fprintf(GinkgoWriter, "Unable to find oc executable: %v\n", err)
		Skip(fmt.Sprintf("unable to find 'oc' executable %v\n", err))
	}

	mgDestDirParam := fmt.Sprintf("--dest-dir=%s", destDir)

	cmdline := []string{
		ocExec,
		"adm",
		"must-gather",
		mgDestDirParam,
	}
	By(fmt.Sprintf("running: %v\n", cmdline))

	cmd := exec.Command(cmdline[0], cmdline[1:]...)
	cmd.Stderr = GinkgoWriter

	_, err = cmd.Output()
	Expect(err).ToNot(HaveOccurred())
})

var _ = AfterSuite(func() {
	os.RemoveAll(destDir)
})

func TestPaoMustgatherTests(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Performance Profile must gather tests")
}

var _ = ReportAfterSuite("e2e render suite", func(r Report) {
	if qe_reporters.Polarion.Run {
		reporters.ReportViaDeprecatedReporter(&qe_reporters.Polarion, r)
	}
})
