package __performance

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"

	corev1 "k8s.io/api/core/v1"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
)

var _ = Describe("[rfe_id:27350][performance]Topology Manager", func() {
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
		kubeletConfig, err := nodes.GetKubeletConfig(&workerRTNodes[0])
		Expect(err).ToNot(HaveOccurred())

		// verify topology manager policy
		if profile.Spec.NUMA != nil && profile.Spec.NUMA.TopologyPolicy != nil {
			Expect(kubeletConfig.TopologyManagerPolicy).To(Equal(*profile.Spec.NUMA.TopologyPolicy), "Topology Manager policy mismatch got %q expected %q", kubeletConfig.TopologyManagerPolicy, *profile.Spec.NUMA.TopologyPolicy)
		} else {
			Expect(kubeletConfig.TopologyManagerPolicy).To(Equal(kubeletconfigv1beta1.BestEffortTopologyManagerPolicy), "Topology Manager policy mismatch got %q expected %q", kubeletconfigv1beta1.BestEffortTopologyManagerPolicy)
		}
	})
})
