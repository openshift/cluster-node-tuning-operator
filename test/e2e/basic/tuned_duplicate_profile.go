package e2e

import (
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	coreapi "k8s.io/api/core/v1"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	ntoconfig "github.com/openshift/cluster-node-tuning-operator/pkg/config"
	util "github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

// Test the creation a Tuned Profile causing the TuneD daemon to bail out with an error message and a recovery after deleting the Profile
var _ = ginkgo.Describe("[basic][tuned_errors_and_recovery] Cause TuneD daemon errors and recover", func() {
	const (
		tunedConflict01                = "../testing_manifests/conflict-01.yaml"
		tunedConflict01Name            = "openshift-profile-dup"
		tunedConflict02                = "../testing_manifests/conflict-02.yaml"
		nodeLabelDuplicateTunedProfile = "tuned.openshift.io/duplicate-tuned-profile"
	)

	ginkgo.Context("TuneD daemon errors and recovery", func() {
		var (
			node *coreapi.Node
			pod  *coreapi.Pod
		)

		// Cleanup code to roll back cluster changes done by this test even if it fails in the middle of ginkgo.It()
		ginkgo.AfterEach(func() {
			// Ignore failures to cleanup resources which are already deleted or not yet created.
			ginkgo.By("cluster changes rollback")
			if node != nil {
				_, _, _ = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelDuplicateTunedProfile+"-")
			}
			_, _, _ = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", tunedConflict01)
			_, _, _ = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", tunedConflict02)
			if pod != nil {
				// Without removing the profile directory this e2e test fails when invoking for the second time on the same system.
				_, _, _ = util.ExecAndLogCommand("oc", "exec", "-n", ntoconfig.WatchNamespace(), pod.Name, "--", "rm", "-rf", "/etc/tuned/openshift-dummy")
			}
		})

		ginkgo.It("Cause TuneD daemon errors on invalid profile load and recover after the profile deletion", func() {
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
			ginkgo.By(fmt.Sprintf("getting a TuneD Pod running on node %s", node.Name))
			pod, err = util.GetTunedForNode(cs, node)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("labelling node %s with label %s", node.Name, nodeLabelDuplicateTunedProfile))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelDuplicateTunedProfile+"=")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("creating the custom profile %s", tunedConflict01))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-n", ntoconfig.WatchNamespace(), "-f", tunedConflict01)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("waiting for Tuned/%s condition type %s == %s", tunedConflict01Name, tunedv1.TunedValid, coreapi.ConditionTrue))
			err = util.WaitForTunedConditionStatus(cs, pollInterval, waitDuration, tunedConflict01Name, tunedv1.TunedValid, coreapi.ConditionTrue)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("creating the custom profile %s", tunedConflict02))
			_, _, err = util.ExecAndLogCommand("oc", "create", "-n", ntoconfig.WatchNamespace(), "-f", tunedConflict02)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("waiting for Tuned/%s condition type %s == %s", tunedConflict01Name, tunedv1.TunedValid, coreapi.ConditionFalse))
			err = util.WaitForTunedConditionStatus(cs, pollInterval, waitDuration, tunedConflict01Name, tunedv1.TunedValid, coreapi.ConditionFalse)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting the custom profile %s", tunedConflict02))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", tunedConflict02)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("waiting for Tuned/%s condition type %s == %s", tunedConflict01Name, tunedv1.TunedValid, coreapi.ConditionTrue))
			err = util.WaitForTunedConditionStatus(cs, pollInterval, waitDuration, tunedConflict01Name, tunedv1.TunedValid, coreapi.ConditionTrue)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("deleting the custom profile %s", tunedConflict01))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "-n", ntoconfig.WatchNamespace(), "-f", tunedConflict01)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			ginkgo.By(fmt.Sprintf("removing label %s from node %s", nodeLabelDuplicateTunedProfile, node.Name))
			_, _, err = util.ExecAndLogCommand("oc", "label", "node", "--overwrite", node.Name, nodeLabelDuplicateTunedProfile+"-")
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		})
	})
})
