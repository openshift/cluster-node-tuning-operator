package __performance_ppc

import (
	"context"
	"fmt"
	"os/exec"
	"testing"

	. "github.com/onsi/ginkgo/v2"

	. "github.com/onsi/gomega"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	nodeinspector "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/node_inspector"
)

const (
	DefaultPodmanBinaryPath = "/usr/bin/podman"
)

var _ = BeforeSuite(func() {
	var err error
	By("Check podman binary exists")
	path, err := exec.LookPath(DefaultPodmanBinaryPath)
	if err != nil {
		Skip(fmt.Sprintf("%s doesn't exists", DefaultPodmanBinaryPath))
	}
	testlog.Infof("Podman binary executed from path %s", path)
	By("Checking Environment Variables")
	testlog.Infof("NTO Image used: %s", testutils.NTOImage)
	if testutils.MustGatherDir == "" {
		Skip("set env variable MUSTGATHER_DIR to ocp mustgather directory")
	}
	testlog.Infof("Mustgather Directory used: %s", testutils.MustGatherDir)

})

var _ = AfterSuite(func() {
	nodeinspector.Delete(context.TODO())
})

func TestPPC(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "PPC Suite")
}
