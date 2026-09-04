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

	configv1 "github.com/openshift/api/config/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/utils/schedstat"
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
const crioRuntimesConfigFile = "/rootfs/etc/crio/crio.conf.d/99-runtimes.conf"

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
			Expect(updatedProfile.Annotations[performancev2.PerformanceProfileCPULoadBalancingOvsDpdkAnnotation]).To(Equal("disable"))
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
			expectedReservedSystem := reservedSet.Union(ovsDpdkSet)

			node := &workerRTNodes[0]
			testlog.Infof("Verifying node %s", node.Name)

			verifyOvsDpdkKernelCmdline(ctx, node, newIsolatedSet, ovsDpdkSet)

			By("Verifying kubelet reservedSystemCPUs is union of reserved + ovsDpdk")
			reservedSystemCPUs, err := getReservedSystemCPUs(ctx, node)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedSystemCPUs.Equals(expectedReservedSystem)).To(BeTrue(),
				"ReservedSystemCPUs should be %s (reserved + ovsDpdk), got %s",
				expectedReservedSystem.String(), reservedSystemCPUs.String())

			verifyOvsDpdkIRQIsolation(ctx, node, ovsDpdkSet)

			By("Verifying ovs-dpdk-cpus-configure script exists")
			_, err = nodes.ExecCommand(ctx, node, []string{
				"stat", "/rootfs/usr/local/bin/ovs-dpdk-cpus-configure.sh",
			})
			Expect(err).ToNot(HaveOccurred(),
				"ovs-dpdk-cpus-configure.sh should be present on the node")

			verifyOvsDpdkServiceEnv(ctx, node, ovsDpdkSet)

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

			verifyOvsDpdkSlice(ctx, node, ovsDpdkSet, "isolated")
		})

		It("[test_id:89989] should preserve ovsDpdk CPU IRQ banning across GU pod lifecycle", func() {
			verifyOvsDpdkIRQBanningAcrossGUPodLifecycle(context.TODO(), &workerRTNodes[0], baselineProfile, ovsDpdkSet, smtAlignmentDisabled)
		})

		It("[test_id:89992] should keep ovsDpdk CPUs outside kernel scheduling domains", func() {
			ctx := context.TODO()
			node := &workerRTNodes[0]
			testlog.Infof("Verifying node %s", node.Name)

			By("Verifying ovsDpdk CPUs are outside kernel scheduling domains before pod creation")
			out, err := nodes.ExecCommand(ctx, node, []string{"/bin/bash", "-c", "cat /proc/schedstat"})
			Expect(err).ToNot(HaveOccurred())
			info, err := schedstat.ParseData(strings.NewReader(testutils.ToString(out)))
			Expect(err).ToNot(HaveOccurred())
			for _, cpuID := range ovsDpdkSet.List() {
				doms, ok := info.GetDomainsByID(cpuID)
				Expect(ok).To(BeTrue(), "cpu%d should appear in /proc/schedstat", cpuID)
				Expect(doms).To(BeEmpty(), "cpu%d should have 0 scheduling domains, got %v", cpuID, doms)
			}

			By("Creating a Guaranteed pod without irq-load-balancing annotation")
			testpod := pods.GetTestPod()
			testpod.Namespace = testutils.NamespaceTesting
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
			runtimeClassName := components.GetComponentName(baselineProfile.Name, components.ComponentNamePrefix)
			testpod.Spec.RuntimeClassName = &runtimeClassName
			testpod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: node.Name}

			err = testclient.DataPlaneClient.Create(ctx, testpod)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				if err := pods.DeleteAndSync(cleanupCtx, testclient.DataPlaneClient, testpod); err != nil {
					testlog.Infof("failed to cleanup pod %s: %v", testpod.Name, err)
				}
			})

			podKey := client.ObjectKeyFromObject(testpod)
			testpod, err = pods.WaitForCondition(ctx, podKey, corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
			pods.DumpStateOnFailure(ctx, testclient.K8sClient, testpod, err)
			Expect(err).ToNot(HaveOccurred())
			Expect(testpod.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed))
			testlog.Infof("GU pod %s is running on node %s", testpod.Name, node.Name)

			By("Deleting the GU pod")
			Expect(pods.DeleteAndSync(ctx, testclient.DataPlaneClient, testpod)).To(Succeed())

			By("Verifying ovsDpdk CPUs are still outside kernel scheduling domains after pod deletion")
			out, err = nodes.ExecCommand(ctx, node, []string{"/bin/bash", "-c", "cat /proc/schedstat"})
			Expect(err).ToNot(HaveOccurred())
			info, err = schedstat.ParseData(strings.NewReader(testutils.ToString(out)))
			Expect(err).ToNot(HaveOccurred())
			for _, cpuID := range ovsDpdkSet.List() {
				doms, ok := info.GetDomainsByID(cpuID)
				Expect(ok).To(BeTrue(), "cpu%d should appear in /proc/schedstat", cpuID)
				Expect(doms).To(BeEmpty(), "cpu%d should have 0 scheduling domains, got %v", cpuID, doms)
			}
		})

		It("[test_id:89993] should keep ovsDpdk IRQ ban after node reboot", func() {
			ctx := context.TODO()
			node := &workerRTNodes[0]
			testlog.Infof("Verifying node %s", node.Name)

			By("Rebooting the node")
			_, _ = nodes.ExecCommand(ctx, node, []string{"sh", "-c", "chroot /rootfs systemctl reboot"})
			nodes.WaitForNotReadyOrFail("Reboot", node.Name, 10*time.Minute, 30*time.Second)
			nodes.WaitForReadyOrFail("Reboot", node.Name, 10*time.Minute, 30*time.Second)

			verifyOvsDpdkIRQIsolation(ctx, node, ovsDpdkSet)
		})

		// Keep this It last in the Context: it removes ovsDpdk and does not restore it.
		It("[test_id:89997] should clean up all ovsDpdk artifacts when ovsDpdk is removed", func() {
			ctx := context.TODO()
			node := &workerRTNodes[0]
			testlog.Infof("Verifying node %s", node.Name)

			By("Removing ovsDpdk and restoring former D CPUs to isolated")
			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			restoredIsolatedSet := newIsolatedSet.Union(ovsDpdkSet)
			restoredIsolated := performancev2.CPUSet(restoredIsolatedSet.String())
			profile.Spec.CPU.OvsDpdk = nil
			profile.Spec.CPU.Isolated = &restoredIsolated
			profilesupdate.ApplyProfileAndWait(ctx, profile)

			By("Verifying kernel cmdline reflects restored isolated CPUs")
			cmdline, err := nodes.ExecCommand(ctx, node, []string{"cat", "/proc/cmdline"})
			Expect(err).ToNot(HaveOccurred())
			cmdlineStr := testutils.ToString(cmdline)

			By("Verifying isolcpus includes restored isolated CPUs")
			isolcpusSet := parseCPUSetFromKernelParam(cmdlineStr, "isolcpus")
			Expect(isolcpusSet.IsEmpty()).To(BeFalse(), "isolcpus param not found in cmdline")
			Expect(restoredIsolatedSet.IsSubsetOf(isolcpusSet)).To(BeTrue(),
				"isolcpus=%s should include restored isolated CPUs %s",
				isolcpusSet.String(), restoredIsolatedSet.String())

			By("Verifying nohz_full includes restored isolated CPUs")
			nohzSet := parseCPUSetFromKernelParam(cmdlineStr, "nohz_full")
			Expect(nohzSet.IsEmpty()).To(BeFalse(), "nohz_full param not found in cmdline")
			Expect(restoredIsolatedSet.IsSubsetOf(nohzSet)).To(BeTrue(),
				"nohz_full=%s should include restored isolated CPUs %s",
				nohzSet.String(), restoredIsolatedSet.String())

			By("Verifying rcu_nocbs includes restored isolated CPUs")
			rcuSet := parseCPUSetFromKernelParam(cmdlineStr, "rcu_nocbs")
			Expect(rcuSet.IsEmpty()).To(BeFalse(), "rcu_nocbs param not found in cmdline")
			Expect(restoredIsolatedSet.IsSubsetOf(rcuSet)).To(BeTrue(),
				"rcu_nocbs=%s should include restored isolated CPUs %s",
				rcuSet.String(), restoredIsolatedSet.String())

			By("Verifying systemd.cpu_affinity excludes restored isolated CPUs")
			affinitySet := parseCPUSetFromKernelParam(cmdlineStr, "systemd.cpu_affinity")
			Expect(affinitySet.IsEmpty()).To(BeFalse(), "systemd.cpu_affinity param not found in cmdline")
			Expect(affinitySet.Intersection(restoredIsolatedSet).IsEmpty()).To(BeTrue(),
				"systemd.cpu_affinity=%s should not contain restored isolated CPUs %s",
				affinitySet.String(), restoredIsolatedSet.String())

			By("Verifying ovsdpdk.slice is gone")
			cmd := []string{"stat", ovsSliceCgroupBase + "/ovs-vswitchd.service/ovsdpdk.slice"}
			_, err = nodes.ExecCommand(ctx, node, cmd)
			Expect(err).To(HaveOccurred(), "ovsdpdk.slice should be removed under ovs.slice/ovs-vswitchd.service/")
			Expect(err.Error()).To(ContainSubstring("No such file or directory"))

			By("Verifying kubelet reservedSystemCPUs is reserved only (no ovsDpdk)")
			reservedSystemCPUs, err := getReservedSystemCPUs(ctx, node)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedSystemCPUs.Equals(reservedSet)).To(BeTrue(),
				"ReservedSystemCPUs should be %s (reserved only), got %s",
				reservedSet.String(), reservedSystemCPUs.String())

			By("Verifying IRQBALANCE_BANNED_CPUS no longer includes former ovsDpdk CPUs")
			bannedSet, err := getNodeIRQBannedCPUSet(ctx, node)
			Expect(err).ToNot(HaveOccurred())
			Expect(bannedSet.Intersection(ovsDpdkSet).IsEmpty()).To(BeTrue(),
				"IRQBALANCE_BANNED_CPUS should not include former ovsDpdk CPUs %s, got %s",
				ovsDpdkSet.String(), bannedSet.String())

			By("Verifying default_smp_affinity includes former ovsDpdk CPUs again")
			smpCPUSet, err := nodes.GetDefaultSmpAffinitySet(ctx, node)
			Expect(err).ToNot(HaveOccurred())
			Expect(ovsDpdkSet.IsSubsetOf(smpCPUSet)).To(BeTrue(),
				"default_smp_affinity should include former ovsDpdk CPUs %s, got %s",
				ovsDpdkSet.String(), smpCPUSet.String())

			By("Verifying ovs-dpdk-cpus-configure script is gone")
			cmd = []string{"stat", "/rootfs/usr/local/bin/ovs-dpdk-cpus-configure.sh"}
			_, err = nodes.ExecCommand(ctx, node, cmd)
			Expect(err).To(HaveOccurred(), "ovs-dpdk-cpus-configure.sh should be removed from the node")
			Expect(err.Error()).To(ContainSubstring("No such file or directory"))

			By("Verifying ovs-vswitchd.service has no OVS_DPDK_CPUS in Environment")
			envBlob, err := systemd.ShowPropertyValue(ctx, "ovs-vswitchd.service", "Environment", node)
			Expect(err).ToNot(HaveOccurred())
			Expect(envBlob).NotTo(ContainSubstring("OVS_DPDK_CPUS"),
				"ovs-vswitchd.service Environment should not contain OVS_DPDK_CPUS, got %s", envBlob)
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

			verifyOvsDpdkSlice(ctx, node, ovsDpdkSet, "member")
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

			verifyOvsDpdkSlice(ctx, node, ovsDpdkSet, "member")
		})

		It("[test_id:89994] should update isolation when ovsDpdk CPUs are expanded", func() {
			ctx := context.TODO()
			node := &workerRTNodes[0]
			testlog.Infof("Verifying node %s", node.Name)

			if newIsolatedSet.Size() < 2 {
				Skip("not enough isolated CPUs to expand ovsDpdk")
			}

			By("Expanding ovsDpdk CPUs (carve from isolated) and waiting for MCP")
			isolatedList := newIsolatedSet.List()
			expandedOvsDpdkSet := ovsDpdkSet.Union(cpuset.New(isolatedList[0]))
			shrunkIsolatedSet := cpuset.New(isolatedList[1:]...)
			expandedOvsDpdk := performancev2.CPUSet(expandedOvsDpdkSet.String())
			shrunkIsolated := performancev2.CPUSet(shrunkIsolatedSet.String())

			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			profile.Spec.CPU.OvsDpdk = &expandedOvsDpdk
			profile.Spec.CPU.Isolated = &shrunkIsolated
			profilesupdate.ApplyProfileAndWait(ctx, profile)

			// No restore: 89995 (next) only needs ovsDpdk present, not a specific size; AfterAll reverts the profile.
			ovsDpdkSet = expandedOvsDpdkSet
			newIsolatedSet = shrunkIsolatedSet

			expectedReservedSystem := reservedSet.Union(ovsDpdkSet)

			verifyOvsDpdkKernelCmdline(ctx, node, newIsolatedSet, ovsDpdkSet)

			By("Verifying kubelet reservedSystemCPUs is reserved + expanded ovsDpdk")
			reservedSystemCPUs, err := getReservedSystemCPUs(ctx, node)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedSystemCPUs.Equals(expectedReservedSystem)).To(BeTrue(),
				"ReservedSystemCPUs should be %s (reserved + ovsDpdk), got %s",
				expectedReservedSystem.String(), reservedSystemCPUs.String())

			verifyOvsDpdkIRQIsolation(ctx, node, ovsDpdkSet)
			verifyOvsDpdkSlice(ctx, node, ovsDpdkSet, "member")
			verifyOvsDpdkServiceEnv(ctx, node, ovsDpdkSet)
		})

		// Keep this It last in the Describe: relies on AfterAll to revert mixedCpus/shared (and prior expand); done so to save a reboot.
		It("[test_id:89995] should coexist with mixed CPUs", Label(string(label.MixedCPUs)), func() {
			ctx := context.TODO()
			node := &workerRTNodes[0]
			testlog.Infof("Verifying node %s", node.Name)

			By("Checking MixedCPUsAllocation feature gate")
			fg := &configv1.FeatureGate{}
			Expect(testclient.ControlPlaneClient.Get(ctx, client.ObjectKey{Name: "cluster"}, fg)).To(Succeed())
			Expect(fg.Status.FeatureGates).ToNot(BeEmpty())

			mixedCPUsFG := false
			for _, enabled := range fg.Status.FeatureGates[0].Enabled {
				if enabled.Name == "MixedCPUsAllocation" {
					mixedCPUsFG = true
					break
				}
			}
			if !mixedCPUsFG {
				Skip("MixedCPUsAllocation feature gate is disabled")
			}

			By("Carving shared from isolated")
			if newIsolatedSet.Size() < 2 {
				Skip("need at least 2 isolated CPUs to carve shared while keeping isolated")
			}
			isolatedList := newIsolatedSet.List()
			sharedSet := cpuset.New(isolatedList[0])
			shrunkIsolatedSet := cpuset.New(isolatedList[1:]...)
			testlog.Infof("Shared: %s, Isolated after carve: %s", sharedSet.String(), shrunkIsolatedSet.String())

			By("Setting workloadHints.mixedCpus to true")
			sharedCPUs := performancev2.CPUSet(sharedSet.String())
			shrunkIsolated := performancev2.CPUSet(shrunkIsolatedSet.String())
			mixedCpusEnabled := true

			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			profile.Spec.CPU.Shared = &sharedCPUs
			profile.Spec.CPU.Isolated = &shrunkIsolated
			if profile.Spec.WorkloadHints == nil {
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{}
			}
			profile.Spec.WorkloadHints.MixedCpus = &mixedCpusEnabled

			By("Applying profile and waiting for MCP")
			profilesupdate.ApplyProfileAndWait(ctx, profile)

			By("Verifying kubelet reservedSystemCPUs is reserved + shared + ovsDpdk")
			expectedReservedSystem := reservedSet.Union(sharedSet).Union(ovsDpdkSet)
			reservedSystemCPUs, err := getReservedSystemCPUs(ctx, node)
			Expect(err).ToNot(HaveOccurred())
			Expect(reservedSystemCPUs.Equals(expectedReservedSystem)).To(BeTrue(),
				"ReservedSystemCPUs should be %s (reserved + shared + ovsDpdk), got %s",
				expectedReservedSystem.String(), reservedSystemCPUs.String())

			By("Verifying CRI-O shared_cpuset is shared only")
			crioSharedOut, err := nodes.ExecCommand(ctx, node, []string{
				"bash", "-c",
				fmt.Sprintf(`awk -F '"' '/shared_cpuset.*/ { print $2 }' %s`, crioRuntimesConfigFile),
			})
			Expect(err).ToNot(HaveOccurred())
			crioSharedSet, err := cpuset.Parse(strings.TrimSpace(testutils.ToString(crioSharedOut)))
			Expect(err).ToNot(HaveOccurred())
			Expect(crioSharedSet.Equals(sharedSet)).To(BeTrue(),
				"CRI-O shared_cpuset should be %s, got %s", sharedSet.String(), crioSharedSet.String())
			Expect(crioSharedSet.Intersection(ovsDpdkSet).IsEmpty()).To(BeTrue(),
				"CRI-O shared_cpuset must not contain ovsDpdk CPUs %s", ovsDpdkSet.String())

			By("Verifying CRI-O infra_ctr_cpuset is reserved")
			crioInfraOut, err := nodes.ExecCommand(ctx, node, []string{
				"bash", "-c",
				fmt.Sprintf(`awk -F '"' '/infra_ctr_cpuset.*/ { print $2 }' %s`, crioRuntimesConfigFile),
			})
			Expect(err).ToNot(HaveOccurred())
			crioInfraSet, err := cpuset.Parse(strings.TrimSpace(testutils.ToString(crioInfraOut)))
			Expect(err).ToNot(HaveOccurred())
			Expect(crioInfraSet.Equals(reservedSet)).To(BeTrue(),
				"CRI-O infra_ctr_cpuset should be %s, got %s", reservedSet.String(), crioInfraSet.String())
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
	bannedBeforePod, err := getNodeIRQBannedCPUSet(ctx, node)
	Expect(err).ToNot(HaveOccurred())
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
		if err := pods.DeleteAndSync(cleanupCtx, testclient.DataPlaneClient, testpod); err != nil {
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
	bannedWithPod, err := getNodeIRQBannedCPUSet(ctx, node)
	Expect(err).ToNot(HaveOccurred())
	Expect(ovsDpdkSet.IsSubsetOf(bannedWithPod)).To(BeTrue(),
		"IRQBALANCE_BANNED_CPUS should include ovsDpdk CPUs %s with pod running, got %s",
		ovsDpdkSet.String(), bannedWithPod.String())

	By("Deleting the GU pod")
	Expect(pods.DeleteAndSync(ctx, testclient.DataPlaneClient, testpod)).To(Succeed())

	By("Verifying IRQBALANCE_BANNED_CPUS still includes ovsDpdk CPUs after pod deletion")
	Eventually(func() bool {
		bannedAfterPod, err := getNodeIRQBannedCPUSet(ctx, node)
		if err != nil {
			return false
		}
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

func getReservedSystemCPUs(ctx context.Context, node *corev1.Node) (cpuset.CPUSet, error) {
	kubeletConfig, err := nodes.GetKubeletConfig(ctx, node)
	if err != nil {
		return cpuset.New(), err
	}
	return cpuset.Parse(kubeletConfig.ReservedSystemCPUs)
}

func getNodeIRQBannedCPUSet(ctx context.Context, node *corev1.Node) (cpuset.CPUSet, error) {
	irqConf, err := nodes.ExecCommand(ctx, node, []string{"cat", "/rootfs/etc/sysconfig/irqbalance"})
	if err != nil {
		return cpuset.New(), err
	}
	return getIRQBannedCPUSet(testutils.ToString(irqConf)), nil
}

func verifyOvsDpdkKernelCmdline(ctx context.Context, node *corev1.Node, isolated, ovsDpdk cpuset.CPUSet) {
	GinkgoHelper()
	expectedIsolatedPlusOvsDpdk := isolated.Union(ovsDpdk)

	By("Reading kernel cmdline")
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
	Expect(ovsDpdk.IsSubsetOf(nohzSet)).To(BeTrue(),
		"nohz_full=%s should include ovsDpdk CPUs %s",
		nohzSet.String(), ovsDpdk.String())

	By("Verifying rcu_nocbs includes ovsDpdk CPUs")
	rcuSet := parseCPUSetFromKernelParam(cmdlineStr, "rcu_nocbs")
	Expect(rcuSet.IsEmpty()).To(BeFalse(), "rcu_nocbs param not found in cmdline")
	Expect(ovsDpdk.IsSubsetOf(rcuSet)).To(BeTrue(),
		"rcu_nocbs=%s should include ovsDpdk CPUs %s",
		rcuSet.String(), ovsDpdk.String())

	By("Verifying systemd.cpu_affinity excludes ovsDpdk CPUs")
	affinitySet := parseCPUSetFromKernelParam(cmdlineStr, "systemd.cpu_affinity")
	Expect(affinitySet.IsEmpty()).To(BeFalse(), "systemd.cpu_affinity param not found in cmdline")
	Expect(affinitySet.Intersection(ovsDpdk).IsEmpty()).To(BeTrue(),
		"systemd.cpu_affinity=%s should not contain ovsDpdk CPUs %s",
		affinitySet.String(), ovsDpdk.String())
}

func verifyOvsDpdkIRQIsolation(ctx context.Context, node *corev1.Node, ovsDpdk cpuset.CPUSet) {
	GinkgoHelper()

	By("Verifying IRQBALANCE_BANNED_CPUS includes ovsDpdk CPUs")
	bannedSet, err := getNodeIRQBannedCPUSet(ctx, node)
	Expect(err).ToNot(HaveOccurred())
	Expect(ovsDpdk.IsSubsetOf(bannedSet)).To(BeTrue(),
		"IRQBALANCE_BANNED_CPUS should include ovsDpdk CPUs %s, got %s",
		ovsDpdk.String(), bannedSet.String())

	By("Verifying default_smp_affinity excludes ovsDpdk CPUs")
	smpCPUSet, err := nodes.GetDefaultSmpAffinitySet(ctx, node)
	Expect(err).ToNot(HaveOccurred())
	testlog.Infof("default_smp_affinity on %s: %s", node.Name, smpCPUSet.String())
	Expect(smpCPUSet.Intersection(ovsDpdk).IsEmpty()).To(BeTrue(),
		"default_smp_affinity should not have ovsDpdk CPU bits set, got CPUs %s",
		smpCPUSet.Intersection(ovsDpdk).String())
}

func verifyOvsDpdkSlice(ctx context.Context, node *corev1.Node, ovsDpdk cpuset.CPUSet, partition string) {
	GinkgoHelper()

	By("Verifying ovsdpdk.slice cpuset.cpus.exclusive matches configured ovsDpdk CPUs")
	cgroupCpuSet, err := getOvsDpdkSliceExclusiveCPUs(ctx, node)
	Expect(err).ToNot(HaveOccurred())
	Expect(cgroupCpuSet.Equals(ovsDpdk)).To(BeTrue(),
		"ovsdpdk.slice cpuset.cpus.exclusive should be %s, got %s",
		ovsDpdk.String(), cgroupCpuSet.String())

	By("Verifying ovsdpdk.slice cpuset.cpus.partition")
	cgroupPartition, err := getOvsDpdkSlicePartition(ctx, node)
	Expect(err).ToNot(HaveOccurred(), "failed to read ovsdpdk.slice cpuset.cpus.partition")
	Expect(cgroupPartition).To(Equal(partition),
		"ovsdpdk.slice cpuset.cpus.partition should be %q, got %q", partition, cgroupPartition)
}

func verifyOvsDpdkServiceEnv(ctx context.Context, node *corev1.Node, ovsDpdk cpuset.CPUSet) {
	GinkgoHelper()
	expectedOvsDpdkEnv := "OVS_DPDK_CPUS=" + ovsDpdk.String()

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
}
