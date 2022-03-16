package e2e

import (
	"fmt"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"

	coreapi "k8s.io/api/core/v1"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	util "github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

// Test the application (and rollback) of a custom profile via node labelling.
var _ = ginkgo.Describe("[basic][tuned_builtin_expand] Node Tuning Operator custom profile, TuneD built-in expansion", func() {
	const (
		profileHugepages       = "../../../examples/hugepages.yaml"
		profileBuiltinExpand   = "../testing_manifests/tuned_builtin_expand.yaml"
		nodeLabelBuiltinExpand = "tuned.openshift.io/tuned-built-in"
		sysctlVar              = "vm.nr_hugepages"
	)

	ginkgo.Context("custom profile: built-in expansion", func() {
		var (
			node *coreapi.Node
		)

		// Cleanup code to roll back cluster changes done by this test even if it fails in the middle of ginkgo.It()
		ginkgo.AfterEach(func() {
			ginkgo.By("cluster changes rollback")
			if node != nil {
				util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelBuiltinExpand+"-")
			}
			util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileBuiltinExpand)
			util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileHugepages)
		})

		ginkgo.It(fmt.Sprintf("%s set", sysctlVar), func() {
			const (
				pollInterval = 5 * time.Second
				waitDuration = 5 * time.Minute
			)
			ginkgo.By("getting a list of worker nodes")
			nodes, err := util.GetNodesByRole(cs, "worker")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(len(nodes)).NotTo(gomega.BeZero(), "number of worker nodes is 0")

			node = &nodes[0]
			ginkgo.By(fmt.Sprintf("getting a TuneD Pod running on node %s", node.Name))
			pod, err := util.GetTunedForNode(cs, node)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Expect the default worker node profile applied prior to getting any current values.
			ginkgo.By(fmt.Sprintf("waiting for TuneD profile %s on node %s", util.DefaultWorkerProfile, node.Name))
			err = util.WaitForProfileConditionStatus(cs, pollInterval, waitDuration, node.Name, util.DefaultWorkerProfile, tunedv1.TunedProfileApplied, coreapi.ConditionTrue)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("getting the current value of %s in Pod %s", sysctlVar, pod.Name))
			valOrig, err := util.WaitForSysctlInPod(pollInterval, waitDuration, pod, sysctlVar)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("labelling node %s with label %s", node.Name, nodeLabelBuiltinExpand))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelBuiltinExpand+"=")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("creating the custom profile %s", profileHugepages))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-n", ntoconfig.OperatorNamespace(), "-f", profileHugepages)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("creating the custom profile %s", profileBuiltinExpand))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-n", ntoconfig.OperatorNamespace(), "-f", profileBuiltinExpand)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By("ensuring the custom worker node profile was set")
			_, err = util.WaitForSysctlValueInPod(pollInterval, waitDuration, pod, sysctlVar, "16")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting the custom profile %s", profileBuiltinExpand))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileBuiltinExpand)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting the custom profile %s", profileHugepages))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileHugepages)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("ensuring the original %s value (%s) is set in Pod %s", sysctlVar, valOrig, pod.Name))
			_, err = util.WaitForSysctlValueInPod(pollInterval, waitDuration, pod, sysctlVar, valOrig)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("removing label %s from node %s", nodeLabelBuiltinExpand, node.Name))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelBuiltinExpand+"-")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})
	})
})
