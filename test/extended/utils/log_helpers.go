// log_helpers.go provides the Logf logging utility used across the package.

package utils

import (
	"fmt"
	"os"
)

// Logf is a simple logging function to replace e2e.Logf.
// It writes a single line to stderr. stderr is intentionally used (not
// GinkgoWriter or stdout) so the message is emitted exactly once: it is
// neither captured into Ginkgo's GinkgoWriter output nor copied into the
// run-test result report. GinkgoWriter output is echoed twice by OTE
// (live to stderr + captured into the stdout report), which would
// duplicate every log line using something like `2>&1 | tee file.log`.
func Logf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
