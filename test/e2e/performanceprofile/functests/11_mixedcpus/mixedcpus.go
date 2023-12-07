package __mixedcpus

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	kubeletconfig "k8s.io/kubelet/config/v1beta1"
	"k8s.io/utils/cpuset"
	"k8s.io/utils/pointer"

	"sigs.k8s.io/controller-runtime/pkg/client"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	profileutil "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	testprofiles "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

const (
	crioRuntimeConfigFile      = "/etc/crio/crio.conf.d/99-runtimes.conf"
	kubeletMixedCPUsConfigFile = "/etc/kubernetes/openshift-workload-mixed-cpus"
)

var _ = Describe("Mixedcpus", Ordered, func() {
	ctx := context.Background()
	BeforeAll(func() {
		var err error
		profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		if profileutil.IsMixedCPUsEnabled(profile) {
			testlog.Infof("mixed cpus already enabled for profile %q", profile.Name)
		}
		if !profileutil.IsMixedCPUsEnabled(profile) {
			testlog.Infof("enable mixed cpus for profile %q", profile.Name)
			teardown := setup(ctx, profile)
			DeferCleanup(teardown, ctx)
		}
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
			found, err := nodes.ExecCommandOnNode(cmd, worker)
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
				fmt.Sprintf("/bin/awk  -F '\"' '/shared_cpuset.*/ { print $2 }' %s", crioRuntimeConfigFile),
			}
			cpus, err := nodes.ExecCommandOnNode(cmd, worker)
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
})

func setup(ctx context.Context, profile *performancev2.PerformanceProfile) func(ctx context.Context) {
	initialProfile := profile.DeepCopy()
	isolated, err := cpuset.Parse(string(*profile.Spec.CPU.Isolated))
	Expect(err).ToNot(HaveOccurred())

	// arbitrary take the first one
	sharedcpu := cpuset.New(isolated.List()[0])
	testlog.Infof("shared cpu ids are: %q", sharedcpu.String())
	updatedIsolated := isolated.Difference(sharedcpu)
	profile.Spec.CPU.Isolated = cpuSetToPerformanceCPUSet(&updatedIsolated)
	profile.Spec.CPU.Shared = cpuSetToPerformanceCPUSet(&sharedcpu)
	profile.Spec.WorkloadHints.MixedCpus = pointer.Bool(true)
	testprofiles.UpdateWithRetry(profile)

	mcp, err := mcps.GetByProfile(profile)
	Expect(err).ToNot(HaveOccurred())

	mcps.WaitForCondition(mcp, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
	mcps.WaitForCondition(mcp, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

	teardown := func(ctx context.Context) {
		By(fmt.Sprintf("executing teardown - revert profile %q back to its intial state", profile.Name))
		Expect(testclient.Client.Get(ctx, client.ObjectKeyFromObject(initialProfile), profile))
		profile.Spec = initialProfile.Spec
		resourceVersion := profile.ResourceVersion
		testprofiles.UpdateWithRetry(profile)

		// do not wait if nothing has changed
		if resourceVersion != profile.ResourceVersion {
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
