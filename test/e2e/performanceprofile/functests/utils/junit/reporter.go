package junit

import (
	"flag"
	"fmt"
	"github.com/onsi/ginkgo/v2/reporters"
)

var junitDir *string

func init() {
	junitDir = flag.String("junitDir", ".", "the directory for the junit format report")
}

// NewJUnitReporter with the given name. testSuiteName must be a valid filename part
func NewJUnitReporter(testSuiteName string) *reporters.JUnitReporter {
	return reporters.NewJUnitReporter(fmt.Sprintf("%s/%s_%s.xml", *junitDir, "unit_report", testSuiteName))
}
