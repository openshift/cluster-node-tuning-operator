package __performance_kubelet_node_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/hypershift"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/infrastructure"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/poolname"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profilesupdate"
)

var _ = Describe("[ref_id: 45487][performance]additional kubelet arguments", Ordered, Label(string(label.ExperimentalAnnotations)), func() {

	var (
		initialProfile, profile *performancev2.PerformanceProfile
		workerRTNodes           []corev1.Node
		poolName                string
		ctx                     context.Context = context.Background()
	)

	testutils.CustomBeforeAll(func() {
		var err error
		workerRTNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred())
		Expect(workerRTNodes).ToNot(BeEmpty(), "no RT worker nodes found")

		profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		poolName = poolname.GetByProfile(ctx, profile)
		initialProfile = profile.DeepCopy()

	})
	Context("Additional kubelet arguments", Label(string(label.Tier2)), func() {
		BeforeEach(func() {
			var err error
			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
		})

		It("[test_id:45488]Test performance profile annotation for changing multiple kubelet settings", func() {
			sysctls := "{\"allowedUnsafeSysctls\":[\"net.core.somaxconn\",\"kernel.msg*\"],\"systemReserved\":{\"memory\":\"300Mi\"},\"kubeReserved\":{\"memory\":\"768Mi\"},\"imageMinimumGCAge\":\"3m\"}"
			profile.Annotations = updateKubeletConfigOverrideAnnotations(profile.Annotations, sysctls)

			By("updating Performance profile")
			profiles.UpdateWithRetry(profile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(ctx, profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(ctx, profile)

			for _, node := range workerRTNodes {
				kubeletConfig, err := nodes.GetKubeletConfig(ctx, &node)
				Expect(err).ToNot(HaveOccurred())
				sysctlsValue := kubeletConfig.AllowedUnsafeSysctls
				Expect(sysctlsValue).Should(ContainElements("net.core.somaxconn", "kernel.msg*"))
				Expect(kubeletConfig.KubeReserved["memory"]).To(Equal("768Mi"))
				Expect(kubeletConfig.ImageMinimumGCAge.Seconds()).To(BeNumerically("==", 180))
			}

			autoSizingCmd := []string{"cat", "/rootfs/etc/openshift/kubelet.conf.d/20-auto-sizing.conf"}
			for _, node := range workerRTNodes {
				out, err := nodes.ExecCommand(ctx, &node, autoSizingCmd)
				Expect(err).ToNot(HaveOccurred())
				stdout := testutils.ToString(out)
				Expect(stdout).To(ContainSubstring("300Mi"))
			}
		})
		Context("When setting cpu manager related parameters", func() {
			It("[test_id:45493]Should not override performance-addon-operator values", func() {
				paoValues := "{\"cpuManagerPolicy\":\"none\",\"cpuManagerReconcilePeriod\":\"10s\"}"
				profile.Annotations = updateKubeletConfigOverrideAnnotations(profile.Annotations, paoValues)

				By("updating Performance profile")
				profiles.UpdateWithRetry(profile)

				By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
				profilesupdate.WaitForTuningUpdating(ctx, profile)

				By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
				profilesupdate.WaitForTuningUpdated(ctx, profile)

				for _, node := range workerRTNodes {
					kubeletConfig, err := nodes.GetKubeletConfig(ctx, &node)
					Expect(err).ToNot(HaveOccurred())
					Expect(kubeletConfig.CPUManagerPolicy).Should(Equal("static"))
					Expect(kubeletConfig.CPUManagerReconcilePeriod.Seconds()).To(BeNumerically("==", 5))
				}
			})
		})
		It("[test_id:45490]Test memory reservation changes", func() {
			// In this test case we check if after applying reserving memory for
			// systemReserved and KubeReserved, the allocatable is reduced and Allocatable
			// Verify that Allocatable = Node capacity - (kubereserved + systemReserved + EvictionMemory)
			reservedMemory := "{\"systemReserved\":{\"memory\":\"300Mi\"},\"kubeReserved\":{\"memory\":\"768Mi\"}}"
			profile.Annotations = updateKubeletConfigOverrideAnnotations(profile.Annotations, reservedMemory)

			By("updating Performance profile")
			profiles.UpdateWithRetry(profile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(ctx, profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(ctx, profile)

			var kubeletConfig machineconfigv1.KubeletConfig

			Eventually(func() error {
				By("Getting that new KubeletConfig")
				configKey := types.NamespacedName{
					Name:      components.GetComponentName(profile.Name, components.ComponentNamePrefix),
					Namespace: metav1.NamespaceNone,
				}
				err := testclient.ControlPlaneClient.Get(ctx, configKey, &kubeletConfig)
				if err != nil {
					klog.Warningf("Failed to get the KubeletConfig %q", configKey.Name)
				}
				return err
			}).WithPolling(5 * time.Second).WithTimeout(3 * time.Minute).Should(Succeed())

			kubeletConfigString := string(kubeletConfig.Spec.KubeletConfig.Raw)
			Expect(kubeletConfigString).To(ContainSubstring(`"kubeReserved":{"memory":"768Mi"}`))
			Expect(kubeletConfigString).To(ContainSubstring(`"systemReserved":{"memory":"300Mi"}`))

			// Re-fetch nodes to get current allocatable and capacity after
			// the tuning update, since workerRTNodes was populated before the
			// annotation was applied and its Status values are stale.
			updatedNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			updatedNodes, err = nodes.MatchingOptionalSelector(updatedNodes)
			Expect(err).ToNot(HaveOccurred())
			Expect(updatedNodes).ToNot(BeEmpty(), "no RT worker nodes found after update")

			for _, node := range updatedNodes {
				kubeletConfig, err := nodes.GetKubeletConfig(ctx, &node)
				Expect(err).ToNot(HaveOccurred())
				totalCapacity := node.Status.Capacity.Memory().MilliValue()
				evictionMemory := kubeletConfig.EvictionHard["memory.available"]
				kubeReserved := kubeletConfig.KubeReserved["memory"]
				evictionMemoryInt, err := strconv.ParseInt(strings.TrimSuffix(evictionMemory, "Mi"), 10, 64)
				Expect(err).ToNot(HaveOccurred())
				kubeReservedMemoryInt, err := strconv.ParseInt(strings.TrimSuffix(kubeReserved, "Mi"), 10, 64)
				Expect(err).ToNot(HaveOccurred())
				systemReservedResource := resource.NewQuantity(300*1024*1024, resource.BinarySI)
				kubeReservedMemoryResource := resource.NewQuantity(kubeReservedMemoryInt*1024*1024, resource.BinarySI)
				evictionMemoryResource := resource.NewQuantity(evictionMemoryInt*1024*1024, resource.BinarySI)
				totalKubeMemory := systemReservedResource.MilliValue() + kubeReservedMemoryResource.MilliValue() + evictionMemoryResource.MilliValue()

				// Pre-allocated hugepages are subtracted from allocatable memory by the
				// kubelet but are still included in node capacity. The standard formula
				// Allocatable = Capacity - kubeReserved - systemReserved - evictionHard
				// does not account for this, so we must subtract hugepages to match the
				// actual allocatable reported by the node.
				var totalHugepages int64
				for resourceName, quantity := range node.Status.Capacity {
					if strings.HasPrefix(string(resourceName), corev1.ResourceHugePagesPrefix) {
						totalHugepages += quantity.MilliValue()
					}
				}

				calculatedAllocatable := totalCapacity - totalKubeMemory - totalHugepages
				currentAllocatable := node.Status.Allocatable.Memory().MilliValue()
				Expect(calculatedAllocatable).To(Equal(currentAllocatable))
			}
		})

		It("[test_id:45495] Test setting PAO managed parameters", func() {
			isArm, err := infrastructure.IsARM(ctx, &workerRTNodes[0])
			Expect(err).ToNot(HaveOccurred())
			if isArm {
				Skip("Changing topologyManagerPolicy is not supported on ARM architecture")
			}

			var paoParameters string
			if *profile.Spec.NUMA.TopologyPolicy == "single-numa-node" {
				paoParameters = "{\"topologyManagerPolicy\":\"restricted\"}"
			} else {
				paoParameters = "{\"topologyManagerPolicy\":\"single-numa-node\"}"
			}
			// when topology manager policy is set using
			// kubelet.experimental annotation, this is disregarded
			// as PAO overrides and no reboot occurs.
			// In the case of standard cluster reboot does occur which could be a bug
			if !hypershift.IsHypershiftCluster() {
				profile.Annotations = updateKubeletConfigOverrideAnnotations(profile.Annotations, paoParameters)
				By("updating Performance profile")
				profiles.UpdateWithRetry(profile)

				By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
				profilesupdate.WaitForTuningUpdating(ctx, profile)

				By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
				profilesupdate.WaitForTuningUpdated(ctx, profile)
			}
			for _, node := range workerRTNodes {
				kubeletConfig, err := nodes.GetKubeletConfig(ctx, &node)
				Expect(err).ToNot(HaveOccurred())
				Expect(kubeletConfig.TopologyManagerPolicy).To(Equal("single-numa-node"))
			}
		})

		It("[test_id:45489] Verify settings are reverted to default profile", func() {
			By("Reverting the Profile")
			profiles.UpdateWithRetry(initialProfile)

			By(fmt.Sprintf("Waiting for %s to be updated after revert", poolName))
			profilesupdate.WaitForTuningUpdated(ctx, initialProfile)

			for _, node := range workerRTNodes {
				kubeletConfig, err := nodes.GetKubeletConfig(ctx, &node)
				Expect(err).ToNot(HaveOccurred())
				Expect(kubeletConfig.AllowedUnsafeSysctls).To(BeEmpty())
				Expect(kubeletConfig.KubeReserved["memory"]).ToNot(Equal("768Mi"))
				Expect(kubeletConfig.ImageMinimumGCAge.Seconds()).ToNot(Equal(180))
			}

			autoSizingCmd := []string{"cat", "/rootfs/etc/openshift/kubelet.conf.d/20-auto-sizing.conf"}
			for _, node := range workerRTNodes {
				out, err := nodes.ExecCommand(ctx, &node, autoSizingCmd)
				Expect(err).ToNot(HaveOccurred())
				stdout := testutils.ToString(out)
				Expect(stdout).ToNot(ContainSubstring("300Mi"))
			}

		})
		AfterAll(func() {
			By("Reverting the Profile")
			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			if !equality.Semantic.DeepEqual(profile.Spec, initialProfile.Spec) || !equality.Semantic.DeepEqual(profile.Annotations, initialProfile.Annotations) {
				profiles.UpdateWithRetry(initialProfile)

				By(fmt.Sprintf("Waiting for %s to be updated after revert", poolName))
				profilesupdate.WaitForTuningUpdated(ctx, initialProfile)
			}
		})

	})
})

func updateKubeletConfigOverrideAnnotations(profileAnnotations map[string]string, annotations string) map[string]string {
	if profileAnnotations == nil {
		profileAnnotations = map[string]string{}
	}
	profileAnnotations["kubeletconfig.experimental"] = annotations
	return profileAnnotations
}
