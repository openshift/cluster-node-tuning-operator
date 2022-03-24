package e2e

import (
	"fmt"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"

	coreapi "k8s.io/api/core/v1"

	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	util "github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

// Test loading a kernel module by applying a custom profile via node labelling.
var _ = ginkgo.Describe("[basic][modules] Node Tuning Operator load kernel module", func() {
	const (
		profileModules   = "../testing_manifests/tuned_modules_load.yaml"
		nodeLabelModules = "tuned.openshift.io/module-load"
		procModules      = "/proc/modules"
		moduleName       = "md4"
	)

	ginkgo.Context("module loading", func() {
		var (
			node *coreapi.Node
		)

		// Cleanup code to roll back cluster changes done by this test even if it fails in the middle of ginkgo.It()
		ginkgo.AfterEach(func() {
			ginkgo.By("cluster changes rollback")
			if node != nil {
				util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelModules+"-")
			}
			util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileModules)
		})

		ginkgo.It(fmt.Sprintf("modules: %s loaded", moduleName), func() {
			const (
				pollInterval = 5 * time.Second
				waitDuration = 5 * time.Minute
			)
			cmdGrepModule := []string{"grep", fmt.Sprintf("^%s ", moduleName), procModules}

			ginkgo.By("getting a list of worker nodes")
			nodes, err := util.GetNodesByRole(cs, "worker")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(len(nodes)).NotTo(gomega.BeZero(), "number of worker nodes is 0")

			node = &nodes[0]
			ginkgo.By(fmt.Sprintf("getting a TuneD Pod running on node %s", node.Name))
			pod, err := util.GetTunedForNode(cs, node)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("labelling node %s with label %s", node.Name, nodeLabelModules))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelModules+"=")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("trying to remove the %s module if loaded", moduleName))
			_, err = util.ExecCmdInPod(pod, "rmmod", moduleName)

			ginkgo.By(fmt.Sprintf("ensuring the %s module is not loaded", moduleName))
			_, err = util.ExecCmdInPod(pod, cmdGrepModule...)
			gomega.Expect(err).To(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("creating profile %s", profileModules))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-n", ntoconfig.OperatorNamespace(), "-f", profileModules)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("ensuring the %s module is loaded", moduleName))
			_, err = util.WaitForCmdInPod(pollInterval, waitDuration, pod, cmdGrepModule...)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting profile %s", profileModules))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileModules)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("removing label %s from node %s", nodeLabelModules, node.Name))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelModules+"-")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})
	})
})
