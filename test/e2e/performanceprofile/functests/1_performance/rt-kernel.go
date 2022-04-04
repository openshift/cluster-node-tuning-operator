package __performance

import (
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
)

var _ = Describe("[performance]RT Kernel", func() {
	var discoveryFailed bool
	var profile *performancev2.PerformanceProfile
	var err error

	testutils.BeforeAll(func() {
		profile, err = discovery.GetFilteredDiscoveryPerformanceProfile(
			func(profile performancev2.PerformanceProfile) bool {
				if profile.Spec.RealTimeKernel != nil &&
					profile.Spec.RealTimeKernel.Enabled != nil &&
					*profile.Spec.RealTimeKernel.Enabled == true {
					return true
				}
				return false
			})

		if err == discovery.ErrProfileNotFound {
			discoveryFailed = true
			return
		}
		Expect(err).ToNot(HaveOccurred(), "failed to get a profile using a filter for RT kernel")
	})

	BeforeEach(func() {
		if discoveryFailed {
			Skip("Skipping RT Kernel tests since no profile found with RT kernel set")
		}

	})

	It("[test_id:26861][crit:high][vendor:cnf-qe@redhat.com][level:acceptance] should have RT kernel enabled", func() {
		workerRTNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("error looking for the optional selector: %v", err))
		Expect(workerRTNodes).ToNot(BeEmpty(), "No RT worker node found!")

		err = nodes.HasPreemptRTKernel(&workerRTNodes[0])
		Expect(err).ToNot(HaveOccurred())
	})

	It("[test_id:28526][crit:high][vendor:cnf-qe@redhat.com][level:acceptance] a node without performance profile applied should not have RT kernel installed", func() {

		By("Skipping test if cluster does not have another available worker node")
		nonPerformancesWorkers, err := nodes.GetNonPerformancesWorkers(profile.Spec.NodeSelector)
		Expect(err).ToNot(HaveOccurred())

		if len(nonPerformancesWorkers) == 0 {
			Skip("Skipping test because there are no additional non-cnf worker nodes")
		}

		cmd := []string{"uname", "-a"}
		kernel, err := nodes.ExecCommandOnNode(cmd, &nonPerformancesWorkers[0])
		Expect(err).ToNot(HaveOccurred(), "failed to execute uname")
		Expect(kernel).To(ContainSubstring("Linux"), "Node should have Linux string")

		err = nodes.HasPreemptRTKernel(&nonPerformancesWorkers[0])
		Expect(err).To(HaveOccurred(), "Node should have non-RT kernel")
	})
})
