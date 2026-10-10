package __arm

import (
	"context"

	corev1 "k8s.io/api/core/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/infrastructure"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
)

// TODO: verify label.OpenShift is required — used by other 14_arm files but unclear what filters depend on it
// TODO: verify if label.SpecializedHardware should be added — other 14_arm files use it
var _ = Describe("kernel boot parameters validation on aarch64", Ordered, Label(string(label.OpenShift), string(label.ARM)), func() {
	var (
		workerRTNodes []corev1.Node
		ctx           context.Context
	)

	BeforeAll(func() {
		ctx = context.Background()
		var err error

		workerRTNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred())
		Expect(workerRTNodes).ToNot(BeEmpty())

		isArm, err := infrastructure.IsARM(ctx, &workerRTNodes[0])
		Expect(err).ToNot(HaveOccurred())
		if !isArm {
			Skip("This test requires an aarch64 arm CPU")
		}
	})

	// Regression test for OCPBUGS-58402
	It("should not add iommu.passthrough to the kernel cmdline", func() {
		cmdline, err := nodes.ExecCommand(ctx, &workerRTNodes[0], []string{"cat", "/proc/cmdline"})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(cmdline)).NotTo(ContainSubstring("iommu.passthrough"))
	})
})
