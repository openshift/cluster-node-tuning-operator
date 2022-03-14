package __render_command_test

import (
	"fmt"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ghodss/yaml"
	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/junit"
	ginkgo_reporters "kubevirt.io/qe-tools/pkg/ginkgo-reporters"
)

var (
	testDir      string
	workspaceDir string
	binPath      string
)

func TestRenderCmd(t *testing.T) {
	RegisterFailHandler(Fail)

	rr := []Reporter{}
	if ginkgo_reporters.Polarion.Run {
		rr = append(rr, &ginkgo_reporters.Polarion)
	}
	rr = append(rr, junit.NewJUnitReporter("render_manifests"))
	RunSpecsWithDefaultAndCustomReporters(t, "Performance Operator render tests", rr)
}

var _ = BeforeSuite(func() {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		Fail("Cannot retrieve test directory")
	}

	testDir = filepath.Dir(file)
	workspaceDir = filepath.Clean(filepath.Join(testDir, "..", ".."))
	binPath = filepath.Clean(filepath.Join(workspaceDir, "build", "_output", "bin"))
	fmt.Fprintf(GinkgoWriter, "using binary at %q\n", binPath)
})

func getFilesDiff(wantFile, gotFile []byte) (string, error) {
	var wantObj interface{}
	var gotObj interface{}

	if err := yaml.Unmarshal(wantFile, &wantObj); err != nil {
		return "", fmt.Errorf("failed to unmarshal data for 'want':%s", err)
	}

	if err := yaml.Unmarshal(gotFile, &gotObj); err != nil {
		return "", fmt.Errorf("failed to unmarshal data for 'got':%s", err)
	}

	return cmp.Diff(wantObj, gotObj), nil
}
