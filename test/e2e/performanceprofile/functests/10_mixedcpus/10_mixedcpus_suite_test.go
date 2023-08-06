package __mixedcpus_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	_ "github.com/openshift-kni/mixed-cpu-node-plugin/test/e2e/mixedcpus"
)

func TestMixedcpus(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "10Mixedcpus Suite")
}
