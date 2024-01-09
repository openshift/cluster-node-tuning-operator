package __mixedcpus

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	kubeletconfig "k8s.io/kubelet/config/v1beta1"
	"k8s.io/utils/cpuset"
	"k8s.io/utils/pointer"

	"sigs.k8s.io/controller-runtime/pkg/client"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	profileutil "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup/controller"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cgroup/runtime"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/namespaces"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	testprofiles "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

const (
	kubeletMixedCPUsConfigFile = "/etc/kubernetes/openshift-workload-mixed-cpus"
	sharedCpusResource         = "workload.openshift.io/enable-shared-cpus"
	// the minimal number of cores for running the test is as follows:
	// reserved = one core, shared = one core, infra workload = one core, test pod = one core - 4 in total
	// smt alignment won't allow us to run the test pod with a single core, hence we should cancel it.
	numberOfCoresThatRequiredCancelingSMTAlignment = 4
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
			found, err := nodes.ExecCommandOnNode(ctx, cmd, worker)
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
			// don't need to check the error, the values were already validated.
			reserved, _ := cpuset.Parse(string(*profile.Spec.CPU.Reserved))
			shared, _ := cpuset.Parse(string(*profile.Spec.CPU.Shared))
			reservedSystemCpus, _ := cpuset.Parse(k8sKc.ReservedSystemCPUs)
			Expect(reservedSystemCpus.Equals(reserved.Union(shared))).To(BeTrue(), "reservedSystemCPUs should contain the shared cpus; reservedSystemCPUs=%q reserved=%q shared=%q",
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
			cpus, err := nodes.ExecCommandOnNode(ctx, cmd, worker)
			Expect(err).ToNot(HaveOccurred(), "failed to execute command on node; cmd=%q node=%q", cmd, worker)
			cpus = strings.Trim(cpus, "\n")
			crioShared, err := cpuset.Parse(cpus)
			Expect(err).ToNot(HaveOccurred())
			// don't need to check the error, the values were already validated.
			shared, _ := cpuset.Parse(string(*profile.Spec.CPU.Shared))
			Expect(shared.Equals(crioShared)).To(BeTrue(), "crio config file does not contain the expected shared cpuset; shared=%q crioShared=%q",
				shared.String(), crioShared.String())
		})
	})

	When("workloads requests access for shared cpus", func() {
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
			cgroupCpuSet, err := cpuset.Parse(cfg.Cpus)
			Expect(err).ToNot(HaveOccurred())
			shared, _ := cpuset.Parse(string(*profile.Spec.CPU.Shared))
			Expect(cgroupCpuSet.Intersection(shared).List()).ToNot(BeEmpty(), "shared cpus are not in the pod cgroups; pod=%q, cgroupscpuset=%q sharedcpuset=%q",
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
			cmd := printMixedCPUsEnvCmd()
			output, err := pods.ExecCommandOnPod(testclient.K8sClient, p, "", cmd)
			Expect(err).ToNot(HaveOccurred(), "failed to execute command on pod; cmd=%q pod=%q", cmd, client.ObjectKeyFromObject(p).String())
			isolatedAndShared := strings.Split(string(output), "\r\n")
			isolated, err := cpuset.Parse(isolatedAndShared[0])
			Expect(err).ToNot(HaveOccurred())
			Expect(isolated.IsEmpty()).ToNot(BeTrue())
			shared, err := cpuset.Parse(isolatedAndShared[1])
			ppShared, _ := cpuset.Parse(string(*profile.Spec.CPU.Shared))
			Expect(err).ToNot(HaveOccurred())
			Expect(shared.Equals(ppShared)).To(BeTrue(), "OPENSHIFT_SHARED_CPUS value not equal to what configure in the performance profile."+
				"OPENSHIFT_SHARED_CPUS=%s spec.cpu.shared=%s", shared.String(), ppShared.String())
		})
	})
})

func setup(ctx context.Context) func(ctx2 context.Context) {
	var updateNeeded bool
	profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
	Expect(err).ToNot(HaveOccurred())
	initialProfile := profile.DeepCopy()

	if !profileutil.IsMixedCPUsEnabled(profile) {
		isolated, err := cpuset.Parse(string(*profile.Spec.CPU.Isolated))
		Expect(err).ToNot(HaveOccurred())

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

func createPod(ctx context.Context, c client.Client, ns string, opts ...func(pod *corev1.Pod)) (*corev1.Pod, error) {
	p := pods.GetTestPod()
	p.Namespace = ns
	for _, opt := range opts {
		opt(p)
	}
	if err := c.Create(ctx, p); err != nil {
		return nil, err
	}
	p, err := pods.WaitForCondition(client.ObjectKeyFromObject(p), corev1.PodReady, corev1.ConditionTrue, time.Minute)
	if err != nil {
		return nil, fmt.Errorf("failed to create pod. pod=%s, podStatus=%v err=%v", client.ObjectKeyFromObject(p).String(), p.Status, err)
	}
	return p, nil
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
