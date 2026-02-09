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

	Describe("RPS Configuration", func() {
		Context("IsRpsEnabled", func() {
			It("should return false by default", func() {
				// No annotations, should default to false
				result := IsRpsEnabled(profile)
				Expect(result).To(BeFalse())
			})

			It("should return true when annotation is 'true'", func() {
				profile.Annotations = map[string]string{
					performancev2.PerformanceProfileEnableRpsAnnotation: "true",
				}
				result := IsRpsEnabled(profile)
				Expect(result).To(BeTrue())
			})

			It("should return true when annotation is 'enable'", func() {
				profile.Annotations = map[string]string{
					performancev2.PerformanceProfileEnableRpsAnnotation: "enable",
				}
				result := IsRpsEnabled(profile)
				Expect(result).To(BeTrue())
			})

			It("should return false when annotation is 'false'", func() {
				profile.Annotations = map[string]string{
					performancev2.PerformanceProfileEnableRpsAnnotation: "false",
				}
				result := IsRpsEnabled(profile)
				Expect(result).To(BeFalse())
			})

			It("should return false when annotation is 'disable'", func() {
				profile.Annotations = map[string]string{
					performancev2.PerformanceProfileEnableRpsAnnotation: "disable",
				}
				result := IsRpsEnabled(profile)
				Expect(result).To(BeFalse())
			})

			It("should return false when annotation has invalid value", func() {
				profile.Annotations = map[string]string{
					performancev2.PerformanceProfileEnableRpsAnnotation: "invalid",
				}
				result := IsRpsEnabled(profile)
				Expect(result).To(BeFalse())
			})
		})
	})

	Describe("DRA Resource Management", func() {
		Context("IsDRAManaged", func() {
			It("should return false when annotations are nil", func() {
				profile.Annotations = nil
				result := IsDRAManaged(profile)
				Expect(result).To(BeFalse())
			})

			It("should return false when annotation is not present", func() {
				profile.Annotations = map[string]string{}
				result := IsDRAManaged(profile)
				Expect(result).To(BeFalse())
			})

			It("should return true when annotation is 'true'", func() {
				profile.Annotations = map[string]string{
					performancev2.PerformanceProfileDRAResourceManagementAnnotation: "true",
				}
				result := IsDRAManaged(profile)
				Expect(result).To(BeTrue())
			})

			It("should return false when annotation is 'false'", func() {
				profile.Annotations = map[string]string{
					performancev2.PerformanceProfileDRAResourceManagementAnnotation: "false",
				}
				result := IsDRAManaged(profile)
				Expect(result).To(BeFalse())
			})

			It("should return false when annotation has invalid value", func() {
				profile.Annotations = map[string]string{
					performancev2.PerformanceProfileDRAResourceManagementAnnotation: "invalid",
				}
				result := IsDRAManaged(profile)
				Expect(result).To(BeFalse())
			})
		})
	})
})

func setValidNodeSelector(profile *performancev2.PerformanceProfile) {
	selector := make(map[string]string)
	selector["fooDomain/"+NodeSelectorRole] = ""
	profile.Spec.NodeSelector = selector
}
