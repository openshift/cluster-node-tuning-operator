package __performance_kubelet_node_test

import (
	"context"
	"encoding/json"
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
	"sigs.k8s.io/controller-runtime/pkg/client"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
)

var _ = Describe("[ref_id: 45487][performance]additional kubelet arguments", Ordered, func() {
	var initialProfile, profile *performancev2.PerformanceProfile
	var workerRTNodes []corev1.Node
	var performanceMCP string

	testutils.CustomBeforeAll(func() {
		workerRTNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred())

		profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		performanceMCP, err = mcps.GetByProfile(profile)
		Expect(err).ToNot(HaveOccurred())

		initialProfile = profile.DeepCopy()
		// Verify that worker and performance MCP have updated state equals to true
		for _, mcpName := range []string{testutils.RoleWorker, performanceMCP} {
			mcps.WaitForCondition(mcpName, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
		}

	})
	Context("Additional kubelet arguments", func() {
		It("[test_id:45488]Test performance profile annotation for changing multiple kubelet settings", func() {
			sysctls := "{\"allowedUnsafeSysctls\":[\"net.core.somaxconn\",\"kernel.msg*\"],\"systemReserved\":{\"memory\":\"300Mi\"},\"kubeReserved\":{\"memory\":\"768Mi\"},\"imageMinimumGCAge\":\"3m\"}"
			profile.Annotations = updateKubeletConfigOverrideAnnotations(profile.Annotations, sysctls)
			annotations, err := json.Marshal(profile.Annotations)
			Expect(err).ToNot(HaveOccurred())

			By("Applying changes in performance profile and waiting until mcp will start updating")
			Expect(testclient.Client.Patch(context.TODO(), profile,
				client.RawPatch(
					types.JSONPatchType,
					[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/metadata/annotations", "value": %s }]`, annotations)),
				),
			)).ToNot(HaveOccurred())
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
			By("Waiting when mcp finishes updates")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
			for _, node := range workerRTNodes {
				kubeletConfig, err := nodes.GetKubeletConfig(context.TODO(), &node)
				Expect(err).ToNot(HaveOccurred())
				sysctlsValue := kubeletConfig.AllowedUnsafeSysctls
				Expect(sysctlsValue).Should(ContainElements("net.core.somaxconn", "kernel.msg*"))
				Expect(kubeletConfig.KubeReserved["memory"]).To(Equal("768Mi"))
				Expect(kubeletConfig.ImageMinimumGCAge.Seconds()).To(Equal(180))
			}
			kubeletArguments := []string{"/bin/bash", "-c", "ps -ef | grep kubelet | grep config"}
			for _, node := range workerRTNodes {
				out, err := nodes.ExecCommand(context.TODO(), &node, kubeletArguments)
				Expect(err).ToNot(HaveOccurred())
				stdout := testutils.ToString(out)
				Expect(strings.Contains(stdout, "300Mi")).To(BeTrue())
			}
		})
		Context("When setting cpu manager related parameters", func() {
			It("[test_id:45493]Should not override performance-addon-operator values", func() {
				paoValues := "{\"cpuManagerPolicy\":\"static\",\"cpuManagerReconcilePeriod\":\"5s\"}"
				profile.Annotations = updateKubeletConfigOverrideAnnotations(profile.Annotations, paoValues)
				annotations, err := json.Marshal(profile.Annotations)
				Expect(err).ToNot(HaveOccurred())

				By("Applying changes in performance profile and waiting until mcp will start updating")
				Expect(testclient.Client.Patch(context.TODO(), profile,
					client.RawPatch(
						types.JSONPatchType,
						[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/metadata/annotations", "value": %s }]`, annotations)),
					),
				)).ToNot(HaveOccurred())
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
				By("Waiting when mcp finishes updates")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
				for _, node := range workerRTNodes {
					kubeletConfig, err := nodes.GetKubeletConfig(context.TODO(), &node)
					Expect(err).ToNot(HaveOccurred())
					Expect(kubeletConfig.CPUManagerPolicy).Should(Equal("static"))
					Expect(kubeletConfig.CPUManagerReconcilePeriod.Seconds()).To(Equal(5))
				}
			})
		})
		It("[test_id:45490]Test memory reservation changes", func() {
			// In this test case we check if after applying reserving memory for
			// systemReserved and KubeReserved, the allocatable is reduced and Allocatable
			// Verify that Allocatable = Node capacity - (kubereserved + systemReserved + EvictionMemory)
			reservedMemory := "{\"systemReserved\":{\"memory\":\"300Mi\"},\"kubeReserved\":{\"memory\":\"768Mi\"}}"
			profile.Annotations = updateKubeletConfigOverrideAnnotations(profile.Annotations, reservedMemory)
			annotations, err := json.Marshal(profile.Annotations)
			Expect(err).ToNot(HaveOccurred())

			By("Applying changes in performance profile and waiting until mcp will start updating")
			Expect(testclient.Client.Patch(context.TODO(), profile,
				client.RawPatch(
					types.JSONPatchType,
					[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/metadata/annotations", "value": %s }]`, annotations)),
				),
			)).ToNot(HaveOccurred())
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

			By("Waiting when mcp finishes updates")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

			var kubeletConfig machineconfigv1.KubeletConfig

			Eventually(func() error {
				By("Getting that new KubeletConfig")
				configKey := types.NamespacedName{
					Name:      components.GetComponentName(profile.Name, components.ComponentNamePrefix),
					Namespace: metav1.NamespaceNone,
				}
				err := testclient.Client.Get(context.TODO(), configKey, &kubeletConfig)
				if err != nil {
					klog.Warningf("Failed to get the KubeletConfig %q", configKey.Name)
				}
				return err
			}).WithPolling(5 * time.Second).WithTimeout(3 * time.Minute).Should(Succeed())

			kubeletConfigString := string(kubeletConfig.Spec.KubeletConfig.Raw)
			Expect(kubeletConfigString).To(ContainSubstring(`"kubeReserved":{"memory":"768Mi"}`))
			Expect(kubeletConfigString).To(ContainSubstring(`"systemReserved":{"memory":"300Mi"}`))

			for _, node := range workerRTNodes {
				kubeletConfig, err := nodes.GetKubeletConfig(context.TODO(), &node)
				Expect(err).ToNot(HaveOccurred())
				totalCapactity := node.Status.Capacity.Memory().MilliValue()
				evictionMemory := kubeletConfig.EvictionHard["memory.available"]
				kubeReserved := kubeletConfig.KubeReserved["memory"]
				evictionMemoryInt, err := strconv.ParseInt(strings.TrimSuffix(evictionMemory, "Mi"), 10, 64)
				kubeReservedMemoryInt, err := strconv.ParseInt(strings.TrimSuffix(kubeReserved, "Mi"), 10, 64)
				systemReservedResource := resource.NewQuantity(300*1024*1024, resource.BinarySI)
				kubeReservedMemoryResource := resource.NewQuantity(kubeReservedMemoryInt*1024*1024, resource.BinarySI)
				evictionMemoryResource := resource.NewQuantity(evictionMemoryInt*1024*1024, resource.BinarySI)
				totalKubeMemory := systemReservedResource.MilliValue() + kubeReservedMemoryResource.MilliValue() + evictionMemoryResource.MilliValue()
				calculatedAllocatable := totalCapactity - totalKubeMemory
				currentAllocatable := node.Status.Allocatable.Memory().MilliValue()
				Expect(calculatedAllocatable).To(Equal(currentAllocatable))
			}
		})
		It("[test_id:45495] Test setting PAO managed parameters", func() {
			var paoParameters string = ""
			if *profile.Spec.NUMA.TopologyPolicy == "single-numa-node" {
				paoParameters = "{\"topologyManagerPolicy\":\"restricted\"}"
			} else {
				paoParameters = "{\"topologyManagerPolicy\":\"single-numa-node\"}"
			}
			profile.Annotations = updateKubeletConfigOverrideAnnotations(profile.Annotations, paoParameters)
			annotations, err := json.Marshal(profile.Annotations)

			Expect(err).ToNot(HaveOccurred())

			By("Applying changes in performance profile and waiting until mcp will start updating")
			Expect(testclient.Client.Patch(context.TODO(), profile,
				client.RawPatch(
					types.JSONPatchType,
					[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/metadata/annotations", "value": %s }]`, annotations)),
				),
			)).ToNot(HaveOccurred())
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
			By("Waiting when mcp finishes updates")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
			for _, node := range workerRTNodes {
				kubeletConfig, err := nodes.GetKubeletConfig(context.TODO(), &node)
				Expect(err).ToNot(HaveOccurred())
				Expect(kubeletConfig.TopologyManagerPolicy).To(Equal("single-numa-node"))
			}
		})

		It("[test_id:45489] Verify settings are reverted to default profile", func() {
			By("Reverting the Profile")
			Expect(testclient.Client.Patch(context.TODO(), profile,
				client.RawPatch(
					types.JSONPatchType,
					[]byte(fmt.Sprintf(`[{ "op": "remove", "path": "/metadata/annotations/kubeletconfig.experimental"}]`)),
				),
			)).ToNot(HaveOccurred())
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
			kubeletArguments := []string{"/bin/bash", "-c", "ps -ef | grep kubelet | grep config"}
			for _, node := range workerRTNodes {
				kubeletConfig, err := nodes.GetKubeletConfig(context.TODO(), &node)
				Expect(err).ToNot(HaveOccurred())
				Expect(kubeletConfig.AllowedUnsafeSysctls).To(Equal(nil))
				Expect(kubeletConfig.KubeReserved["memory"]).ToNot(Equal("768Mi"))
				Expect(kubeletConfig.ImageMinimumGCAge.Seconds()).ToNot(Equal(180))
			}
			for _, node := range workerRTNodes {
				out, err := nodes.ExecCommand(context.TODO(), &node, kubeletArguments)
				Expect(err).ToNot(HaveOccurred())
				stdout := testutils.ToString(out)
				Expect(strings.Contains(stdout, "300Mi")).To(BeTrue())
			}

		})
		AfterAll(func() {
			By("Reverting the Profile")
			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			currentSpec, _ := json.Marshal(profile.Spec)
			spec, _ := json.Marshal(initialProfile.Spec)
			// revert only if the profile changes.
			if !equality.Semantic.DeepEqual(currentSpec, spec) {
				Expect(testclient.Client.Patch(context.TODO(), profile,
					client.RawPatch(
						types.JSONPatchType,
						[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, spec)),
					),
				)).ToNot(HaveOccurred())

				By("Applying changes in performance profile and waiting until mcp will start updating")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

				By("Waiting when mcp finishes updates")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
			}
		})

	})
})

func updateKubeletConfigOverrideAnnotations(profileAnnotations map[string]string, annotations string) map[string]string {
	if _, ok := profileAnnotations["performance.openshift.io/ignore-cgroups-version"]; !ok {
		profileAnnotations = map[string]string{
			"kubeletconfig.experimental": annotations}
	} else {
		profileAnnotations["kubeletconfig.experimental"] = annotations
	}
	return profileAnnotations
}
