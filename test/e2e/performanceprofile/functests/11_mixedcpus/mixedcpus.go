package __mixedcpus

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	kubeletconfig "k8s.io/kubelet/config/v1beta1"
	"k8s.io/utils/cpuset"
	"k8s.io/utils/pointer"

	"sigs.k8s.io/controller-runtime/pkg/client"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	profileutil "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/utils/schedstat"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup/controller"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup/runtime"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/deployments"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/namespaces"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	testprofiles "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
)

const (
	kubeletMixedCPUsConfigFile = "/etc/kubernetes/openshift-workload-mixed-cpus"
	sharedCpusResource         = "workload.openshift.io/enable-shared-cpus"
	// the minimal number of cores for running the test is as follows:
	// reserved = one core, shared = one core, infra workload = one core, test pod = one core - 4 in total
	// smt alignment won't allow us to run the test pod with a single core, hence we should cancel it.
	numberOfCoresThatRequiredCancelingSMTAlignment = 4
	restartCooldownTime                            = 1 * time.Minute
	isolatedCpusEnv                                = "OPENSHIFT_ISOLATED_CPUS"
	sharedCpusEnv                                  = "OPENSHIFT_SHARED_CPUS"
	// DeploymentName contains the name of the deployment
	DeploymentName = "test-deployment"
)

var _ = Describe("Mixedcpus", Ordered, func() {
	ctx := context.Background()
	BeforeAll(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("discovery mode enabled, performance profile not found")
		}
		teardown := setup(ctx)
		DeferCleanup(teardown, ctx)
	})

	Context("configuration files integrity", func() {
		var profile *performancev2.PerformanceProfile
		BeforeEach(func() {
			var err error
			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())

		})

		It("should deploy kubelet configuration file", func() {
			workers, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			// test arbitrary one should be good enough
			worker := &workers[0]
			cmd := isFileExistCmd(kubeletMixedCPUsConfigFile)
			out, err := nodes.ExecCommand(ctx, worker, cmd)
			found := testutils.ToString(out)
			Expect(err).ToNot(HaveOccurred(), "failed to execute command on node; cmd=%q node=%q", cmd, worker)
			Expect(found).To(Equal("true"), "file not found; file=%q", kubeletMixedCPUsConfigFile)
		})

		It("should add Kubelet systemReservedCPUs the shared cpuset", func() {
			name := components.GetComponentName(profile.Name, components.ComponentNamePrefix)
			key := client.ObjectKey{Name: name}
			kc := &machineconfigv1.KubeletConfig{}
			Expect(testclient.Client.Get(ctx, key, kc)).ToNot(HaveOccurred())
			k8sKc := &kubeletconfig.KubeletConfiguration{}
			Expect(json.Unmarshal(kc.Spec.KubeletConfig.Raw, k8sKc)).ToNot(HaveOccurred())
			reserved := mustParse(string(*profile.Spec.CPU.Reserved))
			shared := mustParse(string(*profile.Spec.CPU.Shared))
			reservedSystemCpus := mustParse(k8sKc.ReservedSystemCPUs)
			Expect(reservedSystemCpus.Equals(reserved.Union(*shared))).To(BeTrue(), "reservedSystemCPUs should contain the shared cpus; reservedSystemCPUs=%q reserved=%q shared=%q",
				reservedSystemCpus.String(), reserved.String(), shared.String())
		})

		It("should update runtime configuration with the given shared cpuset", func() {
			workers, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			// test arbitrary one should be good enough
			worker := &workers[0]
			cmd := []string{
				"chroot",
				"/rootfs",
				"/bin/bash",
				"-c",
				fmt.Sprintf("/bin/awk  -F '\"' '/shared_cpuset.*/ { print $2 }' %s", runtime.CRIORuntimeConfigFile),
			}
			out, err := nodes.ExecCommand(ctx, worker, cmd)
			cpus := testutils.ToString(out)
			Expect(err).ToNot(HaveOccurred(), "failed to execute command on node; cmd=%q node=%q", cmd, worker)
			cpus = strings.Trim(cpus, "\n")
			crioShared := mustParse(cpus)
			// don't need to check the error, the values were already validated.
			shared := mustParse(string(*profile.Spec.CPU.Shared))
			Expect(shared.Equals(*crioShared)).To(BeTrue(), "crio config file does not contain the expected shared cpuset; shared=%q crioShared=%q",
				shared.String(), crioShared.String())
		})
	})

	Context("single workload - request validation", func() {
		var profile *performancev2.PerformanceProfile
		var getter cgroup.ControllersGetter
		BeforeEach(func() {
			var err error
			profile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			// create test namespace
			ns := getTestingNamespace()
			Expect(testclient.Client.Create(ctx, &ns)).ToNot(HaveOccurred())
			DeferCleanup(func() {
				Expect(testclient.Client.Delete(ctx, &ns)).ToNot(HaveOccurred())
				Expect(namespaces.WaitForDeletion(testutils.NamespaceTesting, 5*time.Minute)).ToNot(HaveOccurred())
			})
			getter, err = cgroup.BuildGetter(ctx, testclient.Client, testclient.K8sClient)
			Expect(err).ToNot(HaveOccurred())
		})

		When("workloads requests access for shared cpus", func() {
			It("verify cpu load balancing still works with mixed cpus", func() {
				rl := &corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
					sharedCpusResource:    resource.MustParse("1"),
				}
				p, err := createPod(ctx, testclient.Client, testutils.NamespaceTesting,
					withRequests(rl),
					withLimits(rl),
					withAnnotations(map[string]string{"cpu-load-balancing.crio.io": "disable"}),
					withRuntime(components.GetComponentName(profile.Name, components.ComponentNamePrefix)))
				Expect(err).ToNot(HaveOccurred())
				cfg := &controller.CpuSet{}
				shared := mustParse(string(*profile.Spec.CPU.Shared))
				err = getter.Container(ctx, p, p.Spec.Containers[0].Name, cfg)
				Expect(err).ToNot(HaveOccurred())
				cpusIncludingShared, err := cpuset.Parse(cfg.Cpus)
				exclusivepodCpus := cpusIncludingShared.Difference(*shared)
				Expect(err).ToNot(HaveOccurred(), "unable to parse cpuset used by pod")
				// After the testpod is started get the schedstat and check for cpus
				// not participating in scheduling domains
				node := &corev1.Node{}
				err = testclient.Client.Get(ctx, client.ObjectKey{Name: p.Spec.NodeName}, node)
				Expect(err).ToNot(HaveOccurred())
				checkSchedulingDomains(node, exclusivepodCpus, func(cpuIDs cpuset.CPUSet) error {
					if !exclusivepodCpus.IsSubsetOf(cpuIDs) {
						return fmt.Errorf("pod CPUs NOT entirely part of cpus with load balance disabled: %v vs %v", exclusivepodCpus, cpuIDs)
					}
					return nil
				}, 2*time.Minute, 5*time.Second, "checking scheduling domains with pod running")

			})
			It("should have the shared cpus under its cgroups", func() {
				rl := &corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
					sharedCpusResource:    resource.MustParse("1"),
				}
				p, err := createPod(ctx, testclient.Client, testutils.NamespaceTesting,
					withRequests(rl),
					withLimits(rl),
					withRuntime(components.GetComponentName(profile.Name, components.ComponentNamePrefix)))
				Expect(err).ToNot(HaveOccurred())
				cfg := &controller.CpuSet{}
				err = getter.Container(ctx, p, p.Spec.Containers[0].Name, cfg)
				Expect(err).ToNot(HaveOccurred())
				cgroupCpuSet := mustParse(cfg.Cpus)
				Expect(err).ToNot(HaveOccurred())
				shared := mustParse(string(*profile.Spec.CPU.Shared))
				Expect(cgroupCpuSet.Intersection(*shared).List()).ToNot(BeEmpty(), "shared cpus are not in the pod cgroups; pod=%q, cgroupscpuset=%q sharedcpuset=%q",
					fmt.Sprintf("%s/%s", p.Namespace, p.Name), cgroupCpuSet.String(), shared.String())
			})
			It("should be able to disable cfs_quota", func() {
				rl := &corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
					sharedCpusResource:    resource.MustParse("1"),
				}
				p, err := createPod(ctx, testclient.Client, testutils.NamespaceTesting,
					withRequests(rl),
					withLimits(rl),
					withAnnotations(map[string]string{"cpu-quota.crio.io": "disable"}),
					withRuntime(components.GetComponentName(profile.Name, components.ComponentNamePrefix)))
				Expect(err).ToNot(HaveOccurred())
				cfg := &controller.Cpu{}
				err = getter.Container(ctx, p, p.Spec.Containers[0].Name, cfg)
				Expect(err).ToNot(HaveOccurred())
				Expect(cfg.Quota).To(Or(Equal("max"), Equal("-1")))
			})
			It("should have OPENSHIFT_ISOLATED_CPUS and OPENSHIFT_SHARED_CPUS env variables under the container", func() {
				rl := &corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
					sharedCpusResource:    resource.MustParse("1"),
				}
				p, err := createPod(ctx, testclient.Client, testutils.NamespaceTesting,
					withRequests(rl),
					withLimits(rl),
					withRuntime(components.GetComponentName(profile.Name, components.ComponentNamePrefix)))
				Expect(err).ToNot(HaveOccurred())

				By("checking environment variables are under the container's init process")
				shared := fetchSharedCPUsFromEnv(testclient.K8sClient, p, "")
				ppShared := mustParse(string(*profile.Spec.CPU.Shared))
				Expect(err).ToNot(HaveOccurred())
				Expect(shared.Equals(*ppShared)).To(BeTrue(), "OPENSHIFT_SHARED_CPUS value not equal to what configure in the performance profile."+
					"OPENSHIFT_SHARED_CPUS=%s spec.cpu.shared=%s", shared.String(), ppShared.String())

				By("checking environment variables are under the container's child process")
				cmd := printMixedCPUsEnvCmd()
				output, err := pods.ExecCommandOnPod(testclient.K8sClient, p, "", cmd)
				Expect(err).ToNot(HaveOccurred(), "failed to execute command on pod; cmd=%q pod=%q", cmd, client.ObjectKeyFromObject(p).String())
				isolatedAndShared := strings.Split(string(output), "\r\n")
				isolated := mustParse(isolatedAndShared[0])
				Expect(isolated.IsEmpty()).ToNot(BeTrue(), "isolated cpuset is empty")
				shared = mustParse(isolatedAndShared[1])
				Expect(shared.IsEmpty()).ToNot(BeTrue(), "shared cpuset is empty")
				// we don't need to check the shared and isolated values again, we only want to make sure they appear
				// in the child processes
			})
			It("should contains the shared cpus after Kubelet restarts", func() {
				rl := &corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
					sharedCpusResource:    resource.MustParse("1"),
				}
				p, err := createPod(ctx, testclient.Client, testutils.NamespaceTesting,
					withRequests(rl),
					withLimits(rl),
					withRuntime(components.GetComponentName(profile.Name, components.ComponentNamePrefix)))
				Expect(err).ToNot(HaveOccurred())
				cfg := &controller.CpuSet{}
				err = getter.Container(ctx, p, p.Spec.Containers[0].Name, cfg)
				Expect(err).ToNot(HaveOccurred())
				cgroupCpuSet, err := cpuset.Parse(cfg.Cpus)
				Expect(err).ToNot(HaveOccurred())
				shared, _ := cpuset.Parse(string(*profile.Spec.CPU.Shared))
				Expect(cgroupCpuSet.Intersection(shared).List()).ToNot(BeEmpty(), "shared cpus are not in the pod cgroups; pod=%q, cgroupscpuset=%q sharedcpuset=%q",
					fmt.Sprintf("%s/%s", p.Namespace, p.Name), cgroupCpuSet.String(), shared.String())

				node := &corev1.Node{}
				err = testclient.Client.Get(ctx, client.ObjectKey{Name: p.Spec.NodeName}, node)
				Expect(err).ToNot(HaveOccurred())

				cmd := kubeletRestartCmd()
				// The command would fail since it aborts all the pods during restart
				_, _ = nodes.ExecCommand(ctx, node, cmd)
				// check that the node is ready after we restart Kubelet
				nodes.WaitForReadyOrFail("post restart", node.Name, 20*time.Minute, 3*time.Second)

				// giving kubelet more time to stabilize and initialize itself before
				// moving on with the testing.
				testlog.Infof("post restart: entering cooldown time: %v", restartCooldownTime)
				time.Sleep(restartCooldownTime)
				testlog.Infof("post restart: finished cooldown time: %v", restartCooldownTime)

				By("verifying that shared cpus are in the container's cgroup after kubelet restart")
				err = getter.Container(ctx, p, p.Spec.Containers[0].Name, cfg)
				Expect(err).ToNot(HaveOccurred())
				cgroupCpuSet, err = cpuset.Parse(cfg.Cpus)
				Expect(err).ToNot(HaveOccurred())
				Expect(cgroupCpuSet.Intersection(shared).List()).ToNot(BeEmpty(), "shared cpus are not in the pod cgroups; pod=%q, cgroupscpuset=%q sharedcpuset=%q",
					fmt.Sprintf("%s/%s", p.Namespace, p.Name), cgroupCpuSet.String(), shared.String())
			})
		})

		When("Modifying the shared CPUs in the performance profile", func() {
			var newShared cpuset.CPUSet
			BeforeEach(func() {

				isolated := mustParse(string(*profile.Spec.CPU.Isolated))
				shared := mustParse(string(*profile.Spec.CPU.Shared))
				// select one arbitrary
				isolatedCore := mustParse(strconv.Itoa(isolated.List()[0]))
				sharedCore := mustParse(strconv.Itoa(shared.List()[0]))
				// swap one from the isolated with one from the shared
				//  to create new shared cpuset
				newIsolated := isolated.Difference(*isolatedCore).Union(*sharedCore)
				newShared = shared.Difference(*sharedCore).Union(*isolatedCore)
				profile.Spec.CPU.Isolated = cpuSetToPerformanceCPUSet(&newIsolated)
				profile.Spec.CPU.Shared = cpuSetToPerformanceCPUSet(&newShared)
				By("applying new performanceProfile")
				testprofiles.UpdateWithRetry(profile)
				mcp, err := mcps.GetByProfile(profile)
				Expect(err).ToNot(HaveOccurred())
				By("waiting for mcp to catch up")
				mcps.WaitForCondition(mcp, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
				mcps.WaitForCondition(mcp, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
				Expect(testclient.Client.Get(ctx, client.ObjectKeyFromObject(profile), profile))
				testlog.Infof("new isolated CPU set=%q\nnew shared CPU set=%q", string(*profile.Spec.CPU.Isolated), string(*profile.Spec.CPU.Isolated))
				// we do not bother to revert the profile at the end of the test, since its irrelevant which of the cpus are shared
			})

			It("should contains the updated values under the container", func() {
				rl := &corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
					sharedCpusResource:    resource.MustParse("1"),
				}
				p, err := createPod(ctx, testclient.Client, testutils.NamespaceTesting,
					withRequests(rl),
					withLimits(rl),
					withRuntime(components.GetComponentName(profile.Name, components.ComponentNamePrefix)))
				Expect(err).ToNot(HaveOccurred())

				By("checking cgroups under the container")
				cfg := &controller.CpuSet{}
				err = getter.Container(ctx, p, p.Spec.Containers[0].Name, cfg)
				Expect(err).ToNot(HaveOccurred())
				cgroupCpuSet := mustParse(cfg.Cpus)
				Expect(cgroupCpuSet.Intersection(newShared).List()).ToNot(BeEmpty(), "shared cpus are not in the pod cgroups; pod=%q, cgroupscpuset=%q sharedcpuset=%q",
					fmt.Sprintf("%s/%s", p.Namespace, p.Name), cgroupCpuSet.String(), newShared.String())

				By("checking environment variables are under the container's init process")
				sharedFromEnv := fetchSharedCPUsFromEnv(testclient.K8sClient, p, "")
				Expect(sharedFromEnv.Equals(newShared)).To(BeTrue(), "OPENSHIFT_SHARED_CPUS value not equal to what configure in the performance profile."+
					"OPENSHIFT_SHARED_CPUS=%s spec.cpu.shared=%s", sharedFromEnv.String(), newShared.String())

				By("checking environment variables are under the container's child process")
				cmd := printMixedCPUsEnvCmd()
				output, err := pods.ExecCommandOnPod(testclient.K8sClient, p, "", cmd)
				Expect(err).ToNot(HaveOccurred(), "failed to execute command on pod; cmd=%q pod=%q", cmd, client.ObjectKeyFromObject(p).String())
				isolatedAndShared := strings.Split(string(output), "\r\n")
				isolated := mustParse(isolatedAndShared[0])
				Expect(isolated.IsEmpty()).ToNot(BeTrue(), "isolated cpuset is empty")
				shared := mustParse(isolatedAndShared[1])
				Expect(shared.IsEmpty()).ToNot(BeTrue(), "shared cpuset is empty")
				// we don't need to check the shared and isolated values again, we only want to make sure they appear
				// in the child processes
			})
		})
		When("Disabling mixedCpus in the performance profile", func() {
			It("should causes all pods that uses shared CPUs to fail", func() {

				By("Creating a deployment with one pod asking for a shared cpu")
				rl := &corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
					sharedCpusResource:    resource.MustParse("1"),
				}
				p := makePod(ctx, testclient.Client, testutils.NamespaceTesting,
					withRequests(rl),
					withLimits(rl),
					withRuntime(components.GetComponentName(profile.Name, components.ComponentNamePrefix)))
				dp := deployments.Make(DeploymentName, testutils.NamespaceTesting,
					deployments.WithPodTemplate(p),
					deployments.WithNodeSelector(testutils.NodeSelectorLabels))

				Expect(testclient.Client.Create(ctx, dp)).ToNot(HaveOccurred())
				podList := &corev1.PodList{}
				listOptions := &client.ListOptions{Namespace: testutils.NamespaceTesting, LabelSelector: labels.SelectorFromSet(dp.Spec.Selector.MatchLabels)}
				Eventually(func() bool {
					isReady, err := deployments.IsReady(ctx, testclient.Client, listOptions, podList, dp)
					Expect(err).ToNot(HaveOccurred())
					return isReady
				}, time.Minute, time.Second).Should(BeTrue())
				Expect(testclient.Client.List(ctx, podList, listOptions)).To(Succeed())
				Expect(len(podList.Items)).To(Equal(1), "Expected exactly one pod in the list")
				pod := podList.Items[0]

				By("Checking environment variable under the container")
				cmd := printMixedCPUsEnvCmd()
				output, err := pods.ExecCommandOnPod(testclient.K8sClient, &pod, "", cmd)
				Expect(err).ToNot(HaveOccurred(), "failed to execute command on pod; cmd=%q pod=%q", cmd, client.ObjectKeyFromObject(p).String())
				isolatedAndShared := strings.Split(string(output), "\r\n")
				shared := mustParse(isolatedAndShared[1])
				ppShared := mustParse(string(*profile.Spec.CPU.Shared))
				Expect(err).ToNot(HaveOccurred())
				Expect(shared.Equals(*ppShared)).To(BeTrue(), "OPENSHIFT_SHARED_CPUS value not equal to what configure in the performance profile."+
					"OPENSHIFT_SHARED_CPUS=%s spec.cpu.shared=%s", shared.String(), ppShared.String())
				testlog.Infof("shared CPU set=%q", shared.String())

				By("Editing performanceProfile to have empty shared cpus and setting mixedCpus to false")
				profile.Spec.CPU.Shared = cpuSetToPerformanceCPUSet(&cpuset.CPUSet{})
				profile.Spec.WorkloadHints.MixedCpus = pointer.Bool(false)

				By("Applying new performanceProfile")
				testprofiles.UpdateWithRetry(profile)
				mcp, err := mcps.GetByProfile(profile)
				Expect(err).ToNot(HaveOccurred())

				By("Waiting for mcp to catch up")
				mcps.WaitForCondition(mcp, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
				mcps.WaitForCondition(mcp, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

				By("Verifying that the pod is failed to be scheduled")
				podList = &corev1.PodList{}
				Expect(testclient.Client.List(ctx, podList, listOptions)).To(Succeed())
				Expect(len(podList.Items)).To(Equal(1), "Expected exactly one pod in the list")
				pod = podList.Items[0]
				Eventually(func() bool {
					isFailed, err := pods.CheckPODSchedulingFailed(testclient.Client, &pod)
					Expect(err).ToNot(HaveOccurred())
					return isFailed
				}, time.Minute, time.Second).Should(BeTrue())
				Expect(pod.Status.Phase).To(Equal(corev1.PodPending), "Pod %s is not in the pending state", pod.Name)

				By("Reverting the cluster to previous state")
				Expect(testclient.Client.Get(ctx, client.ObjectKeyFromObject(profile), profile))
				profile.Spec.CPU.Shared = cpuSetToPerformanceCPUSet(ppShared)
				profile.Spec.WorkloadHints.MixedCpus = pointer.Bool(true)
				testprofiles.UpdateWithRetry(profile)
				mcp, err = mcps.GetByProfile(profile)
				Expect(err).ToNot(HaveOccurred())

				By("Waiting for mcp to catch up")
				mcps.WaitForCondition(mcp, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
				mcps.WaitForCondition(mcp, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
			})
		})
	})
})

func setup(ctx context.Context) func(ctx2 context.Context) {
	var updateNeeded bool
	profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
	Expect(err).ToNot(HaveOccurred())
	initialProfile := profile.DeepCopy()

	if !profileutil.IsMixedCPUsEnabled(profile) {
		isolated := mustParse(string(*profile.Spec.CPU.Isolated))

		// arbitrary take the first one
		sharedcpu := cpuset.New(isolated.List()[0])
		testlog.Infof("shared cpu ids are: %q", sharedcpu.String())
		updatedIsolated := isolated.Difference(sharedcpu)
		profile.Spec.CPU.Isolated = cpuSetToPerformanceCPUSet(&updatedIsolated)
		profile.Spec.CPU.Shared = cpuSetToPerformanceCPUSet(&sharedcpu)
		profile.Spec.WorkloadHints.MixedCpus = pointer.Bool(true)
		testlog.Infof("enable mixed cpus for profile %q", profile.Name)
		updateNeeded = true
	} else {
		testlog.Infof("mixed cpus already enabled for profile %q", profile.Name)
	}

	workers, err := nodes.GetByLabels(testutils.NodeSelectorLabels)
	Expect(err).ToNot(HaveOccurred())
	for _, worker := range workers {
		//node cpu numbers are integral
		numOfCores, _ := worker.Status.Capacity.Cpu().AsInt64()
		if numOfCores <= numberOfCoresThatRequiredCancelingSMTAlignment {
			profile.Annotations = map[string]string{
				"kubeletconfig.experimental": "{\"cpuManagerPolicyOptions\": {\"full-pcpus-only\": \"false\"}}",
			}
			testlog.Infof("canceling SMT alignment for nodes under profile %q", profile.Name)
			updateNeeded = true
		}
	}

	if !updateNeeded {
		return func(ctx context.Context) {
			By(fmt.Sprintf("skipping teardown - no changes to profile %q were applied", profile.Name))
		}
	}
	testprofiles.UpdateWithRetry(profile)
	mcp, err := mcps.GetByProfile(profile)
	Expect(err).ToNot(HaveOccurred())

	mcps.WaitForCondition(mcp, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
	mcps.WaitForCondition(mcp, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

	teardown := func(ctx2 context.Context) {
		By(fmt.Sprintf("executing teardown - revert profile %q back to its intial state", profile.Name))
		Expect(testclient.Client.Get(ctx2, client.ObjectKeyFromObject(initialProfile), profile))
		testprofiles.UpdateWithRetry(initialProfile)

		// do not wait if nothing has changed
		if initialProfile.ResourceVersion != profile.ResourceVersion {
			mcps.WaitForCondition(mcp, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
			mcps.WaitForCondition(mcp, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
		}
	}
	return teardown
}

func cpuSetToPerformanceCPUSet(set *cpuset.CPUSet) *performancev2.CPUSet {
	c := performancev2.CPUSet(set.String())
	return &c
}

func printMixedCPUsEnvCmd() []string {
	return []string{
		"/bin/printenv",
		"OPENSHIFT_ISOLATED_CPUS",
		"OPENSHIFT_SHARED_CPUS",
	}
}

// checks whether file exists and not empty
func isFileExistCmd(absoluteFileName string) []string {
	return []string{
		"chroot",
		"/rootfs",
		"/bin/bash",
		"-c",
		fmt.Sprintf("if [[ -s %s ]]; then echo true; else echo false; fi", absoluteFileName),
	}
}

func kubeletRestartCmd() []string {
	return []string{
		"chroot",
		"/rootfs",
		"/bin/bash",
		"-c",
		"systemctl restart kubelet",
	}
}

func createPod(ctx context.Context, c client.Client, ns string, opts ...func(pod *corev1.Pod)) (*corev1.Pod, error) {
	p := makePod(ctx, c, ns, opts...)
	if err := c.Create(ctx, p); err != nil {
		return nil, err
	}
	p, err := pods.WaitForCondition(ctx, client.ObjectKeyFromObject(p), corev1.PodReady, corev1.ConditionTrue, time.Minute)
	if err != nil {
		return nil, fmt.Errorf("failed to create pod. pod=%s, podStatus=%v err=%v", client.ObjectKeyFromObject(p).String(), p.Status, err)
	}
	return p, nil
}

func makePod(ctx context.Context, c client.Client, ns string, opts ...func(pod *corev1.Pod)) *corev1.Pod {
	p := pods.GetTestPod()
	p.Namespace = ns
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func withRequests(rl *corev1.ResourceList) func(p *corev1.Pod) {
	return func(p *corev1.Pod) {
		p.Spec.Containers[0].Resources.Requests = *rl
	}
}

func withLimits(rl *corev1.ResourceList) func(p *corev1.Pod) {
	return func(p *corev1.Pod) {
		p.Spec.Containers[0].Resources.Limits = *rl
	}
}

func withAnnotations(annot map[string]string) func(p *corev1.Pod) {
	return func(p *corev1.Pod) {
		if p.Annotations == nil {
			p.Annotations = map[string]string{}
		}
		for k, v := range annot {
			p.Annotations[k] = v
		}
	}
}

func withRuntime(name string) func(p *corev1.Pod) {
	return func(p *corev1.Pod) {
		p.Spec.RuntimeClassName = &name
	}
}

func getTestingNamespace() corev1.Namespace {
	return *namespaces.TestingNamespace
}

func mustParse(cpus string) *cpuset.CPUSet {
	GinkgoHelper()
	set, err := cpuset.Parse(cpus)
	Expect(err).ToNot(HaveOccurred(), "failed to parse cpuset; cpus=%q", cpus)
	return &set
}

func fetchSharedCPUsFromEnv(c *kubernetes.Clientset, p *corev1.Pod, containerName string) *cpuset.CPUSet {
	GinkgoHelper()
	// we should check the environment variable under the init process (pid 1)
	cmd := []string{
		"/bin/cat",
		"/proc/1/environ",
	}
	output, err := pods.ExecCommandOnPod(c, p, containerName, cmd)
	Expect(err).ToNot(HaveOccurred(), "failed to execute command on pod; cmd=%q pod=%q", cmd, client.ObjectKeyFromObject(p).String())
	Expect(string(output)).To(And(ContainSubstring(isolatedCpusEnv), ContainSubstring(sharedCpusEnv)))
	// \x00 is the NULL (C programming) of UTF-8
	envs := strings.Split(string(output), "\x00")
	var shared *cpuset.CPUSet
	for _, env := range envs {
		if strings.HasPrefix(env, sharedCpusEnv) {
			shared = mustParse(strings.Split(env, "=")[1])
		}
	}
	return shared
}

// getCPUswithLoadBalanceDisabled Return cpus which are not in any scheduling domain
func getCPUswithLoadBalanceDisabled(ctx context.Context, targetNode *corev1.Node) ([]string, error) {
	cmd := []string{"/bin/bash", "-c", "cat /proc/schedstat"}
	out, err := nodes.ExecCommand(ctx, targetNode, cmd)
	schedstatData := testutils.ToString(out)
	if err != nil {
		return nil, err
	}

	info, err := schedstat.ParseData(strings.NewReader(schedstatData))
	if err != nil {
		return nil, err
	}

	cpusWithoutDomain := []string{}
	for _, cpu := range info.GetCPUs() {
		doms, ok := info.GetDomains(cpu)
		if !ok {
			return nil, fmt.Errorf("unknown cpu: %v", cpu)
		}
		if len(doms) > 0 {
			continue
		}
		cpusWithoutDomain = append(cpusWithoutDomain, cpu)
	}

	return cpusWithoutDomain, nil
}

// checkSchedulingDomains Check cpus are part of any scheduling domain
func checkSchedulingDomains(workerRTNode *corev1.Node, podCpus cpuset.CPUSet, testFunc func(cpuset.CPUSet) error, timeout, polling time.Duration, errMsg string) {
	Eventually(func() error {
		cpusNotInSchedulingDomains, err := getCPUswithLoadBalanceDisabled(context.TODO(), workerRTNode)
		Expect(err).ToNot(HaveOccurred())
		testlog.Infof("cpus with load balancing disabled are: %v", cpusNotInSchedulingDomains)
		Expect(err).ToNot(HaveOccurred(), "unable to fetch cpus with load balancing disabled from /proc/schedstat")
		cpuIDList, err := schedstat.MakeCPUIDListFromCPUList(cpusNotInSchedulingDomains)
		if err != nil {
			return err
		}
		cpuIDs := cpuset.New(cpuIDList...)
		return testFunc(cpuIDs)
	}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).ShouldNot(HaveOccurred(), errMsg)

}
