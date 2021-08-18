package kubeletconfig

import (
	"fmt"

	"github.com/ghodss/yaml"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/openshift/cluster-node-tuning-operator/pkg/performance-addon-controller/components"
	testutils "github.com/openshift/cluster-node-tuning-operator/pkg/performance-addon-controller/utils/testing"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
	"k8s.io/utils/pointer"
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
})
