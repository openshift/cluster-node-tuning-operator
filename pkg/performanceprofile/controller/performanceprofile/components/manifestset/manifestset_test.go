package manifestset

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"

	testutils "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/utils/testing"
)

var _ = Describe("LLC Enablement file", func() {
	var profile *performancev2.PerformanceProfile
	var opts *components.Options
	var expectedPath string

	BeforeEach(func() {
		profile = testutils.NewPerformanceProfile("test-llc-ann")
		opts = &components.Options{}
		expectedPath = "/etc/kubernetes/openshift-llc-alignment"
	})

	When("the option is configured", func() {
		It("should not be generated if missing", func() {
			mfs, err := GetNewComponents(profile, opts)
			Expect(err).ToNot(HaveOccurred())

			ok, err := findFileInIgnition(mfs, expectedPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeFalse(), "expected path %q found in ignition", expectedPath)
		})

		It("should be generated if missing control annotation, but injected in experimental", func() {
			profile.Annotations = map[string]string{
				"kubeletconfig.experimental": `{"cpuManagerPolicyOptions": { "prefer-align-cpus-by-uncorecache": "true", "full-pcpus-only": "false" }}`,
			}

			mfs, err := GetNewComponents(profile, opts)
			Expect(err).ToNot(HaveOccurred())

			ok, err := findFileInIgnition(mfs, expectedPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeTrue(), "expected path %q not found in ignition", expectedPath)
		})

		It("should be generated if the master flag is enabled, but the injected value is disabled", func() {
			profile.Annotations = map[string]string{
				"kubeletconfig.prefer-align-cpus-by-uncorecache": "true",
				"kubeletconfig.experimental":                     `{"cpuManagerPolicyOptions": { "prefer-align-cpus-by-uncorecache": "false", "full-pcpus-only": "false" }}`,
			}

			mfs, err := GetNewComponents(profile, opts)
			Expect(err).ToNot(HaveOccurred())

			ok, err := findFileInIgnition(mfs, expectedPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeTrue(), "expected path %q not found in ignition", expectedPath)
		})

		It("should NOT be generated if the master flag is disabled, but the injected value is enabled", func() {
			profile.Annotations = map[string]string{
				"kubeletconfig.prefer-align-cpus-by-uncorecache": "false",
				"kubeletconfig.experimental":                     `{"cpuManagerPolicyOptions": { "prefer-align-cpus-by-uncorecache": "true", "full-pcpus-only": "false" }}`,
			}

			mfs, err := GetNewComponents(profile, opts)
			Expect(err).ToNot(HaveOccurred())

			ok, err := findFileInIgnition(mfs, expectedPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeFalse(), "expected path %q found in ignition", expectedPath)
		})

		It("should be generated if enabled both by the master flag and injected", func() {
			profile.Annotations = map[string]string{
				"kubeletconfig.prefer-align-cpus-by-uncorecache": "true",
				"kubeletconfig.experimental":                     `{"cpuManagerPolicyOptions": { "prefer-align-cpus-by-uncorecache": "true", "full-pcpus-only": "false" }}`,
			}

			mfs, err := GetNewComponents(profile, opts)
			Expect(err).ToNot(HaveOccurred())

			ok, err := findFileInIgnition(mfs, expectedPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeTrue(), "expected path %q not found in ignition", expectedPath)
		})

		It("should be generated if enabled both by the master flag but not injected", func() {
			profile.Annotations = map[string]string{
				"kubeletconfig.prefer-align-cpus-by-uncorecache": "true",
			}

			mfs, err := GetNewComponents(profile, opts)
			Expect(err).ToNot(HaveOccurred())

			ok, err := findFileInIgnition(mfs, expectedPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeTrue(), "expected path %q not found in ignition", expectedPath)
		})
	})
})

func findFileInIgnition(mfs *ManifestResultSet, filePath string) (bool, error) {
	result := igntypes.Config{}
	err := json.Unmarshal(mfs.MachineConfig.Spec.Config.Raw, &result)
	if err != nil {
		return false, err
	}

	for _, ignFile := range result.Storage.Files {
		if ignFile.Path == filePath {
			return true, nil
		}
	}
	return false, nil
}
