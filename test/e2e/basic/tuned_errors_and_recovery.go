package e2e

import (
	"fmt"
	"time"

	"github.com/onsi/ginkgo"
	"github.com/onsi/gomega"

	coreapi "k8s.io/api/core/v1"

	configv1 "github.com/openshift/api/config/v1"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	util "github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

// Test the creation a Tuned Profile causing the Tuned daemon to bail out with an error message and a recovery after deleting the Profile
var _ = ginkgo.Describe("[basic][tuned_errors_and_recovery] Cause Tuned daemon errors and recover", func() {
	const (
		profileCauseTunedFailure   = "../testing_manifests/cause_tuned_failure.yaml"
		profileDummy               = "../testing_manifests/dummy.yaml"
		nodeLabelCauseTunedFailure = "tuned.openshift.io/cause-tuned-failure"
	)

	ginkgo.Context("Tuned daemon errors and recovery", func() {
		var (
			node *coreapi.Node
		)

		// Cleanup code to roll back cluster changes done by this test even if it fails in the middle of ginkgo.It()
		ginkgo.AfterEach(func() {
			ginkgo.By("cluster changes rollback")
			if node != nil {
				util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelCauseTunedFailure+"-")
			}
			util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileCauseTunedFailure)
			util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileDummy)
		})

		ginkgo.It("Cause Tuned daemon errors on invalid profile load and recover after the profile deletion", func() {
			const (
				pollInterval          = 5 * time.Second
				waitDuration          = 5 * time.Minute
				reasonAsExpected      = "AsExpected"
				reasonProfileDegraded = "ProfileDegraded"
			)
			ginkgo.By("getting a list of worker nodes")
			nodes, err := util.GetNodesByRole(cs, "worker")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(len(nodes)).NotTo(gomega.BeZero(), "number of worker nodes is 0")

			node = &nodes[0]

			ginkgo.By(fmt.Sprintf("labelling node %s with label %s", node.Name, nodeLabelCauseTunedFailure))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelCauseTunedFailure+"=")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("waiting for ClusterOperator/%s condition %s reason %s", tunedv1.TunedClusterOperatorResourceName, configv1.OperatorAvailable, reasonAsExpected))
			err = util.WaitForClusterOperatorConditionReason(cs, pollInterval, waitDuration, configv1.OperatorAvailable, reasonAsExpected)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("creating the custom profile %s", profileCauseTunedFailure))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-n", ntoconfig.OperatorNamespace(), "-f", profileCauseTunedFailure)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("waiting for ClusterOperator/%s condition %s reason %s", tunedv1.TunedClusterOperatorResourceName, configv1.OperatorAvailable, reasonProfileDegraded))
			err = util.WaitForClusterOperatorConditionReason(cs, pollInterval, waitDuration, configv1.OperatorAvailable, reasonProfileDegraded)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("creating the custom profile %s", profileDummy))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-n", ntoconfig.OperatorNamespace(), "-f", profileDummy)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// Check for rhbz#1998247, reload TuneD when deps of recommended profile change.
			ginkgo.By(fmt.Sprintf("waiting for ClusterOperator/%s condition %s reason %s", tunedv1.TunedClusterOperatorResourceName, configv1.OperatorAvailable, reasonAsExpected))
			err = util.WaitForClusterOperatorConditionReason(cs, pollInterval, waitDuration, configv1.OperatorAvailable, reasonAsExpected)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting the custom profile %s", profileCauseTunedFailure))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileCauseTunedFailure)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting the custom profile %s", profileDummy))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.OperatorNamespace(), "-f", profileDummy)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("waiting for ClusterOperator/%s condition %s reason %s", tunedv1.TunedClusterOperatorResourceName, configv1.OperatorAvailable, reasonAsExpected))
			err = util.WaitForClusterOperatorConditionReason(cs, pollInterval, waitDuration, configv1.OperatorAvailable, reasonAsExpected)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("removing label %s from node %s", nodeLabelCauseTunedFailure, node.Name))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelCauseTunedFailure+"-")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})
	})
})
