package __performance

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/cpuset"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/tuned"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	e2etuned "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/tuned"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
	"github.com/openshift/cluster-node-tuning-operator/test/framework"
)

var (
	cs = framework.NewClientSet()
)

var _ = Describe("[performance] Checking IRQBalance settings", Ordered, func() {
	var workerRTNodes []corev1.Node
	var targetNode *corev1.Node
	var profile, initialProfile *performancev2.PerformanceProfile
	var performanceMCP string
	var err error

	BeforeEach(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}

		workerRTNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		initialProfile = profile.DeepCopy()

		performanceMCP, err = mcps.GetByProfile(profile)
		Expect(err).ToNot(HaveOccurred())

		// Verify that worker and performance MCP have updated state equals to true
		for _, mcpName := range []string{testutils.RoleWorker, performanceMCP} {
			mcps.WaitForCondition(mcpName, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
		}

		nodeIdx := pickNodeIdx(workerRTNodes)
		targetNode = &workerRTNodes[nodeIdx]
		By(fmt.Sprintf("verifying worker node %q", targetNode.Name))
	})

	Context("Verify GloballyDisableIrqLoadBalancing Spec field", Label(string(label.Tier0)), func() {
		It("[test_id:36150] Verify that IRQ load balancing is enabled/disabled correctly", func() {

			irqLoadBalancingDisabled := profile.Spec.GloballyDisableIrqLoadBalancing != nil && *profile.Spec.GloballyDisableIrqLoadBalancing

			Expect(profile.Spec.CPU.Isolated).NotTo(BeNil(), "expected isolated CPUs, found none")
			isolatedCPUSet, err := cpuset.Parse(string(*profile.Spec.CPU.Isolated))
			Expect(err).ToNot(HaveOccurred())

			By(fmt.Sprintf("verifying the behavior with irqLoadBalancingDisabled=%v isolatedCPUSet=%v", irqLoadBalancingDisabled, isolatedCPUSet))
			verifyNodes := func(irqLoadBalancingDisabled bool) error {
				var expectedBannedCPUs cpuset.CPUSet
				if irqLoadBalancingDisabled {
					expectedBannedCPUs = isolatedCPUSet
				} else {
					expectedBannedCPUs = cpuset.New()
				}
				for _, node := range workerRTNodes {
					By(fmt.Sprintf("verifying worker node %q", node.Name))

					condStatus := corev1.ConditionUnknown
					EventuallyWithOffset(1, context.TODO(), func() bool {
						tunedProfile, err := e2etuned.GetProfile(context.TODO(), testclient.DataPlaneClient, components.NamespaceNodeTuningOperator, node.Name)
						Expect(err).ToNot(HaveOccurred(), "failed to get Tuned Profile for node %q", node.Name)
						for _, cond := range tunedProfile.Status.Conditions {
							if cond.Type != tunedv1.TunedProfileApplied {
								continue
							}
							if cond.Status != corev1.ConditionTrue {
								condStatus = cond.Status
								return false
							}
							return true
						}
						return false
					}).WithPolling(time.Second*10).WithTimeout(3*time.Minute).Should(BeTrue(), "Tuned Profile for node %q was not applied successfully conditionStatus=%q", node.Name, condStatus)

					bannedCPUs, err := getIrqBalanceBannedCPUs(context.TODO(), &node)
					Expect(err).ToNot(HaveOccurred(), "failed to extract the banned CPUs from node %s", node.Name)

					By(fmt.Sprintf("node %q banned CPUs: expected %v detected %v", node.Name, expectedBannedCPUs, bannedCPUs))

					if !bannedCPUs.Equals(expectedBannedCPUs) {
						return fmt.Errorf("banned CPUs %q do not match the expected mask %q on node %q",
							bannedCPUs, expectedBannedCPUs, node.Name)
					}

					smpAffinitySet, err := nodes.GetDefaultSmpAffinitySet(context.TODO(), &node)
					Expect(err).ToNot(HaveOccurred(), "failed to get default smp affinity")

					onlineCPUsSet, err := nodes.GetOnlineCPUsSet(context.TODO(), &node)
					Expect(err).ToNot(HaveOccurred(), "failed to get Online CPUs list")

					By(fmt.Sprintf("node %q SMP affinity set %v online CPUs set %v", node.Name, smpAffinitySet, onlineCPUsSet))

					if irqLoadBalancingDisabled {
						if !smpAffinitySet.Equals(onlineCPUsSet.Difference(isolatedCPUSet)) {
							return fmt.Errorf("found default_smp_affinity %v, expected %v",
								smpAffinitySet, onlineCPUsSet.Difference(isolatedCPUSet))
						}
					} else {
						if !smpAffinitySet.Equals(onlineCPUsSet) {
							return fmt.Errorf("found default_smp_affinity %v, expected %v",
								smpAffinitySet, onlineCPUsSet)
						}
					}
				}
				return nil
			}

			defer func() { // return initial configuration
				By("reverting the profile into its initial state")
				profile.Spec = initialProfile.Spec
				Expect(testclient.ControlPlaneClient.Update(context.TODO(), profile)).ToNot(HaveOccurred())
				// in the initial profile `GloballyDisableIrqLoadBalancing` value should be false,
				// so we expect the banned cpu set to be empty
				Eventually(verifyNodes).WithArguments(false).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).ShouldNot(HaveOccurred())
			}()

			err = verifyNodes(irqLoadBalancingDisabled)
			Expect(err).ToNot(HaveOccurred())

			irqLoadBalancingDisabled = !irqLoadBalancingDisabled
			profile.Spec.GloballyDisableIrqLoadBalancing = &irqLoadBalancingDisabled

			By(fmt.Sprintf("Modifying profile: irqLoadBalancingDisabled switched to %v", irqLoadBalancingDisabled))

			Expect(testclient.ControlPlaneClient.Update(context.TODO(), profile)).ToNot(HaveOccurred())
			Eventually(verifyNodes).WithArguments(irqLoadBalancingDisabled).WithPolling(10 * time.Second).WithTimeout(1 * time.Minute).ShouldNot(HaveOccurred())
		})
	})

	Context("Verify irqbalance configuration handling", Label(string(label.Tier0)), func() {
		It("Should not overwrite the banned CPU set on tuned restart", func() {
			if profile.Status.RuntimeClass == nil {
				Skip("runtime class not generated")
			}

			if tuned.IsIRQBalancingGloballyDisabled(profile) {
				Skip("this test needs dynamic IRQ balancing")
			}

			targetNodeIdx := pickNodeIdx(workerRTNodes)
			targetNode = &workerRTNodes[targetNodeIdx]
			Expect(targetNode).ToNot(BeNil(), "missing target node")
			By(fmt.Sprintf("verifying worker node %q", targetNode.Name))

			irqAffBegin, err := getIrqDefaultSMPAffinity(context.TODO(), targetNode)
			Expect(err).ToNot(HaveOccurred(), "failed to extract the default IRQ affinity from node %q", targetNode.Name)
			testlog.Infof("IRQ Default affinity on %q when test begins: {%s}", targetNode.Name, irqAffBegin)

			bannedCPUs, err := getIrqBalanceBannedCPUs(context.TODO(), targetNode)
			Expect(err).ToNot(HaveOccurred(), "failed to extract the banned CPUs from node %q", targetNode.Name)
			testlog.Infof("banned CPUs on %q when test begins: {%s}", targetNode.Name, bannedCPUs.String())

			smpAffinitySet, err := nodes.GetDefaultSmpAffinitySet(context.TODO(), targetNode)
			Expect(err).ToNot(HaveOccurred(), "failed to get default smp affinity")

			onlineCPUsSet, err := nodes.GetOnlineCPUsSet(context.TODO(), targetNode)
			Expect(err).ToNot(HaveOccurred(), "failed to get Online CPUs list")

			// Mask the smpAffinitySet according to the current onlineCpuSet
			// as smp_default_affinity is not online cpu aware
			smpAffinitySet = smpAffinitySet.Intersection(onlineCPUsSet)

			// expect no irqbalance run in the system already, AKA start from pristine conditions.
			// This is not an hard requirement, just the easier state to manage and check
			Expect(smpAffinitySet.Equals(onlineCPUsSet)).To(BeTrue(), "found default_smp_affinity %v, expected %v - IRQBalance already run?", smpAffinitySet, onlineCPUsSet)

			cpuRequest := 2 // minimum amount to be reasonably sure we're SMT-aligned
			annotations := map[string]string{
				"irq-load-balancing.crio.io": "disable",
			}
			testpod := getTestPodWithProfileAndAnnotations(profile, annotations, cpuRequest)
			testpod.Spec.NodeName = targetNode.Name

			data, _ := json.Marshal(testpod)
			testlog.Infof("using testpod:\n%s", string(data))

			err = testclient.DataPlaneClient.Create(context.TODO(), testpod)
			Expect(err).ToNot(HaveOccurred())
			defer func() {
				if testpod != nil {
					testlog.Infof("deleting pod %q", testpod.Name)
					deleteTestPod(context.TODO(), testpod)
				}
				bannedCPUs, err := getIrqBalanceBannedCPUs(context.TODO(), targetNode)
				Expect(err).ToNot(HaveOccurred(), "failed to extract the banned CPUs from node %q", targetNode.Name)

				testlog.Infof("banned CPUs on %q when test ends: {%s}", targetNode.Name, bannedCPUs.String())

				irqAffBegin, err := getIrqDefaultSMPAffinity(context.TODO(), targetNode)
				Expect(err).ToNot(HaveOccurred(), "failed to extract the default IRQ affinity from node %q", targetNode.Name)

				testlog.Infof("IRQ Default affinity on %q when test ends: {%s}", targetNode.Name, irqAffBegin)
			}()

			testpod, err = pods.WaitForCondition(context.TODO(), client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
			logEventsForPod(testpod)
			Expect(err).ToNot(HaveOccurred())

			// now we have something in the IRQBalance cpu list. Let's make sure the restart doesn't overwrite this data.
			postCreateBannedCPUs, err := getIrqBalanceBannedCPUs(context.TODO(), targetNode)
			Expect(err).ToNot(HaveOccurred(), "failed to extract the banned CPUs from node %q", targetNode.Name)
			testlog.Infof("banned CPUs on %q just before the tuned restart: {%s}", targetNode.Name, postCreateBannedCPUs.String())

			Expect(postCreateBannedCPUs.IsEmpty()).To(BeFalse(), "banned CPUs %v should not be empty on node %q", postCreateBannedCPUs, targetNode.Name)

			By(fmt.Sprintf("getting a TuneD Pod running on node %s", targetNode.Name))
			pod, err := util.GetTunedForNode(cs, targetNode)
			Expect(err).NotTo(HaveOccurred())

			By(fmt.Sprintf("causing a restart of the tuned pod (deleting the pod) on %s", targetNode.Name))
			_, _, err = util.ExecAndLogCommand("oc", "delete", "pod", "--wait=true", "-n", pod.Namespace, pod.Name)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() error {
				By(fmt.Sprintf("getting again a TuneD Pod running on node %s", targetNode.Name))
				pod, err = util.GetTunedForNode(cs, targetNode)
				if err != nil {
					return err
				}

				By(fmt.Sprintf("waiting for the TuneD daemon running on node %s", targetNode.Name))
				_, err = util.WaitForCmdInPod(5*time.Second, 5*time.Minute, pod, "test", "-e", "/run/tuned/tuned.pid")
				return err
			}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).ShouldNot(HaveOccurred())

			By(fmt.Sprintf("re-verifying worker node %q after TuneD restart", targetNode.Name))
			postRestartBannedCPUs, err := getIrqBalanceBannedCPUs(context.TODO(), targetNode)
			Expect(err).ToNot(HaveOccurred(), "failed to extract the banned CPUs from node %q", targetNode.Name)
			testlog.Infof("banned CPUs on %q after the tuned restart: {%s}", targetNode.Name, postRestartBannedCPUs.String())

			Expect(postRestartBannedCPUs.List()).To(Equal(postCreateBannedCPUs.List()), "banned CPUs changed post tuned restart on node %q", postRestartBannedCPUs.List(), targetNode.Name)
		})

		It("Should store empty cpu mask in the backup file", func() {
			// crio stores the irqbalance CPU ban list in the backup file once, at startup, if the file doesn't exist.
			// This _likely_ means the first time the provisioned node boots, and in this case is _likely_ the node
			// has not any IRQ pinning, thus the saved CPU ban list is the empty list. But we don't control nor declare this state.
			// It's all best effort.

			nodeIdx := pickNodeIdx(workerRTNodes)
			node := &workerRTNodes[nodeIdx]
			By(fmt.Sprintf("verifying worker node %q", node.Name))

			By(fmt.Sprintf("Checking the default IRQ affinity on node %q", node.Name))
			smpAffinitySet, err := nodes.GetDefaultSmpAffinitySet(context.TODO(), node)
			Expect(err).ToNot(HaveOccurred(), "failed to get default smp affinity")

			By(fmt.Sprintf("Checking the online CPU Set on node %q", node.Name))
			onlineCPUsSet, err := nodes.GetOnlineCPUsSet(context.TODO(), node)
			Expect(err).ToNot(HaveOccurred(), "failed to get Online CPUs list")

			// Mask the smpAffinitySet according to the current onlineCpuSet
			// as smp_default_affinity is not online cpu aware
			smpAffinitySet = smpAffinitySet.Intersection(onlineCPUsSet)

			// expect no irqbalance run in the system already, AKA start from pristine conditions.
			// This is not an hard requirement, just the easier state to manage and check
			Expect(smpAffinitySet.Equals(onlineCPUsSet)).To(BeTrue(), "found default_smp_affinity %v, expected %v - IRQBalance already run?", smpAffinitySet, onlineCPUsSet)

			origBannedCPUsFile := "/etc/sysconfig/orig_irq_banned_cpus"
			By(fmt.Sprintf("Checking content of %q on node %q", origBannedCPUsFile, node.Name))
			fullPath := filepath.Join("/", "rootfs", origBannedCPUsFile)
			output, err := nodes.ExecCommand(context.TODO(), node, []string{"/usr/bin/cat", fullPath})
			Expect(err).ToNot(HaveOccurred())
			out := testutils.ToString(output)
			out = strings.TrimSuffix(out, "\r\n")
			Expect(out).To(Equal("0"), "file %s does not contain the expect output; expected=0 actual=%s", fullPath, out)
		})
	})
})

// nodes.BannedCPUs fails (!!!) if the current banned list is empty because, deep down, ExecCommandOnNode expects non-empty stdout.
// In turn, we do this to at least have a chance to detect failed commands vs failed to execute commands (we had this issue in
// not-so-distant past, legit command output lost somewhere in the communication). Fixing ExecCommandOnNode isn't trivial and
// require close attention. For the time being we reimplement a form of nodes.BannedCPUs which can handle empty ban list.
func getIrqBalanceBannedCPUs(ctx context.Context, node *corev1.Node) (cpuset.CPUSet, error) {
	cmd := []string{"cat", "/rootfs/etc/sysconfig/irqbalance"}
	out, err := nodes.ExecCommand(ctx, node, cmd)
	if err != nil {
		return cpuset.New(), err
	}
	conf := testutils.ToString(out)

	keyValue := findIrqBalanceBannedCPUsVarFromConf(conf)
	if len(keyValue) == 0 {
		// can happen: everything commented out (default if no tuning ever)
		testlog.Warningf("cannot find the CPU ban list in the configuration (\n%s)\n", conf)
		return cpuset.New(), nil
	}

	testlog.Infof("banned CPUs setting: %q", keyValue)

	items := strings.FieldsFunc(keyValue, func(c rune) bool {
		return c == '='
	})
	if len(items) != 2 {
		return cpuset.New(), fmt.Errorf("malformed CPU ban list in the configuration")
	}

	bannedCPUs := unquote(strings.TrimSpace(items[1]))
	testlog.Infof("banned CPUs: %q", bannedCPUs)

	banned, err := components.CPUMaskToCPUSet(bannedCPUs)
	if err != nil {
		return cpuset.New(), fmt.Errorf("failed to parse the banned CPUs: %v", err)
	}

	return banned, nil
}

func getIrqDefaultSMPAffinity(ctx context.Context, node *corev1.Node) (string, error) {
	cmd := []string{"cat", "/rootfs/proc/irq/default_smp_affinity"}
	out, err := nodes.ExecCommand(ctx, node, cmd)
	if err != nil {
		return "", err
	}
	output := testutils.ToString(out)
	return output, nil
}

func findIrqBalanceBannedCPUsVarFromConf(conf string) string {
	scanner := bufio.NewScanner(strings.NewReader(conf))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "IRQBALANCE_BANNED_CPUS") {
			continue
		}
		return line
	}
	return ""
}

func pickNodeIdx(nodes []corev1.Node) int {
	name, ok := os.LookupEnv("E2E_PAO_TARGET_NODE")
	if !ok {
		return 0 // "random" default
	}
	for idx := range nodes {
		if nodes[idx].Name == name {
			testlog.Infof("node %q found among candidates, picking", name)
			return idx
		}
	}
	testlog.Infof("node %q not found among candidates, fall back to random one", name)
	return 0 // "safe" default
}

func unquote(s string) string {
	q := "\""
	s = strings.TrimPrefix(s, q)
	s = strings.TrimSuffix(s, q)
	return s
}
