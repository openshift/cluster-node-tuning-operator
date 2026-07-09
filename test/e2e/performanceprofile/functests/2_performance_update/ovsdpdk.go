//go:build !unittests

package __performance_update

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
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

var _ = Describe("[performance] OVS-DPDK CPUs", Ordered, Label(string(label.OvsDpdk), string(label.Slow), string(label.Tier2)), func() {
	var (
		workerRTNodes  []corev1.Node
		profile        *performancev2.PerformanceProfile
		initialProfile *performancev2.PerformanceProfile

		reservedSet    cpuset.CPUSet
		ovsDpdkSet     cpuset.CPUSet
		newIsolatedSet cpuset.CPUSet
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
			"need at least 2 isolated CPUs to split into isolated+ovsDpdk")

		ovsDpdkSet = cpuset.New(isolatedList[0])
		newIsolatedSet = cpuset.New(isolatedList[1:]...)

		ovsDpdkCPUs := performancev2.CPUSet(ovsDpdkSet.String())
		newIsolated := performancev2.CPUSet(newIsolatedSet.String())

		testlog.Infof("Reserved: %s, Isolated: %s, OvsDpdk: %s",
			reservedSet.String(), newIsolatedSet.String(), ovsDpdkSet.String())

		ctx := context.TODO()
		isWPEnabled, err := cluster.IsWorkloadPartitioningEnabled(ctx, testclient.Client)
		Expect(err).ToNot(HaveOccurred())

		By("Updating the profile with OVS-DPDK CPUs")
		currentProfile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		currentProfile.Spec.CPU.Isolated = &newIsolated
		currentProfile.Spec.CPU.OvsDpdk = &ovsDpdkCPUs
		if currentProfile.Annotations == nil {
			currentProfile.Annotations = make(map[string]string)
		}
		currentProfile.Annotations[performancev2.PerformanceProfileDisableLoadBalancingForOvsDpdkAnnotation] = "true"

		policyOptions := map[string]string{}
		if !isWPEnabled {
			testlog.Infof("Workload partitioning not enabled, adding strict-cpu-reservation via experimental annotation")
			policyOptions["strict-cpu-reservation"] = "true"
			optJSON, err := json.Marshal(map[string]interface{}{"cpuManagerPolicyOptions": policyOptions})
			Expect(err).ToNot(HaveOccurred())
			currentProfile.Annotations["kubeletconfig.experimental"] = string(optJSON)
		}

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
		delete(currentProfile.Annotations, performancev2.PerformanceProfileDisableLoadBalancingForOvsDpdkAnnotation)
		profiles.UpdateWithRetry(currentProfile)

		profilesupdate.WaitForTuningUpdating(ctx, currentProfile)
		profilesupdate.WaitForTuningUpdated(ctx, currentProfile)
	})

	Context("when OVS-DPDK CPUs and disable-load-balancing-ovs-dpdk annotation are set", func() {
		It("should apply OVS-DPDK CPU node configuration", func() {
			ctx := context.TODO()
			expectedIsolatedPlusOvsDpdk := newIsolatedSet.Union(ovsDpdkSet)
			expectedReservedSystem := reservedSet.Union(ovsDpdkSet)

			for i := range workerRTNodes {
				node := &workerRTNodes[i]
				testlog.Infof("Verifying node %s", node.Name)

				By(fmt.Sprintf("Verifying kernel cmdline on node %s", node.Name))
				cmdline, err := nodes.ExecCommand(ctx, node, []string{"cat", "/proc/cmdline"})
				Expect(err).ToNot(HaveOccurred())
				cmdlineStr := testutils.ToString(cmdline)

				By(fmt.Sprintf("Verifying isolcpus includes OVS-DPDK CPUs on node %s", node.Name))
				isolcpusSet := parseCPUSetFromKernelParam(cmdlineStr, "isolcpus")
				Expect(isolcpusSet.IsEmpty()).To(BeFalse(), "isolcpus param not found in cmdline")
				Expect(expectedIsolatedPlusOvsDpdk.IsSubsetOf(isolcpusSet)).To(BeTrue(),
					fmt.Sprintf("isolcpus=%s should include all isolated+ovsDpdk CPUs %s",
						isolcpusSet.String(), expectedIsolatedPlusOvsDpdk.String()))

				By(fmt.Sprintf("Verifying nohz_full includes OVS-DPDK CPUs on node %s", node.Name))
				nohzSet := parseCPUSetFromKernelParam(cmdlineStr, "nohz_full")
				Expect(nohzSet.IsEmpty()).To(BeFalse(), "nohz_full param not found in cmdline")
				Expect(ovsDpdkSet.IsSubsetOf(nohzSet)).To(BeTrue(),
					fmt.Sprintf("nohz_full=%s should include OVS-DPDK CPUs %s",
						nohzSet.String(), ovsDpdkSet.String()))

				By(fmt.Sprintf("Verifying rcu_nocbs includes OVS-DPDK CPUs on node %s", node.Name))
				rcuSet := parseCPUSetFromKernelParam(cmdlineStr, "rcu_nocbs")
				Expect(rcuSet.IsEmpty()).To(BeFalse(), "rcu_nocbs param not found in cmdline")
				Expect(ovsDpdkSet.IsSubsetOf(rcuSet)).To(BeTrue(),
					fmt.Sprintf("rcu_nocbs=%s should include OVS-DPDK CPUs %s",
						rcuSet.String(), ovsDpdkSet.String()))

				By(fmt.Sprintf("Verifying systemd.cpu_affinity excludes OVS-DPDK CPUs on node %s", node.Name))
				affinitySet := parseCPUSetFromKernelParam(cmdlineStr, "systemd.cpu_affinity")
				Expect(affinitySet.IsEmpty()).To(BeFalse(), "systemd.cpu_affinity param not found in cmdline")
				Expect(affinitySet.Intersection(ovsDpdkSet).IsEmpty()).To(BeTrue(),
					fmt.Sprintf("systemd.cpu_affinity=%s should not contain OVS-DPDK CPUs %s",
						affinitySet.String(), ovsDpdkSet.String()))

				By(fmt.Sprintf("Verifying kubelet reservedSystemCPUs is union of reserved+ovsDpdk on node %s", node.Name))
				kubeletConfig, err := nodes.GetKubeletConfig(ctx, node)
				Expect(err).ToNot(HaveOccurred())
				reservedSystemCPUs, err := cpuset.Parse(kubeletConfig.ReservedSystemCPUs)
				Expect(err).ToNot(HaveOccurred())
				Expect(reservedSystemCPUs.Equals(expectedReservedSystem)).To(BeTrue(),
					fmt.Sprintf("ReservedSystemCPUs should be %s (reserved+ovsDpdk), got %s",
						expectedReservedSystem.String(), reservedSystemCPUs.String()))

				By(fmt.Sprintf("Verifying IRQBALANCE_BANNED_CPUS on node %s", node.Name))
				irqConf, err := nodes.ExecCommand(ctx, node, []string{"cat", "/rootfs/etc/sysconfig/irqbalance"})
				Expect(err).ToNot(HaveOccurred())
				irqConfStr := testutils.ToString(irqConf)

				bannedSet := parseIRQBannedCPUSet(irqConfStr)
				Expect(ovsDpdkSet.IsSubsetOf(bannedSet)).To(BeTrue(),
					fmt.Sprintf("IRQBALANCE_BANNED_CPUS should include OVS-DPDK CPUs %s, got %s",
						ovsDpdkSet.String(), bannedSet.String()))

				By(fmt.Sprintf("Verifying default_smp_affinity excludes OVS-DPDK CPUs on node %s", node.Name))
				smpAffinity, err := nodes.ExecCommand(ctx, node, []string{"cat", "/proc/irq/default_smp_affinity"})
				Expect(err).ToNot(HaveOccurred())
				smpAffinityStr := strings.TrimSpace(testutils.ToString(smpAffinity))
				testlog.Infof("default_smp_affinity on %s: %s", node.Name, smpAffinityStr)
				smpCPUSet := parseHexMaskToCPUSet(smpAffinityStr)
				Expect(smpCPUSet.Intersection(ovsDpdkSet).IsEmpty()).To(BeTrue(),
					fmt.Sprintf("default_smp_affinity should not have OVS-DPDK CPU bits set, got CPUs %s",
						smpCPUSet.Intersection(ovsDpdkSet).String()))

				By(fmt.Sprintf("Verifying OVS dynamic pinning trigger file is absent on node %s", node.Name))
				_, err = nodes.ExecCommand(ctx, node, []string{
					"ls", "/rootfs/var/lib/ovn-ic/etc/enable_dynamic_cpu_affinity",
				})
				Expect(err).To(HaveOccurred(),
					fmt.Sprintf("OVS dynamic pinning trigger file should not exist on node %s when disable-load-balancing-ovs-dpdk annotation is set", node.Name))

				By(fmt.Sprintf("Verifying ovs-dpdk-cpus-configure script exists on node %s", node.Name))
				_, err = nodes.ExecCommand(ctx, node, []string{
					"test", "-f", "/rootfs/usr/local/bin/ovs-dpdk-cpus-configure.sh",
				})
				Expect(err).ToNot(HaveOccurred(),
					"ovs-dpdk-cpus-configure.sh should be present on the node")

				cgroupBase := "/rootfs/sys/fs/cgroup/ovs.slice"

				By(fmt.Sprintf("Verifying ovsdpdk.slice cgroup hierarchy exists on node %s", node.Name))
				_, err = nodes.ExecCommand(ctx, node, []string{
					"test", "-d", cgroupBase + "/ovs-vswitchd.service/ovsdpdk.slice",
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

				By(fmt.Sprintf("Verifying ovsdpdk.slice cpuset.cpus matches configured OVS-DPDK CPUs on node %s", node.Name))
				cgroupCpus, err := nodes.ExecCommand(ctx, node, []string{
					"cat", cgroupBase + "/ovs-vswitchd.service/ovsdpdk.slice/cpuset.cpus",
				})
				Expect(err).ToNot(HaveOccurred())
				cgroupCpuSet, err := cpuset.Parse(strings.TrimSpace(testutils.ToString(cgroupCpus)))
				Expect(err).ToNot(HaveOccurred())
				Expect(cgroupCpuSet.Equals(ovsDpdkSet)).To(BeTrue(),
					fmt.Sprintf("ovsdpdk.slice cpuset.cpus should be %s, got %s",
						ovsDpdkSet.String(), cgroupCpuSet.String()))

				By(fmt.Sprintf("Verifying ovsdpdk.slice cpuset.cpus.partition is isolated on node %s", node.Name))
				cgroupPartition, err := nodes.ExecCommand(ctx, node, []string{
					"cat", cgroupBase + "/ovs-vswitchd.service/ovsdpdk.slice/cpuset.cpus.partition",
				})
				Expect(err).ToNot(HaveOccurred(),
					"failed to read ovsdpdk.slice cpuset.cpus.partition")
				Expect(strings.TrimSpace(testutils.ToString(cgroupPartition))).To(Equal("isolated"),
					"ovsdpdk.slice cpuset.cpus.partition should be 'isolated'")
			}
		})

		It("should preserve OVS-DPDK CPU IRQ banning across GU pod lifecycle", func() {
			ctx := context.TODO()
			node := &workerRTNodes[0]
			testlog.Infof("Testing CRI-O IRQ interaction on node %s", node.Name)

			By("Verifying default_smp_affinity has OVS-DPDK CPU bits cleared before pod creation")
			smpAffinity, err := nodes.ExecCommand(ctx, node, []string{"cat", "/proc/irq/default_smp_affinity"})
			Expect(err).ToNot(HaveOccurred())
			smpBeforePod := strings.TrimSpace(testutils.ToString(smpAffinity))
			testlog.Infof("default_smp_affinity before pod: %s", smpBeforePod)
			smpBeforeSet := parseHexMaskToCPUSet(smpBeforePod)
			Expect(smpBeforeSet.Intersection(ovsDpdkSet).IsEmpty()).To(BeTrue(),
				fmt.Sprintf("default_smp_affinity should not have OVS-DPDK CPU bits set before pod, got CPUs %s",
					smpBeforeSet.Intersection(ovsDpdkSet).String()))

			By("Verifying IRQBALANCE_BANNED_CPUS is set to OVS-DPDK hex mask before pod creation")
			irqConf, err := nodes.ExecCommand(ctx, node, []string{"cat", "/rootfs/etc/sysconfig/irqbalance"})
			Expect(err).ToNot(HaveOccurred())
			bannedBeforePod := parseIRQBannedCPUSet(testutils.ToString(irqConf))
			Expect(ovsDpdkSet.IsSubsetOf(bannedBeforePod)).To(BeTrue(),
				fmt.Sprintf("IRQBALANCE_BANNED_CPUS should include OVS-DPDK CPUs %s before pod, got %s",
					ovsDpdkSet.String(), bannedBeforePod.String()))

			By("Creating a Guaranteed pod with irq-load-balancing=disable")
			testpod := pods.GetTestPod()
			testpod.Namespace = testutils.NamespaceTesting
			testpod.Annotations = map[string]string{
				"irq-load-balancing.crio.io": "disable",
			}
			testpod.Spec.Containers[0].Resources = corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			}
			runtimeClassName := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
			testpod.Spec.RuntimeClassName = &runtimeClassName
			testpod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: node.Name}

			err = testclient.DataPlaneClient.Create(ctx, testpod)
			Expect(err).ToNot(HaveOccurred())

			podKey := client.ObjectKeyFromObject(testpod)
			testpod, err = pods.WaitForCondition(ctx, podKey, corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
			pods.DumpStateOnFailure(ctx, testclient.K8sClient, testpod, err)
			Expect(err).ToNot(HaveOccurred())
			Expect(testpod.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed))
			testlog.Infof("GU pod %s is running on node %s", testpod.Name, node.Name)

			By("Verifying IRQBALANCE_BANNED_CPUS still includes OVS-DPDK CPUs with pod running")
			irqConf, err = nodes.ExecCommand(ctx, node, []string{"cat", "/rootfs/etc/sysconfig/irqbalance"})
			Expect(err).ToNot(HaveOccurred())
			bannedWithPod := parseIRQBannedCPUSet(testutils.ToString(irqConf))
			Expect(ovsDpdkSet.IsSubsetOf(bannedWithPod)).To(BeTrue(),
				fmt.Sprintf("IRQBALANCE_BANNED_CPUS should include OVS-DPDK CPUs %s with pod running, got %s",
					ovsDpdkSet.String(), bannedWithPod.String()))

			By("Deleting the GU pod")
			err = testclient.DataPlaneClient.Delete(ctx, testpod)
			Expect(err).ToNot(HaveOccurred())
			err = pods.WaitForDeletion(ctx, testclient.DataPlaneClient, testpod, 5*time.Minute)
			Expect(err).ToNot(HaveOccurred())

			By("Verifying IRQBALANCE_BANNED_CPUS still includes OVS-DPDK CPUs after pod deletion")
			Eventually(func() bool {
				irqConf, err := nodes.ExecCommand(ctx, node, []string{"cat", "/rootfs/etc/sysconfig/irqbalance"})
				if err != nil {
					return false
				}
				bannedAfterPod := parseIRQBannedCPUSet(testutils.ToString(irqConf))
				return ovsDpdkSet.IsSubsetOf(bannedAfterPod)
			}, 2*time.Minute, 10*time.Second).Should(BeTrue(),
				"IRQBALANCE_BANNED_CPUS should still include OVS-DPDK CPUs after pod deletion")

			By("Verifying default_smp_affinity still has OVS-DPDK CPU bits cleared after pod deletion")
			smpAffinity, err = nodes.ExecCommand(ctx, node, []string{"cat", "/proc/irq/default_smp_affinity"})
			Expect(err).ToNot(HaveOccurred())
			smpAfterPod := strings.TrimSpace(testutils.ToString(smpAffinity))
			testlog.Infof("default_smp_affinity after pod deletion: %s", smpAfterPod)
			smpAfterSet := parseHexMaskToCPUSet(smpAfterPod)
			Expect(smpAfterSet.Intersection(ovsDpdkSet).IsEmpty()).To(BeTrue(),
				fmt.Sprintf("default_smp_affinity should keep OVS-DPDK CPU bits cleared after pod deletion, got CPUs %s",
					smpAfterSet.Intersection(ovsDpdkSet).String()))
		})
	})
})

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

// parseHexMaskToCPUSet converts a comma-separated hex bitmask (e.g.
// "ff,fffffffe" or "00000040") into a cpuset.CPUSet by setting each
// CPU whose bit is 1 in the mask.
func parseHexMaskToCPUSet(mask string) cpuset.CPUSet {
	cleaned := strings.ReplaceAll(mask, ",", "")
	n := new(big.Int)
	if _, ok := n.SetString(cleaned, 16); !ok {
		return cpuset.New()
	}
	var cpus []int
	for i := 0; i < n.BitLen(); i++ {
		if n.Bit(i) == 1 {
			cpus = append(cpus, i)
		}
	}
	return cpuset.New(cpus...)
}

// parseIRQBannedCPUSet extracts the IRQBALANCE_BANNED_CPUS value from the
// irqbalance config content and returns it as a cpuset.CPUSet.
func parseIRQBannedCPUSet(irqbalanceContent string) cpuset.CPUSet {
	for _, line := range strings.Split(irqbalanceContent, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "IRQBALANCE_BANNED_CPUS=") {
			continue
		}
		val := strings.TrimPrefix(line, "IRQBALANCE_BANNED_CPUS=")
		val = strings.Trim(val, `"`)
		return parseHexMaskToCPUSet(val)
	}
	return cpuset.New()
}
