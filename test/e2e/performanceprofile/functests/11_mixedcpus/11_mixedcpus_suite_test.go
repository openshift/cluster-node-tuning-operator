package __mixedcpus_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMixedCPUs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Performance Profile Mixed Cpus")
}
