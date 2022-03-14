package machineconfig

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestMachineConfig(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Machine Config Suite")
}
