package __performance_workloadhints

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"time"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/ini.v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	"k8s.io/utils/cpuset"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/tuned"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	utilstuned "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/tuned"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

const (
	cgroupRoot = "/rootfs/sys/fs/cgroup"
)

var _ = Describe("[rfe_id:49062][workloadHints] Telco friendly workload specific PerformanceProfile API", func() {
	var workerRTNodes []corev1.Node
	var profile, initialProfile *performancev2.PerformanceProfile
	var performanceMCP string
	var err error

	nodeLabel := testutils.NodeSelectorLabels

	BeforeEach(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}

		workerRTNodes = getUpdatedNodes()
		profile, err = profiles.GetByNodeLabels(nodeLabel)
		Expect(err).ToNot(HaveOccurred())
		klog.Infof("using profile: %q", profile.Name)
		performanceMCP, err = mcps.GetByProfile(profile)
		Expect(err).ToNot(HaveOccurred())
		klog.Infof("using performanceMCP: %q", performanceMCP)

		// Verify that worker and performance MCP have updated state equals to true
		for _, mcpName := range []string{testutils.RoleWorker, performanceMCP} {
			mcps.WaitForCondition(mcpName, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
		}
	})

	Context("WorkloadHints", func() {
		var testpod *corev1.Pod
		BeforeEach(func() {
			By("Saving the old performance profile")
			initialProfile = profile.DeepCopy()
		})
		When("workloadHint RealTime is disabled", func() {
			It("should update kernel arguments and tuned accordingly to realTime Hint enabled by default", func() {
				By("Modifying profile")
				profile.Spec.WorkloadHints = nil

				profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
					Enabled: pointer.Bool(false),
				}

				By("Updating the performance profile")
				profiles.UpdateWithRetry(profile)

				By("Applying changes in performance profile and waiting until mcp will start updating")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

				By("Waiting for MCP being updated")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

				stalldEnabled, rtKernel := true, false
				noHzParam := fmt.Sprintf("nohz_full=%s", *profile.Spec.CPU.Isolated)
				sysctlMap := map[string]string{
					"kernel.hung_task_timeout_secs": "600",
					"kernel.nmi_watchdog":           "0",
					"kernel.sched_rt_runtime_us":    "-1",
					"vm.stat_interval":              "10",
				}
				kernelParameters := []string{noHzParam, "tsc=reliable", "nosoftlockup", "nmi_watchdog=0", "mce=off", "skew_tick=1"}

				wg := sync.WaitGroup{}
				By("Waiting for TuneD to start on nodes")
				for i := 0; i < len(workerRTNodes); i++ {
					node := &workerRTNodes[i]
					wg.Add(1)
					go func() {
						defer GinkgoRecover()
						defer wg.Done()

						pod, err := utilstuned.GetPod(context.TODO(), node)
						Expect(err).ToNot(HaveOccurred())
						cmd := []string{"test", "-e", "/run/tuned/tuned.pid"}
						_, err = util.WaitForCmdInPod(5*time.Second, 5*time.Minute, pod, cmd...)
						Expect(err).ToNot(HaveOccurred())

						By(fmt.Sprintf("Waiting for stalld to be running on %q", node.Name))
						Expect(utilstuned.WaitForStalldTo(context.TODO(), stalldEnabled, 10*time.Second, 1*time.Minute, node)).ToNot(HaveOccurred(),
							fmt.Sprintf("stalld is not running on %q when it should", node.Name))

						By(fmt.Sprintf("Checking TuneD parameters on %q", node.Name))
						utilstuned.CheckParameters(context.TODO(), node, sysctlMap, kernelParameters, stalldEnabled, rtKernel)
					}()
				}
				wg.Wait()
			})
		})

		When("RealTime Workload with RealTime Kernel set to false", func() {
			It("[test_id:50991][crit:high][vendor:cnf-qe@redhat.com][level:acceptance]should update kernel arguments and tuned accordingly", func() {
				By("Modifying profile")

				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption: pointer.Bool(false),
					RealTime:             pointer.Bool(true),
				}
				profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
					Enabled: pointer.Bool(false),
				}

				By("Updating the performance profile")
				profiles.UpdateWithRetry(profile)

				By("Applying changes in performance profile and waiting until mcp will start updating")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

				By("Waiting for MCP being updated")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

				stalldEnabled, rtKernel := true, false
				noHzParam := fmt.Sprintf("nohz_full=%s", *profile.Spec.CPU.Isolated)
				sysctlMap := map[string]string{
					"kernel.hung_task_timeout_secs": "600",
					"kernel.nmi_watchdog":           "0",
					"kernel.sched_rt_runtime_us":    "-1",
					"vm.stat_interval":              "10",
				}
				kernelParameters := []string{noHzParam, "tsc=reliable", "nosoftlockup", "nmi_watchdog=0", "mce=off", "skew_tick=1"}

				wg := sync.WaitGroup{}
				By("Waiting for TuneD to start on nodes")
				for i := 0; i < len(workerRTNodes); i++ {
					node := &workerRTNodes[i]
					wg.Add(1)
					go func() {
						defer GinkgoRecover()
						defer wg.Done()

						pod, err := utilstuned.GetPod(context.TODO(), node)
						Expect(err).ToNot(HaveOccurred())
						cmd := []string{"test", "-e", "/run/tuned/tuned.pid"}
						_, err = util.WaitForCmdInPod(5*time.Second, 5*time.Minute, pod, cmd...)
						Expect(err).ToNot(HaveOccurred())

						By(fmt.Sprintf("Waiting for stalld to be running on %q", node.Name))
						Expect(utilstuned.WaitForStalldTo(context.TODO(), stalldEnabled, 10*time.Second, 1*time.Minute, node)).ToNot(HaveOccurred(),
							fmt.Sprintf("stalld is not running on %q when it should", node.Name))

						By(fmt.Sprintf("Checking TuneD parameters on %q", node.Name))
						utilstuned.CheckParameters(context.TODO(), node, sysctlMap, kernelParameters, stalldEnabled, rtKernel)
					}()
				}
				wg.Wait()
			})
		})
		When("HighPower Consumption workload enabled", func() {
			It("[test_id:50992][crit:high][vendor:cnf-qe@redhat.com][level:acceptance]should update kernel arguments and tuned accordingly", func() {
				By("Modifying profile")
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption: pointer.Bool(true),
					RealTime:             pointer.Bool(false),
				}

				profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
					Enabled: pointer.Bool(false),
				}

				By("Updating the performance profile")
				profiles.UpdateWithRetry(profile)

				By("Applying changes in performance profile and waiting until mcp will start updating")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

				By("Waiting for MCP being updated")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

				stalldEnabled, rtKernel := false, false
				sysctlMap := map[string]string{
					"kernel.hung_task_timeout_secs": "600",
					"kernel.nmi_watchdog":           "0",
					"kernel.sched_rt_runtime_us":    "950000",
					"vm.stat_interval":              "10",
				}
				kernelParameters := []string{"processor.max_cstate=1", "intel_idle.max_cstate=0"}

				wg := sync.WaitGroup{}
				By("Waiting for TuneD to start on nodes")
				for i := 0; i < len(workerRTNodes); i++ {
					node := &workerRTNodes[i]
					wg.Add(1)
					go func() {
						defer GinkgoRecover()
						defer wg.Done()

						pod, err := utilstuned.GetPod(context.TODO(), node)
						Expect(err).ToNot(HaveOccurred())
						cmd := []string{"test", "-e", "/run/tuned/tuned.pid"}
						_, err = util.WaitForCmdInPod(5*time.Second, 5*time.Minute, pod, cmd...)
						Expect(err).ToNot(HaveOccurred())

						By(fmt.Sprintf("Waiting for stalld to NOT be running on %q", node.Name))
						Expect(utilstuned.WaitForStalldTo(context.TODO(), stalldEnabled, 10*time.Second, 1*time.Minute, node)).ToNot(HaveOccurred(),
							fmt.Sprintf("stalld should not running on node %q ", node.Name))

						By(fmt.Sprintf("Checking TuneD parameters on %q", node.Name))
						utilstuned.CheckParameters(context.TODO(), node, sysctlMap, kernelParameters, stalldEnabled, rtKernel)
					}()
				}
				wg.Wait()
			})
		})

		When("realtime and high power consumption enabled", func() {
			It("[test_id:50993][crit:high][vendor:cnf-qe@redhat.com][level:acceptance]should update kernel arguments and tuned accordingly", func() {
				currentWorkloadHints := profile.Spec.WorkloadHints
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption:  pointer.Bool(true),
					RealTime:              pointer.Bool(true),
					PerPodPowerManagement: pointer.Bool(false),
				}
				// If current workload hints already contains the changes
				// skip mcp wait
				if !(cmp.Equal(currentWorkloadHints, profile.Spec.WorkloadHints)) {
					By("Patching the performance profile with workload hints")
					workloadHints, err := json.Marshal(profile.Spec.WorkloadHints)
					Expect(err).ToNot(HaveOccurred())

					Expect(testclient.Client.Patch(context.TODO(), profile,
						client.RawPatch(
							types.JSONPatchType,
							[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec/workloadHints", "value": %s }]`, workloadHints)),
						),
					)).ToNot(HaveOccurred())

					By("Applying changes in performance profile and waiting until mcp will start updating")
					mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

					By("Waiting when mcp finishes updates")
					mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
				}
				stalldEnabled, rtKernel := true, true
				noHzParam := fmt.Sprintf("nohz_full=%s", *profile.Spec.CPU.Isolated)
				sysctlMap := map[string]string{
					"kernel.hung_task_timeout_secs": "600",
					"kernel.nmi_watchdog":           "0",
					"kernel.sched_rt_runtime_us":    "-1",
					"vm.stat_interval":              "10",
				}
				kernelParameters := []string{noHzParam, "tsc=reliable", "nosoftlockup", "nmi_watchdog=0", "mce=off", "skew_tick=1",
					"processor.max_cstate=1", "intel_idle.max_cstate=0", "idle=poll"}

				wg := sync.WaitGroup{}
				By("Waiting for TuneD to start on nodes")
				for i := 0; i < len(workerRTNodes); i++ {
					node := &workerRTNodes[i]
					wg.Add(1)
					go func() {
						defer GinkgoRecover()
						defer wg.Done()

						pod, err := utilstuned.GetPod(context.TODO(), node)
						Expect(err).ToNot(HaveOccurred())
						cmd := []string{"test", "-e", "/run/tuned/tuned.pid"}
						_, err = util.WaitForCmdInPod(5*time.Second, 5*time.Minute, pod, cmd...)
						Expect(err).ToNot(HaveOccurred())

						By(fmt.Sprintf("Waiting for stalld to be running on %q", node.Name))
						Expect(utilstuned.WaitForStalldTo(context.TODO(), stalldEnabled, 10*time.Second, 1*time.Minute, node)).ToNot(HaveOccurred(),
							fmt.Sprintf("stalld is not running on %q when it should", node.Name))

						By(fmt.Sprintf("Checking TuneD parameters on %q", node.Name))
						kernelParameters = append(kernelParameters, utilstuned.AddPstateParameter(context.TODO(), node))
						utilstuned.CheckParameters(context.TODO(), node, sysctlMap, kernelParameters, stalldEnabled, rtKernel)
					}()
				}
				wg.Wait()
			})
		})

		When("perPodPowerManagent enabled", func() {
			It("[test_id:54177]should update kernel arguments and tuned accordingly", func() {
				currentWorkloadHints := profile.Spec.WorkloadHints
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					PerPodPowerManagement: pointer.Bool(true),
					HighPowerConsumption:  pointer.Bool(false),
					RealTime:              pointer.Bool(true),
				}
				if !(cmp.Equal(currentWorkloadHints, profile.Spec.WorkloadHints)) {
					By("Patching the performance profile with workload hints")
					workloadHints, err := json.Marshal(profile.Spec.WorkloadHints)
					Expect(err).ToNot(HaveOccurred())
					Expect(testclient.Client.Patch(context.TODO(), profile,
						client.RawPatch(
							types.JSONPatchType,
							[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec/workloadHints", "value": %s }]`, workloadHints)),
						),
					)).ToNot(HaveOccurred())

					By("Applying changes in performance profile and waiting until mcp will start updating")
					mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

					By("Waiting when mcp finishes updates")
					mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
				}

				By("Verifying node kernel arguments")
				cmdline, err := nodes.ExecCommand(context.TODO(), &workerRTNodes[0], []string{"cat", "/proc/cmdline"})
				Expect(err).ToNot(HaveOccurred())
				Expect(cmdline).To(ContainSubstring("intel_pstate=passive"))
				Expect(cmdline).ToNot(ContainSubstring("intel_pstate=active"))

				By("Verifying tuned profile")
				key := types.NamespacedName{
					Name:      components.GetComponentName(profile.Name, components.ProfileNamePerformance),
					Namespace: components.NamespaceNodeTuningOperator,
				}
				tuned := &tunedv1.Tuned{}
				err = testclient.Client.Get(context.TODO(), key, tuned)
				Expect(err).ToNot(HaveOccurred(), "cannot find the Cluster Node Tuning Operator object")
				tunedData := getTunedStructuredData(profile)
				cpuSection, err := tunedData.GetSection("cpu")
				Expect(err).ToNot(HaveOccurred())
				Expect(cpuSection.Key("enabled").String()).To(Equal("false"))
			})

			It("[test_id:54178]Verify System is tuned when updating from HighPowerConsumption to PerPodPowermanagment", func() {

				// This test requires real hardware with powermanagement settings done on BIOS
				// Using numa nodes to check if we are running on real hardware.
				checkHardwareCapability(context.TODO(), workerRTNodes)
				// First enable HighPowerConsumption
				currentWorkloadHints := profile.Spec.WorkloadHints
				By("Modifying profile")
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption:  pointer.Bool(true),
					RealTime:              pointer.Bool(true),
					PerPodPowerManagement: pointer.Bool(false),
				}
				if !*profile.Spec.RealTimeKernel.Enabled {
					profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
						Enabled: pointer.Bool(true),
					}
				}
				if !(cmp.Equal(currentWorkloadHints, profile.Spec.WorkloadHints)) {
					By("Patching the performance profile with workload hints")
					spec, err := json.Marshal(profile.Spec)
					Expect(err).ToNot(HaveOccurred())

					Expect(testclient.Client.Patch(context.TODO(), profile,
						client.RawPatch(
							types.JSONPatchType,
							[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, spec)),
						),
					)).ToNot(HaveOccurred())

					By("Applying changes in performance profile and waiting until mcp will start updating")
					mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

					By("Waiting when mcp finishes updates")
					mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
				}
				stalldEnabled, rtKernel := true, true
				noHzParam := fmt.Sprintf("nohz_full=%s", *profile.Spec.CPU.Isolated)
				sysctlMap := map[string]string{
					"kernel.hung_task_timeout_secs": "600",
					"kernel.nmi_watchdog":           "0",
					"kernel.sched_rt_runtime_us":    "-1",
					"vm.stat_interval":              "10",
				}
				kernelParameters := []string{noHzParam, "tsc=reliable", "nosoftlockup", "nmi_watchdog=0", "mce=off", "skew_tick=1",
					"processor.max_cstate=1", "intel_idle.max_cstate=0", "idle=poll"}

				wg := sync.WaitGroup{}
				By("Waiting for TuneD to start on nodes")
				for i := 0; i < len(workerRTNodes); i++ {
					node := &workerRTNodes[i]
					wg.Add(1)
					go func() {
						defer GinkgoRecover()
						defer wg.Done()

						pod, err := utilstuned.GetPod(context.TODO(), node)
						Expect(err).ToNot(HaveOccurred())
						cmd := []string{"test", "-e", "/run/tuned/tuned.pid"}
						_, err = util.WaitForCmdInPod(5*time.Second, 5*time.Minute, pod, cmd...)
						Expect(err).ToNot(HaveOccurred())

						By(fmt.Sprintf("Waiting for stalld to be running on %q", node.Name))
						Expect(utilstuned.WaitForStalldTo(context.TODO(), stalldEnabled, 10*time.Second, 1*time.Minute, node)).ToNot(HaveOccurred(),
							fmt.Sprintf("stalld is not running on %q when it should", node.Name))

						By(fmt.Sprintf("Checking TuneD parameters on %q", node.Name))
						kernelParameters = append(kernelParameters, utilstuned.AddPstateParameter(context.TODO(), node))
						utilstuned.CheckParameters(context.TODO(), node, sysctlMap, kernelParameters, stalldEnabled, rtKernel)
					}()
				}
				wg.Wait()

				//Update the profile to disable HighPowerConsumption and enable PerPodPowerManagment
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption:  pointer.Bool(false),
					RealTime:              pointer.Bool(true),
					PerPodPowerManagement: pointer.Bool(true),
				}
				if !*profile.Spec.RealTimeKernel.Enabled {
					profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
						Enabled: pointer.Bool(true),
					}
				}

				By("Patching the performance profile with workload hints")
				newspec, err := json.Marshal(profile.Spec)
				Expect(err).ToNot(HaveOccurred())

				Expect(testclient.Client.Patch(context.TODO(), profile,
					client.RawPatch(
						types.JSONPatchType,
						[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, newspec)),
					),
				)).ToNot(HaveOccurred())

				By("Applying changes in performance profile and waiting until mcp will start updating")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

				By("Waiting when mcp finishes updates")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

				stalldEnabled, rtKernel = true, true
				noHzParam = fmt.Sprintf("nohz_full=%s", *profile.Spec.CPU.Isolated)
				sysctlMap = map[string]string{
					"kernel.hung_task_timeout_secs": "600",
					"kernel.nmi_watchdog":           "0",
					"kernel.sched_rt_runtime_us":    "-1",
					"vm.stat_interval":              "10",
				}
				kernelParameters = []string{noHzParam, "tsc=reliable", "nosoftlockup", "nmi_watchdog=0", "mce=off", "skew_tick=1", "intel_pstate=passive"}

				wg = sync.WaitGroup{}
				By("Waiting for TuneD to start on nodes")
				for i := 0; i < len(workerRTNodes); i++ {
					node := &workerRTNodes[i]
					wg.Add(1)
					go func() {
						defer GinkgoRecover()
						defer wg.Done()

						pod, err := utilstuned.GetPod(context.TODO(), node)
						Expect(err).ToNot(HaveOccurred())
						cmd := []string{"test", "-e", "/run/tuned/tuned.pid"}
						_, err = util.WaitForCmdInPod(5*time.Second, 5*time.Minute, pod, cmd...)
						Expect(err).ToNot(HaveOccurred())

						By(fmt.Sprintf("Waiting for stalld to be running on %q", node.Name))
						Expect(utilstuned.WaitForStalldTo(context.TODO(), stalldEnabled, 10*time.Second, 1*time.Minute, node)).ToNot(HaveOccurred(),
							fmt.Sprintf("stalld is not running on %q when it should", node.Name))

						By(fmt.Sprintf("Checking TuneD parameters on %q", node.Name))
						utilstuned.CheckParameters(context.TODO(), node, sysctlMap, kernelParameters, stalldEnabled, rtKernel)
					}()
				}
				wg.Wait()
			})

			It("[test_id:54179]Verify System is tuned when reverting from PerPodPowerManagement to HighPowerConsumption", func() {

				// This test requires real hardware with powermanagement settings done on BIOS
				// Using numa nodes to check if we are running on real hardware.
				checkHardwareCapability(context.TODO(), workerRTNodes)
				currentWorkloadHints := profile.Spec.WorkloadHints
				// First enable HighPowerConsumption
				By("Modifying profile")
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption:  pointer.Bool(false),
					RealTime:              pointer.Bool(true),
					PerPodPowerManagement: pointer.Bool(true),
				}
				if !*profile.Spec.RealTimeKernel.Enabled {
					profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
						Enabled: pointer.Bool(true),
					}
				}
				if !(cmp.Equal(currentWorkloadHints, profile.Spec.WorkloadHints)) {
					By("Patching the performance profile with workload hints")
					spec, err := json.Marshal(profile.Spec)
					Expect(err).ToNot(HaveOccurred())

					Expect(testclient.Client.Patch(context.TODO(), profile,
						client.RawPatch(
							types.JSONPatchType,
							[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, spec)),
						),
					)).ToNot(HaveOccurred())

					By("Applying changes in performance profile and waiting until mcp will start updating")
					mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

					By("Waiting when mcp finishes updates")
					mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
				}
				stalldEnabled, rtKernel := true, true
				noHzParam := fmt.Sprintf("nohz_full=%s", *profile.Spec.CPU.Isolated)
				sysctlMap := map[string]string{
					"kernel.hung_task_timeout_secs": "600",
					"kernel.nmi_watchdog":           "0",
					"kernel.sched_rt_runtime_us":    "-1",
					"vm.stat_interval":              "10",
				}
				kernelParameters := []string{noHzParam, "tsc=reliable", "nosoftlockup", "nmi_watchdog=0", "mce=off", "skew_tick=1", "intel_pstate=passive"}

				wg := sync.WaitGroup{}
				By("Waiting for TuneD to start on nodes")
				for i := 0; i < len(workerRTNodes); i++ {
					node := &workerRTNodes[i]
					wg.Add(1)
					go func() {
						defer GinkgoRecover()
						defer wg.Done()

						pod, err := utilstuned.GetPod(context.TODO(), node)
						Expect(err).ToNot(HaveOccurred())
						cmd := []string{"test", "-e", "/run/tuned/tuned.pid"}
						_, err = util.WaitForCmdInPod(5*time.Second, 5*time.Minute, pod, cmd...)
						Expect(err).ToNot(HaveOccurred())

						By(fmt.Sprintf("Waiting for stalld to be running on %q", node.Name))
						Expect(utilstuned.WaitForStalldTo(context.TODO(), stalldEnabled, 10*time.Second, 1*time.Minute, node)).ToNot(HaveOccurred(),
							fmt.Sprintf("stalld is not running on %q when it should", node.Name))

						By(fmt.Sprintf("Checking TuneD parameters on %q", node.Name))
						utilstuned.CheckParameters(context.TODO(), node, sysctlMap, kernelParameters, stalldEnabled, rtKernel)
					}()
				}
				wg.Wait()

				//Update the profile to enable HighPowerConsumption and disable PerPodPowerManagment
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption:  pointer.Bool(true),
					RealTime:              pointer.Bool(true),
					PerPodPowerManagement: pointer.Bool(false),
				}
				if !*profile.Spec.RealTimeKernel.Enabled {
					profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
						Enabled: pointer.Bool(true),
					}
				}

				By("Patching the performance profile with workload hints")
				newspec, err := json.Marshal(profile.Spec)
				Expect(err).ToNot(HaveOccurred())

				Expect(testclient.Client.Patch(context.TODO(), profile,
					client.RawPatch(
						types.JSONPatchType,
						[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, newspec)),
					),
				)).ToNot(HaveOccurred())

				By("Applying changes in performance profile and waiting until mcp will start updating")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

				By("Waiting when mcp finishes updates")
				mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

				stalldEnabled, rtKernel = true, true
				noHzParam = fmt.Sprintf("nohz_full=%s", *profile.Spec.CPU.Isolated)
				sysctlMap = map[string]string{
					"kernel.hung_task_timeout_secs": "600",
					"kernel.nmi_watchdog":           "0",
					"kernel.sched_rt_runtime_us":    "-1",
					"vm.stat_interval":              "10",
				}
				kernelParameters = []string{noHzParam, "tsc=reliable", "nosoftlockup", "nmi_watchdog=0", "mce=off", "skew_tick=1",
					"processor.max_cstate=1", "intel_idle.max_cstate=0", "idle=poll"}

				wg = sync.WaitGroup{}
				By("Waiting for TuneD to start on nodes")
				for i := 0; i < len(workerRTNodes); i++ {
					node := &workerRTNodes[i]
					wg.Add(1)
					go func() {
						defer GinkgoRecover()
						defer wg.Done()

						pod, err := utilstuned.GetPod(context.TODO(), node)
						Expect(err).ToNot(HaveOccurred())
						cmd := []string{"test", "-e", "/run/tuned/tuned.pid"}
						_, err = util.WaitForCmdInPod(5*time.Second, 5*time.Minute, pod, cmd...)
						Expect(err).ToNot(HaveOccurred())

						By(fmt.Sprintf("Waiting for stalld to be running on %q", node.Name))
						Expect(utilstuned.WaitForStalldTo(context.TODO(), stalldEnabled, 10*time.Second, 1*time.Minute, node)).ToNot(HaveOccurred(),
							fmt.Sprintf("stalld is not running on %q when it should", node.Name))

						By(fmt.Sprintf("Checking TuneD parameters on %q", node.Name))
						kernelParameters = append(kernelParameters, utilstuned.AddPstateParameter(context.TODO(), node))
						utilstuned.CheckParameters(context.TODO(), node, sysctlMap, kernelParameters, stalldEnabled, rtKernel)
					}()
				}
				wg.Wait()
			})

			It("[test_id:54184]Verify enabling both HighPowerConsumption and PerPodPowerManagment fails", func() {

				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					PerPodPowerManagement: pointer.Bool(true),
					HighPowerConsumption:  pointer.Bool(true),
					RealTime:              pointer.Bool(true),
				}
				EventuallyWithOffset(1, func() string {
					err := testclient.Client.Update(context.TODO(), profile)
					if err != nil {
						statusErr, _ := err.(*errors.StatusError)
						return statusErr.Status().Message
					}
					return fmt.Sprint("Profile applied successfully")
				}, time.Minute, 5*time.Second).Should(ContainSubstring("HighPowerConsumption and PerPodPowerManagement can not be both enabled"))
			})

			It("[test_id:54185] Verify sysfs parameters of guaranteed pod with powersave annotations", func() {

				var fullPath string = ""
				// This test requires real hardware with powermanagement settings done on BIOS
				// Using numa nodes to check if we are running on real hardware.
				checkHardwareCapability(context.TODO(), workerRTNodes)
				currentWorkloadHints := profile.Spec.WorkloadHints
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					PerPodPowerManagement: pointer.Bool(true),
					HighPowerConsumption:  pointer.Bool(false),
					RealTime:              pointer.Bool(true),
				}
				if !(cmp.Equal(currentWorkloadHints, profile.Spec.WorkloadHints)) {
					By("Patching the performance profile with workload hints")
					workloadHints, err := json.Marshal(profile.Spec.WorkloadHints)
					Expect(err).ToNot(HaveOccurred())

					Expect(testclient.Client.Patch(context.TODO(), profile,
						client.RawPatch(
							types.JSONPatchType,
							[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec/workloadHints", "value": %s }]`, workloadHints)),
						),
					)).ToNot(HaveOccurred())
					By("Applying changes in performance profile and waiting until mcp will start updating")
					mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

					By("Waiting when mcp finishes updates")
					mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
				}

				annotations := map[string]string{
					"cpu-c-states.crio.io":      "enable",
					"cpu-freq-governor.crio.io": "schedutil",
				}

				cpuCount := "2"
				resCpu := resource.MustParse(cpuCount)
				resMem := resource.MustParse("100Mi")
				testpod = pods.GetTestPod()
				testpod.Namespace = testutils.NamespaceTesting
				testpod.Spec.Containers[0].Resources = corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resCpu,
						corev1.ResourceMemory: resMem,
					},
				}
				testpod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNodes[0].Name}
				testpod.Annotations = annotations
				runtimeClass := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
				testpod.Spec.RuntimeClassName = &runtimeClass

				By("creating test pod")
				err = testclient.Client.Create(context.TODO(), testpod)
				Expect(err).ToNot(HaveOccurred())
				testpod, err = pods.WaitForCondition(context.TODO(), client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
				Expect(err).ToNot(HaveOccurred())
				Expect(testpod.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed), "Test pod does not have QoS class of Guaranteed")

				By("Getting the container cpuset.cpus cgroup")
				containerID, err := pods.GetContainerIDByName(testpod, "test")
				Expect(err).ToNot(HaveOccurred())

				containerCgroup := ""
				pid, err := nodes.ContainerPid(context.TODO(), &workerRTNodes[0], containerID)
				cmd := []string{"cat", fmt.Sprintf("/rootfs/proc/%s/cgroup", pid)}
				out, err := nodes.ExecCommand(context.TODO(), &workerRTNodes[0], cmd)
				containerCgroup, err = cgroup.PidParser(out)
				cgroupv2, err := cgroup.IsVersion2(context.TODO(), testclient.Client)
				Expect(err).ToNot(HaveOccurred())
				if cgroupv2 {
					fullPath = filepath.Join(cgroupRoot, containerCgroup)
				} else {
					fullPath = filepath.Join(cgroupRoot, "cpuset", containerCgroup)
				}
				cpusetCpusPath := filepath.Join(fullPath, "cpuset.cpus")
				testlog.Infof("test pod %s with container id %s cgroup path %s", testpod.Name, containerID, cpusetCpusPath)
				By("Verify powersetting of cpus used by the pod")
				cmd = []string{"cat", cpusetCpusPath}
				out, err = nodes.ExecCommand(context.TODO(), &workerRTNodes[0], cmd)
				Expect(err).ToNot(HaveOccurred())
				output := testutils.ToString(out)
				cpus, err := cpuset.Parse(output)
				targetCpus := cpus.List()
				err = checkCpuGovernorsAndResumeLatency(context.TODO(), targetCpus, &workerRTNodes[0], "0", "schedutil")
				Expect(err).ToNot(HaveOccurred())
				//verify the rest of the cpus do not have powersave cpu governors
				By("Verify the rest of the cpus donot haver powersave settings")
				numaInfo, err := nodes.GetNumaNodes(context.TODO(), &workerRTNodes[0])
				Expect(err).ToNot(HaveOccurred())
				var otherCpus []int
				for _, cpusiblings := range numaInfo {
					for _, cpu := range cpusiblings {
						if cpu != targetCpus[0] && cpu != targetCpus[1] {
							otherCpus = append(otherCpus, cpu)
						}
					}
				}
				err = checkCpuGovernorsAndResumeLatency(context.TODO(), otherCpus, &workerRTNodes[0], "0", "performance")
				deleteTestPod(context.TODO(), testpod)
				//Verify after the pod is deleted the cpus assigned to container have default powersave settings
				By("Verify after pod is delete cpus assigned to container have default powersave settings")
				err = checkCpuGovernorsAndResumeLatency(context.TODO(), targetCpus, &workerRTNodes[0], "0", "performance")
			})

			It("[test_id:54186] Verify sysfs paramters of guaranteed pod with performance annotiations", func() {

				// This test requires real hardware with powermanagement settings done on BIOS
				// Using numa nodes to check if we are running on real hardware
				var containerCgroup, fullPath string
				checkHardwareCapability(context.TODO(), workerRTNodes)
				currentWorkloadHints := profile.Spec.WorkloadHints
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					PerPodPowerManagement: pointer.Bool(false),
					HighPowerConsumption:  pointer.Bool(true),
					RealTime:              pointer.Bool(true),
				}
				if !(cmp.Equal(currentWorkloadHints, profile.Spec.WorkloadHints)) {
					By("Patching the performance profile with workload hints")
					workloadHints, err := json.Marshal(profile.Spec.WorkloadHints)
					Expect(err).ToNot(HaveOccurred())

					Expect(testclient.Client.Patch(context.TODO(), profile,
						client.RawPatch(
							types.JSONPatchType,
							[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec/workloadHints", "value": %s }]`, workloadHints)),
						),
					)).ToNot(HaveOccurred())
					By("Applying changes in performance profile and waiting until mcp will start updating")
					mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

					By("Waiting when mcp finishes updates")
					mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
				}
				annotations := map[string]string{
					"cpu-load-balancing.crio.io": "disable",
					"cpu-quota.crio.io":          "disable",
					"irq-load-balancing.crio.io": "disable",
					"cpu-c-states.crio.io":       "disable",
					"cpu-freq-governor.crio.io":  "performance",
				}

				cpuCount := "2"
				resCpu := resource.MustParse(cpuCount)
				resMem := resource.MustParse("100Mi")
				testpod = pods.GetTestPod()
				testpod.Namespace = testutils.NamespaceTesting
				testpod.Spec.Containers[0].Resources = corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resCpu,
						corev1.ResourceMemory: resMem,
					},
				}
				testpod.Spec.NodeSelector = map[string]string{testutils.LabelHostname: workerRTNodes[0].Name}
				testpod.Annotations = annotations
				runtimeClass := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
				testpod.Spec.RuntimeClassName = &runtimeClass

				By("creating test pod")
				err = testclient.Client.Create(context.TODO(), testpod)
				Expect(err).ToNot(HaveOccurred())
				testpod, err = pods.WaitForCondition(context.TODO(), client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
				Expect(err).ToNot(HaveOccurred())
				Expect(testpod.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed), "Test pod does not have QoS class of Guaranteed")

				By("Getting the container cpuset.cpus cgroup")
				containerID, err := pods.GetContainerIDByName(testpod, "test")
				Expect(err).ToNot(HaveOccurred())

				pid, err := nodes.ContainerPid(context.TODO(), &workerRTNodes[0], containerID)
				cmd := []string{"cat", fmt.Sprintf("/rootfs/proc/%s/cgroup", pid)}
				out, err := nodes.ExecCommand(context.TODO(), &workerRTNodes[0], cmd)
				containerCgroup, err = cgroup.PidParser(out)
				cgroupv2, err := cgroup.IsVersion2(context.TODO(), testclient.Client)
				Expect(err).ToNot(HaveOccurred())
				if cgroupv2 {
					fullPath = filepath.Join(cgroupRoot, containerCgroup)
				} else {
					fullPath = filepath.Join(cgroupRoot, "cpuset", containerCgroup)
				}
				cpusetCpusPath := filepath.Join(fullPath, "cpuset.cpus")
				testlog.Infof("test pod %s with container id %s cgroup path %s", testpod.Name, containerID, cpusetCpusPath)
				By("Verify powersetting of cpus used by the pod")
				cmd = []string{"cat", cpusetCpusPath}
				out, err = nodes.ExecCommand(context.TODO(), &workerRTNodes[0], cmd)
				Expect(err).ToNot(HaveOccurred())
				output := testutils.ToString(out)
				cpus, err := cpuset.Parse(output)
				targetCpus := cpus.List()
				err = checkCpuGovernorsAndResumeLatency(context.TODO(), targetCpus, &workerRTNodes[0], "n/a", "performance")
				Expect(err).ToNot(HaveOccurred())
				By("Verify the rest of cpus have default power setting")
				var otherCpus []int
				numaInfo, err := nodes.GetNumaNodes(context.TODO(), &workerRTNodes[0])
				for _, cpusiblings := range numaInfo {
					for _, cpu := range cpusiblings {
						if cpu != targetCpus[0] && cpu != targetCpus[1] {
							otherCpus = append(otherCpus, cpu)
						}
					}
				}
				//Verify cpus not assigned to the pod have default power settings
				err = checkCpuGovernorsAndResumeLatency(context.TODO(), otherCpus, &workerRTNodes[0], "0", "performance")
				deleteTestPod(context.TODO(), testpod)
				//Test after pod is deleted the governors are set back to default for the cpus that were alloted to containers.
				By("Verify after pod is delete cpus assigned to container have default powersave settings")
				err = checkCpuGovernorsAndResumeLatency(context.TODO(), targetCpus, &workerRTNodes[0], "0", "performance")
			})
		})

		AfterEach(func() {
			currentProfile := &performancev2.PerformanceProfile{}
			if err := testclient.Client.Get(context.TODO(), client.ObjectKeyFromObject(initialProfile), currentProfile); err != nil {
				klog.Errorf("failed to get performance profile %q", initialProfile.Name)
				return
			}

			if reflect.DeepEqual(currentProfile.Spec, initialProfile.Spec) {
				return
			}

			By("Restoring the old performance profile")
			spec, err := json.Marshal(initialProfile.Spec)
			Expect(err).ToNot(HaveOccurred())

			Expect(testclient.Client.Patch(context.TODO(), profile,
				client.RawPatch(
					types.JSONPatchType,
					[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, spec)),
				),
			)).ToNot(HaveOccurred())

			By("Applying changes in performance profile and waiting until mcp will start updating")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

			By("Waiting when mcp finishes updates")
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

		})
	})
})

func getUpdatedNodes() []corev1.Node {
	workerRTNodes, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
	Expect(err).ToNot(HaveOccurred())
	klog.Infof("updated nodes from %#v: %v", testutils.NodeSelectorLabels, getNodeNames(workerRTNodes))
	workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
	klog.Infof("updated nodes matching optional selector: %v", getNodeNames(workerRTNodes))
	Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("error looking for the optional selector: %v", err))
	Expect(workerRTNodes).ToNot(BeEmpty(), "cannot find RT enabled worker nodes")
	return workerRTNodes
}

func getNodeNames(nodes []corev1.Node) []string {
	names := []string{}
	for _, node := range nodes {
		names = append(names, node.Name)
	}
	return names
}

func getTunedStructuredData(profile *performancev2.PerformanceProfile) *ini.File {
	tuned, err := tuned.NewNodePerformance(profile)
	Expect(err).ToNot(HaveOccurred())
	tunedData := []byte(*tuned.Spec.Profile[0].Data)
	cfg, err := ini.Load(tunedData)
	Expect(err).ToNot(HaveOccurred())
	return cfg
}

// deleteTestPod removes guaranteed pod
func deleteTestPod(ctx context.Context, testpod *corev1.Pod) {
	// it possible that the pod already was deleted as part of the test, in this case we want to skip teardown
	err := testclient.Client.Get(ctx, client.ObjectKeyFromObject(testpod), testpod)
	if errors.IsNotFound(err) {
		return
	}

	err = testclient.Client.Delete(ctx, testpod)
	Expect(err).ToNot(HaveOccurred())

	err = pods.WaitForDeletion(ctx, testpod, pods.DefaultDeletionTimeout*time.Second)
	Expect(err).ToNot(HaveOccurred())
}

// checkCpuGovernorsAndResumeLatency  Checks power and latency settings of the cpus
func checkCpuGovernorsAndResumeLatency(ctx context.Context, cpus []int, targetNode *corev1.Node, pm_qos string, governor string) error {
	for _, cpu := range cpus {
		cmd := []string{"/bin/bash", "-c", fmt.Sprintf("cat /sys/devices/system/cpu/cpu%d/power/pm_qos_resume_latency_us", cpu)}
		out, err := nodes.ExecCommand(ctx, targetNode, cmd)
		if err != nil {
			return err
		}
		output := testutils.ToString(out)
		Expect(output).To(Equal(pm_qos))
		cmd = []string{"/bin/bash", "-c", fmt.Sprintf("cat /sys/devices/system/cpu/cpu%d/cpufreq/scaling_governor", cpu)}
		out, err = nodes.ExecCommand(ctx, targetNode, cmd)
		if err != nil {
			return err
		}
		output = testutils.ToString(out)
		Expect(output).To(Equal(governor))
	}
	return nil
}

// checkHardwareCapability Checks if test is run on baremetal worker
func checkHardwareCapability(ctx context.Context, workerRTNodes []corev1.Node) {
	const totalCpus = 32
	for _, node := range workerRTNodes {
		numaInfo, err := nodes.GetNumaNodes(ctx, &node)
		Expect(err).ToNot(HaveOccurred())
		if len(numaInfo) < 2 {
			Skip(fmt.Sprintf("This test need 2 NUMA nodes.The number of NUMA nodes on node %s < 2", node.Name))
		}
		// Additional check so that test gets skipped on vm with fake numa
		out, err := nodes.ExecCommand(ctx, &node, []string{"nproc", "--all"})
		Expect(err).ToNot(HaveOccurred())
		onlineCPUCount := testutils.ToString(out)
		onlineCPUInt, err := strconv.Atoi(onlineCPUCount)
		Expect(err).ToNot(HaveOccurred())
		if onlineCPUInt < totalCpus {
			Skip(fmt.Sprintf("This test needs system with %d CPUs to work correctly, current CPUs are %s", totalCpus, onlineCPUCount))
		}
	}
}
