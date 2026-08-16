package __arm

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/infrastructure"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profilesupdate"
)

const (
	kernelPageSize4k       = "4k"
	kernelPageSize64k      = "64k"
	kernelPageSizeBytes4k  = "4096"
	kernelPageSizeBytes64k = "65536"
)

var _ = Describe("[rfe_id:80342] kernelPageSize configuration validation on aarch64", Ordered, Label(string(label.OpenShift), string(label.KernelPageSize), string(label.SpecializedHardware), string(label.ARM)), func() {
	var (
		workerRTNodes               []corev1.Node
		perfProfile, initialProfile *performancev2.PerformanceProfile
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
		Expect(err).ToNot(HaveOccurred())
		initialProfile = perfProfile.DeepCopy()

		isArm, err = infrastructure.IsARM(ctx, &workerRTNode)
		Expect(err).ToNot(HaveOccurred())
		if !isArm {
			Skip("This test requires an aarch64 arm CPU")
		}

		By("Ensuring profile baseline: kernelPageSize=4k, no hugepages, RT disabled")
		perfProfile.Spec.HugePages = nil
		perfProfile.Spec.KernelPageSize = ptr.To(performancev2.KernelPageSize(kernelPageSize4k))
		perfProfile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
			Enabled: ptr.To(false),
		}

		if !equality.Semantic.DeepEqual(perfProfile.Spec, initialProfile.Spec) {
			profilesupdate.ApplyProfileAndWait(ctx, perfProfile)
		}
	})

	AfterAll(func() {
		By("Reverting the Profile to its original state")
		profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		if !equality.Semantic.DeepEqual(profile.Spec, initialProfile.Spec) {
			profilesupdate.ApplyProfileAndWait(ctx, initialProfile)
		}
	})

	Context("kernelPageSize validation on aarch64", func() {
		When("real time kernel is disabled", func() {
			DescribeTable("should accept kernelPageSize values",
				func(newKernelPageSize, expectedKernelPageSize string) {
					By(fmt.Sprintf("Modifying the profile to use %s", newKernelPageSize))
					klog.Infof("Changing kernelPageSize to: %s from: %s", newKernelPageSize, *perfProfile.Spec.KernelPageSize)
					perfProfile.Spec.KernelPageSize = ptr.To(performancev2.KernelPageSize(newKernelPageSize))
					profilesupdate.ApplyProfileAndWait(ctx, perfProfile)

					By(fmt.Sprintf("Verifying the kernelPageSize has changed to %s on the affected node", expectedKernelPageSize))
					kernelPageSize, err := getKernelPageSizeFromNode(ctx, workerRTNode)
					Expect(err).ToNot(HaveOccurred())
					Expect(kernelPageSize).To(Equal(expectedKernelPageSize), "kernel page size mismatch on node")
				},
				Entry("[test_id:80459] should accept 64k kernelPageSize", kernelPageSize64k, kernelPageSizeBytes64k),
				Entry("[test_id:80461] should accept 4k kernelPageSize", kernelPageSize4k, kernelPageSizeBytes4k),
			)
		})
	})
})

func getKernelPageSizeFromNode(ctx context.Context, node corev1.Node) (string, error) {
	GinkgoHelper()
	nodeCmd := []string{"getconf", "PAGE_SIZE"}
	out, err := nodes.ExecCommand(ctx, &node, nodeCmd)
	Expect(err).ToNot(HaveOccurred())
	kernelPageSize := testutils.ToString(out)
	if kernelPageSize == "" {
		return "", fmt.Errorf("unable to determine kernel page size from node %s", node.Name)
	}
	return kernelPageSize, nil
}
