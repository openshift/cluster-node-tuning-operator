package __performance_ppc

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"k8s.io/utils/cpuset"
	"sigs.k8s.io/yaml"
)

type PPCTest struct {
	PodmanMakeOptions func(args []string) []string
	PodmanBinary      string
}

type PPCSession struct {
	*gexec.Session
}

func (p *PPCTest) MakeOptions(args []string) []string {
	return p.PodmanMakeOptions(args)
}

func (p *PPCTest) PodmanAsUserBase(args []string, noEvents, noCache bool) (*PPCSession, error) {
	var command *exec.Cmd
	podmanOptions := p.MakeOptions(args)
	podmanBinary := p.PodmanBinary
	fmt.Printf("Running: %s %s\n", podmanBinary, strings.Join(podmanOptions, " "))
	command = exec.Command(podmanBinary, podmanOptions...)
	session, err := gexec.Start(command, GinkgoWriter, GinkgoWriter)
	if err != nil {
		return nil, err
	}

	return &PPCSession{session}, nil
}

type PPCTestIntegration struct {
	PPCTest
}

func (p *PPCTestIntegration) MakeOptions(args []string) []string {
	return args
}
func PPCTestCreateUtil() *PPCTestIntegration {
	p := &PPCTestIntegration{
		PPCTest: PPCTest{
			PodmanBinary: DefaultPodmanBinaryPath,
		},
	}
	p.PodmanMakeOptions = p.MakeOptions
	return p
}

var _ = Describe("[rfe_id: 38968] PerformanceProfile setup helper and platform awareness", Label(string(label.PerformanceProfileCreator)), func() {
	mustgatherDir := testutils.MustGatherDir
	mcpName := "worker"
	ntoImage := testutils.NTOImage
	Context("PPC Sanity Tests", Label(string(label.Tier0)), func() {
		ppcIntgTest := PPCTestCreateUtil()
		defaultArgs := []string{
			"run",
			"--entrypoint",
			"performance-profile-creator",
			"-v",
		}
		It("[test_id:40940] Performance Profile regression tests", func() {
			pp := &performancev2.PerformanceProfile{}
			var reservedCpuCount = 2
			cmdArgs := []string{
				fmt.Sprintf("%s:%s:z", mustgatherDir, mustgatherDir),
				ntoImage,
				fmt.Sprintf("--mcp-name=%s", mcpName),
				fmt.Sprintf("--reserved-cpu-count=%d", reservedCpuCount),
				"--rt-kernel=true",
				"--power-consumption-mode=low-latency",
				"--split-reserved-cpus-across-numa=false",
				fmt.Sprintf("--must-gather-dir-path=%s", mustgatherDir),
			}
			podmanArgs := append(defaultArgs, cmdArgs...)
			session, err := ppcIntgTest.PodmanAsUserBase(podmanArgs, false, false)
			Expect(err).ToNot(HaveOccurred(), "Podman command failed")
			output := session.Wait(20).Out.Contents()
			err = yaml.Unmarshal(output, pp)
			Expect(err).ToNot(HaveOccurred(), "Unable to marshal the ppc output")
			reservedCpus, err := cpuset.Parse(string(*pp.Spec.CPU.Reserved))
			Expect(err).ToNot(HaveOccurred(), "Unable to parse cpus")
			totalReservedCpus := reservedCpus.Size()
			Expect(totalReservedCpus).To(Equal(reservedCpuCount))
			Expect(*pp.Spec.RealTimeKernel.Enabled).To(BeTrue())
			Expect(*pp.Spec.WorkloadHints.RealTime).To(BeTrue())
			Expect(*pp.Spec.NUMA.TopologyPolicy).To(Equal("restricted"))
			Eventually(session).Should(gexec.Exit(0))
		})
		It("[test_id:41405] Verify PPC script fails when the splitting of reserved cpus and single numa-node policy is specified", func() {
			cmdArgs := []string{
				fmt.Sprintf("%s:%s:z", mustgatherDir, mustgatherDir),
				ntoImage,
				fmt.Sprintf("--mcp-name=%s", mcpName),
				"--reserved-cpu-count=2",
				"--rt-kernel=true",
				"--power-consumption-mode=low-latency",
				"--split-reserved-cpus-across-numa=true",
				"--topology-manager-policy=single-numa-node",
				fmt.Sprintf("--must-gather-dir-path=%s", mustgatherDir),
			}
			podmanArgs := append(defaultArgs, cmdArgs...)
			session, err := ppcIntgTest.PodmanAsUserBase(podmanArgs, false, false)
			Expect(err).ToNot(HaveOccurred(), "Podman command failed")
			output := session.Wait(20).Err.Contents()
			errString := "Error: failed to obtain data from flags not appropriate to split reserved CPUs in case of topology-manager-policy: single-numa-node"
			ok, err := regexp.MatchString(errString, string(output))
			Expect(err).ToNot(HaveOccurred())
			if ok {
				testlog.Info(errString)
			}
			Eventually(session).Should(gexec.Exit(1))
		})

		It("[test_id:41419] Verify PPC script fails when reserved cpu count is 2 and requires to split across numa nodes", func() {
			cmdArgs := []string{
				fmt.Sprintf("%s:%s:z", mustgatherDir, mustgatherDir),
				ntoImage,
				fmt.Sprintf("--mcp-name=%s", mcpName),
				"--reserved-cpu-count=2",
				"--rt-kernel=true",
				"--power-consumption-mode=low-latency",
				"--split-reserved-cpus-across-numa=true",
				fmt.Sprintf("--must-gather-dir-path=%s", mustgatherDir),
			}
			podmanArgs := append(defaultArgs, cmdArgs...)
			session, err := ppcIntgTest.PodmanAsUserBase(podmanArgs, false, false)
			Expect(err).ToNot(HaveOccurred(), "Podman command failed")
			output := session.Wait(20).Err.Contents()
			errString := "Error: failed to compute the reserved and isolated CPUs: can't allocate odd number of CPUs from a NUMA Node"
			ok, err := regexp.MatchString(errString, string(output))
			Expect(err).ToNot(HaveOccurred(), "did not fail with Expected:%s failure", errString)
			if ok {
				testlog.Info(errString)
			}
			Eventually(session).Should(gexec.Exit(1))
		})

		It("[test_id:41420] Verify PPC script fails when reserved cpu count is more than available cpus", func() {
			cmdArgs := []string{
				fmt.Sprintf("%s:%s:z", mustgatherDir, mustgatherDir),
				ntoImage,
				fmt.Sprintf("--mcp-name=%s", mcpName),
				"--reserved-cpu-count=1000",
				"--rt-kernel=true",
				"--power-consumption-mode=low-latency",
				"--split-reserved-cpus-across-numa=true",
				fmt.Sprintf("--rt-kernel=%t", true),
				fmt.Sprintf("--must-gather-dir-path=%s", mustgatherDir),
			}
			podmanArgs := append(defaultArgs, cmdArgs...)
			session, err := ppcIntgTest.PodmanAsUserBase(podmanArgs, false, false)
			Expect(err).ToNot(HaveOccurred(), "Podman command failed")
			output := session.Wait(20).Err.Contents()
			errString := "Error: failed to compute the reserved and isolated CPUs: please specify the reserved CPU count in the range [1,3]"
			ok, err := regexp.MatchString(errString, string(output))
			Expect(err).ToNot(HaveOccurred(), "did not fail with Expected:%s failure", errString)
			if ok {
				testlog.Info(errString)
			}
			Eventually(session).Should(gexec.Exit(1))
		})

		It("[test_id: 54187] PPC generates profile with PerPodPowerManagement workload hint", func() {
			pp := &performancev2.PerformanceProfile{}
			cmdArgs := []string{
				fmt.Sprintf("%s:%s:z", mustgatherDir, mustgatherDir),
				ntoImage,
				fmt.Sprintf("--mcp-name=%s", mcpName),
				"--reserved-cpu-count=4",
				"--rt-kernel=true",
				"--per-pod-power-management",
				"--power-consumption-mode=low-latency",
				"--split-reserved-cpus-across-numa=true",
				fmt.Sprintf("--must-gather-dir-path=%s", mustgatherDir),
			}
			podmanArgs := append(defaultArgs, cmdArgs...)
			session, err := ppcIntgTest.PodmanAsUserBase(podmanArgs, false, false)
			Expect(err).ToNot(HaveOccurred(), "Podman command failed")
			output := session.Wait(20).Out.Contents()
			err = yaml.Unmarshal(output, pp)
			Expect(err).ToNot(HaveOccurred(), "Unable to marshal the ppc output")
			Expect(*pp.Spec.WorkloadHints.PerPodPowerManagement).To(BeTrue())
			Expect(*pp.Spec.WorkloadHints.HighPowerConsumption).To(BeFalse())
		})

		It("[test_id: 54188] PPC Fails when per-pod-powermanagement is used with ultra-low-latency", func() {
			cmdArgs := []string{
				fmt.Sprintf("%s:%s:z", mustgatherDir, mustgatherDir),
				ntoImage,
				fmt.Sprintf("--mcp-name=%s", mcpName),
				"--reserved-cpu-count=4",
				"--rt-kernel=true",
				"--per-pod-power-management",
				"--power-consumption-mode=ultra-low-latency",
				"--split-reserved-cpus-across-numa=true",
				fmt.Sprintf("--must-gather-dir-path=%s", mustgatherDir),
			}
			podmanArgs := append(defaultArgs, cmdArgs...)
			session, err := ppcIntgTest.PodmanAsUserBase(podmanArgs, false, false)
			Expect(err).ToNot(HaveOccurred(), "Podman command failed")
			output := session.Wait(20).Err.Contents()
			errString := `please use one of \[default low-latency\] power consumption modes together with the perPodPowerManagement`
			ok, err := regexp.MatchString(errString, string(output))
			Expect(err).ToNot(HaveOccurred(), "did not fail with Expected:%s failure", errString)
			if ok {
				testlog.Info(errString)
			}
			Eventually(session).Should(gexec.Exit(1))
		})
	})
})
