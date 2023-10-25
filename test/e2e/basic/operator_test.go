package e2e

import (
	"testing"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/openshift/cluster-node-tuning-operator/test/framework"
)

var (
	cs  = framework.NewClientSet()
	cli client.Client
)

var _ = ginkgo.BeforeSuite(func() {
	var err error
	ginkgo.By("Getting new client")
	cli, err = newClient()
	gomega.Expect(err).To(gomega.Succeed())
})

func TestNodeTuningOperator(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "Node Tuning Operator e2e tests: basic")
}

func newClient() (client.Client, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, err
	}

	c, err := client.New(cfg, client.Options{})
	return c, err
}
