package __arm

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/utils/ptr"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/infrastructure"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/poolname"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profilesupdate"
)

const (
	kernelPageSize4k  = "4k"
	kernelPageSize64k = "64k"
)

var _ = Describe("[rfe_id: 11111] KernelPageSize validation", func() {
	var (
		workerRTNodes               []corev1.Node
		perfProfile, initialProfile *performancev2.PerformanceProfile
		poolName                    string
		err                         error
		ctx                         context.Context = context.Background()
		isArm                       bool
		workerRTNode                corev1.Node
	)

	BeforeAll(func() {
		workerRTNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred())
		workerRTNode = workerRTNodes[0]

		perfProfile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		initialProfile = perfProfile.DeepCopy()

		poolName = poolname.GetByProfile(ctx, perfProfile)
		Expect(err).ToNot(HaveOccurred())

		isArm, err = infrastructure.IsARM(ctx, &workerRTNode)
		Expect(err).ToNot(HaveOccurred())

		if !isArm {
			Skip("This test requires an aarch64 arm CPU")
		}
	})

	AfterAll(func() {
		By("Reverting the Profile")
		profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		currentSpec, _ := json.Marshal(profile.Spec)
		spec, _ := json.Marshal(initialProfile.Spec)

		// revert only if the profile changes
		if !equality.Semantic.DeepEqual(currentSpec, spec) {
			profiles.UpdateWithRetry(initialProfile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(ctx, initialProfile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(ctx, initialProfile)
		}
	})

	Context("KernelPageSize Validation on aarch64", func() {
		When("real time kernel is disabled", func() {
			It("should accept 64k kernelPageSize", func() {
				perfProfile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
					Enabled: ptr.To(false),
				}
				By("Modifying the profile")
				KernelPageSize := performancev2.KernelPageSize(kernelPageSize64k)
				perfProfile.Spec.KernelPageSize = &KernelPageSize
				profiles.UpdateWithRetry(perfProfile)

				By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
				profilesupdate.WaitForTuningUpdating(ctx, perfProfile)

				By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
				profilesupdate.WaitForTuningUpdated(ctx, perfProfile)

				By("Verifying the kernelPageSize has changed to 64k on the affected node")
				kernelPageSize, err := getKernelPageSizeFromNode(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				Expect(kernelPageSize).To(Equal(kernelPageSize64k), fmt.Sprintf("Expected kernel page size to be %s, but got %s", kernelPageSize64k, kernelPageSize))
			})
		})

		When("real time kernel is enabled", func() {
			It("should not accept 64k kernelPageSize", func() {
				perfProfile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
					Enabled: ptr.To(true),
				}
				By("Modifying the profile")
				KernelPageSize := performancev2.KernelPageSize(kernelPageSize64k)
				perfProfile.Spec.KernelPageSize = &KernelPageSize
				profiles.UpdateWithRetry(perfProfile)

				By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
				profilesupdate.WaitForTuningUpdating(ctx, perfProfile)

				By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
				profilesupdate.WaitForTuningUpdated(ctx, perfProfile)

				By("Verifying the kernelPageSize has changed to 64k on the affected node")
				kernelPageSize, err := getKernelPageSizeFromNode(ctx, workerRTNode)
				Expect(err).ToNot(HaveOccurred())
				Expect(kernelPageSize).To(Equal(kernelPageSize64k), fmt.Sprintf("Expected kernel page size to be %s, but got %s", kernelPageSize64k, kernelPageSize))
			})
		})
	})
})

// getKernelPageSizeFromNode retrieves the kernel page size from the worker node
func getKernelPageSizeFromNode(ctx context.Context, node corev1.Node) (string, error) {
	nodeCmd := []string{"getconf", "PAGE_SIZE"}
	out, err := nodes.ExecCommand(ctx, &node, nodeCmd)
	Expect(err).ToNot(HaveOccurred())
	kernelPageSize := testutils.ToString(out)

	if kernelPageSize == "" {
		return "", fmt.Errorf("unable to determine kernel page size from node %s", node.Name)
	}

	return kernelPageSize, nil
}
