package v2

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v1"
	"k8s.io/utils/ptr"
)

var _ = Describe("PerformanceProfile conversion", func() {
	It("should preserve newly added v1 fields through a v2 -> v1 -> v2 round trip", func() {
		shared := CPUSet("8-9")
		ovsDpdk := CPUSet("10-11")
		kernelPageSize := KernelPageSize("4k")

		src := &PerformanceProfile{}
		src.Spec.CPU = &CPU{
			Reserved: ptr.To(ReservedCPUs),
			Isolated: ptr.To(IsolatedCPUs),
			Shared:   &shared,
			OvsDpdk:  &ovsDpdk,
		}
		src.Spec.KernelPageSize = &kernelPageSize
		src.Spec.WorkloadHints = &WorkloadHints{
			MixedCpus: ptr.To(true),
		}

		hub := &v1.PerformanceProfile{}
		Expect(src.ConvertTo(hub)).To(Succeed())

		Expect(hub.Spec.CPU.Shared).NotTo(BeNil())
		Expect(*hub.Spec.CPU.Shared).To(Equal(v1.CPUSet(shared)))
		Expect(hub.Spec.CPU.OvsDpdk).NotTo(BeNil())
		Expect(*hub.Spec.CPU.OvsDpdk).To(Equal(v1.CPUSet(ovsDpdk)))
		Expect(hub.Spec.KernelPageSize).NotTo(BeNil())
		Expect(*hub.Spec.KernelPageSize).To(Equal(v1.KernelPageSize(kernelPageSize)))
		Expect(hub.Spec.WorkloadHints).NotTo(BeNil())
		Expect(hub.Spec.WorkloadHints.MixedCpus).NotTo(BeNil())
		Expect(*hub.Spec.WorkloadHints.MixedCpus).To(BeTrue())

		dst := &PerformanceProfile{}
		Expect(dst.ConvertFrom(hub)).To(Succeed())

		Expect(dst.Spec.CPU.Shared).NotTo(BeNil())
		Expect(*dst.Spec.CPU.Shared).To(Equal(shared))
		Expect(dst.Spec.CPU.OvsDpdk).NotTo(BeNil())
		Expect(*dst.Spec.CPU.OvsDpdk).To(Equal(ovsDpdk))
		Expect(dst.Spec.KernelPageSize).NotTo(BeNil())
		Expect(*dst.Spec.KernelPageSize).To(Equal(kernelPageSize))
		Expect(dst.Spec.WorkloadHints).NotTo(BeNil())
		Expect(dst.Spec.WorkloadHints.MixedCpus).NotTo(BeNil())
		Expect(*dst.Spec.WorkloadHints.MixedCpus).To(BeTrue())
	})
})
