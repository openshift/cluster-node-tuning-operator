package __performance_update

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/cpuset"
	"sigs.k8s.io/controller-runtime/pkg/client"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cluster"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profilesupdate"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/systemd"
)

const numberOfCoresThatRequiredCancelingSMTAlignment = 4
const ovsSliceCgroupBase = "/rootfs/sys/fs/cgroup/ovs.slice"

var _ = Describe("[performance] ovsDpdk CPUs", Ordered, Label(string(label.OvsDpdk), string(label.Slow), string(label.Tier2)), func() {
	var (
		workerRTNodes   []corev1.Node
		baselineProfile *performancev2.PerformanceProfile
		initialProfile  *performancev2.PerformanceProfile

		reservedSet          cpuset.CPUSet
		ovsDpdkSet           cpuset.CPUSet
		newIsolatedSet       cpuset.CPUSet
		smtAlignmentDisabled bool
	)

	BeforeAll(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}

		var err error
		workerRTNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred())
		Expect(workerRTNodes).ToNot(BeEmpty())

		initialProfile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		reservedSet, err = cpuset.Parse(string(*initialProfile.Spec.CPU.Reserved))
		Expect(err).ToNot(HaveOccurred())
		isolatedSet, err := cpuset.Parse(string(*initialProfile.Spec.CPU.Isolated))
		Expect(err).ToNot(HaveOccurred())

		isolatedList := isolatedSet.List()
		Expect(len(isolatedList)).To(BeNumerically(">=", 2),
			"need at least 2 isolated CPUs to split into isolated + ovsDpdk")

		ovsDpdkSet = cpuset.New(isolatedList[0])
		newIsolatedSet = cpuset.New(isolatedList[1:]...)

		ovsDpdkCPUs := performancev2.CPUSet(ovsDpdkSet.String())
		newIsolated := performancev2.CPUSet(newIsolatedSet.String())

		testlog.Infof("Reserved: %s, Isolated: %s, OvsDpdk: %s",
			reservedSet.String(), newIsolatedSet.String(), ovsDpdkSet.String())

		ctx := context.TODO()
		isWPEnabled, err := cluster.IsWorkloadPartitioningEnabled(ctx, testclient.Client)
		Expect(err).ToNot(HaveOccurred())

		By("Preparing the baseline profile with ovsDpdk CPUs")
		baselineProfile = initialProfile.DeepCopy()
		baselineProfile.Spec.CPU.Isolated = &newIsolated
		baselineProfile.Spec.CPU.OvsDpdk = &ovsDpdkCPUs
		if baselineProfile.Annotations == nil {
			baselineProfile.Annotations = make(map[string]string)
		}

		// we're working under the assumption that all worker RT nodes have the same number of CPUs
		if numOfCores, _ := workerRTNodes[0].Status.Capacity.Cpu().AsInt64(); numOfCores <= numberOfCoresThatRequiredCancelingSMTAlignment {
			smtAlignmentDisabled = true
		}
		setPolicyOptions(baselineProfile, isWPEnabled, smtAlignmentDisabled)
	})

	AfterAll(func() {
		if initialProfile == nil {
			return
		}
		By("Reverting the profile to its initial state")
		profilesupdate.ApplyProfileAndWait(context.TODO(), initialProfile)
	})

	Context("with cpu-load-balancing-ovs-dpdk=disable", func() {
		BeforeAll(func() {
			By("Applying the profile with cpu-load-balancing-ovs-dpdk=disable")
			baselineWithAnnotation := baselineProfile.DeepCopy()
			baselineWithAnnotation.Annotations[performancev2.PerformanceProfileCPULoadBalancingOvsDpdkAnnotation] = "disable"
			profilesupdate.ApplyProfileAndWait(context.TODO(), baselineWithAnnotation)

			updatedProfile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			testlog.Infof("Updated profile: reserved=%s isolated=%s ovsDpdk=%s annotations=%v",
				*updatedProfile.Spec.CPU.Reserved, *updatedProfile.Spec.CPU.Isolated,
				*updatedProfile.Spec.CPU.OvsDpdk, updatedProfile.Annotations)

			By("Refreshing the node list after the update")
			workerRTNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
			Expect(err).ToNot(HaveOccurred())
			Expect(workerRTNodes).ToNot(BeEmpty())
		})

		It("[test_id:89987] should apply ovsDpdk CPU node configuration", func() {
			ctx := context.TODO()
			expectedIsolatedPlusOvsDpdk := newIsolatedSet.Union(ovsDpdkSet)
			expectedReservedSystem := reservedSet.Union(ovsDpdkSet)

			node := &workerRTNodes[0]
			testlog.Infof("Verifying node %s", node.Name)

			By("Verifying kernel cmdline")
			cmdline, err := nodes.ExecCommand(ctx, node, []string{"cat", "/proc/cmdline"})
			Expect(err).ToNot(HaveOccurred())
			cmdlineStr := testutils.ToString(cmdline)

			By("Verifying isolcpus includes ovsDpdk CPUs")
			isolcpusSet := parseCPUSetFromKernelParam(cmdlineStr, "isolcpus")
			Expect(isolcpusSet.IsEmpty()).To(BeFalse(), "isolcpus param not found in cmdline")
			Expect(expectedIsolatedPlusOvsDpdk.IsSubsetOf(isolcpusSet)).To(BeTrue(),
				"isolcpus=%s should include all isolated + ovsDpdk CPUs %s",
				isolcpusSet.String(), expectedIsolatedPlusOvsDpdk.String())

			By("Verifying nohz_full includes ovsDpdk CPUs")
			nohzSet := parseCPUSetFromKernelParam(cmdlineStr, "nohz_full")
			Expect(nohzSet.IsEmpty()).To(BeFalse(), "nohz_full param not found in cmdline")
			Expect(ovsDpdkSet.IsSubsetOf(nohzSet)).To(BeTrue(),
				"nohz_full=%s should include ovsDpdk CPUs %s",
				nohzSet.String(), ovsDpdkSet.String())

			By("Verifying rcu_nocbs includes ovsDpdk CPUs")
			rcuSet := parseCPUSetFromKernelParam(cmdlineStr, "rcu_nocbs")
			Expect(rcuSet.IsEmpty()).To(BeFalse(), "rcu_nocbs param not found in cmdline")
			Expect(ovsDpdkSet.IsSubsetOf(rcuSet)).To(BeTrue(),
				"rcu_nocbs=%s should include ovsDpdk CPUs %s",
				rcuSet.String(), ovsDpdkSet.String())

			By("Verifying systemd.cpu_affinity excludes ovsDpdk CPUs")
			affinitySet := parseCPUSetFromKernelParam(cmdlineStr, "systemd.cpu_affinity")
			Expect(affinitySet.IsEmpty()).To(BeFalse(), "systemd.cpu_affinity param not found in cmdline")
			Expect(affinitySet.Intersection(ovsDpdkSet).IsEmpty()).To(BeTrue(),
				"systemd.cpu_affinity=%s should not contain ovsDpdk CPUs %s",
				affinitySet.String(), ovsDpdkSet.String())

			By("Verifying kubelet reservedSystemCPUs is union of reserved + ovsDpdk")
			kubeletConfig, err := nodes.GetKubeletConfig(ctx, node)
			Expect(err).ToNot(HaveOccurred())
			reservedSystemCPUs, err := cpuset.Parse(kubeletConfig.ReservedSystemCPUs)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedSystemCPUs.Equals(expectedReservedSystem)).To(BeTrue(),
				"ReservedSystemCPUs should be %s (reserved + ovsDpdk), got %s",
				expectedReservedSystem.String(), reservedSystemCPUs.String())

			By("Verifying IRQBALANCE_BANNED_CPUS")
			irqConf, err := nodes.ExecCommand(ctx, node, []string{"cat", "/rootfs/etc/sysconfig/irqbalance"})
			Expect(err).ToNot(HaveOccurred())
			irqConfStr := testutils.ToString(irqConf)

			bannedSet := getIRQBannedCPUSet(irqConfStr)
			Expect(ovsDpdkSet.IsSubsetOf(bannedSet)).To(BeTrue(),
				"IRQBALANCE_BANNED_CPUS should include ovsDpdk CPUs %s, got %s",
				ovsDpdkSet.String(), bannedSet.String())

			By("Verifying default_smp_affinity excludes ovsDpdk CPUs")
			smpCPUSet, err := nodes.GetDefaultSmpAffinitySet(ctx, node)
			Expect(err).ToNot(HaveOccurred())
			testlog.Infof("default_smp_affinity on %s: %s", node.Name, smpCPUSet.String())
			Expect(smpCPUSet.Intersection(ovsDpdkSet).IsEmpty()).To(BeTrue(),
				"default_smp_affinity should not have ovsDpdk CPU bits set, got CPUs %s",
				smpCPUSet.Intersection(ovsDpdkSet).String())

			By("Verifying ovs-dpdk-cpus-configure script exists")
			_, err = nodes.ExecCommand(ctx, node, []string{
				"stat", "/rootfs/usr/local/bin/ovs-dpdk-cpus-configure.sh",
			})
			Expect(err).ToNot(HaveOccurred(),
				"ovs-dpdk-cpus-configure.sh should be present on the node")

			expectedOvsDpdkEnv := "OVS_DPDK_CPUS=" + ovsDpdkSet.String()

			By("Verifying ovs-vswitchd drop-in OVS_DPDK_CPUS on node")
			dropin, err := nodes.ExecCommand(ctx, node, []string{
				"grep", "-r", "OVS_DPDK_CPUS=", "/rootfs/etc/systemd/system/ovs-vswitchd.service.d/",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(testutils.ToString(dropin)).To(ContainSubstring(expectedOvsDpdkEnv),
				"ovs-vswitchd drop-in should contain %s, got %s",
				expectedOvsDpdkEnv, testutils.ToString(dropin))

			By("Verifying ovs-vswitchd.service OVS_DPDK_CPUS on node")
			envBlob, err := systemd.ShowPropertyValue(ctx, "ovs-vswitchd.service", "Environment", node)
			Expect(err).ToNot(HaveOccurred())
			Expect(envBlob).To(ContainSubstring(expectedOvsDpdkEnv),
				"ovs-vswitchd.service Environment should contain %s, got %s",
				expectedOvsDpdkEnv, envBlob)

			By("Verifying ovsdpdk.slice cgroup hierarchy exists")
			_, err = nodes.ExecCommand(ctx, node, []string{
				"stat", ovsSliceCgroupBase + "/ovs-vswitchd.service/ovsdpdk.slice",
			})
			Expect(err).ToNot(HaveOccurred(),
				"ovsdpdk.slice directory should exist inside ovs.slice/ovs-vswitchd.service/")

			By("Verifying ovs.slice cgroup.subtree_control enables cpuset")
			subtreeCtl, err := nodes.ExecCommand(ctx, node, []string{
				"cat", ovsSliceCgroupBase + "/cgroup.subtree_control",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(testutils.ToString(subtreeCtl)).To(ContainSubstring("cpuset"),
				"ovs.slice cgroup.subtree_control should contain 'cpuset'")

			By("Verifying ovs-vswitchd.service cgroup.subtree_control enables cpuset")
			subtreeCtl, err = nodes.ExecCommand(ctx, node, []string{
				"cat", ovsSliceCgroupBase + "/ovs-vswitchd.service/cgroup.subtree_control",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(testutils.ToString(subtreeCtl)).To(ContainSubstring("cpuset"),
				"ovs-vswitchd.service cgroup.subtree_control should contain 'cpuset'")

			By("Verifying ovsdpdk.slice cgroup.type is threaded")
			cgroupType, err := nodes.ExecCommand(ctx, node, []string{
				"cat", ovsSliceCgroupBase + "/ovs-vswitchd.service/ovsdpdk.slice/cgroup.type",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.TrimSpace(testutils.ToString(cgroupType))).To(Equal("threaded"),
				"ovsdpdk.slice cgroup.type should be 'threaded'")

			By("Verifying ovsdpdk.slice cpuset.cpus.exclusive matches configured ovsDpdk CPUs")
			cgroupCpuSet, err := getOvsDpdkSliceExclusiveCPUs(ctx, node)
			Expect(err).ToNot(HaveOccurred())
			Expect(cgroupCpuSet.Equals(ovsDpdkSet)).To(BeTrue(),
				"ovsdpdk.slice cpuset.cpus.exclusive should be %s, got %s",
				ovsDpdkSet.String(), cgroupCpuSet.String())

			By("Verifying ovsdpdk.slice cpuset.cpus.partition is isolated")
			cgroupPartition, err := getOvsDpdkSlicePartition(ctx, node)
			Expect(err).ToNot(HaveOccurred(), "failed to read ovsdpdk.slice cpuset.cpus.partition")
			Expect(cgroupPartition).To(Equal("isolated"), "ovsdpdk.slice cpuset.cpus.partition should be 'isolated'")
		})

		It("[test_id:89989] should preserve ovsDpdk CPU IRQ banning across GU pod lifecycle", func() {
			verifyOvsDpdkIRQBanningAcrossGUPodLifecycle(context.TODO(), &workerRTNodes[0], baselineProfile, ovsDpdkSet, smtAlignmentDisabled)
		})
	})

	Context("without cpu-load-balancing-ovs-dpdk annotation", func() {
		BeforeAll(func() {
			By("Applying the baseline profile without cpu-load-balancing-ovs-dpdk")
			profilesupdate.ApplyProfileAndWait(context.TODO(), baselineProfile)

			updatedProfile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			testlog.Infof("Updated profile: reserved=%s isolated=%s ovsDpdk=%s annotations=%v",
				*updatedProfile.Spec.CPU.Reserved, *updatedProfile.Spec.CPU.Isolated,
				*updatedProfile.Spec.CPU.OvsDpdk, updatedProfile.Annotations)

			By("Refreshing the node list after the update")
			workerRTNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
			Expect(err).ToNot(HaveOccurred())
			Expect(workerRTNodes).ToNot(BeEmpty())
		})

		It("[test_id:89988] should configure ovsdpdk.slice with partition=member", func() {
			ctx := context.TODO()
			node := &workerRTNodes[0]
			testlog.Infof("Verifying node %s", node.Name)

			By("Verifying ovsdpdk.slice cgroup hierarchy exists")
			_, err := nodes.ExecCommand(ctx, node, []string{
				"stat", ovsSliceCgroupBase + "/ovs-vswitchd.service/ovsdpdk.slice",
			})
			Expect(err).ToNot(HaveOccurred(),
				"ovsdpdk.slice directory should exist inside ovs.slice/ovs-vswitchd.service/")

			By("Verifying ovsdpdk.slice cgroup.type is threaded")
			cgroupType, err := nodes.ExecCommand(ctx, node, []string{
				"cat", ovsSliceCgroupBase + "/ovs-vswitchd.service/ovsdpdk.slice/cgroup.type",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.TrimSpace(testutils.ToString(cgroupType))).To(Equal("threaded"),
				"ovsdpdk.slice cgroup.type should be 'threaded'")

			By("Verifying ovsdpdk.slice cpuset.cpus.exclusive matches configured ovsDpdk CPUs")
			cgroupCpuSet, err := getOvsDpdkSliceExclusiveCPUs(ctx, node)
			Expect(err).ToNot(HaveOccurred())
			Expect(cgroupCpuSet.Equals(ovsDpdkSet)).To(BeTrue(),
				"ovsdpdk.slice cpuset.cpus.exclusive should be %s, got %s",
				ovsDpdkSet.String(), cgroupCpuSet.String())

			By("Verifying ovsdpdk.slice cpuset.cpus.partition is member")
			cgroupPartition, err := getOvsDpdkSlicePartition(ctx, node)
			Expect(err).ToNot(HaveOccurred(), "failed to read ovsdpdk.slice cpuset.cpus.partition")
			Expect(cgroupPartition).To(Equal("member"), "ovsdpdk.slice cpuset.cpus.partition should be 'member'")
		})

		It("[test_id:89990] should preserve ovsDpdk CPU IRQ banning across GU pod lifecycle", func() {
			verifyOvsDpdkIRQBanningAcrossGUPodLifecycle(context.TODO(), &workerRTNodes[0], baselineProfile, ovsDpdkSet, smtAlignmentDisabled)
		})

		It("[test_id:89996] should ensure ovsdpdk.slice survives ovs-vswitchd service restart", func() {
			ctx := context.TODO()
			node := &workerRTNodes[0]
			testlog.Infof("Verifying node %s", node.Name)

			By("Restarting ovs-vswitchd.service")
			// systemctl restart produces no output; ExecCommand waits for non-empty stdout and may time out.
			_, _ = nodes.ExecCommand(ctx, node, []string{
				"chroot", "/rootfs", "/bin/bash", "-c",
				"systemctl restart ovs-vswitchd.service",
			})

			By("Waiting for ovs-vswitchd.service to become active")
			Eventually(func() (string, error) {
				state, err := systemd.ShowPropertyValue(ctx, "ovs-vswitchd.service", "ActiveState", node)
				return strings.TrimSpace(state), err
			}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(
				Equal("active"),
			)

			By("Verifying ovsdpdk.slice cpuset.cpus.exclusive and cpuset.cpus.partition after ovs-vswitchd restart")
			cgroupCpuSet, err := getOvsDpdkSliceExclusiveCPUs(ctx, node)
			Expect(err).ToNot(HaveOccurred())
			Expect(cgroupCpuSet.Equals(ovsDpdkSet)).To(BeTrue(),
				"ovsdpdk.slice exclusive: want %s, got %s", ovsDpdkSet, cgroupCpuSet)

			cgroupPartition, err := getOvsDpdkSlicePartition(ctx, node)
			Expect(err).ToNot(HaveOccurred())
			Expect(cgroupPartition).To(Equal("member"),
				"ovsdpdk.slice partition: want member, got %s", cgroupPartition)
		})

	})
})

func verifyOvsDpdkIRQBanningAcrossGUPodLifecycle(ctx context.Context, node *corev1.Node, profile *performancev2.PerformanceProfile, ovsDpdkSet cpuset.CPUSet, smtAlignmentDisabled bool) {
	GinkgoHelper()
	testlog.Infof("Testing CRI-O IRQ interaction on node %s", node.Name)

	By("Verifying default_smp_affinity has ovsDpdk CPU bits cleared before pod creation")
	smpBeforeSet, err := nodes.GetDefaultSmpAffinitySet(ctx, node)
	Expect(err).ToNot(HaveOccurred())
	testlog.Infof("default_smp_affinity before pod: %s", smpBeforeSet.String())
	Expect(smpBeforeSet.Intersection(ovsDpdkSet).IsEmpty()).To(BeTrue(),
		"default_smp_affinity should not have ovsDpdk CPU bits set before pod, got CPUs %s",
		smpBeforeSet.Intersection(ovsDpdkSet).String())

	By("Verifying IRQBALANCE_BANNED_CPUS is set to ovsDpdk hex mask before pod creation")
	irqConf, err := nodes.ExecCommand(ctx, node, []string{"cat", "/rootfs/etc/sysconfig/irqbalance"})
	Expect(err).ToNot(HaveOccurred())
	bannedBeforePod := getIRQBannedCPUSet(testutils.ToString(irqConf))
	Expect(ovsDpdkSet.IsSubsetOf(bannedBeforePod)).To(BeTrue(),
		"IRQBALANCE_BANNED_CPUS should include ovsDpdk CPUs %s before pod, got %s",
		ovsDpdkSet.String(), bannedBeforePod.String())

	By("Creating a Guaranteed pod with irq-load-balancing=disable")
	testpod := pods.GetTestPod()
	testpod.Namespace = testutils.NamespaceTesting
	testpod.Annotations = map[string]string{
		"irq-load-balancing.crio.io": "disable",
	}
	var resourceCPULimit resource.Quantity

	if smtAlignmentDisabled {
		resourceCPULimit = resource.MustParse("1")
	} else {
		resourceCPULimit = resource.MustParse("2")
	}

	testpod.Spec.Containers[0].Resources = corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resourceCPULimit,
			corev1.ResourceMemory: resource.MustParse("100Mi"),
		},
	}
	runtimeClassName := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
	testpod.Spec.RuntimeClassName = &runtimeClassName
	testpod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: node.Name}

	err = testclient.DataPlaneClient.Create(ctx, testpod)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := client.IgnoreNotFound(testclient.DataPlaneClient.Delete(cleanupCtx, testpod)); err != nil {
			testlog.Infof("failed to cleanup pod %s: %v", testpod.Name, err)
		}
	})

	podKey := client.ObjectKeyFromObject(testpod)
	testpod, err = pods.WaitForCondition(ctx, podKey, corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
	pods.DumpStateOnFailure(ctx, testclient.K8sClient, testpod, err)
	Expect(err).ToNot(HaveOccurred())
	Expect(testpod.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed))
	testlog.Infof("GU pod %s is running on node %s", testpod.Name, node.Name)

	By("Verifying IRQBALANCE_BANNED_CPUS still includes ovsDpdk CPUs with pod running")
	irqConf, err = nodes.ExecCommand(ctx, node, []string{"cat", "/rootfs/etc/sysconfig/irqbalance"})
	Expect(err).ToNot(HaveOccurred())
	bannedWithPod := getIRQBannedCPUSet(testutils.ToString(irqConf))
	Expect(ovsDpdkSet.IsSubsetOf(bannedWithPod)).To(BeTrue(),
		"IRQBALANCE_BANNED_CPUS should include ovsDpdk CPUs %s with pod running, got %s",
		ovsDpdkSet.String(), bannedWithPod.String())

	By("Deleting the GU pod")
	err = testclient.DataPlaneClient.Delete(ctx, testpod)
	Expect(err).ToNot(HaveOccurred())
	err = pods.WaitForDeletion(ctx, testclient.DataPlaneClient, testpod, 5*time.Minute)
	Expect(err).ToNot(HaveOccurred())

	By("Verifying IRQBALANCE_BANNED_CPUS still includes ovsDpdk CPUs after pod deletion")
	Eventually(func() bool {
		irqConf, err := nodes.ExecCommand(ctx, node, []string{"cat", "/rootfs/etc/sysconfig/irqbalance"})
		if err != nil {
			return false
		}
		bannedAfterPod := getIRQBannedCPUSet(testutils.ToString(irqConf))
		return ovsDpdkSet.IsSubsetOf(bannedAfterPod)
	}, 2*time.Minute, 10*time.Second).Should(BeTrue(),
		"IRQBALANCE_BANNED_CPUS should still include ovsDpdk CPUs after pod deletion")

	By("Verifying default_smp_affinity still has ovsDpdk CPU bits cleared after pod deletion")
	smpAfterSet, err := nodes.GetDefaultSmpAffinitySet(ctx, node)
	Expect(err).ToNot(HaveOccurred())
	testlog.Infof("default_smp_affinity after pod deletion: %s", smpAfterSet.String())
	Expect(smpAfterSet.Intersection(ovsDpdkSet).IsEmpty()).To(BeTrue(),
		"default_smp_affinity should keep ovsDpdk CPU bits cleared after pod deletion, got CPUs %s",
		smpAfterSet.Intersection(ovsDpdkSet).String())
}

// parseCPUSetFromKernelParam extracts the CPU list from a kernel cmdline
// parameter like "isolcpus=managed_irq,1-5" or "nohz_full=2-5" and returns it
// as a cpuset.CPUSet. For isolcpus, non-numeric flag prefixes (e.g.
// "managed_irq,") are stripped before parsing.
func parseCPUSetFromKernelParam(cmdline, param string) cpuset.CPUSet {
	for _, field := range strings.Fields(cmdline) {
		if !strings.HasPrefix(field, param+"=") {
			continue
		}
		val := strings.TrimPrefix(field, param+"=")
		for i, c := range val {
			if c >= '0' && c <= '9' {
				val = val[i:]
				break
			}
		}
		set, err := cpuset.Parse(val)
		if err != nil {
			return cpuset.New()
		}
		return set
	}
	return cpuset.New()
}

// getIRQBannedCPUSet extracts the IRQBALANCE_BANNED_CPUS value from the
// irqbalance config content and returns it as a cpuset.CPUSet.
func getIRQBannedCPUSet(irqbalanceContent string) cpuset.CPUSet {
	for _, line := range strings.Split(irqbalanceContent, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "IRQBALANCE_BANNED_CPUS=") {
			continue
		}
		val := strings.TrimPrefix(line, "IRQBALANCE_BANNED_CPUS=")
		val = strings.Trim(val, `"`)
		banned, err := components.CPUMaskToCPUSet(val)
		if err != nil {
			return cpuset.New()
		}
		return banned
	}
	return cpuset.New()
}

func setPolicyOptions(profile *performancev2.PerformanceProfile, isWPEnabled bool, smtAlignmentDisabled bool) {
	GinkgoHelper()
	policyOptions := map[string]string{}
	if smtAlignmentDisabled {
		testlog.Infof("canceling SMT alignment, adding full-pcpus-only=false via experimental annotation")
		policyOptions["full-pcpus-only"] = "false"
	}

	if !isWPEnabled {
		testlog.Infof("Workload partitioning not enabled, adding strict-cpu-reservation via experimental annotation")
		policyOptions["strict-cpu-reservation"] = "true"
	}

	if len(policyOptions) > 0 {
		optJSON, err := json.Marshal(map[string]interface{}{"cpuManagerPolicyOptions": policyOptions})
		Expect(err).ToNot(HaveOccurred())
		profile.Annotations["kubeletconfig.experimental"] = string(optJSON)
	}
}

func getOvsDpdkSlicePartition(ctx context.Context, node *corev1.Node) (string, error) {
	cgroupPartition, err := nodes.ExecCommand(ctx, node, []string{
		"cat", ovsSliceCgroupBase + "/ovs-vswitchd.service/ovsdpdk.slice/cpuset.cpus.partition",
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(testutils.ToString(cgroupPartition)), nil
}

func getOvsDpdkSliceExclusiveCPUs(ctx context.Context, node *corev1.Node) (cpuset.CPUSet, error) {
	cgroupCpus, err := nodes.ExecCommand(ctx, node, []string{
		"cat", ovsSliceCgroupBase + "/ovs-vswitchd.service/ovsdpdk.slice/cpuset.cpus.exclusive",
	})
	if err != nil {
		return cpuset.New(), err
	}
	return cpuset.Parse(strings.TrimSpace(testutils.ToString(cgroupCpus)))
}
