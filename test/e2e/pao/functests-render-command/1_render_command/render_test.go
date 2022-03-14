package __render_command_test

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var (
	assetsOutDir string
	assetsInDir  string
	ppInFiles    string
	testDataPath string
)

var _ = Describe("render command e2e test", func() {

	BeforeEach(func() {
		assetsOutDir = createTempAssetsDir()
		assetsInDir = filepath.Join(workspaceDir, "build", "assets")
		ppInFiles = filepath.Join(workspaceDir, "cluster-setup", "manual-cluster", "performance", "performance_profile.yaml")
		testDataPath = filepath.Join(workspaceDir, "testdata")
	})

	Context("With a single performance-profile", func() {
		It("Gets cli args and produces the expected components to output directory", func() {

			cmdline := []string{
				filepath.Join(binPath, "performance-addon-operators"),
				"render",
				"--performance-profile-input-files", ppInFiles,
				"--asset-input-dir", assetsInDir,
				"--asset-output-dir", assetsOutDir,
			}
			fmt.Fprintf(GinkgoWriter, "running: %v\n", cmdline)

			cmd := exec.Command(cmdline[0], cmdline[1:]...)
			runAndCompare(cmd)

		})

		It("Gets environment variables and produces the expected components to output directory", func() {
			cmdline := []string{
				filepath.Join(binPath, "performance-addon-operators"),
				"render",
			}
			fmt.Fprintf(GinkgoWriter, "running: %v\n", cmdline)

			cmd := exec.Command(cmdline[0], cmdline[1:]...)
			cmd.Env = append(cmd.Env,
				fmt.Sprintf("PERFORMANCE_PROFILE_INPUT_FILES=%s", ppInFiles),
				fmt.Sprintf("ASSET_INPUT_DIR=%s", assetsInDir),
				fmt.Sprintf("ASSET_OUTPUT_DIR=%s", assetsOutDir),
			)
			runAndCompare(cmd)
		})
	})

	AfterEach(func() {
		cleanArtifacts()
	})

})

func createTempAssetsDir() string {
	assets, err := ioutil.TempDir("", "assets")
	Expect(err).ToNot(HaveOccurred())
	fmt.Printf("assets` output dir at: %q\n", assets)
	return assets
}

func cleanArtifacts() {
	os.RemoveAll(assetsOutDir)
}

func runAndCompare(cmd *exec.Cmd) {
	_, err := cmd.Output()
	Expect(err).ToNot(HaveOccurred())

	outputAssetsFiles, err := ioutil.ReadDir(assetsOutDir)
	Expect(err).ToNot(HaveOccurred())

	refPath := filepath.Join(testDataPath, "render-expected-output")
	fmt.Fprintf(GinkgoWriter, "reference data at: %q\n", refPath)

	for _, f := range outputAssetsFiles {
		refData, err := ioutil.ReadFile(filepath.Join(refPath, f.Name()))
		Expect(err).ToNot(HaveOccurred())

		data, err := ioutil.ReadFile(filepath.Join(assetsOutDir, f.Name()))
		Expect(err).ToNot(HaveOccurred())

		diff, err := getFilesDiff(data, refData)
		Expect(err).ToNot(HaveOccurred())
		Expect(diff).To(BeZero(), "rendered %s file is not identical to its reference file; diff: %v",
			f.Name(),
			diff)
	}
}
