package __2_performance_update_second

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

var _ = Describe("[ref_id: 45487][performance]additional kubelet arguments", func() {
	var profile *performancev2.PerformanceProfile
	var workerRTNodes []corev1.Node
	var performanceMCP string

	testutils.BeforeAll(func() {
		workerRTNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
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
	BeforeEach(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}
	})
	Context("Additional kubelet arguments", func() {
		It("[test_id:45488]Test performance profile annotation for changing multiple kubelet settings", func() {
			profile.Annotations = map[string]string{
				"kubeletconfig.experimental": "{\"allowedUnsafeSysctls\":[\"net.core.somaxconn\",\"kernel.msg*\"],\"systemReserved\":{\"memory\":\"300Mi\"},\"kubeReserved\":{\"memory\":\"768Mi\"},\"imageMinimumGCAge\":\"3m\"}",
			}
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
				kubeletConfig, err := nodes.GetKubeletConfig(&node)
				Expect(err).ToNot(HaveOccurred())
				sysctlsValue := kubeletConfig.AllowedUnsafeSysctls
				Expect(sysctlsValue).Should(ContainElements("net.core.somaxconn", "kernel.msg*"))
				Expect(kubeletConfig.KubeReserved["memory"]).To(Equal("768Mi"))
				Expect(kubeletConfig.ImageMinimumGCAge.Seconds()).To(Equal(180))
			}
			kubeletArguments := []string{"/bin/bash", "-c", "ps -ef | grep kubelet | grep config"}
			for _, node := range workerRTNodes {
				stdout, err := nodes.ExecCommandOnNode(kubeletArguments, &node)
				Expect(err).ToNot(HaveOccurred())
				Expect(strings.Contains(stdout, "300Mi")).To(BeTrue())
			}
		})
		Context("When setting cpu manager related parameters", func() {
			It("[test_id:45493]Should not override performance-addon-operator values", func() {
				profile.Annotations = map[string]string{
					"kubeletconfig.experimental": "{\"cpuManagerPolicy\":\"static\",\"cpuManagerReconcilePeriod\":\"5s\"}",
				}
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
					kubeletConfig, err := nodes.GetKubeletConfig(&node)
					Expect(err).ToNot(HaveOccurred())
					Expect(kubeletConfig.CPUManagerPolicy).Should(Equal("static"))
					Expect(kubeletConfig.CPUManagerReconcilePeriod.Seconds()).To(Equal(5))
				}
			})
		})
		It("[test_id:45490]Test memory reservation changes", func() {
			// In this test case we are testing if after applying reserving memory for
			// systemReserved and KubeReserved, the allocatable is reduced and Allocatable
			// Verify that Allocatable = Node capacity - (kubereserved + systemReserved + EvictionMemory)
			profile.Annotations = map[string]string{
				"kubeletconfig.experimental": "{\"systemReserved\":{\"memory\":\"300Mi\"},\"kubeReserved\":{\"memory\":\"768Mi\"}}",
			}
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
				kubeletConfig, err := nodes.GetKubeletConfig(&node)
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
			profile.Annotations = map[string]string{
				"kubeletconfig.experimental": "{\"topologyManagerPolicy\":\"single-numa-node\"}",
			}
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
				kubeletConfig, err := nodes.GetKubeletConfig(&node)
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
				kubeletConfig, err := nodes.GetKubeletConfig(&node)
				Expect(err).ToNot(HaveOccurred())
				Expect(kubeletConfig.AllowedUnsafeSysctls).To(Equal(nil))
				Expect(kubeletConfig.KubeReserved["memory"]).ToNot(Equal("768Mi"))
				Expect(kubeletConfig.ImageMinimumGCAge.Seconds()).ToNot(Equal(180))
			}
			for _, node := range workerRTNodes {
				stdout, err := nodes.ExecCommandOnNode(kubeletArguments, &node)
				Expect(err).ToNot(HaveOccurred())
				Expect(strings.Contains(stdout, "300Mi")).To(BeTrue())
			}

		})

	})
})
