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
	"k8s.io/utils/ptr"
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

var _ = Describe("[performance] Dedicated CPUs for DPDK", Ordered, Label(string(label.DedicatedCPUs), string(label.Slow), string(label.Tier2)), func() {
	var (
		workerRTNodes  []corev1.Node
		profile        *performancev2.PerformanceProfile
		initialProfile *performancev2.PerformanceProfile

		reservedSet    cpuset.CPUSet
		dedicatedSet   cpuset.CPUSet
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
			"need at least 2 isolated CPUs to split into isolated+dedicated")

		dedicatedSet = cpuset.New(isolatedList[0])
		newIsolatedSet = cpuset.New(isolatedList[1:]...)

		dedicatedCPUs := performancev2.CPUSet(dedicatedSet.String())
		newIsolated := performancev2.CPUSet(newIsolatedSet.String())

		testlog.Infof("Reserved: %s, Isolated: %s, Dedicated: %s",
			reservedSet.String(), newIsolatedSet.String(), dedicatedSet.String())

		ctx := context.TODO()
		isWPEnabled, err := cluster.IsWorkloadPartitioningEnabled(ctx)
		Expect(err).ToNot(HaveOccurred())

		By("Updating the profile with dedicated CPUs and disableOvsDynamicPinning")
		currentProfile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		currentProfile.Spec.CPU.Isolated = &newIsolated
		currentProfile.Spec.CPU.Dedicated = &dedicatedCPUs
		if currentProfile.Spec.Net == nil {
			currentProfile.Spec.Net = &performancev2.Net{}
		}
		currentProfile.Spec.Net.DisableOvsDynamicPinning = ptr.To(true)

		policyOptions := map[string]string{}
		if !isWPEnabled {
			testlog.Infof("Workload partitioning not enabled, adding strict-cpu-reservation via experimental annotation")
			policyOptions["strict-cpu-reservation"] = "true"
			if currentProfile.Annotations == nil {
				currentProfile.Annotations = make(map[string]string)
			}
			optJSON, err := json.Marshal(map[string]interface{}{"cpuManagerPolicyOptions": policyOptions})
			Expect(err).ToNot(HaveOccurred())
			currentProfile.Annotations["kubeletconfig.experimental"] = string(optJSON)
		}

		profiles.UpdateWithRetry(currentProfile)

		updatedProfile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		testlog.Infof("Updated profile: reserved=%s isolated=%s dedicated=%s annotations=%v",
			*updatedProfile.Spec.CPU.Reserved, *updatedProfile.Spec.CPU.Isolated,
			*updatedProfile.Spec.CPU.Dedicated, updatedProfile.Annotations)

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

		initialProfile.ResourceVersion = currentProfile.ResourceVersion
		profiles.UpdateWithRetry(initialProfile)

		profilesupdate.WaitForTuningUpdating(ctx, initialProfile)
		profilesupdate.WaitForTuningUpdated(ctx, initialProfile)
	})

	Context("when dedicated CPUs and disableOvsDynamicPinning are set", func() {
		It("should apply dedicated CPU node configuration", func() {
			ctx := context.TODO()
			expectedIsolatedPlusDedicated := newIsolatedSet.Union(dedicatedSet)
			expectedReservedSystem := reservedSet.Union(dedicatedSet)

			for i := range workerRTNodes {
				node := &workerRTNodes[i]
				testlog.Infof("Verifying node %s", node.Name)

				By(fmt.Sprintf("Verifying kernel cmdline on node %s", node.Name))
				cmdline, err := nodes.ExecCommand(ctx, node, []string{"cat", "/proc/cmdline"})
				Expect(err).ToNot(HaveOccurred())
				cmdlineStr := testutils.ToString(cmdline)

				By(fmt.Sprintf("Verifying isolcpus includes dedicated CPUs on node %s", node.Name))
				for _, cpu := range expectedIsolatedPlusDedicated.List() {
					Expect(cmdlineStr).To(
						MatchRegexp(`isolcpus=\S*%d`, cpu),
						fmt.Sprintf("isolcpus should include CPU %d (isolated+dedicated)", cpu))
				}

				By(fmt.Sprintf("Verifying nohz_full includes dedicated CPUs on node %s", node.Name))
				for _, cpu := range dedicatedSet.List() {
					Expect(cmdlineStr).To(
						MatchRegexp(`nohz_full=\S*%d`, cpu),
						fmt.Sprintf("nohz_full should include dedicated CPU %d", cpu))
				}

				By(fmt.Sprintf("Verifying rcu_nocbs includes dedicated CPUs on node %s", node.Name))
				for _, cpu := range dedicatedSet.List() {
					Expect(cmdlineStr).To(
						MatchRegexp(`rcu_nocbs=\S*%d`, cpu),
						fmt.Sprintf("rcu_nocbs should include dedicated CPU %d", cpu))
				}

				By(fmt.Sprintf("Verifying systemd.cpu_affinity excludes dedicated CPUs on node %s", node.Name))
				Expect(cmdlineStr).To(ContainSubstring("systemd.cpu_affinity="))
				for _, cpu := range dedicatedSet.List() {
					cpuAffinityRegex := fmt.Sprintf(`systemd\.cpu_affinity=\S*\b%d\b`, cpu)
					Expect(cmdlineStr).ToNot(MatchRegexp(cpuAffinityRegex),
						fmt.Sprintf("systemd.cpu_affinity should not contain dedicated CPU %d", cpu))
				}

				By(fmt.Sprintf("Verifying kubelet reservedSystemCPUs is union of reserved+dedicated on node %s", node.Name))
				kubeletConfig, err := nodes.GetKubeletConfig(ctx, node)
				Expect(err).ToNot(HaveOccurred())
				reservedSystemCPUs, err := cpuset.Parse(kubeletConfig.ReservedSystemCPUs)
				Expect(err).ToNot(HaveOccurred())
				Expect(reservedSystemCPUs.Equals(expectedReservedSystem)).To(BeTrue(),
					fmt.Sprintf("ReservedSystemCPUs should be %s (reserved+dedicated), got %s",
						expectedReservedSystem.String(), reservedSystemCPUs.String()))

				By(fmt.Sprintf("Verifying IRQBALANCE_BANNED_CPUS on node %s", node.Name))
				irqConf, err := nodes.ExecCommand(ctx, node, []string{"cat", "/rootfs/etc/sysconfig/irqbalance"})
				Expect(err).ToNot(HaveOccurred())
				irqConfStr := testutils.ToString(irqConf)

				bannedSet := parseIRQBannedCPUSet(irqConfStr)
				Expect(dedicatedSet.IsSubsetOf(bannedSet)).To(BeTrue(),
					fmt.Sprintf("IRQBALANCE_BANNED_CPUS should include dedicated CPUs %s, got %s",
						dedicatedSet.String(), bannedSet.String()))

				By(fmt.Sprintf("Verifying default_smp_affinity excludes dedicated CPUs on node %s", node.Name))
				smpAffinity, err := nodes.ExecCommand(ctx, node, []string{"cat", "/proc/irq/default_smp_affinity"})
				Expect(err).ToNot(HaveOccurred())
				smpAffinityStr := strings.TrimSpace(testutils.ToString(smpAffinity))
				testlog.Infof("default_smp_affinity on %s: %s", node.Name, smpAffinityStr)
				smpCPUSet := parseHexMaskToCPUSet(smpAffinityStr)
				Expect(smpCPUSet.Intersection(dedicatedSet).IsEmpty()).To(BeTrue(),
					fmt.Sprintf("default_smp_affinity should not have dedicated CPU bits set, got CPUs %s",
						smpCPUSet.Intersection(dedicatedSet).String()))

				By(fmt.Sprintf("Verifying OVS dynamic pinning trigger file is absent on node %s", node.Name))
				_, err = nodes.ExecCommand(ctx, node, []string{
					"ls", "/rootfs/var/lib/ovn-ic/etc/enable_dynamic_cpu_affinity",
				})
				Expect(err).To(HaveOccurred(),
					fmt.Sprintf("OVS dynamic pinning trigger file should not exist on node %s when disableOvsDynamicPinning=true", node.Name))

				By(fmt.Sprintf("Verifying dedicatedcpus.slice exists on node %s", node.Name))
				_, err = nodes.ExecCommand(ctx, node, []string{
					"cat", "/rootfs/etc/systemd/system/dedicatedcpus.slice",
				})
				Expect(err).ToNot(HaveOccurred(),
					"dedicatedcpus.slice should be present on the node")

				By(fmt.Sprintf("Verifying dedicated-cpus-configure script exists on node %s", node.Name))
				_, err = nodes.ExecCommand(ctx, node, []string{
					"cat", "/rootfs/usr/local/bin/dedicated-cpus-configure.sh",
				})
				Expect(err).ToNot(HaveOccurred(),
					"dedicated-cpus-configure.sh should be present on the node")

				By(fmt.Sprintf("Verifying dedicatedcpus.slice cpuset partition is isolated on node %s", node.Name))
				cgroupPartition, err := nodes.ExecCommand(ctx, node, []string{
					"cat", "/rootfs/sys/fs/cgroup/dedicatedcpus.slice/cpuset.cpus.partition",
				})
				Expect(err).ToNot(HaveOccurred(),
					"failed to read dedicatedcpus.slice cpuset.cpus.partition")
				partitionStr := strings.TrimSpace(testutils.ToString(cgroupPartition))
				Expect(partitionStr).To(Equal("isolated"),
					"dedicatedcpus.slice cpuset.cpus.partition should be 'isolated'")
			}
		})

		It("should preserve dedicated CPU IRQ banning across GU pod lifecycle", func() {
			ctx := context.TODO()
			node := &workerRTNodes[0]
			testlog.Infof("Testing CRI-O IRQ interaction on node %s", node.Name)

			By("Verifying default_smp_affinity has dedicated CPU bits cleared before pod creation")
			smpAffinity, err := nodes.ExecCommand(ctx, node, []string{"cat", "/proc/irq/default_smp_affinity"})
			Expect(err).ToNot(HaveOccurred())
			smpBeforePod := strings.TrimSpace(testutils.ToString(smpAffinity))
			testlog.Infof("default_smp_affinity before pod: %s", smpBeforePod)
			smpBeforeSet := parseHexMaskToCPUSet(smpBeforePod)
			Expect(smpBeforeSet.Intersection(dedicatedSet).IsEmpty()).To(BeTrue(),
				fmt.Sprintf("default_smp_affinity should not have dedicated CPU bits set before pod, got CPUs %s",
					smpBeforeSet.Intersection(dedicatedSet).String()))

			By("Verifying IRQBALANCE_BANNED_CPUS is set to dedicated hex mask before pod creation")
			irqConf, err := nodes.ExecCommand(ctx, node, []string{"cat", "/rootfs/etc/sysconfig/irqbalance"})
			Expect(err).ToNot(HaveOccurred())
			bannedBeforePod := parseIRQBannedCPUSet(testutils.ToString(irqConf))
			Expect(dedicatedSet.IsSubsetOf(bannedBeforePod)).To(BeTrue(),
				fmt.Sprintf("IRQBALANCE_BANNED_CPUS should include dedicated CPUs %s before pod, got %s",
					dedicatedSet.String(), bannedBeforePod.String()))

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

			By("Verifying IRQBALANCE_BANNED_CPUS still includes dedicated CPUs with pod running")
			irqConf, err = nodes.ExecCommand(ctx, node, []string{"cat", "/rootfs/etc/sysconfig/irqbalance"})
			Expect(err).ToNot(HaveOccurred())
			bannedWithPod := parseIRQBannedCPUSet(testutils.ToString(irqConf))
			Expect(dedicatedSet.IsSubsetOf(bannedWithPod)).To(BeTrue(),
				fmt.Sprintf("IRQBALANCE_BANNED_CPUS should include dedicated CPUs %s with pod running, got %s",
					dedicatedSet.String(), bannedWithPod.String()))

			By("Deleting the GU pod")
			err = testclient.DataPlaneClient.Delete(ctx, testpod)
			Expect(err).ToNot(HaveOccurred())
			err = pods.WaitForDeletion(ctx, testclient.DataPlaneClient, testpod, 5*time.Minute)
			Expect(err).ToNot(HaveOccurred())

			By("Verifying IRQBALANCE_BANNED_CPUS still includes dedicated CPUs after pod deletion")
			Eventually(func() bool {
				irqConf, err := nodes.ExecCommand(ctx, node, []string{"cat", "/rootfs/etc/sysconfig/irqbalance"})
				if err != nil {
					return false
				}
				bannedAfterPod := parseIRQBannedCPUSet(testutils.ToString(irqConf))
				return dedicatedSet.IsSubsetOf(bannedAfterPod)
			}, 2*time.Minute, 10*time.Second).Should(BeTrue(),
				"IRQBALANCE_BANNED_CPUS should still include dedicated CPUs after pod deletion")

			By("Verifying default_smp_affinity still has dedicated CPU bits cleared after pod deletion")
			smpAffinity, err = nodes.ExecCommand(ctx, node, []string{"cat", "/proc/irq/default_smp_affinity"})
			Expect(err).ToNot(HaveOccurred())
			smpAfterPod := strings.TrimSpace(testutils.ToString(smpAffinity))
			testlog.Infof("default_smp_affinity after pod deletion: %s", smpAfterPod)
			smpAfterSet := parseHexMaskToCPUSet(smpAfterPod)
			Expect(smpAfterSet.Intersection(dedicatedSet).IsEmpty()).To(BeTrue(),
				fmt.Sprintf("default_smp_affinity should keep dedicated CPU bits cleared after pod deletion, got CPUs %s",
					smpAfterSet.Intersection(dedicatedSet).String()))
		})
	})
})

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
