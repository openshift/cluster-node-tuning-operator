package __performance_update

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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
		Expect(err).ToNot(HaveOccurred(), "failed to list worker CNF nodes by selector labels")
		workerCNFNodes, err = nodes.MatchingOptionalSelector(workerCNFNodes)
		Expect(err).ToNot(HaveOccurred(), "failed to apply optional node selector to worker CNF nodes")
		Expect(workerCNFNodes).ToNot(BeEmpty(), "expected at least one worker CNF node for tuned reboot test")

		isSNO, err = cluster.IsSingleNode()
		Expect(err).ToNot(HaveOccurred(), "failed to detect whether cluster is single-node")
		if isSNO {
			Skip("CNF-22592 targets MNO clusters with worker nodes; single-node clusters are out of scope")
		}

		targetNode = &workerCNFNodes[0]
		testlog.Info("selected first worker CNF node as reboot target for tuned reboot test")

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
		Expect(err).ToNot(HaveOccurred(), "failed to create tuned profile patch for reboot test")

		Eventually(func() bool {
			profile, profileErr := tunedutil.GetProfile(ctx, testclient.ControlPlaneClient, components.NamespaceNodeTuningOperator, targetNode.Name)
			if profileErr != nil {
				return false
			}
			return profile.Spec.Config.TunedProfile == tunedPatch.Name
		}, 2*time.Minute, 10*time.Second).Should(BeTrue(),
			"Timed out waiting for patched TuneD profile to become active on the target worker")
	})

	AfterAll(func() {
		if tunedPatch == nil {
			return
		}
		Eventually(func() error {
			err := testclient.ControlPlaneClient.Delete(ctx, tunedPatch)
			if errors.IsNotFound(err) {
				return nil
			}
			return err
		}, 2*time.Minute, 10*time.Second).Should(Succeed(), "failed to delete tuned profile patch during test cleanup")
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
		Expect(err).ToNot(HaveOccurred(), "failed to read boot ID before worker reboot")
		Expect(previousBootID).ToNot(BeEmpty(), "boot ID must be set before reboot to detect an actual restart")

		By("Rebooting target worker node")
		_, rebootErr := nodes.ExecCommand(ctx, targetNode, []string{"sh", "-c", rebootNodeCommand})
		if rebootErr != nil {
			testlog.Info("reboot exec returned an error (may be expected once the node shuts down)")
			Eventually(func(g Gomega) {
				node, getErr := nodes.GetByName(targetNode.Name)
				if getErr != nil {
					return
				}
				if node.Status.NodeInfo.BootID != previousBootID {
					return
				}
				for _, c := range node.Status.Conditions {
					if c.Type == corev1.NodeReady && c.Status != corev1.ConditionTrue {
						return
					}
				}
				g.Expect(false).To(BeTrue(), "reboot command did not trigger node shutdown on the target worker")
			}, 2*time.Minute, 5*time.Second).Should(Succeed(), "reboot command failed to start on the target worker")
		}
		nodes.WaitForRebootedOrFail("tuned-reboot", targetNode.Name, previousBootID, 20*time.Minute, 10*time.Second)

		By("Waiting for the TuneD daemon on the rebooted node")
		nodes.TunedForNode(targetNode, isSNO)

		By("Verifying TuneD profile is not degraded after reboot")
		expectTunedProfileNotDegradedOnNode(ctx, targetNode)

		By("Verifying patched kernel.shmmni after reboot")
		expectKernelSHMMNIOnNode(ctx, targetNode, tunedRebootSHMMNI)

		By("Verifying TuneD pod logs do not report affinity EINVAL errors")
		expectTunedPodLogsWithoutAffinityErrno22(ctx, targetNode, isSNO)
	})
})

// expectTunedProfileNotDegradedOnNode waits until the TuneD Profile Degraded condition is
// absent or False. On VM nodes a True Degraded condition is logged as a warning only.
func expectTunedProfileNotDegradedOnNode(ctx context.Context, node *corev1.Node) {
	GinkgoHelper()

	isVM, err := infrastructure.IsVM(ctx, node)
	Expect(err).ToNot(HaveOccurred(), "failed to detect whether reboot target runs on VM infrastructure")

	Eventually(func(g Gomega) {
		key := types.NamespacedName{
			Name:      node.Name,
			Namespace: components.NamespaceNodeTuningOperator,
		}
		tunedProfile := &tunedv1.Profile{}
		g.Expect(testclient.DataPlaneClient.Get(ctx, key, tunedProfile)).To(Succeed(),
			"failed to get TuneD Profile for reboot target worker")

		degraded := findTunedProfileCondition(tunedProfile.Status.Conditions, tunedv1.TunedDegraded)
		g.Expect(degraded).ToNot(BeNil(), "Degraded condition not found in TuneD profile status")

		if degraded.Status == corev1.ConditionTrue {
			if isVM {
				testlog.Warning("TuneD profile is degraded on a VM worker after reboot")
				return
			}
			g.Expect(degraded.Status).To(Equal(corev1.ConditionFalse),
				"TuneD profile is degraded on the target worker after reboot")
		}
	}).WithTimeout(10*time.Minute).WithPolling(10*time.Second).Should(Succeed(),
		"TuneD profile did not become healthy on the target worker after reboot")
}

// expectKernelSHMMNIOnNode verifies kernel.shmmni on the node matches the patched value.
func expectKernelSHMMNIOnNode(ctx context.Context, node *corev1.Node, expected string) {
	GinkgoHelper()
	execSysctlOnWorkers(ctx, []corev1.Node{*node}, map[string]string{
		"kernel.shmmni": expected,
	})
}

// expectTunedPodLogsWithoutAffinityErrno22 fails if TuneD logs contain affinity-related [Errno 22] lines.
func expectTunedPodLogsWithoutAffinityErrno22(ctx context.Context, node *corev1.Node, isSNO bool) {
	GinkgoHelper()

	Eventually(func(g Gomega) {
		tunedPod := nodes.TunedForNode(node, isSNO)
		logs, err := pods.GetLogs(testclient.K8sClient, tunedPod)
		g.Expect(err).ToNot(HaveOccurred(), "failed to read TuneD pod logs on reboot target worker")

		for _, line := range strings.Split(logs, "\n") {
			if !strings.Contains(line, tunedAffinityErrno22) {
				continue
			}
			if strings.Contains(strings.ToLower(line), "affinity") {
				g.Expect(line).NotTo(ContainSubstring(tunedAffinityErrno22),
					"TuneD pod logs contain an affinity-related [Errno 22] error after worker reboot")
			}
		}
	}).WithTimeout(5*time.Minute).WithPolling(15*time.Second).Should(Succeed(),
		"TuneD pod log check for affinity [Errno 22] did not succeed")
}

// findTunedProfileCondition returns the Profile status condition of the given type, if present.
func findTunedProfileCondition(conditions []tunedv1.StatusCondition, conditionType tunedv1.ConditionType) *tunedv1.StatusCondition {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return &condition
		}
	}
	return nil
}
