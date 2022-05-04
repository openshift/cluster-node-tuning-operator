package pao_mustgather

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestPaoMustgatherTests(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "PaoMustgatherTests Suite")
}
