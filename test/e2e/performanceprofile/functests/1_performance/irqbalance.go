package __performance

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
	"github.com/openshift/cluster-node-tuning-operator/test/framework"
)

var (
	cs = framework.NewClientSet()
)

var _ = Describe("[performance] Checking IRQBalance settings", func() {
	var workerRTNodes []corev1.Node
	var profile *performancev2.PerformanceProfile
	var performanceMCP string
	var err error

	BeforeEach(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}

		workerRTNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		performanceMCP, err = mcps.GetByProfile(profile)
		Expect(err).ToNot(HaveOccurred())

		// Verify that worker and performance MCP have updated state equals to true
		for _, mcpName := range []string{testutils.RoleWorker, performanceMCP} {
			mcps.WaitForCondition(mcpName, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
		}
	})

	Context("Verify IRQ Balancing", func() {

		var testpod *corev1.Pod

		AfterEach(func() {
			deleteTestPod(testpod)
		})

		It("Should not overwrite the banned CPU set on tuned restart", func() {
			if profile.Status.RuntimeClass == nil {
				Skip("runtime class not generated")
			}
			runtimeClass := profile.Status.RuntimeClass

			irqLoadBalancingDisabled := profile.Spec.GloballyDisableIrqLoadBalancing != nil && *profile.Spec.GloballyDisableIrqLoadBalancing
			if irqLoadBalancingDisabled {
				Skip("this test needs dynamic IRQ balancing")
			}

			node := workerRTNodes[0] // any random node is fine
			By(fmt.Sprintf("verifying worker node %q", node.Name))

			var err error

			bannedCPUs, err := nodes.BannedCPUs(node)
			Expect(err).ToNot(HaveOccurred(), "failed to extract the banned CPUs from node %q", node.Name)
			Expect(bannedCPUs.IsEmpty()).To(BeTrue(), "banned CPUs %v not empty on node %q", bannedCPUs, node.Name)

			smpAffinitySet, err := nodes.GetDefaultSmpAffinitySet(&node)
			Expect(err).ToNot(HaveOccurred(), "failed to get default smp affinity")

			onlineCPUsSet, err := nodes.GetOnlineCPUsSet(&node)
			Expect(err).ToNot(HaveOccurred(), "failed to get Online CPUs list")

			// expect no irqbalance run in the system already, AKA start from pristine conditions.
			// This is not an hard requirement, just the easier state to manage and check
			Expect(smpAffinitySet.Equals(onlineCPUsSet)).To(BeTrue(), "found default_smp_affinity %v, expected %v - IRQBalance already run?", smpAffinitySet, onlineCPUsSet)

			cpuRequest := 2 // minimum amount to be reasonably sure we're SMT-aligned
			testpod = getStressPod(node.Name, cpuRequest)
			testpod.Namespace = testutils.NamespaceTesting
			testpod.Annotations = map[string]string{
				"irq-load-balancing.crio.io": "disable",
				"cpu-quota.crio.io":          "disable",
			}
			testpod.Spec.NodeName = node.Name
			testpod.Spec.RuntimeClassName = runtimeClass
			promotePodToGuaranteed(testpod)

			err = testclient.Client.Create(context.TODO(), testpod)
			Expect(err).ToNot(HaveOccurred())

			err = pods.WaitForCondition(testpod, corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
			logEventsForPod(testpod)
			Expect(err).ToNot(HaveOccurred())

			// now we have something in the IRQBalance cpu list. Let's make sure the restart doesn't overwrite this data.
			postCreateBannedCPUs, err := nodes.BannedCPUs(node)
			Expect(err).ToNot(HaveOccurred(), "failed to extract the banned CPUs from node %q", node.Name)
			Expect(postCreateBannedCPUs.IsEmpty()).To(BeFalse(), "banned CPUs %v not empty on node %q", postCreateBannedCPUs, node.Name)

			By(fmt.Sprintf("getting a TuneD Pod running on node %s", node.Name))
			pod, err := util.GetTunedForNode(cs, &node)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("causing a restart of the tuned pod (deleting the pod) on %s", node.Name))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "pod", "--wait=true", "-n", pod.Namespace, pod.Name)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("getting again a TuneD Pod running on node %s", node.Name))
			pod, err = util.GetTunedForNode(cs, &node)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("waiting for the TuneD daemon running on node %s", node.Name))
			_, err = util.WaitForCmdInPod(5*time.Second, 5*time.Minute, pod, "test", "-e", "/run/tuned/tuned.pid")
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("re-verifying worker node %q after TuneD restart", node.Name))

			postRestartBannedCPUs, err := nodes.BannedCPUs(node)
			Expect(err).ToNot(HaveOccurred(), "failed to extract the banned CPUs from node %q", node.Name)
			Expect(postRestartBannedCPUs).To(Equal(postCreateBannedCPUs), "banned CPUs changed post tuned restart on node %q", postRestartBannedCPUs, node.Name)
		})
	})
})
