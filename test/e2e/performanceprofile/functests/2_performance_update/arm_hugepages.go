package __performance_update

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/infrastructure"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profilesupdate"
)

const (
	// reference for the supported hugepages sizes through the performance profile for aarch64
	// https://github.com/openshift/cluster-node-tuning-operator/blob/main/docs/performanceprofile/performance_profile.md#hugepagesize
	armHugepageSize64k  = "64k"
	armHugepageSize2M   = "2M"
	armHugepageSize32M  = "32M"
	armHugepageSize1G   = "1G"
	armHugepageSize512M = "512M"
	armHugepageSize16G  = "16G"
)

var _ = Describe("hugepage configuration validation on aarch64", Ordered, Label(string(label.OpenShift), string(label.SpecializedHardware), string(label.KernelPageSize), string(label.ARM)), func() {
	var (
		workerRTNodes               []corev1.Node
		perfProfile, initialProfile *performancev2.PerformanceProfile
		err                         error
		ctx                         context.Context
		isArm                       bool
		workerRTNode                corev1.Node
	)

	BeforeEach(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}
	})

	BeforeAll(func() {
		ctx = context.Background()
		workerRTNodes = getUpdatedNodes()

		isArm, err = infrastructure.IsARM(ctx, &workerRTNodes[0])
		Expect(err).ToNot(HaveOccurred())
		if !isArm {
			Skip("This test requires an aarch64 arm CPU")
		}

		workerRTNode = workerRTNodes[0]

		perfProfile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		initialProfile = perfProfile.DeepCopy()

		By("Make sure that the performance profile starts the test suite with real time kernel disabled")
		var realTimeKernelEnabled bool
		if perfProfile.Spec.RealTimeKernel != nil {
			realTimeKernelEnabled = *perfProfile.Spec.RealTimeKernel.Enabled
		}

		if realTimeKernelEnabled {
			perfProfile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
				Enabled: ptr.To(false),
			}
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

	Context("validating hugepages sizes for different kernel page sizes", func() {
		When("real time kernel is disabled", func() {
			// when kernel page size is set to 4k, the supported hugepages sizes are 64k, 2M, 32M, 1G
			When("when kernel page size is set to 4k", func() {
				BeforeAll(func() {
					By("make sure that the kernelPageSize is set to 4k and hugepages are not configured")
					currentKernelPageSize, err := getARMKernelPageSizeFromNode(ctx, workerRTNode)
					Expect(err).ToNot(HaveOccurred())

					if currentKernelPageSize != armKernelPageSizeBytes4k || perfProfile.Spec.HugePages != nil {
						*perfProfile.Spec.KernelPageSize = performancev2.KernelPageSize(armKernelPageSize4k)
						perfProfile.Spec.HugePages = nil
						profilesupdate.ApplyProfileAndWait(ctx, perfProfile)
					}
				})

				DescribeTable("should support hugepage sizes for 4k kernel page size",
					func(hugepageSize performancev2.HugePageSize) {
						By(fmt.Sprintf("Configuring HugePagesSize to %s", hugepageSize))
						perfProfile.Spec.HugePages = &performancev2.HugePages{
							DefaultHugePagesSize: ptr.To(hugepageSize),
							Pages: []performancev2.HugePage{
								{
									Size:  hugepageSize,
									Count: 1,
								},
							},
						}
						profilesupdate.ApplyProfileAndWait(ctx, perfProfile)

						By(fmt.Sprintf("Verifying that hugepages of size %s are properly configured", hugepageSize))
						hugepageSizeFromNode, err := getCurrentARMHugepageSizeFromNode(ctx, workerRTNode)
						Expect(err).ToNot(HaveOccurred())
						Expect(hugepageSizeFromNode).To(Equal(string(hugepageSize)), "hugepageSizeFromNode: %s, hugepageSize: %s", hugepageSizeFromNode, hugepageSize)
					},
					Entry("[test_id:83651] should support 64k hugepages size", performancev2.HugePageSize(armHugepageSize64k)),
					Entry("[test_id:83652] should support 2M hugepages size", performancev2.HugePageSize(armHugepageSize2M)),
					Entry("[test_id:83653] should support 32M hugepages size", performancev2.HugePageSize(armHugepageSize32M)),
					Entry("[test_id:83654] should support 1G hugepages size", performancev2.HugePageSize(armHugepageSize1G)),
				)
			})

			// when kernel page size is set to 64k, the supported hugepages sizes are 2M, 512M, 16G
			When("kernel page size is set to 64k", func() {
				BeforeAll(func() {
					By("Make sure that the kernelPageSize is set to 64k and hugepages are not configured")
					currentKernelPageSize, err := getARMKernelPageSizeFromNode(ctx, workerRTNode)
					Expect(err).ToNot(HaveOccurred())

					if currentKernelPageSize != armKernelPageSizeBytes64k || perfProfile.Spec.HugePages != nil {
						*perfProfile.Spec.KernelPageSize = performancev2.KernelPageSize(armKernelPageSize64k)
						perfProfile.Spec.HugePages = nil
						profilesupdate.ApplyProfileAndWait(ctx, perfProfile)
					}
				})

				DescribeTable("should support hugepage sizes for 64k kernel page size",
					func(hugepageSize performancev2.HugePageSize) {
						By(fmt.Sprintf("Configuring HugePagesSize to %s", hugepageSize))
						perfProfile.Spec.HugePages = &performancev2.HugePages{
							DefaultHugePagesSize: ptr.To(hugepageSize),
							Pages: []performancev2.HugePage{
								{
									Size:  hugepageSize,
									Count: 1,
								},
							},
						}
						profilesupdate.ApplyProfileAndWait(ctx, perfProfile)

						By(fmt.Sprintf("Verifying that hugepages of size %s are properly configured", hugepageSize))
						hugepageSizeFromNode, err := getCurrentARMHugepageSizeFromNode(ctx, workerRTNode)
						Expect(err).ToNot(HaveOccurred())
						Expect(hugepageSizeFromNode).To(Equal(string(hugepageSize)), "hugepageSizeFromNode: %s, hugepageSize: %s", hugepageSizeFromNode, hugepageSize)
					},
					Entry("[test_id:83655] should support 2M hugepages size", performancev2.HugePageSize(armHugepageSize2M)),
					Entry("[test_id:83656] should support 512M hugepages size", performancev2.HugePageSize(armHugepageSize512M)),
					Entry("[test_id:83657] should support 16G hugepages size", performancev2.HugePageSize(armHugepageSize16G)),
				)
			})
		})
	})
})

func getCurrentARMHugepageSizeFromNode(ctx context.Context, node corev1.Node) (string, error) {
	GinkgoHelper()
	nodeCmd := []string{"grep", "Hugepagesize:", "/proc/meminfo"}
	out, err := nodes.ExecCommand(ctx, &node, nodeCmd)
	if err != nil {
		return "", fmt.Errorf("failed to execute command: %w", err)
	}

	line := strings.TrimSpace(testutils.ToString(out))
	klog.Infof("Hugepagesize line: %q", line)
	if !strings.Contains(line, "Hugepagesize:") {
		return "", fmt.Errorf("Hugepagesize not found in /proc/meminfo; raw output line: %q", line)
	}

	parts := strings.Fields(line)
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid Hugepagesize format")
	}

	hugepagesSize, err := convertARMHugepageSizeKBToString(parts[1])
	Expect(err).ToNot(HaveOccurred(), "failed to convert hugepage size: %s", err)
	return hugepagesSize, nil
}

func convertARMHugepageSizeKBToString(kbStr string) (string, error) {
	kb, err := strconv.Atoi(kbStr)
	if err != nil {
		return "", fmt.Errorf("invalid KB value: %s", kbStr)
	}

	switch {
	case kb >= 16777216: // 16GB
		return "16G", nil
	case kb >= 1048576: // 1GB
		return "1G", nil
	case kb >= 524288: // 512MB
		return "512M", nil
	case kb >= 32768: // 32MB
		return "32M", nil
	case kb >= 2048: // 2MB
		return "2M", nil
	case kb >= 64: // 64KB
		return "64k", nil
	default:
		return "", fmt.Errorf("unsupported hugepage size: %d KB", kb)
	}
}
