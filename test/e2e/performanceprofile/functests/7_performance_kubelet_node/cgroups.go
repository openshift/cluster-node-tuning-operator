package __performance_kubelet_node_test

import (
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
	"strings"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cluster"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
)

var workerRTNode *corev1.Node
var profile *performancev2.PerformanceProfile
var RunningOnSingleNode bool

var _ = Describe("[performance] Cgroups and affinity", Ordered, func() {
	var onlineCPUSet cpuset.CPUSet

	testutils.CustomBeforeAll(func() {
		isSNO, err := cluster.IsSingleNode()
		Expect(err).ToNot(HaveOccurred())
		RunningOnSingleNode = isSNO
	})

	BeforeEach(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}

		workerRTNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("error looking for the optional selector: %v", err))
		Expect(workerRTNodes).ToNot(BeEmpty())
		workerRTNode = &workerRTNodes[0]
		profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		By(fmt.Sprintf("Checking the profile %s with cpus %s", profile.Name, cpuSpecToString(profile.Spec.CPU)))

		Expect(profile.Spec.CPU.Isolated).NotTo(BeNil())
		Expect(profile.Spec.CPU.Reserved).NotTo(BeNil())

		onlineCPUSet, err = nodes.GetOnlineCPUsSet(workerRTNode)
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Verification of cgroup layout on the worker node", func() {
		It("Verify OVS was placed to its own cgroup", func() {
			By("checking the cgroup process list")
			cmd := []string{"cat", "/rootfs/sys/fs/cgroup/cpuset/ovs.slice/cgroup.procs"}
			ovsSliceTasks, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
			Expect(err).ToNot(HaveOccurred())
			Expect(ovsSliceTasks).To(Not(BeEmpty()))
		})

		It("Verify OVS slice is configured correctly", func() {
			By("checking the cpuset spans all cpus")
			cmd := []string{"cat", "/rootfs/sys/fs/cgroup/cpuset/ovs.slice/cpuset.cpus"}
			ovsCpuList, err := nodes.ExecCommandOnNode(cmd, workerRTNode)

			ovsCPUSet, err := cpuset.Parse(ovsCpuList)
			Expect(err).ToNot(HaveOccurred())
			Expect(ovsCPUSet).To(Equal(onlineCPUSet))

			By("checking the cpuset has balancing disabled")
			cmd = []string{"cat", "/rootfs/sys/fs/cgroup/cpuset/ovs.slice/cpuset.sched_load_balance"}
			ovsLoadBalancing, err := nodes.ExecCommandOnNode(cmd, workerRTNode)
			Expect(err).ToNot(HaveOccurred())
			Expect(ovsLoadBalancing).To(Equal("0"))
		})
	})
})

func cpuSpecToString(cpus *performancev2.CPU) string {
	if cpus == nil {
		return "<nil>"
	}
	sb := strings.Builder{}
	if cpus.Reserved != nil {
		fmt.Fprintf(&sb, "reserved=[%s]", *cpus.Reserved)
	}
	if cpus.Isolated != nil {
		fmt.Fprintf(&sb, " isolated=[%s]", *cpus.Isolated)
	}
	if cpus.BalanceIsolated != nil {
		fmt.Fprintf(&sb, " balanceIsolated=%t", *cpus.BalanceIsolated)
	}
	return sb.String()
}
