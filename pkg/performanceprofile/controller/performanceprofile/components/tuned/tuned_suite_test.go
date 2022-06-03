package tuned

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTuned(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tuned Suite")
}
