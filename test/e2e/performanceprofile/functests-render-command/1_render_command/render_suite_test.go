package __render_command_test

import (
	"fmt"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/reporters"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/yaml"

	qe_reporters "kubevirt.io/qe-tools/pkg/ginkgo-reporters"
)

var (
	testDir      string
	workspaceDir string
	binPath      string

	ignorePathTestCase = []string{
		`{map[string]any}["metadata"].(map[string]any)["ownerReferences"].([]any)[0].(map[string]any)["uid"].(string)`,
	}
)

func TestRenderCmd(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Performance Profile render tests")
}

var _ = ReportAfterSuite("e2e render suite", func(r Report) {
	if qe_reporters.Polarion.Run {
		reporters.ReportViaDeprecatedReporter(&qe_reporters.Polarion, r)
	}
})

var _ = BeforeSuite(func() {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		Fail("Cannot retrieve test directory")
	}

	testDir = filepath.Dir(file)
	workspaceDir = filepath.Clean(filepath.Join(testDir, "..", "..", "..", "..", ".."))
	binPath = filepath.Clean(filepath.Join(workspaceDir, "_output"))
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
	return cmp.Diff(wantObj, gotObj, cmp.FilterPath(func(p cmp.Path) bool {
		for _, value := range ignorePathTestCase {
			if p.GoString() == value {
				return true
			}
		}
		return false
	}, cmp.Ignore())), nil
}
