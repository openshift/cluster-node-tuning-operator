package kubeletconfig

import (
	"fmt"

	"github.com/ghodss/yaml"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
	"k8s.io/utils/pointer"

	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components"
	testutils "github.com/openshift-kni/performance-addon-operators/pkg/utils/testing"
)

const testReservedMemory = `reservedMemory:
    - limits:
        memory: 1100Mi
      numaNode: 0`

var _ = Describe("Kubelet Config", func() {
	It("should generate yaml with expected parameters", func() {
		profile := testutils.NewPerformanceProfile("test")
		selectorKey, selectorValue := components.GetFirstKeyAndValue(profile.Spec.MachineConfigPoolSelector)
		kc, err := New(profile, map[string]string{selectorKey: selectorValue})
		Expect(err).ToNot(HaveOccurred())

		y, err := yaml.Marshal(kc)
		Expect(err).ToNot(HaveOccurred())

		manifest := string(y)

		Expect(manifest).To(ContainSubstring(fmt.Sprintf("%s: %s", selectorKey, selectorValue)))
		Expect(manifest).To(ContainSubstring("reservedSystemCPUs: 0-3"))
		Expect(manifest).To(ContainSubstring("topologyManagerPolicy: single-numa-node"))
		Expect(manifest).To(ContainSubstring("cpuManagerPolicy: static"))
		Expect(manifest).To(ContainSubstring("memoryManagerPolicy: Static"))
		Expect(manifest).To(ContainSubstring("cpuManagerPolicyOptions"))
		Expect(manifest).To(ContainSubstring(testReservedMemory))
	})

	Context("with topology manager restricted policy", func() {
		It("should have the memory manager related parameters", func() {
			profile := testutils.NewPerformanceProfile("test")
			profile.Spec.NUMA.TopologyPolicy = pointer.String(kubeletconfigv1beta1.RestrictedTopologyManagerPolicy)
			selectorKey, selectorValue := components.GetFirstKeyAndValue(profile.Spec.MachineConfigPoolSelector)
			kc, err := New(profile, map[string]string{selectorKey: selectorValue})
			Expect(err).ToNot(HaveOccurred())

			y, err := yaml.Marshal(kc)
			Expect(err).ToNot(HaveOccurred())

			manifest := string(y)
			Expect(manifest).To(ContainSubstring("memoryManagerPolicy: Static"))
			Expect(manifest).To(ContainSubstring(testReservedMemory))
		})

		It("should not have the cpumanager policy options set", func() {
			profile := testutils.NewPerformanceProfile("test")
			profile.Spec.NUMA.TopologyPolicy = pointer.String(kubeletconfigv1beta1.RestrictedTopologyManagerPolicy)
			selectorKey, selectorValue := components.GetFirstKeyAndValue(profile.Spec.MachineConfigPoolSelector)
			kc, err := New(profile, map[string]string{selectorKey: selectorValue})
			Expect(err).ToNot(HaveOccurred())

			y, err := yaml.Marshal(kc)
			Expect(err).ToNot(HaveOccurred())

			manifest := string(y)
			Expect(manifest).ToNot(ContainSubstring("cpuManagerPolicyOptions"))
		})

	})

	Context("with topology manager best-effort policy", func() {
		It("should not have the memory manager related parameters", func() {
			profile := testutils.NewPerformanceProfile("test")
			profile.Spec.NUMA.TopologyPolicy = pointer.String(kubeletconfigv1beta1.BestEffortTopologyManagerPolicy)
			selectorKey, selectorValue := components.GetFirstKeyAndValue(profile.Spec.MachineConfigPoolSelector)
			kc, err := New(profile, map[string]string{selectorKey: selectorValue})
			Expect(err).ToNot(HaveOccurred())

			y, err := yaml.Marshal(kc)
			Expect(err).ToNot(HaveOccurred())

			manifest := string(y)
			Expect(manifest).ToNot(ContainSubstring("memoryManagerPolicy: Static"))
			Expect(manifest).ToNot(ContainSubstring(testReservedMemory))
		})
	})

	Context("with additional kubelet arguments", func() {
		It("should not override CPU manager parameters", func() {
			profile := testutils.NewPerformanceProfile("test")
			profile.Annotations = map[string]string{
				experimentalKubeletSnippetAnnotation: `{"cpuManagerPolicy": "none", "cpuManagerReconcilePeriod": "10s", "reservedSystemCPUs": "4,5"}`,
			}
			selectorKey, selectorValue := components.GetFirstKeyAndValue(profile.Spec.MachineConfigPoolSelector)
			kc, err := New(profile, map[string]string{selectorKey: selectorValue})
			y, err := yaml.Marshal(kc)
			Expect(err).ToNot(HaveOccurred())

			manifest := string(y)
			Expect(manifest).ToNot(ContainSubstring("cpuManagerPolicy: none"))
			Expect(manifest).ToNot(ContainSubstring("cpuManagerReconcilePeriod: 10s"))
			Expect(manifest).ToNot(ContainSubstring("reservedSystemCPUs: 4-5"))
		})

		It("should not override topology manager parameters", func() {
			profile := testutils.NewPerformanceProfile("test")
			profile.Annotations = map[string]string{
				experimentalKubeletSnippetAnnotation: `{"topologyManagerPolicy": "none"}`,
			}
			selectorKey, selectorValue := components.GetFirstKeyAndValue(profile.Spec.MachineConfigPoolSelector)
			kc, err := New(profile, map[string]string{selectorKey: selectorValue})
			y, err := yaml.Marshal(kc)
			Expect(err).ToNot(HaveOccurred())

			manifest := string(y)
			Expect(manifest).ToNot(ContainSubstring("topologyManagerPolicy: none"))
		})

		It("should not override memory manager policy", func() {
			profile := testutils.NewPerformanceProfile("test")

			profile.Annotations = map[string]string{
				experimentalKubeletSnippetAnnotation: `{"memoryManagerPolicy": "None", "reservedMemory": [{"numaNode": 10, "limits": {"test": "1024"}}]}`,
			}
			selectorKey, selectorValue := components.GetFirstKeyAndValue(profile.Spec.MachineConfigPoolSelector)
			kc, err := New(profile, map[string]string{selectorKey: selectorValue})
			y, err := yaml.Marshal(kc)
			Expect(err).ToNot(HaveOccurred())

			manifest := string(y)
			Expect(manifest).ToNot(ContainSubstring("memoryManagerPolicy: None"))
			Expect(manifest).To(ContainSubstring("numaNode: 10"))
		})

		It("should set the kubelet config accordingly", func() {
			profile := testutils.NewPerformanceProfile("test")
			profile.Annotations = map[string]string{
				experimentalKubeletSnippetAnnotation: `{"allowedUnsafeSysctls": ["net.core.somaxconn"], "evictionHard": {"memory.available": "200Mi"}}`,
			}
			selectorKey, selectorValue := components.GetFirstKeyAndValue(profile.Spec.MachineConfigPoolSelector)
			kc, err := New(profile, map[string]string{selectorKey: selectorValue})
			y, err := yaml.Marshal(kc)
			Expect(err).ToNot(HaveOccurred())

			manifest := string(y)
			Expect(manifest).To(ContainSubstring("net.core.somaxconn"))
			Expect(manifest).To(ContainSubstring("memory.available: 200Mi"))
		})

		It("should allow to override the cpumanager policy options and update the kubelet config accordingly", func() {
			profile := testutils.NewPerformanceProfile("test")
			profile.Annotations = map[string]string{
				experimentalKubeletSnippetAnnotation: `{"allowedUnsafeSysctls": ["net.core.somaxconn"], "cpuManagerPolicyOptions": {"full-pcpus-only": "false"}}`,
			}
			selectorKey, selectorValue := components.GetFirstKeyAndValue(profile.Spec.MachineConfigPoolSelector)
			kc, err := New(profile, map[string]string{selectorKey: selectorValue})
			data, err := yaml.Marshal(kc)
			Expect(err).ToNot(HaveOccurred())

			manifest := string(data)
			Expect(manifest).To(ContainSubstring("net.core.somaxconn"))
			Expect(manifest).To(ContainSubstring(`full-pcpus-only: "false"`))
		})

	})
})
