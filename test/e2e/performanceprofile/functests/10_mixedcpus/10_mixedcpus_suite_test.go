package __mixedcpus_test

import (
	"context"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	corev1 "k8s.io/api/core/v1"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/kubernetes/pkg/kubelet/cm/cpuset"
	"k8s.io/utils/pointer"

	"sigs.k8s.io/controller-runtime/pkg/client"

	_ "github.com/openshift-kni/mixed-cpu-node-plugin/test/e2e/mixedcpus"

	v2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
)

func TestMixedcpus(t *testing.T) {
	BeforeSuite(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}
		profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		initialProfile := profile.DeepCopy()
		isolated, err := cpuset.Parse(string(*profile.Spec.CPU.Isolated))
		Expect(err).ToNot(HaveOccurred())
		// arbitrary take the first one
		sharedcpu := cpuset.New(isolated.List()[0])
		updatedIsolated := isolated.Difference(sharedcpu)
		profile.Spec.CPU.Isolated = CPUSetToPerformanceCPUSet(&updatedIsolated)
		profile.Spec.CPU.Shared = CPUSetToPerformanceCPUSet(&sharedcpu)
		profile.Spec.WorkloadHints.MixedCpus = pointer.Bool(true)
		Expect(testclient.Client.Update(context.TODO(), profile)).ToNot(HaveOccurred(), "failed to update profile: %q", profile.Name)
		mcp, err := mcps.GetByProfile(profile)
		Expect(err).ToNot(HaveOccurred())
		mcps.WaitForCondition(mcp, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
		mcps.WaitForCondition(mcp, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

		DeferCleanup(func() {
			Expect(testclient.Client.Get(context.TODO(), client.ObjectKeyFromObject(initialProfile), profile))
			profile.Spec = initialProfile.Spec
			Expect(testclient.Client.Update(context.TODO(), profile)).ToNot(HaveOccurred(), "failed to update profile: %q", profile.Name)
			mcps.WaitForCondition(mcp, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
			mcps.WaitForCondition(mcp, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
		})
	})
	RegisterFailHandler(Fail)
	RunSpecs(t, "10Mixedcpus Suite")
}

func CPUSetToPerformanceCPUSet(set *cpuset.CPUSet) *v2.CPUSet {
	c := v2.CPUSet(set.String())
	return &c
}
