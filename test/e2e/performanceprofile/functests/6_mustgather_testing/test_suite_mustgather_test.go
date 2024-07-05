package pao_mustgather

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"testing"

	"github.com/go-logr/stdr"
	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/reporters"
	. "github.com/onsi/gomega"

	nodeinspector "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/node_inspector"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

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
	nodeinspector.Delete(context.TODO())
})

func TestPaoMustgatherTests(t *testing.T) {
	ctrllog.SetLogger(stdr.New(log.New(os.Stderr, "", log.LstdFlags|log.Lshortfile)))

	RegisterFailHandler(Fail)

	RunSpecs(t, "Performance Profile must gather tests")
}

var _ = ReportAfterSuite("e2e render suite", func(r Report) {
	if qe_reporters.Polarion.Run {
		reporters.ReportViaDeprecatedReporter(&qe_reporters.Polarion, r)
	}
})
