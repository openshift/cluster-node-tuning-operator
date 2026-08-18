package __performance_update

import (
	"context"
	"encoding/json"
	"fmt"
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
)

const numberOfCoresThatRequiredCancelingSMTAlignment = 4

var _ = Describe("[performance] ovsDpdk CPUs", Ordered, Label(string(label.OvsDpdk), string(label.Slow), string(label.Tier2)), func() {
	var (
		workerRTNodes  []corev1.Node
		profile        *performancev2.PerformanceProfile
		initialProfile *performancev2.PerformanceProfile

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

		profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		initialProfile = profile.DeepCopy()

		reservedSet, err = cpuset.Parse(string(*profile.Spec.CPU.Reserved))
		Expect(err).ToNot(HaveOccurred())
		isolatedSet, err := cpuset.Parse(string(*profile.Spec.CPU.Isolated))
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

		By("Updating the profile with ovsDpdk CPUs")
		currentProfile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		currentProfile.Spec.CPU.Isolated = &newIsolated
		currentProfile.Spec.CPU.OvsDpdk = &ovsDpdkCPUs
		if currentProfile.Annotations == nil {
			currentProfile.Annotations = make(map[string]string)
		}
		currentProfile.Annotations[performancev2.PerformanceProfileCPULoadBalancingOvsDpdkAnnotation] = "disable"

		// we're working under the assumption that all worker RT nodes have the same number of CPUs
		if numOfCores, _ := workerRTNodes[0].Status.Capacity.Cpu().AsInt64(); numOfCores <= numberOfCoresThatRequiredCancelingSMTAlignment {
			smtAlignmentDisabled = true
		}
		setPolicyOptions(currentProfile, isWPEnabled, smtAlignmentDisabled)
		profiles.UpdateWithRetry(currentProfile)

		updatedProfile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		testlog.Infof("Updated profile: reserved=%s isolated=%s ovsDpdk=%s annotations=%v",
			*updatedProfile.Spec.CPU.Reserved, *updatedProfile.Spec.CPU.Isolated,
			*updatedProfile.Spec.CPU.OvsDpdk, updatedProfile.Annotations)

		By("Waiting for the tuning to be applied")
		profilesupdate.WaitForTuningUpdating(ctx, currentProfile)
		profilesupdate.WaitForTuningUpdated(ctx, currentProfile)

		By("Refreshing the node list after the update")
		workerRTNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred())
		Expect(workerRTNodes).ToNot(BeEmpty())
	})

	AfterAll(func() {
		if initialProfile == nil {
			return
		}
		By("Reverting the profile to its initial state")
		ctx := context.TODO()
		currentProfile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		currentProfile.Spec = *initialProfile.Spec.DeepCopy()
		currentProfile.Spec.CPU.OvsDpdk = nil
		delete(currentProfile.Annotations, performancev2.PerformanceProfileCPULoadBalancingOvsDpdkAnnotation)
		delete(currentProfile.Annotations, "kubeletconfig.experimental")
		profiles.UpdateWithRetry(currentProfile)

		profilesupdate.WaitForTuningUpdating(ctx, currentProfile)
		profilesupdate.WaitForTuningUpdated(ctx, currentProfile)
	})

	Context("when ovsDpdk CPUs and cpu-load-balancing-ovs-dpdk annotation are set", func() {
		It("should apply ovsDpdk CPU node configuration", func() {
			ctx := context.TODO()
			expectedIsolatedPlusOvsDpdk := newIsolatedSet.Union(ovsDpdkSet)
			expectedReservedSystem := reservedSet.Union(ovsDpdkSet)

			node := &workerRTNodes[0]
			testlog.Infof("Verifying node %s", node.Name)

			By(fmt.Sprintf("Verifying kernel cmdline on node %s", node.Name))
			cmdline, err := nodes.ExecCommand(ctx, node, []string{"cat", "/proc/cmdline"})
			Expect(err).ToNot(HaveOccurred())
			cmdlineStr := testutils.ToString(cmdline)

			By(fmt.Sprintf("Verifying isolcpus includes ovsDpdk CPUs on node %s", node.Name))
			isolcpusSet := parseCPUSetFromKernelParam(cmdlineStr, "isolcpus")
			Expect(isolcpusSet.IsEmpty()).To(BeFalse(), "isolcpus param not found in cmdline")
			Expect(expectedIsolatedPlusOvsDpdk.IsSubsetOf(isolcpusSet)).To(BeTrue(),
				fmt.Sprintf("isolcpus=%s should include all isolated + ovsDpdk CPUs %s",
					isolcpusSet.String(), expectedIsolatedPlusOvsDpdk.String()))

			By(fmt.Sprintf("Verifying nohz_full includes ovsDpdk CPUs on node %s", node.Name))
			nohzSet := parseCPUSetFromKernelParam(cmdlineStr, "nohz_full")
			Expect(nohzSet.IsEmpty()).To(BeFalse(), "nohz_full param not found in cmdline")
			Expect(ovsDpdkSet.IsSubsetOf(nohzSet)).To(BeTrue(),
				fmt.Sprintf("nohz_full=%s should include ovsDpdk CPUs %s",
					nohzSet.String(), ovsDpdkSet.String()))

			By(fmt.Sprintf("Verifying rcu_nocbs includes ovsDpdk CPUs on node %s", node.Name))
			rcuSet := parseCPUSetFromKernelParam(cmdlineStr, "rcu_nocbs")
			Expect(rcuSet.IsEmpty()).To(BeFalse(), "rcu_nocbs param not found in cmdline")
			Expect(ovsDpdkSet.IsSubsetOf(rcuSet)).To(BeTrue(),
				fmt.Sprintf("rcu_nocbs=%s should include ovsDpdk CPUs %s",
					rcuSet.String(), ovsDpdkSet.String()))

			By(fmt.Sprintf("Verifying systemd.cpu_affinity excludes ovsDpdk CPUs on node %s", node.Name))
			affinitySet := parseCPUSetFromKernelParam(cmdlineStr, "systemd.cpu_affinity")
			Expect(affinitySet.IsEmpty()).To(BeFalse(), "systemd.cpu_affinity param not found in cmdline")
			Expect(affinitySet.Intersection(ovsDpdkSet).IsEmpty()).To(BeTrue(),
				fmt.Sprintf("systemd.cpu_affinity=%s should not contain ovsDpdk CPUs %s",
					affinitySet.String(), ovsDpdkSet.String()))

			By(fmt.Sprintf("Verifying kubelet reservedSystemCPUs is union of reserved + ovsDpdk on node %s", node.Name))
			kubeletConfig, err := nodes.GetKubeletConfig(ctx, node)
			Expect(err).ToNot(HaveOccurred())
			reservedSystemCPUs, err := cpuset.Parse(kubeletConfig.ReservedSystemCPUs)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedSystemCPUs.Equals(expectedReservedSystem)).To(BeTrue(),
				fmt.Sprintf("ReservedSystemCPUs should be %s (reserved + ovsDpdk), got %s",
					expectedReservedSystem.String(), reservedSystemCPUs.String()))

			By(fmt.Sprintf("Verifying IRQBALANCE_BANNED_CPUS on node %s", node.Name))
			irqConf, err := nodes.ExecCommand(ctx, node, []string{"cat", "/rootfs/etc/sysconfig/irqbalance"})
			Expect(err).ToNot(HaveOccurred())
			irqConfStr := testutils.ToString(irqConf)

			bannedSet := getIRQBannedCPUSet(irqConfStr)
			Expect(ovsDpdkSet.IsSubsetOf(bannedSet)).To(BeTrue(),
				fmt.Sprintf("IRQBALANCE_BANNED_CPUS should include ovsDpdk CPUs %s, got %s",
					ovsDpdkSet.String(), bannedSet.String()))

			By(fmt.Sprintf("Verifying default_smp_affinity excludes ovsDpdk CPUs on node %s", node.Name))
			smpCPUSet, err := nodes.GetDefaultSmpAffinitySet(ctx, node)
			Expect(err).ToNot(HaveOccurred())
			testlog.Infof("default_smp_affinity on %s: %s", node.Name, smpCPUSet.String())
			Expect(smpCPUSet.Intersection(ovsDpdkSet).IsEmpty()).To(BeTrue(),
				fmt.Sprintf("default_smp_affinity should not have ovsDpdk CPU bits set, got CPUs %s",
					smpCPUSet.Intersection(ovsDpdkSet).String()))

			By(fmt.Sprintf("Verifying ovs-dpdk-cpus-configure script exists on node %s", node.Name))
			_, err = nodes.ExecCommand(ctx, node, []string{
				"stat", "/rootfs/usr/local/bin/ovs-dpdk-cpus-configure.sh",
			})
			Expect(err).ToNot(HaveOccurred(),
				"ovs-dpdk-cpus-configure.sh should be present on the node")

			cgroupBase := "/rootfs/sys/fs/cgroup/ovs.slice"

			By(fmt.Sprintf("Verifying ovsdpdk.slice cgroup hierarchy exists on node %s", node.Name))
			_, err = nodes.ExecCommand(ctx, node, []string{
				"stat", cgroupBase + "/ovs-vswitchd.service/ovsdpdk.slice",
			})
			Expect(err).ToNot(HaveOccurred(),
				"ovsdpdk.slice directory should exist inside ovs.slice/ovs-vswitchd.service/")

			By(fmt.Sprintf("Verifying ovs.slice cgroup.subtree_control enables cpuset on node %s", node.Name))
			subtreeCtl, err := nodes.ExecCommand(ctx, node, []string{
				"cat", cgroupBase + "/cgroup.subtree_control",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(testutils.ToString(subtreeCtl)).To(ContainSubstring("cpuset"),
				"ovs.slice cgroup.subtree_control should contain 'cpuset'")

			By(fmt.Sprintf("Verifying ovs-vswitchd.service cgroup.subtree_control enables cpuset on node %s", node.Name))
			subtreeCtl, err = nodes.ExecCommand(ctx, node, []string{
				"cat", cgroupBase + "/ovs-vswitchd.service/cgroup.subtree_control",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(testutils.ToString(subtreeCtl)).To(ContainSubstring("cpuset"),
				"ovs-vswitchd.service cgroup.subtree_control should contain 'cpuset'")

			By(fmt.Sprintf("Verifying ovsdpdk.slice cgroup.type is threaded on node %s", node.Name))
			cgroupType, err := nodes.ExecCommand(ctx, node, []string{
				"cat", cgroupBase + "/ovs-vswitchd.service/ovsdpdk.slice/cgroup.type",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.TrimSpace(testutils.ToString(cgroupType))).To(Equal("threaded"),
				"ovsdpdk.slice cgroup.type should be 'threaded'")

			By(fmt.Sprintf("Verifying ovsdpdk.slice cpuset.cpus.exclusive matches configured ovsDpdk CPUs on node %s", node.Name))
			cgroupCpus, err := nodes.ExecCommand(ctx, node, []string{
				"cat", cgroupBase + "/ovs-vswitchd.service/ovsdpdk.slice/cpuset.cpus.exclusive",
			})
			Expect(err).ToNot(HaveOccurred())
			cgroupCpuSet, err := cpuset.Parse(strings.TrimSpace(testutils.ToString(cgroupCpus)))
			Expect(err).ToNot(HaveOccurred())
			Expect(cgroupCpuSet.Equals(ovsDpdkSet)).To(BeTrue(),
				fmt.Sprintf("ovsdpdk.slice cpuset.cpus.exclusive should be %s, got %s",
					ovsDpdkSet.String(), cgroupCpuSet.String()))

			By(fmt.Sprintf("Verifying ovsdpdk.slice cpuset.cpus.partition is isolated on node %s", node.Name))
			cgroupPartition, err := nodes.ExecCommand(ctx, node, []string{
				"cat", cgroupBase + "/ovs-vswitchd.service/ovsdpdk.slice/cpuset.cpus.partition",
			})
			Expect(err).ToNot(HaveOccurred(),
				"failed to read ovsdpdk.slice cpuset.cpus.partition")
			Expect(strings.TrimSpace(testutils.ToString(cgroupPartition))).To(Equal("isolated"),
				"ovsdpdk.slice cpuset.cpus.partition should be 'isolated'")
		})

		It("should preserve ovsDpdk CPU IRQ banning across GU pod lifecycle", func() {
			verifyOvsDpdkIRQBanningAcrossGUPodLifecycle(context.TODO(), &workerRTNodes[0], profile, ovsDpdkSet, smtAlignmentDisabled)
		})
	})
})

var _ = Describe("[performance] ovsDpdk CPUs default partition", Ordered, Label(string(label.OvsDpdk), string(label.Slow), string(label.Tier2)), func() {
	var (
		workerRTNodes  []corev1.Node
		profile        *performancev2.PerformanceProfile
		initialProfile *performancev2.PerformanceProfile

		ovsDpdkSet           cpuset.CPUSet
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

		profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		initialProfile = profile.DeepCopy()

		isolatedSet, err := cpuset.Parse(string(*profile.Spec.CPU.Isolated))
		Expect(err).ToNot(HaveOccurred())

		isolatedList := isolatedSet.List()
		Expect(len(isolatedList)).To(BeNumerically(">=", 2),
			"need at least 2 isolated CPUs to split into isolated + ovsDpdk")

		ovsDpdkSet = cpuset.New(isolatedList[0])
		newIsolatedSet := cpuset.New(isolatedList[1:]...)

		ovsDpdkCPUs := performancev2.CPUSet(ovsDpdkSet.String())
		newIsolated := performancev2.CPUSet(newIsolatedSet.String())

		testlog.Infof("Isolated: %s, OvsDpdk: %s", newIsolatedSet.String(), ovsDpdkSet.String())

		ctx := context.TODO()
		isWPEnabled, err := cluster.IsWorkloadPartitioningEnabled(ctx, testclient.Client)
		Expect(err).ToNot(HaveOccurred())

		By("Updating the profile with ovsDpdk CPUs (no cpu-load-balancing annotation)")
		currentProfile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		currentProfile.Spec.CPU.Isolated = &newIsolated
		currentProfile.Spec.CPU.OvsDpdk = &ovsDpdkCPUs
		if currentProfile.Annotations == nil {
			currentProfile.Annotations = make(map[string]string)
		}

		// we're working under the assumption that all worker RT nodes have the same number of CPUs
		if numOfCores, _ := workerRTNodes[0].Status.Capacity.Cpu().AsInt64(); numOfCores <= numberOfCoresThatRequiredCancelingSMTAlignment {
			smtAlignmentDisabled = true
		}
		setPolicyOptions(currentProfile, isWPEnabled, smtAlignmentDisabled)
		profiles.UpdateWithRetry(currentProfile)

		updatedProfile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		testlog.Infof("Updated profile: reserved=%s isolated=%s ovsDpdk=%s annotations=%v",
			*updatedProfile.Spec.CPU.Reserved, *updatedProfile.Spec.CPU.Isolated,
			*updatedProfile.Spec.CPU.OvsDpdk, updatedProfile.Annotations)

		By("Waiting for the tuning to be applied")
		profilesupdate.WaitForTuningUpdating(ctx, currentProfile)
		profilesupdate.WaitForTuningUpdated(ctx, currentProfile)

		By("Refreshing the node list after the update")
		workerRTNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred())
		Expect(workerRTNodes).ToNot(BeEmpty())
	})

	AfterAll(func() {
		if initialProfile == nil {
			return
		}
		By("Reverting the profile to its initial state")
		ctx := context.TODO()
		currentProfile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		currentProfile.Spec = *initialProfile.Spec.DeepCopy()
		currentProfile.Spec.CPU.OvsDpdk = nil
		delete(currentProfile.Annotations, "kubeletconfig.experimental")
		profiles.UpdateWithRetry(currentProfile)

		profilesupdate.WaitForTuningUpdating(ctx, currentProfile)
		profilesupdate.WaitForTuningUpdated(ctx, currentProfile)
	})

	Context("when ovsDpdk CPUs are set without cpu-load-balancing-ovs-dpdk annotation", func() {
		It("should configure ovsdpdk.slice with partition=member", func() {
			ctx := context.TODO()
			node := &workerRTNodes[0]
			testlog.Infof("Verifying node %s", node.Name)

			cgroupBase := "/rootfs/sys/fs/cgroup/ovs.slice"

			By(fmt.Sprintf("Verifying ovsdpdk.slice cgroup hierarchy exists on node %s", node.Name))
			_, err := nodes.ExecCommand(ctx, node, []string{
				"stat", cgroupBase + "/ovs-vswitchd.service/ovsdpdk.slice",
			})
			Expect(err).ToNot(HaveOccurred(),
				"ovsdpdk.slice directory should exist inside ovs.slice/ovs-vswitchd.service/")

			By(fmt.Sprintf("Verifying ovsdpdk.slice cgroup.type is threaded on node %s", node.Name))
			cgroupType, err := nodes.ExecCommand(ctx, node, []string{
				"cat", cgroupBase + "/ovs-vswitchd.service/ovsdpdk.slice/cgroup.type",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.TrimSpace(testutils.ToString(cgroupType))).To(Equal("threaded"),
				"ovsdpdk.slice cgroup.type should be 'threaded'")

			By(fmt.Sprintf("Verifying ovsdpdk.slice cpuset.cpus.exclusive matches configured ovsDpdk CPUs on node %s", node.Name))
			cgroupCpus, err := nodes.ExecCommand(ctx, node, []string{
				"cat", cgroupBase + "/ovs-vswitchd.service/ovsdpdk.slice/cpuset.cpus.exclusive",
			})
			Expect(err).ToNot(HaveOccurred())
			cgroupCpuSet, err := cpuset.Parse(strings.TrimSpace(testutils.ToString(cgroupCpus)))
			Expect(err).ToNot(HaveOccurred())
			Expect(cgroupCpuSet.Equals(ovsDpdkSet)).To(BeTrue(),
				fmt.Sprintf("ovsdpdk.slice cpuset.cpus.exclusive should be %s, got %s",
					ovsDpdkSet.String(), cgroupCpuSet.String()))

			By(fmt.Sprintf("Verifying ovsdpdk.slice cpuset.cpus.partition is member on node %s", node.Name))
			cgroupPartition, err := nodes.ExecCommand(ctx, node, []string{
				"cat", cgroupBase + "/ovs-vswitchd.service/ovsdpdk.slice/cpuset.cpus.partition",
			})
			Expect(err).ToNot(HaveOccurred(),
				"failed to read ovsdpdk.slice cpuset.cpus.partition")
			Expect(strings.TrimSpace(testutils.ToString(cgroupPartition))).To(Equal("member"),
				"ovsdpdk.slice cpuset.cpus.partition should be 'member'")
		})

		It("should preserve ovsDpdk CPU IRQ banning across GU pod lifecycle", func() {
			verifyOvsDpdkIRQBanningAcrossGUPodLifecycle(context.TODO(), &workerRTNodes[0], profile, ovsDpdkSet, smtAlignmentDisabled)
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
		fmt.Sprintf("default_smp_affinity should not have ovsDpdk CPU bits set before pod, got CPUs %s",
			smpBeforeSet.Intersection(ovsDpdkSet).String()))

	By("Verifying IRQBALANCE_BANNED_CPUS is set to ovsDpdk hex mask before pod creation")
	irqConf, err := nodes.ExecCommand(ctx, node, []string{"cat", "/rootfs/etc/sysconfig/irqbalance"})
	Expect(err).ToNot(HaveOccurred())
	bannedBeforePod := getIRQBannedCPUSet(testutils.ToString(irqConf))
	Expect(ovsDpdkSet.IsSubsetOf(bannedBeforePod)).To(BeTrue(),
		fmt.Sprintf("IRQBALANCE_BANNED_CPUS should include ovsDpdk CPUs %s before pod, got %s",
			ovsDpdkSet.String(), bannedBeforePod.String()))

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
		fmt.Sprintf("IRQBALANCE_BANNED_CPUS should include ovsDpdk CPUs %s with pod running, got %s",
			ovsDpdkSet.String(), bannedWithPod.String()))

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
		fmt.Sprintf("default_smp_affinity should keep ovsDpdk CPU bits cleared after pod deletion, got CPUs %s",
			smpAfterSet.Intersection(ovsDpdkSet).String()))
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
