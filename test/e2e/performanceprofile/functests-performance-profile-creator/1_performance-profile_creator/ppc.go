package __performance_profile_creator

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/ghodss/yaml"

	"github.com/openshift/cluster-node-tuning-operator/cmd/performance-profile-creator/cmd"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
)

const (
	mustGatherPath       = "../../testdata/must-gather"
	expectedProfilesPath = "../../testdata/ppc-expected-profiles"
	expectedInfoPath     = "../../testdata/ppc-expected-info"
	ppcPath              = "../../../../../_output/performance-profile-creator"
)

var mustGatherFullPath = path.Join(mustGatherPath, "must-gather.bare-metal")

var defaultArgs = []string{
	"--disable-ht=false",
	"--mcp-name=worker-cnf",
	"--user-level-networking=false",
	"--profile-name=Performance",
	fmt.Sprintf("--must-gather-dir-path=%s", mustGatherFullPath),
}

var _ = Describe("[rfe_id:OCP-38968][ppc] Performance Profile Creator", func() {
	BeforeEach(func() {
		Expect(ppcPath).To(BeAnExistingFile())
		Expect(mustGatherFullPath).To(BeADirectory())
	})

	It("[test_id:OCP-40940] performance profile creator regression tests", func() {
		// directory base name => full path
		mustGatherDirs := getMustGatherDirs(mustGatherPath)
		// full profile path => arguments the profile was created with
		expectedProfiles := getExpectedProfiles(expectedProfilesPath, mustGatherDirs)

		for expectedProfilePath, args := range expectedProfiles {
			cmdArgs := []string{
				fmt.Sprintf("--disable-ht=%v", args.DisableHT),
				fmt.Sprintf("--mcp-name=%s", args.MCPName),
				fmt.Sprintf("--must-gather-dir-path=%s", args.MustGatherDirPath),
				fmt.Sprintf("--reserved-cpu-count=%d", args.ReservedCPUCount),
				fmt.Sprintf("--rt-kernel=%v", args.RTKernel),
				fmt.Sprintf("--split-reserved-cpus-across-numa=%v", args.SplitReservedCPUsAcrossNUMA),
			}

			if args.UserLevelNetworking != nil {
				cmdArgs = append(cmdArgs, fmt.Sprintf("--user-level-networking=%v", *args.UserLevelNetworking))
			}

			// do not pass empty strings for optional args
			if len(args.ProfileName) > 0 {
				cmdArgs = append(cmdArgs, fmt.Sprintf("--profile-name=%s", args.ProfileName))
			}
			if len(args.PowerConsumptionMode) > 0 {
				cmdArgs = append(cmdArgs, fmt.Sprintf("--power-consumption-mode=%s", args.PowerConsumptionMode))
			}
			if len(args.TMPolicy) > 0 {
				cmdArgs = append(cmdArgs, fmt.Sprintf("--topology-manager-policy=%s", args.TMPolicy))
			}

			out, err := testutils.ExecAndLogCommand(ppcPath, cmdArgs...)
			Expect(err).To(BeNil(), "failed to run ppc for '%s': %v", expectedProfilePath, err)

			profile := &performancev2.PerformanceProfile{}
			err = yaml.Unmarshal(out, profile)
			Expect(err).To(BeNil(), "failed to unmarshal the output yaml for '%s': %v", expectedProfilePath, err)

			bytes, err := ioutil.ReadFile(expectedProfilePath)
			Expect(err).To(BeNil(), "failed to read the expected yaml for '%s': %v", expectedProfilePath, err)

			expectedProfile := &performancev2.PerformanceProfile{}
			err = yaml.Unmarshal(bytes, expectedProfile)
			Expect(err).To(BeNil(), "failed to unmarshal the expected yaml for '%s': %v", expectedProfilePath, err)

			Expect(profile).To(BeEquivalentTo(expectedProfile), "regression test failed for '%s' case", expectedProfilePath)
		}
	})

	It("should describe the cluster from must-gather data in info mode", func() {
		// directory base name => full path
		mustGatherDirs := getMustGatherDirs(mustGatherPath)

		for name, path := range mustGatherDirs {
			cmdArgs := []string{
				"--info=json",
				fmt.Sprintf("--must-gather-dir-path=%s", path),
			}

			out, err := testutils.ExecAndLogCommand(ppcPath, cmdArgs...)
			Expect(err).To(BeNil(), "failed to run ppc for %q: %v", path, err)

			var cInfo cmd.ClusterInfo
			err = json.Unmarshal(out, &cInfo)
			Expect(err).To(BeNil(), "failed to unmarshal the output json for %q: %v", path, err)
			expectedClusterInfoPath := filepath.Join(expectedInfoPath, fmt.Sprintf("%s.json", name))
			bytes, err := ioutil.ReadFile(expectedClusterInfoPath)
			Expect(err).To(BeNil(), "failed to read the expected json for %q: %v", expectedClusterInfoPath, err)

			var expectedInfo cmd.ClusterInfo
			err = json.Unmarshal(bytes, &expectedInfo)
			Expect(err).To(BeNil(), "failed to unmarshal the expected json for '%s': %v", expectedClusterInfoPath, err)

			expectedInfo.Sort()

			Expect(cInfo).To(BeEquivalentTo(expectedInfo), "regression test failed for '%s' case", expectedClusterInfoPath)
		}
	})
	Context("Systems with Hyperthreading enabled", func() {
		It("[test_id:41419] Verify PPC script fails when reserved cpu count is 2 and requires to split across numa nodes", func() {
			ppcArgs := []string{
				"--reserved-cpu-count=2",
				"--split-reserved-cpus-across-numa=true",
				"--rt-kernel=false",
			}
			cmdArgs := append(defaultArgs, ppcArgs...)
			_, errData, _ := testutils.ExecAndLogCommandWithStderr(ppcPath, cmdArgs...)
			ppcErrorString := errorStringParser(errData)
			Expect(ppcErrorString).To(ContainSubstring("can't allocate odd number of CPUs from a NUMA Node"))
		})

		It("[test_id:41405] Verify PPC fails when splitting of reserved cpus and single numa-node policy is specified", func() {
			ppcArgs := []string{
				fmt.Sprintf("--reserved-cpu-count=%d", 2),
				fmt.Sprintf("--split-reserved-cpus-across-numa=%t", true),
				fmt.Sprintf("--topology-manager-policy=%s", "single-numa-node"),
				"--rt-kernel=false",
			}
			cmdArgs := append(defaultArgs, ppcArgs...)
			_, errData, _ := testutils.ExecAndLogCommandWithStderr(ppcPath, cmdArgs...)
			ppcErrorString := errorStringParser(errData)
			Expect(ppcErrorString).To(ContainSubstring("not appropriate to split reserved CPUs in case of topology-manager-policy: single-numa-node"))
		})

		It("[test_id:41420] Verify PPC fails when reserved cpu count is more than available cpus", func() {
			ppcArgs := []string{
				fmt.Sprintf("--reserved-cpu-count=%d", 100),
				"--rt-kernel=false",
			}
			cmdArgs := append(defaultArgs, ppcArgs...)
			_, errData, _ := testutils.ExecAndLogCommandWithStderr(ppcPath, cmdArgs...)
			ppcErrorString := errorStringParser(errData)
			Expect(ppcErrorString).To(ContainSubstring("please specify the reserved CPU count in the range"))
		})

		It("[test_id:41421] Verify PPC fails when odd number of reserved cpus are specified", func() {
			ppcArgs := []string{
				fmt.Sprintf("--reserved-cpu-count=%d", 5),
				"--rt-kernel=false",
			}
			cmdArgs := append(defaultArgs, ppcArgs...)
			_, errData, _ := testutils.ExecAndLogCommandWithStderr(ppcPath, cmdArgs...)
			ppcErrorString := errorStringParser(errData)
			Expect(ppcErrorString).To(ContainSubstring("can't allocate odd number of CPUs from a NUMA Node"))
		})
	})

	Context("Systems with Hyperthreading disabled", func() {
		It("[test_id:42035] verify PPC fails when splitting of reserved cpus and single numa-node policy is specified", func() {
			ppcArgs := []string{
				fmt.Sprintf("--reserved-cpu-count=%d", 2),
				fmt.Sprintf("--split-reserved-cpus-across-numa=%t", true),
				fmt.Sprintf("--topology-manager-policy=%s", "single-numa-node"),
				"--rt-kernel=false",
			}
			cmdArgs := append(defaultArgs, ppcArgs...)
			_, errData, _ := testutils.ExecAndLogCommandWithStderr(ppcPath, cmdArgs...)
			ppcErrorString := errorStringParser(errData)
			Expect(ppcErrorString).To(ContainSubstring("not appropriate to split reserved CPUs in case of topology-manager-policy: single-numa-node"))
		})
	})

	Context("with power consumption mode flag", func() {
		var extraArgs []string

		BeforeEach(func() {
			extraArgs = []string{}
			extraArgs = append(extraArgs, defaultArgs...)
			extraArgs = append(extraArgs, "--reserved-cpu-count=2")
		})

		Context("with default latency", func() {
			It("should set both realtime and highPowerConsumption workload hints to false", func() {
				extraArgs = append(extraArgs, "--rt-kernel=false")
				outData, _, err := testutils.ExecAndLogCommandWithStderr(ppcPath, extraArgs...)
				Expect(err).ToNot(HaveOccurred())

				profile := &performancev2.PerformanceProfile{}
				Expect(yaml.Unmarshal(outData, profile)).ToNot(HaveOccurred())
				Expect(profile.Spec.WorkloadHints).ToNot(BeNil())
				Expect(profile.Spec.WorkloadHints.RealTime).ToNot(BeNil())
				Expect(*profile.Spec.WorkloadHints.RealTime).To(BeFalse())
				Expect(profile.Spec.WorkloadHints.HighPowerConsumption).ToNot(BeNil())
				Expect(*profile.Spec.WorkloadHints.HighPowerConsumption).To(BeFalse())
			})

			Context("with realtime kernel", func() {
				BeforeEach(func() {
					extraArgs = append(extraArgs, "--rt-kernel=true")
				})

				It("should drop an error", func() {
					_, errData, err := testutils.ExecAndLogCommandWithStderr(ppcPath, extraArgs...)
					Expect(err).To(HaveOccurred())
					Expect(errData).ToNot(BeEmpty())
					Expect(string(errData)).To(ContainSubstring("power consumption modes together with the real-time kernel"))
				})
			})
		})

		Context("with low-latency", func() {
			BeforeEach(func() {
				extraArgs = append(extraArgs, "--rt-kernel=true")
				extraArgs = append(extraArgs, "--power-consumption-mode=low-latency")
			})

			It("should set realtime workload hint to true", func() {
				outData, _, err := testutils.ExecAndLogCommandWithStderr(ppcPath, extraArgs...)
				Expect(err).ToNot(HaveOccurred())

				profile := &performancev2.PerformanceProfile{}
				Expect(yaml.Unmarshal(outData, profile)).ToNot(HaveOccurred())
				Expect(profile.Spec.WorkloadHints).NotTo(BeNil())
				Expect(profile.Spec.WorkloadHints.RealTime).ToNot(BeNil())
				Expect(*profile.Spec.WorkloadHints.RealTime).To(BeTrue())
				Expect(profile.Spec.WorkloadHints.HighPowerConsumption).ToNot(BeNil())
				Expect(*profile.Spec.WorkloadHints.HighPowerConsumption).To(BeFalse())
			})
		})

		Context("with ultra-low-latency", func() {
			BeforeEach(func() {
				extraArgs = append(extraArgs, "--rt-kernel=true")
				extraArgs = append(extraArgs, "--power-consumption-mode=ultra-low-latency")
			})

			It("should set both realtime and highPowerConsumption workload hints to true", func() {
				outData, _, err := testutils.ExecAndLogCommandWithStderr(ppcPath, extraArgs...)
				Expect(err).ToNot(HaveOccurred())

				profile := &performancev2.PerformanceProfile{}
				Expect(yaml.Unmarshal(outData, profile)).ToNot(HaveOccurred())
				Expect(profile.Spec.WorkloadHints).NotTo(BeNil())
				Expect(profile.Spec.WorkloadHints.RealTime).ToNot(BeNil())
				Expect(*profile.Spec.WorkloadHints.RealTime).To(BeTrue())
				Expect(profile.Spec.WorkloadHints.HighPowerConsumption).ToNot(BeNil())
				Expect(*profile.Spec.WorkloadHints.HighPowerConsumption).To(BeTrue())
			})
		})
	})
})

func getMustGatherDirs(mustGatherPath string) map[string]string {
	Expect(mustGatherPath).To(BeADirectory())

	mustGatherDirs := make(map[string]string)
	mustGatherPathContent, err := ioutil.ReadDir(mustGatherPath)
	Expect(err).To(BeNil(), fmt.Errorf("can't list '%s' files: %v", mustGatherPath, err))

	for _, file := range mustGatherPathContent {
		fullFilePath := filepath.Join(mustGatherPath, file.Name())
		Expect(fullFilePath).To(BeADirectory())

		mustGatherDirs[file.Name()] = fullFilePath
	}

	return mustGatherDirs
}

func getExpectedProfiles(expectedProfilesPath string, mustGatherDirs map[string]string) map[string]cmd.ProfileCreatorArgs {
	Expect(expectedProfilesPath).To(BeADirectory())

	expectedProfilesPathContent, err := ioutil.ReadDir(expectedProfilesPath)
	Expect(err).To(BeNil(), fmt.Errorf("can't list '%s' files: %v", expectedProfilesPath, err))

	// read ppc params files
	ppcParams := make(map[string]cmd.ProfileCreatorArgs)
	for _, file := range expectedProfilesPathContent {
		if filepath.Ext(file.Name()) != ".json" {
			continue
		}

		fullFilePath := filepath.Join(expectedProfilesPath, file.Name())
		bytes, err := ioutil.ReadFile(fullFilePath)
		Expect(err).To(BeNil(), "failed to read the ppc params file for '%s': %v", fullFilePath, err)

		var ppcArgs cmd.ProfileCreatorArgs
		err = json.Unmarshal(bytes, &ppcArgs)
		Expect(err).To(BeNil(), "failed to decode the ppc params file for '%s': %v", fullFilePath, err)

		Expect(ppcArgs.MustGatherDirPath).ToNot(BeEmpty(), "must-gather arg missing for '%s'", fullFilePath)
		ppcArgs.MustGatherDirPath = path.Join(mustGatherPath, ppcArgs.MustGatherDirPath)
		Expect(ppcArgs.MustGatherDirPath).To(BeADirectory())

		profileKey := strings.TrimSuffix(file.Name(), filepath.Ext(file.Name()))
		ppcParams[profileKey] = ppcArgs
	}

	// pickup profile files
	expectedProfiles := make(map[string]cmd.ProfileCreatorArgs)
	for _, file := range expectedProfilesPathContent {
		if filepath.Ext(file.Name()) != ".yaml" {
			continue
		}
		profileKey := strings.TrimSuffix(file.Name(), filepath.Ext(file.Name()))
		ppcArgs, ok := ppcParams[profileKey]
		Expect(ok).To(BeTrue(), "can't find ppc params for the expected profile: '%s'", file.Name())

		fullFilePath := filepath.Join(expectedProfilesPath, file.Name())
		expectedProfiles[fullFilePath] = ppcArgs
	}

	return expectedProfiles
}

// PPC stderr parser
func errorStringParser(errData []byte) string {
	stdError := string(errData)
	for _, line := range strings.Split(stdError, "\n") {
		if strings.Contains(line, "Error: ") {
			return line
		}
	}
	return ""
}
