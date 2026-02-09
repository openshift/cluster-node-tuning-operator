package __performance

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/poolname"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profilesupdate"

	corev1 "k8s.io/api/core/v1"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
)

var _ = Describe("[rfe_id:27350][performance]Topology Manager", Ordered, func() {
	var workerRTNodes []corev1.Node
	var profile *performancev2.PerformanceProfile

	BeforeEach(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}

		var err error
		workerRTNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred(), "Error looking for the optional selector: %v", err)
		Expect(workerRTNodes).ToNot(BeEmpty(), "No RT worker node found!")
		profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
	})

	It("[test_id:26932][crit:high][vendor:cnf-qe@redhat.com][level:acceptance] should be enabled with the policy specified in profile", func() {
		kubeletConfig, err := nodes.GetKubeletConfig(context.TODO(), &workerRTNodes[0])
		Expect(err).ToNot(HaveOccurred())

		// verify topology manager policy
		if profile.Spec.NUMA != nil && profile.Spec.NUMA.TopologyPolicy != nil {
			Expect(kubeletConfig.TopologyManagerPolicy).To(Equal(*profile.Spec.NUMA.TopologyPolicy), "Topology Manager policy mismatch got %q expected %q", kubeletConfig.TopologyManagerPolicy, *profile.Spec.NUMA.TopologyPolicy)
		} else {
			Expect(kubeletConfig.TopologyManagerPolicy).To(Equal(kubeletconfigv1beta1.BestEffortTopologyManagerPolicy), "Topology Manager policy mismatch got %q expected %q", kubeletconfigv1beta1.BestEffortTopologyManagerPolicy)
		}
	})

	Context("Deactivating topology and resource managers", func() {
		var initialProfile *performancev2.PerformanceProfile
		var targetNode *corev1.Node

		BeforeEach(func() {
			var err error
			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())

			workerRTNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())

			workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
			Expect(err).ToNot(HaveOccurred(), "Error looking for the optional selector: %v", err)
			Expect(workerRTNodes).ToNot(BeEmpty(), "No RT worker node found!")
			targetNode = &workerRTNodes[0]

			initialProfile = profile.DeepCopy()

			By("Adding the DRA resource management annotation to the profile")
			if profile.Annotations == nil {
				profile.Annotations = make(map[string]string)
			}
			profile.Annotations[performancev2.PerformanceProfileDRAResourceManagementAnnotation] = "true"

			By("Updating the performance profile")
			profiles.UpdateWithRetry(profile)

			poolName := poolname.GetByProfile(context.TODO(), profile)
			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(context.TODO(), profile)

			DeferCleanup(func() {
				By("Reverting the performance profile")
				profiles.UpdateWithRetry(initialProfile)

				poolName := poolname.GetByProfile(context.TODO(), initialProfile)
				By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
				profilesupdate.WaitForTuningUpdating(context.TODO(), initialProfile)

				By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
				profilesupdate.WaitForTuningUpdated(context.TODO(), initialProfile)

				By("Verifying managers are restored to their original configuration")
				kubeletConfig, err := nodes.GetKubeletConfig(context.TODO(), targetNode)
				Expect(err).ToNot(HaveOccurred())

				By("Verifying CPU manager policy is restored to static")
				Expect(kubeletConfig.CPUManagerPolicy).To(Equal("static"), "CPUManagerPolicy should be 'static' after revert, got %q", kubeletConfig.CPUManagerPolicy)

				By("Verifying Topology manager policy is restored")
				expectedTopologyPolicy := kubeletconfigv1beta1.BestEffortTopologyManagerPolicy
				if initialProfile.Spec.NUMA != nil && initialProfile.Spec.NUMA.TopologyPolicy != nil {
					expectedTopologyPolicy = *initialProfile.Spec.NUMA.TopologyPolicy
				}
				Expect(kubeletConfig.TopologyManagerPolicy).To(Equal(expectedTopologyPolicy), "TopologyManagerPolicy should be %q after revert, got %q", expectedTopologyPolicy, kubeletConfig.TopologyManagerPolicy)

				By("Verifying Memory manager policy is restored")
				// Memory manager is set to Static only for restricted or single-numa-node topology policies
				if expectedTopologyPolicy == kubeletconfigv1beta1.RestrictedTopologyManagerPolicy ||
					expectedTopologyPolicy == kubeletconfigv1beta1.SingleNumaNodeTopologyManagerPolicy {
					Expect(kubeletConfig.MemoryManagerPolicy).To(Equal("Static"), "MemoryManagerPolicy should be 'Static' after revert, got %q", kubeletConfig.MemoryManagerPolicy)
				}
			})
		})

		It("should disable CPU, Memory, and Topology managers when DRA annotation is set", func() {
			kubeletConfig, err := nodes.GetKubeletConfig(context.TODO(), targetNode)
			Expect(err).ToNot(HaveOccurred())

			By("Verifying CPU manager policy is set to none")
			Expect(kubeletConfig.CPUManagerPolicy).To(Equal("none"), "CPUManagerPolicy should be 'none' when DRA is enabled, got %q", kubeletConfig.CPUManagerPolicy)

			By("Verifying Topology manager policy is set to none")
			Expect(kubeletConfig.TopologyManagerPolicy).To(Equal(kubeletconfigv1beta1.NoneTopologyManagerPolicy), "TopologyManagerPolicy should be 'none' when DRA is enabled, got %q", kubeletConfig.TopologyManagerPolicy)

			By("Verifying Memory manager policy is set to None")
			Expect(kubeletConfig.MemoryManagerPolicy).To(Equal(kubeletconfigv1beta1.NoneMemoryManagerPolicy), "MemoryManagerPolicy should be 'None' when DRA is enabled, got %q", kubeletConfig.MemoryManagerPolicy)
		})
	})
})
