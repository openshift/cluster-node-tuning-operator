package __performance_workloadhints

import (
	"context"
	"fmt"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/infrastructure"
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
	"k8s.io/utils/ptr"
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
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/hypershift"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodepools"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profilesupdate"
	utilstuned "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/tuned"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

const (
	cgroupRoot = "/rootfs/sys/fs/cgroup"
)

var _ = Describe("[rfe_id:49062][workloadHints] Telco friendly workload specific PerformanceProfile API", Label(string(label.WorkloadHints)), func() {
	var (
		workerRTNodes           []corev1.Node
		profile, initialProfile *performancev2.PerformanceProfile
		poolName                string
		err                     error
		ctx                     context.Context = context.Background()
		isIntel                 bool
		isAMD                   bool
	)

	nodeLabel := testutils.NodeSelectorLabels
	BeforeEach(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}
		var err error
		workerRTNodes = getUpdatedNodes()
		profile, err = profiles.GetByNodeLabels(nodeLabel)
		Expect(err).ToNot(HaveOccurred())
		klog.Infof("using profile: %q", profile.Name)
		// Check if one of the nodes is intel or AMD using Vendor ID
		isIntel, err = infrastructure.IsIntel(ctx, &workerRTNodes[0])
		Expect(err).ToNot(HaveOccurred(), "Unable to fetch Vendor ID")
		isAMD, err = infrastructure.IsAMD(ctx, &workerRTNodes[0])
		Expect(err).ToNot(HaveOccurred(), "Unable to fetch Vendor ID")
		if !hypershift.IsHypershiftCluster() {
			poolName, err = mcps.GetByProfile(profile)
			Expect(err).ToNot(HaveOccurred())
			for _, mcpName := range []string{testutils.RoleWorker, poolName} {
				mcps.WaitForCondition(mcpName, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
			}
		} else {
			hostedClusterName, err := hypershift.GetHostedClusterName()
			Expect(err).ToNot(HaveOccurred(), "unable to fetch hosted cluster name")
			np, err := nodepools.GetByClusterName(ctx, testclient.ControlPlaneClient, hostedClusterName)
			Expect(err).ToNot(HaveOccurred())
			poolName = client.ObjectKeyFromObject(np).String()
		}

	})

	Context("WorkloadHints", Label(string(label.Tier3)), func() {
		var testpod *corev1.Pod
		BeforeEach(func() {
			By("Saving the old performance profile")
			initialProfile = profile.DeepCopy()
		})
		When("workloadHint RealTime is disabled", func() {
			It("should update kernel arguments and tuned accordingly to realTime Hint enabled by default", Label(string(label.Slow)), func() {
				currentWorkloadHints := profile.Spec.WorkloadHints
				By("Modifying profile")
				profile.Spec.WorkloadHints = nil

				profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
					Enabled: ptr.To(false),
				}
				// If current workload hints already contains the changes skip updating
				if !(cmp.Equal(currentWorkloadHints, profile.Spec.WorkloadHints)) {
					By("Updating the performance profile")
					profiles.UpdateWithRetry(profile)

					By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
					profilesupdate.WaitForTuningUpdating(ctx, profile)

					By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
					profilesupdate.WaitForTuningUpdated(ctx, profile)
				}
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
			It("[test_id:50991][crit:high][vendor:cnf-qe@redhat.com][level:acceptance]should update kernel arguments and tuned accordingly", Label(string(label.Slow)), func() {

				currentWorkloadHints := profile.Spec.WorkloadHints
				By("Modifying profile")
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption: ptr.To(false),
					RealTime:             ptr.To(true),
				}
				profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
					Enabled: ptr.To(false),
				}

				// If current workload hints already contains the changes skip updating
				if !(cmp.Equal(currentWorkloadHints, profile.Spec.WorkloadHints)) {
					By("Updating the performance profile")
					profiles.UpdateWithRetry(profile)

					By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
					profilesupdate.WaitForTuningUpdating(ctx, profile)

					By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
					profilesupdate.WaitForTuningUpdated(ctx, profile)
				}
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
			It("[test_id:50992][crit:high][vendor:cnf-qe@redhat.com][level:acceptance]should update kernel arguments and tuned accordingly", Label(string(label.Slow)), func() {
				currentWorkloadHints := profile.Spec.WorkloadHints
				By("Modifying profile")
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption: ptr.To(true),
					RealTime:             ptr.To(false),
				}

				profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
					Enabled: ptr.To(false),
				}

				// If current workload hints already contains the changes skip updating
				if !(cmp.Equal(currentWorkloadHints, profile.Spec.WorkloadHints)) {
					By("Updating the performance profile")
					profiles.UpdateWithRetry(profile)

					By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
					profilesupdate.WaitForTuningUpdating(ctx, profile)

					By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
					profilesupdate.WaitForTuningUpdated(ctx, profile)
				}
				stalldEnabled, rtKernel := false, false
				sysctlMap := map[string]string{
					"kernel.hung_task_timeout_secs": "600",
					"kernel.nmi_watchdog":           "0",
					"kernel.sched_rt_runtime_us":    "950000",
					"vm.stat_interval":              "10",
				}
				kernelParameters := []string{"processor.max_cstate=1"}
				if isIntel {
					kernelParameters = append(kernelParameters, "intel_idle.max_cstate=0")
				}
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
			It("[test_id:50993][crit:high][vendor:cnf-qe@redhat.com][level:acceptance]should update kernel arguments and tuned accordingly", Label(string(label.Slow)), func() {
				currentWorkloadHints := profile.Spec.WorkloadHints
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption:  ptr.To(true),
					RealTime:              ptr.To(true),
					PerPodPowerManagement: ptr.To(false),
				}
				// If current workload hints already contains the changes
				// skip mcp wait
				if !(cmp.Equal(currentWorkloadHints, profile.Spec.WorkloadHints)) {
					By("Updating the performance profile")
					profiles.UpdateWithRetry(profile)

					By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
					profilesupdate.WaitForTuningUpdating(ctx, profile)

					By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
					profilesupdate.WaitForTuningUpdated(ctx, profile)

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
					"processor.max_cstate=1", "idle=poll"}
				if isIntel {
					kernelParameters = append(kernelParameters, "intel_idle.max_cstate=0")
				}
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
			It("[test_id:54177]should update kernel arguments and tuned accordingly", Label(string(label.Slow)), func() {
				currentWorkloadHints := profile.Spec.WorkloadHints
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					PerPodPowerManagement: ptr.To(true),
					HighPowerConsumption:  ptr.To(false),
					RealTime:              ptr.To(true),
				}
				if !(cmp.Equal(currentWorkloadHints, profile.Spec.WorkloadHints)) {
					By("Patching the performance profile with workload hints")
					profiles.UpdateWithRetry(profile)

					By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
					profilesupdate.WaitForTuningUpdating(ctx, profile)

					By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
					profilesupdate.WaitForTuningUpdated(ctx, profile)

				}

				By("Verifying node kernel arguments")
				cmdline, err := nodes.ExecCommand(context.TODO(), &workerRTNodes[0], []string{"cat", "/proc/cmdline"})
				Expect(err).ToNot(HaveOccurred())

				if isIntel {
					Expect(cmdline).To(ContainSubstring("intel_pstate=passive"))
					Expect(cmdline).ToNot(ContainSubstring("intel_pstate=active"))
				} else {
					Expect(cmdline).To(ContainSubstring("amd_pstate=passive"))
					Expect(cmdline).ToNot(ContainSubstring("amd_pstate=active"))
				}
				By("Verifying tuned profile")
				key := types.NamespacedName{
					Name:      components.GetComponentName(profile.Name, components.ProfileNamePerformance),
					Namespace: components.NamespaceNodeTuningOperator,
				}
				tuned := &tunedv1.Tuned{}
				err = testclient.DataPlaneClient.Get(context.TODO(), key, tuned)
				Expect(err).ToNot(HaveOccurred(), "cannot find the Cluster Node Tuning Operator object")
				tunedData := getTunedStructuredData(profile)
				cpuSection, err := tunedData.GetSection("cpu")
				Expect(err).ToNot(HaveOccurred())
				Expect(cpuSection.Key("enabled").String()).To(Equal("false"))
			})

			It("[test_id:54178]Verify System is tuned when updating from HighPowerConsumption to PerPodPowermanagment", Label(string(label.Slow)), func() {

				// This test requires real hardware with powermanagement settings done on BIOS
				// Using numa nodes to check if we are running on real hardware.
				checkHardwareCapability(context.TODO(), workerRTNodes)

				// First enable HighPowerConsumption
				currentWorkloadHints := profile.Spec.WorkloadHints
				By("Modifying profile")
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption:  ptr.To(true),
					RealTime:              ptr.To(true),
					PerPodPowerManagement: ptr.To(false),
				}
				if !*profile.Spec.RealTimeKernel.Enabled {
					profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
						Enabled: ptr.To(true),
					}
				}
				if !(cmp.Equal(currentWorkloadHints, profile.Spec.WorkloadHints)) {
					By("Patching the performance profile with workload hints")
					profiles.UpdateWithRetry(profile)

					By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
					profilesupdate.WaitForTuningUpdating(ctx, profile)

					By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
					profilesupdate.WaitForTuningUpdated(ctx, profile)

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
					"processor.max_cstate=1", "idle=poll"}
				if isIntel {
					kernelParameters = append(kernelParameters, "intel_idle.max_cstate=0")
				}
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
					HighPowerConsumption:  ptr.To(false),
					RealTime:              ptr.To(true),
					PerPodPowerManagement: ptr.To(true),
				}
				if !*profile.Spec.RealTimeKernel.Enabled {
					profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
						Enabled: ptr.To(true),
					}
				}

				By("Patching the performance profile with workload hints")
				profiles.UpdateWithRetry(profile)

				By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
				profilesupdate.WaitForTuningUpdating(ctx, profile)

				By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
				profilesupdate.WaitForTuningUpdated(ctx, profile)

				stalldEnabled, rtKernel = true, true
				noHzParam = fmt.Sprintf("nohz_full=%s", *profile.Spec.CPU.Isolated)
				sysctlMap = map[string]string{
					"kernel.hung_task_timeout_secs": "600",
					"kernel.nmi_watchdog":           "0",
					"kernel.sched_rt_runtime_us":    "-1",
					"vm.stat_interval":              "10",
				}
				kernelParameters = []string{noHzParam, "tsc=reliable", "nosoftlockup", "nmi_watchdog=0", "mce=off", "skew_tick=1"}

				// Note: Here if it's not intel, its AMD
				if isIntel {
					kernelParameters = append(kernelParameters, "intel_pstate=passive")
				}
				if isAMD {
					kernelParameters = append(kernelParameters, "amd_pstate=passive")
				}
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

			It("[test_id:54179]Verify System is tuned when reverting from PerPodPowerManagement to HighPowerConsumption", Label(string(label.Slow)), func() {

				// This test requires real hardware with powermanagement settings done on BIOS
				// Using numa nodes to check if we are running on real hardware.
				checkHardwareCapability(context.TODO(), workerRTNodes)
				currentWorkloadHints := profile.Spec.WorkloadHints
				// First enable HighPowerConsumption
				By("Modifying profile")
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					HighPowerConsumption:  ptr.To(false),
					RealTime:              ptr.To(true),
					PerPodPowerManagement: ptr.To(true),
				}
				if !*profile.Spec.RealTimeKernel.Enabled {
					profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
						Enabled: ptr.To(true),
					}
				}
				if !(cmp.Equal(currentWorkloadHints, profile.Spec.WorkloadHints)) {
					By("Patching the performance profile with workload hints")
					profiles.UpdateWithRetry(profile)

					By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
					profilesupdate.WaitForTuningUpdating(ctx, profile)

					By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
					profilesupdate.WaitForTuningUpdated(ctx, profile)
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
				if isIntel {
					kernelParameters = append(kernelParameters, "intel_pstate=passive")
				}
				if isAMD {
					kernelParameters = append(kernelParameters, "amd_pstate=passive")
				}
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
					HighPowerConsumption:  ptr.To(true),
					RealTime:              ptr.To(true),
					PerPodPowerManagement: ptr.To(false),
				}
				if !*profile.Spec.RealTimeKernel.Enabled {
					profile.Spec.RealTimeKernel = &performancev2.RealTimeKernel{
						Enabled: ptr.To(true),
					}
				}
				By("Updating the performance profile")
				profiles.UpdateWithRetry(profile)

				By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
				profilesupdate.WaitForTuningUpdating(ctx, profile)

				By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
				profilesupdate.WaitForTuningUpdated(ctx, profile)

				stalldEnabled, rtKernel = true, true
				noHzParam = fmt.Sprintf("nohz_full=%s", *profile.Spec.CPU.Isolated)
				sysctlMap = map[string]string{
					"kernel.hung_task_timeout_secs": "600",
					"kernel.nmi_watchdog":           "0",
					"kernel.sched_rt_runtime_us":    "-1",
					"vm.stat_interval":              "10",
				}
				kernelParameters = []string{noHzParam, "tsc=reliable", "nosoftlockup", "nmi_watchdog=0", "mce=off", "skew_tick=1",
					"processor.max_cstate=1", "idle=poll"}

				if isIntel {
					kernelParameters = append(kernelParameters, "intel_idle.max_cstate=0")
				}
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

			It("[test_id:54184]Verify enabling both HighPowerConsumption and PerPodPowerManagment fails", Label(string(label.Tier0)), func() {
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					PerPodPowerManagement: ptr.To(true),
					HighPowerConsumption:  ptr.To(true),
					RealTime:              ptr.To(true),
				}
				if hypershift.IsHypershiftCluster() {
					hostedClusterName, err := hypershift.GetHostedClusterName()
					Expect(err).ToNot(HaveOccurred(), "Unable to fetch hosted cluster name")
					np, err := nodepools.GetByClusterName(ctx, testclient.ControlPlaneClient, hostedClusterName)
					Expect(err).ToNot(HaveOccurred())
					profiles.UpdateWithRetry(profile)
					EventuallyWithOffset(1, func() string {
						reason := "ValidationFailed"
						messages, err := nodepools.NodePoolStatusMessages(ctx, testclient.ControlPlaneClient, np.Name, np.Namespace, reason)
						if err != nil {
							statusErr, _ := err.(*errors.StatusError)
							return statusErr.Status().Message
						}
						return messages[0]
					}, time.Minute, 5*time.Second).Should(ContainSubstring("HighPowerConsumption and PerPodPowerManagement can not be both enabled"))
				} else {
					EventuallyWithOffset(1, func() string {
						err := testclient.ControlPlaneClient.Update(context.TODO(), profile)
						if err != nil {
							statusErr, _ := err.(*errors.StatusError)
							return statusErr.Status().Message
						}
						return "Profile applied successfully"
					}, time.Minute, 5*time.Second).Should(ContainSubstring("HighPowerConsumption and PerPodPowerManagement can not be both enabled"))
				}
			})

			It("[test_id:54185] Verify sysfs parameters of guaranteed pod with powersave annotations", Label(string(label.Slow)), func() {
				var fullPath string
				var err error
				// This test requires real hardware with powermanagement settings done on BIOS
				// Using numa nodes to check if we are running on real hardware.
				if isAMD {
					Skip("Crio Powersave annotations test can only be run on Intel systems")
				}
				checkHardwareCapability(context.TODO(), workerRTNodes)
				currentWorkloadHints := profile.Spec.WorkloadHints
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					PerPodPowerManagement: ptr.To(true),
					HighPowerConsumption:  ptr.To(false),
					RealTime:              ptr.To(true),
				}
				if !(cmp.Equal(currentWorkloadHints, profile.Spec.WorkloadHints)) {
					By("Updating the performance profile")
					profiles.UpdateWithRetry(profile)

					By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
					profilesupdate.WaitForTuningUpdating(ctx, profile)

					By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
					profilesupdate.WaitForTuningUpdated(ctx, profile)

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
				err = testclient.DataPlaneClient.Create(context.TODO(), testpod)
				Expect(err).ToNot(HaveOccurred())
				testpod, err = pods.WaitForCondition(context.TODO(), client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
				Expect(err).ToNot(HaveOccurred())
				Expect(testpod.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed), "Test pod does not have QoS class of Guaranteed")

				By("Getting the container cpuset.cpus cgroup")
				containerID, err := pods.GetContainerIDByName(testpod, "test")
				Expect(err).ToNot(HaveOccurred())

				containerCgroup := ""
				pid, err := nodes.ContainerPid(context.TODO(), &workerRTNodes[0], containerID)
				Expect(err).ToNot(HaveOccurred(), "Unable to fetch pid of container process")
				cmd := []string{"cat", fmt.Sprintf("/rootfs/proc/%s/cgroup", pid)}
				out, err := nodes.ExecCommand(context.TODO(), &workerRTNodes[0], cmd)
				Expect(err).ToNot(HaveOccurred(), "unable to fetch cgroup path")
				containerCgroup, err = cgroup.PidParser(out)
				Expect(err).ToNot(HaveOccurred())
				cgroupv2, err := cgroup.IsVersion2(context.TODO(), testclient.DataPlaneClient)
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
				Expect(err).ToNot(HaveOccurred(), "unable to parse string %s", cpus)
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
				Expect(err).ToNot(HaveOccurred())
				deleteTestPod(context.TODO(), testpod)
				//Verify after the pod is deleted the cpus assigned to container have default powersave settings
				By("Verify after pod is delete cpus assigned to container have default powersave settings")
				err = checkCpuGovernorsAndResumeLatency(context.TODO(), targetCpus, &workerRTNodes[0], "0", "performance")
				Expect(err).ToNot(HaveOccurred())
			})

			It("[test_id:54186] Verify sysfs parameters of guaranteed pod with performance annotiations", Label(string(label.Slow)), func() {

				// This test requires real hardware with powermanagement settings done on BIOS
				// Using numa nodes to check if we are running on real hardware
				var containerCgroup, fullPath string
				if isAMD {
					Skip("Crio Performance annotations test can only be run on Intel systems")
				}
				checkHardwareCapability(context.TODO(), workerRTNodes)
				currentWorkloadHints := profile.Spec.WorkloadHints
				profile.Spec.WorkloadHints = &performancev2.WorkloadHints{
					PerPodPowerManagement: ptr.To(false),
					HighPowerConsumption:  ptr.To(true),
					RealTime:              ptr.To(true),
				}
				if !(cmp.Equal(currentWorkloadHints, profile.Spec.WorkloadHints)) {
					By("Updating the performance profile")
					profiles.UpdateWithRetry(profile)

					By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
					profilesupdate.WaitForTuningUpdating(ctx, profile)

					By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
					profilesupdate.WaitForTuningUpdated(ctx, profile)
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
				err = testclient.DataPlaneClient.Create(context.TODO(), testpod)
				Expect(err).ToNot(HaveOccurred())
				testpod, err = pods.WaitForCondition(context.TODO(), client.ObjectKeyFromObject(testpod), corev1.PodReady, corev1.ConditionTrue, 10*time.Minute)
				Expect(err).ToNot(HaveOccurred())
				Expect(testpod.Status.QOSClass).To(Equal(corev1.PodQOSGuaranteed), "Test pod does not have QoS class of Guaranteed")

				By("Getting the container cpuset.cpus cgroup")
				containerID, err := pods.GetContainerIDByName(testpod, "test")
				Expect(err).ToNot(HaveOccurred())

				pid, err := nodes.ContainerPid(context.TODO(), &workerRTNodes[0], containerID)
				Expect(err).ToNot(HaveOccurred())
				cmd := []string{"cat", fmt.Sprintf("/rootfs/proc/%s/cgroup", pid)}
				out, err := nodes.ExecCommand(context.TODO(), &workerRTNodes[0], cmd)
				Expect(err).ToNot(HaveOccurred())
				containerCgroup, err = cgroup.PidParser(out)
				Expect(err).ToNot(HaveOccurred())
				cgroupv2, err := cgroup.IsVersion2(context.TODO(), testclient.DataPlaneClient)
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
				Expect(err).ToNot(HaveOccurred())
				targetCpus := cpus.List()
				err = checkCpuGovernorsAndResumeLatency(context.TODO(), targetCpus, &workerRTNodes[0], "n/a", "performance")
				Expect(err).ToNot(HaveOccurred())
				By("Verify the rest of cpus have default power setting")
				var otherCpus []int
				numaInfo, err := nodes.GetNumaNodes(context.TODO(), &workerRTNodes[0])
				Expect(err).ToNot(HaveOccurred())
				for _, cpusiblings := range numaInfo {
					for _, cpu := range cpusiblings {
						if cpu != targetCpus[0] && cpu != targetCpus[1] {
							otherCpus = append(otherCpus, cpu)
						}
					}
				}
				//Verify cpus not assigned to the pod have default power settings
				err = checkCpuGovernorsAndResumeLatency(context.TODO(), otherCpus, &workerRTNodes[0], "0", "performance")
				Expect(err).ToNot(HaveOccurred())
				deleteTestPod(context.TODO(), testpod)
				//Test after pod is deleted the governors are set back to default for the cpus that were alloted to containers.
				By("Verify after pod is delete cpus assigned to container have default powersave settings")
				err = checkCpuGovernorsAndResumeLatency(context.TODO(), targetCpus, &workerRTNodes[0], "0", "performance")
				Expect(err).ToNot(HaveOccurred())
			})
		})

		AfterEach(func() {
			currentProfile := &performancev2.PerformanceProfile{}
			if err := testclient.ControlPlaneClient.Get(context.TODO(), client.ObjectKeyFromObject(initialProfile), currentProfile); err != nil {
				klog.Errorf("failed to get performance profile %q", initialProfile.Name)
				return
			}

			if reflect.DeepEqual(currentProfile.Spec, initialProfile.Spec) {
				return
			}

			By("Restoring the old performance profile")
			profiles.UpdateWithRetry(initialProfile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(ctx, initialProfile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(ctx, profile)

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
	err := testclient.DataPlaneClient.Get(ctx, client.ObjectKeyFromObject(testpod), testpod)
	if errors.IsNotFound(err) {
		return
	}

	err = testclient.DataPlaneClient.Delete(ctx, testpod)
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
			Skip(fmt.Sprintf("This test need 2 NUMA nodes. The number of NUMA nodes on node %s < 2", node.Name))
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
