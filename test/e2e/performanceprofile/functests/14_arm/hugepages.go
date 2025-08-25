package __arm

import (
	"context"
	"encoding/json"
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
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/infrastructure"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/poolname"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profilesupdate"
)

const (
	// reference for the supported hugepages sizes through the performance profile for aarch64
	// https://github.com/openshift/cluster-node-tuning-operator/blob/main/docs/performanceprofile/performance_profile.md#hugepagesize
	hugepageSize64k  = "64k"
	hugepageSize2M   = "2M"
	hugepageSize32M  = "32M"
	hugepageSize1G   = "1G"
	hugepageSize512M = "512M"
	hugepageSize16G  = "16G"
)

var _ = Describe("hugepage configuration validation on aarch64", Ordered, Label(string(label.OpenShift), string(label.SpecializedHardware), string(label.KernelPageSize), string(label.ARM)), func() {
	var (
		workerRTNodes               []corev1.Node
		perfProfile, initialProfile *performancev2.PerformanceProfile
		poolName                    string
		err                         error
		ctx                         context.Context
		isArm                       bool
		workerRTNode                corev1.Node
	)

	BeforeAll(func() {
		ctx = context.Background()
		workerRTNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred())
		Expect(workerRTNodes).ToNot(BeEmpty())
		workerRTNode = workerRTNodes[0]

		isArm, err = infrastructure.IsARM(ctx, &workerRTNode)
		Expect(err).ToNot(HaveOccurred())
		if !isArm {
			Skip("This test requires an aarch64 arm CPU")
		}

		perfProfile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		initialProfile = perfProfile.DeepCopy()
		poolName = poolname.GetByProfile(ctx, perfProfile)

		By("Make sure that the performance profile starts the test suite with real time kernel disabled")
		var realTimeKernelEnabled bool
		if perfProfile.Spec.RealTimeKernel != nil {
			realTimeKernelEnabled = *perfProfile.Spec.RealTimeKernel.Enabled
		}

		if realTimeKernelEnabled {
			perfProfile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
				Enabled: ptr.To(false),
			}
			profiles.UpdateWithRetry(perfProfile)
			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(ctx, perfProfile)
			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(ctx, perfProfile)
		}
	})

	AfterAll(func() {
		By("Reverting the Profile to its original state")
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

	Context("validating hugepages sizes for different kernel page sizes", func() {
		When("real time kernel is disabled", func() {
			// when kernel page size is set to 4k, the supported hugepages sizes are 64k, 2M, 32M, 1G
			When("when kernel page size is set to 4k", func() {
				BeforeAll(func() {
					By("make sure that the kernelPageSize is set to 4k and hugepages are not configured")
					currentKernelPageSize, err := getKernelPageSizeFromNode(ctx, workerRTNode)
					Expect(err).ToNot(HaveOccurred())

					if currentKernelPageSize != kernelPageSizeBytes4k || perfProfile.Spec.HugePages != nil {
						*perfProfile.Spec.KernelPageSize = kernelPageSize4k
						perfProfile.Spec.HugePages = nil
						profiles.UpdateWithRetry(perfProfile)

						By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
						profilesupdate.WaitForTuningUpdating(ctx, perfProfile)

						By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
						profilesupdate.WaitForTuningUpdated(ctx, perfProfile)
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
						profiles.UpdateWithRetry(perfProfile)

						By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
						profilesupdate.WaitForTuningUpdating(ctx, perfProfile)

						By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
						profilesupdate.WaitForTuningUpdated(ctx, perfProfile)

						By(fmt.Sprintf("Verifying that hugepages of size %s are properly configured", hugepageSize))
						hugepageSizeFromNode, err := getCurrentHugepageSizeFromNode(ctx, workerRTNode)
						Expect(err).ToNot(HaveOccurred())
						Expect(hugepageSizeFromNode).To(Equal(string(hugepageSize)), "hugepageSizeFromNode: %s, hugepageSize: %s", hugepageSizeFromNode, hugepageSize)
					},
					Entry("[test_id:83651] should support 64k hugepages size", performancev2.HugePageSize(hugepageSize64k)),
					Entry("[test_id:83652] should support 2M hugepages size", performancev2.HugePageSize(hugepageSize2M)),
					Entry("[test_id:83653] should support 32M hugepages size", performancev2.HugePageSize(hugepageSize32M)),
					Entry("[test_id:83654] should support 1G hugepages size", performancev2.HugePageSize(hugepageSize1G)),
				)
			})

			// when kernel page size is set to 64k, the supported hugepages sizes are 2M, 512M, 16G
			When("kernel page size is set to 64k", func() {
				BeforeAll(func() {
					By("Make sure that the kernelPageSize is set to 64k and hugepages are not configured")
					currentKernelPageSize, err := getKernelPageSizeFromNode(ctx, workerRTNode)
					Expect(err).ToNot(HaveOccurred())

					if currentKernelPageSize != kernelPageSizeBytes64k || perfProfile.Spec.HugePages != nil {
						*perfProfile.Spec.KernelPageSize = kernelPageSize64k
						perfProfile.Spec.HugePages = nil
						profiles.UpdateWithRetry(perfProfile)

						By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
						profilesupdate.WaitForTuningUpdating(ctx, perfProfile)

						By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
						profilesupdate.WaitForTuningUpdated(ctx, perfProfile)
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
						profiles.UpdateWithRetry(perfProfile)

						By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
						profilesupdate.WaitForTuningUpdating(ctx, perfProfile)

						By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
						profilesupdate.WaitForTuningUpdated(ctx, perfProfile)

						By(fmt.Sprintf("Verifying that hugepages of size %s are properly configured", hugepageSize))
						hugepageSizeFromNode, err := getCurrentHugepageSizeFromNode(ctx, workerRTNode)
						Expect(err).ToNot(HaveOccurred())
						Expect(hugepageSizeFromNode).To(Equal(string(hugepageSize)), "hugepageSizeFromNode: %s, hugepageSize: %s", hugepageSizeFromNode, hugepageSize)
					},
					Entry("[test_id:83655] should support 2M hugepages size", performancev2.HugePageSize(hugepageSize2M)),
					Entry("[test_id:83656] should support 512M hugepages size", performancev2.HugePageSize(hugepageSize512M)),
					Entry("[test_id:83657] should support 16G hugepages size", performancev2.HugePageSize(hugepageSize16G)),
				)
			})
		})
	})
})

func getCurrentHugepageSizeFromNode(ctx context.Context, node corev1.Node) (string, error) {
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

	hugepagesSize, err := convertHugepageSizeKBToString(parts[1])
	Expect(err).ToNot(HaveOccurred(), "failed to convert hugepage size: %s", err)
	return hugepagesSize, nil
}

func convertHugepageSizeKBToString(kbStr string) (string, error) {
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
