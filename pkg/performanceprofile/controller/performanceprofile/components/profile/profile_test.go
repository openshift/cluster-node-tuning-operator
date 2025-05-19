package profile

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"

	testutils "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/utils/testing"
)

const (
	NodeSelectorRole = "barRole"
)

var _ = Describe("PerformanceProfile", func() {
	var profile *performancev2.PerformanceProfile

	BeforeEach(func() {
		profile = testutils.NewPerformanceProfile("test")
	})

	Describe("Defaulting", func() {
		It("should return given MachineConfigLabel", func() {
			labels := GetMachineConfigLabel(profile)
			k, v := components.GetFirstKeyAndValue(labels)
			Expect(k).To(Equal(testutils.MachineConfigLabelKey))
			Expect(v).To(Equal(testutils.MachineConfigLabelValue))

		})

		It("should return given MachineConfigPoolSelector", func() {
			labels := GetMachineConfigPoolSelector(profile, nil)
			k, v := components.GetFirstKeyAndValue(labels)
			Expect(k).To(Equal(testutils.MachineConfigPoolLabelKey))
			Expect(v).To(Equal(testutils.MachineConfigPoolLabelValue))
		})

		It("should return default MachineConfigLabels", func() {
			profile.Spec.MachineConfigLabel = nil
			setValidNodeSelector(profile)

			labels := GetMachineConfigLabel(profile)
			k, v := components.GetFirstKeyAndValue(labels)
			Expect(k).To(Equal(components.MachineConfigRoleLabelKey))
			Expect(v).To(Equal(NodeSelectorRole))

		})

		It("should return default MachineConfigPoolSelector", func() {
			profile.Spec.MachineConfigPoolSelector = nil
			setValidNodeSelector(profile)

			labels := GetMachineConfigPoolSelector(profile, nil)
			k, v := components.GetFirstKeyAndValue(labels)
			Expect(k).To(Equal(components.MachineConfigRoleLabelKey))
			Expect(v).To(Equal(NodeSelectorRole))

		})
	})

	DescribeTable("Annotation detection", func(expected bool, anns map[string]string) {
		profile.Annotations = anns
		got := IsLLCAlignmentEnabled(profile)
		Expect(got).To(Equal(expected), "anns=%v", anns)

	},
		Entry("nil annotations", false, nil),
		Entry("empty annotations", false, map[string]string{}),
		Entry("unrelated annotations", false, map[string]string{
			"foo": "bar", // TODO: use real cluster annotation? should be no different
		}),
		Entry("different annotations", false, map[string]string{
			"kubeletconfig.experimental": `{"cpuManagerPolicyOptions": { "full-pcpus-only": "true" }}`,
		}),
		Entry("expected annotation", true, map[string]string{
			"kubeletconfig.experimental": `{"cpuManagerPolicyOptions": { "prefer-align-cpus-by-uncorecache": "true" }}`,
		}),
		Entry("expected annotation, but disabled", false, map[string]string{
			"kubeletconfig.experimental": `{"cpuManagerPolicyOptions": { "prefer-align-cpus-by-uncorecache": "false" }}`,
		}),
		Entry("mixed annotations, including expected", true, map[string]string{
			"kubeletconfig.experimental": `{"cpuManagerPolicyOptions": { "prefer-align-cpus-by-uncorecache": "true", "full-pcpus-only": "false" }}`,
		}),
		Entry("mixed annotations, including expected, but disabled", false, map[string]string{
			"kubeletconfig.experimental": `{"cpuManagerPolicyOptions": { "prefer-align-cpus-by-uncorecache": "false", "full-pcpus-only": "false" }}`,
		}),
	)
})

func setValidNodeSelector(profile *performancev2.PerformanceProfile) {
	selector := make(map[string]string)
	selector["fooDomain/"+NodeSelectorRole] = ""
	profile.Spec.NodeSelector = selector
}
