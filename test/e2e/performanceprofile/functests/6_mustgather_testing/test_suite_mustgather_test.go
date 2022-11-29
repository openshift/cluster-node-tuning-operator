package pao_mustgather

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo"
	"github.com/onsi/gomega"
	. "github.com/onsi/gomega"

	ginkgo_reporters "kubevirt.io/qe-tools/pkg/ginkgo-reporters"

	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/junit"
)

const must_gather_version = "4.12-snapshot"
const must_gather_image = "quay.io/openshift-kni/performance-addon-operator-must-gather"

var _ = BeforeSuite(func() {
	By("Looking for oc tool")
	ocExec, err := exec.LookPath("oc")
	if err != nil {
		fmt.Fprintf(ginkgo.GinkgoWriter, "Unable to find oc executable: %v\n", err)
		Skip(fmt.Sprintf("unable to find 'oc' executable %v\n", err))
	}

	mgImageParam := fmt.Sprintf("--image=%s:%s", must_gather_image, must_gather_version)
	mgDestDirParam := fmt.Sprintf("--dest-dir=%s", destDir)

	cmdline := []string{
		ocExec,
		"adm",
		"must-gather",
		mgImageParam,
		mgDestDirParam,
	}
	ginkgo.By(fmt.Sprintf("running: %v\n", cmdline))

	cmd := exec.Command(cmdline[0], cmdline[1:]...)
	cmd.Stderr = ginkgo.GinkgoWriter

	_, err = cmd.Output()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
})

var _ = AfterSuite(func() {
	os.RemoveAll(destDir)
})

func TestPaoMustgatherTests(t *testing.T) {
	RegisterFailHandler(Fail)

	rr := []Reporter{}
	if ginkgo_reporters.Polarion.Run {
		rr = append(rr, &ginkgo_reporters.Polarion)
	}
	rr = append(rr, junit.NewJUnitReporter("must-gather"))
	RunSpecsWithDefaultAndCustomReporters(t, "PAO must-gather tests", rr)
}
