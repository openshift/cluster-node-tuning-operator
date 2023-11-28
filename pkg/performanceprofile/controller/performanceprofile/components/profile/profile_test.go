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

	DescribeTable("IsCgroupsVersionIgnored", func(anns map[string]string, exp bool) {
		profile := testutils.NewPerformanceProfile("test")
		profile.Annotations = anns
		got := IsCgroupsVersionIgnored(profile)
		Expect(got).To(Equal(exp), "got=%v exp=%v anns=%+v", got, exp, anns)
	},
		Entry("nil anns", nil, false),
		Entry("empty anns", map[string]string{}, false),
		Entry("unrelated anns", map[string]string{
			performancev2.PerformanceProfilePauseAnnotation: "true",
		}, false),
		Entry("unexpected value - 1", map[string]string{
			performancev2.PerformanceProfileIgnoreCgroupsVersion: "True",
		}, false),
		Entry("unexpected value - 2", map[string]string{
			performancev2.PerformanceProfileIgnoreCgroupsVersion: "1",
		}, false),
		Entry("expected value - one and only", map[string]string{
			performancev2.PerformanceProfileIgnoreCgroupsVersion: "true",
		}, true),
	)
})

func setValidNodeSelector(profile *performancev2.PerformanceProfile) {
	selector := make(map[string]string)
	selector["fooDomain/"+NodeSelectorRole] = ""
	profile.Spec.NodeSelector = selector
}
