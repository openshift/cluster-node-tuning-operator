package profilecreator

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestProfileCreator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Profile Creator Suite")
}
