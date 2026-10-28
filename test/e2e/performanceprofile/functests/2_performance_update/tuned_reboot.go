package __performance_update

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"

	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cluster"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/hypershift"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/infrastructure"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	tunedutil "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/tuned"
)

const (
	// OCPBUGS-36615 / CNF-22592: TuneD profile must stay healthy after node reboot when a
	// performance-derived custom Tuned profile is active.
	tunedRebootPatchName = "cnf22592-performance-patch"
	tunedRebootSHMMNI    = "8192"

	tunedAffinityErrno22 = "[Errno 22]"
	rebootNodeCommand    = "chroot /rootfs systemctl reboot"
)

var _ = Describe("[reboot][tuned] TuneD profile after node reboot", Ordered, Label(string(label.Tier3), string(label.Slow)), func() {
	var (
		ctx            context.Context
		workerCNFNodes []corev1.Node
		targetNode     *corev1.Node
		isSNO          bool
		tunedPatch     *tunedv1.Tuned
	)

	BeforeAll(func() {
		ctx = context.TODO()

		// Todo: check assertions on hypershift and compact cluster
		if hypershift.IsHypershiftCluster() {
			Skip("HyperShift is not supported for this MNO reboot test")
		}
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}

		var err error
		workerCNFNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		workerCNFNodes, err = nodes.MatchingOptionalSelector(workerCNFNodes)
		Expect(err).ToNot(HaveOccurred(), "error looking for the optional selector: %v", err)
		Expect(workerCNFNodes).ToNot(BeEmpty())

		isSNO, err = cluster.IsSingleNode()
		Expect(err).ToNot(HaveOccurred())
		if isSNO {
			Skip("CNF-22592 targets MNO clusters with worker nodes; single-node clusters are out of scope")
		}

		targetNode = &workerCNFNodes[0]
		testlog.Infof("target worker node for tuned reboot test: %q", targetNode.Name)

		if existing := getTunedProfile(tunedRebootPatchName); existing != nil {
			testlog.Infof("Deleting Tuned Profile Patch: %s", tunedRebootPatchName)
			Expect(testclient.ControlPlaneClient.Delete(ctx, existing)).To(Succeed())
			Eventually(func() bool {
				profile, profileErr := tunedutil.GetProfile(context.TODO(), testclient.ControlPlaneClient, components.NamespaceNodeTuningOperator, workerCNFNodes[0].Name)
				if profileErr != nil {
					return false
				}
				return profile.Spec.Config.TunedProfile == "openshift-node-performance-performance"
			}, 2*time.Minute, 10*time.Second).Should(BeTrue(), "Tuned profile deletion did not complete in time")
		}

		tunedPatch, err = createTunedObject("", tunedRebootSHMMNI, tunedRebootPatchName)
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() bool {
			profile, profileErr := tunedutil.GetProfile(ctx, testclient.ControlPlaneClient, components.NamespaceNodeTuningOperator, targetNode.Name)
			if profileErr != nil {
				return false
			}
			return profile.Spec.Config.TunedProfile == tunedPatch.Name
		}, 2*time.Minute, 10*time.Second).Should(BeTrue(),
			"Timed out waiting for profile %q on node %q", tunedRebootPatchName, targetNode.Name)
	})

	AfterAll(func() {
		if tunedPatch == nil {
			return
		}
		if err := testclient.ControlPlaneClient.Delete(ctx, tunedPatch); err != nil {
			testlog.Warningf("failed to delete tuned patch %q: %v", tunedRebootPatchName, err)
			return
		}
		if len(workerCNFNodes) == 0 {
			return
		}
		Eventually(func() bool {
			profile, profileErr := tunedutil.GetProfile(context.TODO(), testclient.ControlPlaneClient, components.NamespaceNodeTuningOperator, workerCNFNodes[0].Name)
			if profileErr != nil {
				return false
			}
			return profile.Spec.Config.TunedProfile == "openshift-node-performance-performance"
		}, 2*time.Minute, 10*time.Second).Should(BeTrue(), "Tuned profile did not revert after deleting patch %q", tunedRebootPatchName)
	})

	It("should keep TuneD profile healthy and affinity error-free after worker reboot", func() {
		previousBootID, err := nodes.GetBootID(targetNode.Name)
		Expect(err).ToNot(HaveOccurred())

		By(fmt.Sprintf("Rebooting worker node %q", targetNode.Name))
		_, _ = nodes.ExecCommand(ctx, targetNode, []string{"sh", "-c", rebootNodeCommand})
		nodes.WaitForRebootedOrFail("tuned-reboot", targetNode.Name, previousBootID, 20*time.Minute, 10*time.Second)

		By("Waiting for the TuneD daemon on the rebooted node")
		nodes.TunedForNode(targetNode, isSNO)

		By("Verifying TuneD profile is not degraded after reboot")
		expectTunedProfileNotDegradedOnNode(ctx, targetNode)

		By("Verifying TuneD pod logs do not report affinity EINVAL errors")
		expectTunedPodLogsWithoutAffinityErrno22(ctx, targetNode, isSNO)
	})
})

func expectTunedProfileNotDegradedOnNode(ctx context.Context, node *corev1.Node) {
	GinkgoHelper()

	isVM, err := infrastructure.IsVM(ctx, node)
	Expect(err).ToNot(HaveOccurred())

	Eventually(func(g Gomega) {
		key := types.NamespacedName{
			Name:      node.Name,
			Namespace: components.NamespaceNodeTuningOperator,
		}
		tunedProfile := &tunedv1.Profile{}
		g.Expect(testclient.DataPlaneClient.Get(ctx, key, tunedProfile)).To(Succeed())

		degraded := findTunedProfileCondition(tunedProfile.Status.Conditions, tunedv1.TunedDegraded)
		g.Expect(degraded).ToNot(BeNil(), "Degraded condition not found in TuneD profile status for node %s", node.Name)

		if degraded.Status == corev1.ConditionTrue {
			msg := fmt.Sprintf("TuneD profile is degraded on node %s: %s", node.Name, degraded.Message)
			if isVM {
				testlog.Warning(msg)
				return
			}
			g.Expect(degraded.Status).To(Equal(corev1.ConditionFalse), msg)
		}
	}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
}

func expectTunedPodLogsWithoutAffinityErrno22(ctx context.Context, node *corev1.Node, isSNO bool) {
	GinkgoHelper()

	Eventually(func(g Gomega) {
		tunedPod := nodes.TunedForNode(node, isSNO)
		logs, err := pods.GetLogs(testclient.K8sClient, tunedPod)
		g.Expect(err).ToNot(HaveOccurred())

		for _, line := range strings.Split(logs, "\n") {
			if !strings.Contains(line, tunedAffinityErrno22) {
				continue
			}
			if strings.Contains(strings.ToLower(line), "affinity") {
				g.Expect(line).NotTo(ContainSubstring(tunedAffinityErrno22),
					"TuneD pod %s/%s log contains affinity [Errno 22] error: %s", tunedPod.Namespace, tunedPod.Name, strings.TrimSpace(line))
			}
		}
	}).WithTimeout(5 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())
}

func findTunedProfileCondition(conditions []tunedv1.StatusCondition, conditionType tunedv1.ConditionType) *tunedv1.StatusCondition {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return &condition
		}
	}
	return nil
}
