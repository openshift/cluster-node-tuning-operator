package components

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	ClearIRQBalanceBannedCPUs = "clear-irqbalance-banned-cpus.sh"
)

var _ = Describe("Assets scripts", func() {
	var scriptsPath string

	BeforeEach(func() {
		var err error
		scriptsPath, err = getScriptsPath()
		Expect(err).ToNot(HaveOccurred())
	})

	Context("Clear IRQBalance Banned CPU List", func() {
		It("should handle missing irqbalance file", func() {
			cmdline := []string{
				filepath.Join(scriptsPath, ClearIRQBalanceBannedCPUs),
				"/tmp/this/path/will/never/exist/conf.txt",
				"",
			}
			fmt.Fprintf(GinkgoWriter, "running: %v\n", cmdline)

			cmd := exec.Command(cmdline[0], cmdline[1:]...)
			cmd.Stderr = GinkgoWriter

			_, err := cmd.Output()
			Expect(err).ToNot(HaveOccurred())
		})

		It("should handle missing ban list", func() {
			confName, err := writeTempFile(confTemplate)
			Expect(err).ToNot(HaveOccurred())
			defer os.Remove(confName)

			cmdline := []string{
				filepath.Join(scriptsPath, ClearIRQBalanceBannedCPUs),
				confName,
				"",
			}
			fmt.Fprintf(GinkgoWriter, "running: %v\n", cmdline)

			cmd := exec.Command(cmdline[0], cmdline[1:]...)
			cmd.Stderr = GinkgoWriter

			_, err = cmd.Output()
			Expect(err).ToNot(HaveOccurred())

			data, err := os.ReadFile(confName)
			Expect(err).ToNot(HaveOccurred())

			bannedCPUs, ok := extractBannedCPUs(string(data))
			Expect(ok).To(BeTrue())
			Expect(bannedCPUs).To(Equal("0"))
		})

		It("should handle empty ban list", func() {
			confData := confTemplate + "\nIRQBALANCE_BANNED_CPUS=\n"
			confName, err := writeTempFile(confData)
			Expect(err).ToNot(HaveOccurred())
			defer os.Remove(confName)

			cmdline := []string{
				filepath.Join(scriptsPath, ClearIRQBalanceBannedCPUs),
				confName,
				"",
			}
			fmt.Fprintf(GinkgoWriter, "running: %v\n", cmdline)

			cmd := exec.Command(cmdline[0], cmdline[1:]...)
			cmd.Stderr = GinkgoWriter

			_, err = cmd.Output()
			Expect(err).ToNot(HaveOccurred())

			data, err := os.ReadFile(confName)
			Expect(err).ToNot(HaveOccurred())

			bannedCPUs, ok := extractBannedCPUs(string(data))
			Expect(ok).To(BeTrue())
			Expect(bannedCPUs).To(Equal("0"))
		})

		It("should clear existing ban list", func() {
			// the actual ban list doesn't matter, we just need a value
			bannedCPUs := "0,1-3"
			confData := confTemplate + "\nIRQBALANCE_BANNED_CPUS=" + bannedCPUs + "\n"
			confName, err := writeTempFile(confData)
			Expect(err).ToNot(HaveOccurred())
			defer os.Remove(confName)

			restoreConf, err := os.CreateTemp("", "test-irqbalance-orig")
			Expect(err).ToNot(HaveOccurred())
			defer os.Remove(restoreConf.Name())

			cmdline := []string{
				filepath.Join(scriptsPath, ClearIRQBalanceBannedCPUs),
				confName,
				restoreConf.Name(),
			}
			fmt.Fprintf(GinkgoWriter, "running: %v\n", cmdline)

			cmd := exec.Command(cmdline[0], cmdline[1:]...)
			cmd.Stderr = GinkgoWriter

			_, err = cmd.Output()
			Expect(err).ToNot(HaveOccurred())

			data, err := os.ReadFile(confName)
			Expect(err).ToNot(HaveOccurred())

			updatedBannedCPUs, ok := extractBannedCPUs(string(data))
			Expect(ok).To(BeTrue())
			Expect(updatedBannedCPUs).To(Equal("0"))
		})
	})
})

func writeTempFile(content string) (string, error) {
	f, err := os.CreateTemp("", "test-irqbalance-conf")
	if err != nil {
		return "", err
	}

	if _, err := f.Write([]byte(confTemplate)); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return f.Name(), nil
}

func getScriptsPath() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot retrieve tests directory")
	}
	basedir := filepath.Dir(file)
	return filepath.Abs(
		filepath.Join(
			basedir,
			"..", "..", "..", "..", "..",
			"assets", "performanceprofile", "scripts",
		),
	)
}

func extractBannedCPUs(text string) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") {
			continue
		}
		if idx := strings.Index(line, "#"); idx != -1 {
			line = line[:idx]
		}

		if !strings.Contains(line, "IRQBALANCE_BANNED_CPUS") {
			continue
		}

		return getBannedCPUs(line)
	}

	return "", false
}

func getBannedCPUs(line string) (string, bool) {
	line = strings.TrimSpace(line)
	items := strings.FieldsFunc(line, func(r rune) bool {
		return r == '='
	})
	// too many values, bail out
	if len(items) != 2 {
		return "", false
	}
	// expected values (key, value)
	return strings.TrimSpace(items[1]), true
}

const confTemplate = `# irqbalance is a daemon process that distributes interrupts across
# CPUS on SMP systems. The default is to rebalance once every 10
# seconds. This is the environment file that is specified to systemd via the
# EnvironmentFile key in the service unit file (or via whatever method the init
# system you're using has.
#
# ONESHOT=yes
# after starting, wait for a minute, then look at the interrupt
# load and balance it once; after balancing exit and do not change
# it again.
#IRQBALANCE_ONESHOT=

#
# IRQBALANCE_BANNED_CPUS
# 64 bit bitmask which allows you to indicate which cpu's should
# be skipped when reblancing irqs. Cpu numbers which have their
# corresponding bits set to one in this mask will not have any
# irq's assigned to them on rebalance
#
#IRQBALANCE_BANNED_CPUS=

#
# IRQBALANCE_ARGS
# append any args here to the irqbalance daemon as documented in the man page
#
#IRQBALANCE_ARGS=
`
