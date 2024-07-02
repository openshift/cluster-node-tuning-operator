package profilesupdate

import (
	"context"
	"fmt"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	mcv1 "github.com/openshift/api/machineconfiguration/v1"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	profilecontroller "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/hypershift"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodepools"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
)

// UpdateIsolatedReservedCpus Updates the current performance profile with new sets of isolated and reserved cpus, and returns true if the update was successfull and false otherwise
func UpdateIsolatedReservedCpus(isolatedSet performancev2.CPUSet, reservedSet performancev2.CPUSet) error {
	profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
	if err != nil {
		return fmt.Errorf("could not get the performance profile: %v", err)
	}
	updatedProfile := profile.DeepCopy()
	updatedProfile.Spec.CPU = &performancev2.CPU{
		Isolated: &isolatedSet,
		Reserved: &reservedSet,
	}

	err = ApplyProfile(updatedProfile)
	if err == nil {
		testlog.Infof("successfully updated performance profile %q with new isolated cpus set: %q and new reserved cpus set: %q", profile.Name, string(*updatedProfile.Spec.CPU.Isolated), string(*updatedProfile.Spec.CPU.Reserved))
	}
	return err
}

// ApplyProfile applies the new profile and returns true if the changes were applied indeed and false otherwise
func ApplyProfile(profile *performancev2.PerformanceProfile) error {
	testlog.Info("Getting MCP for profile")
	mcpLabel := profilecontroller.GetMachineConfigLabel(profile)
	key, value := components.GetFirstKeyAndValue(mcpLabel)
	mcpsByLabel, err := mcps.GetByLabel(key, value)
	if err != nil {
		return fmt.Errorf("failed getting MCP by label key %v value %v: %v", key, value, err)
	}
	if len(mcpsByLabel) != 1 {
		return fmt.Errorf("unexpected number of MCPs found: %v", len(mcpsByLabel))
	}
	performanceMCP := &mcpsByLabel[0]
	testlog.Info("Verifying that mcp is ready for update")
	mcps.WaitForCondition(performanceMCP.Name, mcv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

	testlog.Info("Applying changes in performance profile and waiting until mcp will start updating")
	profiles.UpdateWithRetry(profile)
	mcps.WaitForCondition(performanceMCP.Name, mcv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

	testlog.Info("Waiting when mcp finishes updates")
	mcps.WaitForCondition(performanceMCP.Name, mcv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

	//check if the values were indeed updated
	profilekey := types.NamespacedName{
		Name:      profile.Name,
		Namespace: profile.Namespace,
	}
	updatedProfile := &performancev2.PerformanceProfile{}
	if err = testclient.Client.Get(context.TODO(), profilekey, updatedProfile); err != nil {
		return fmt.Errorf("could not fetch the profile: %v", err)
	}

	if !reflect.DeepEqual(updatedProfile.Spec, profile.Spec) {
		return fmt.Errorf("the profile %q was not updated as expected", updatedProfile.Name)
	}
	return nil
}

func WaitForTuningUpdating(ctx context.Context, profile *performancev2.PerformanceProfile) {
	GinkgoHelper()
	// In case we are on OCP, we can query the MCP to determine if the update has started.
	if !hypershift.IsHypershiftCluster() {
		performanceMCP, err := mcps.GetByProfile(profile)
		Expect(err).ToNot(HaveOccurred())
		mcps.WaitForCondition(performanceMCP, mcv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
		return
	}

	// On hypershift we can check the nodepool UpdatingConfig condition to determine if the update has started.
	hostedClusterName, err := hypershift.GetHostedClusterName()
	Expect(err).ToNot(HaveOccurred())
	np, err := nodepools.GetByClusterName(ctx, testclient.ControlPlaneClient, hostedClusterName)
	Expect(err).ToNot(HaveOccurred())
	err = nodepools.WaitForUpdatingConfig(ctx, testclient.ControlPlaneClient, np.Name, np.Namespace)
	Expect(err).ToNot(HaveOccurred())
}

func WaitForTuningUpdated(ctx context.Context, profile *performancev2.PerformanceProfile) {
	GinkgoHelper()
	// In case we are on OCP, we can query the MCP to determine if the update has completed.
	if !hypershift.IsHypershiftCluster() {
		performanceMCP, err := mcps.GetByProfile(profile)
		Expect(err).ToNot(HaveOccurred())
		mcps.WaitForCondition(performanceMCP, mcv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
		return
	}

	// On hypershift, we can check the nodepool UpdatingConfig condition to determine if the update has completed.
	hostedClusterName, err := hypershift.GetHostedClusterName()
	Expect(err).ToNot(HaveOccurred())
	np, err := nodepools.GetByClusterName(ctx, testclient.ControlPlaneClient, hostedClusterName)
	Expect(err).ToNot(HaveOccurred())
	err = nodepools.WaitForConfigToBeReady(ctx, testclient.ControlPlaneClient, np.Name, np.Namespace)
	Expect(err).ToNot(HaveOccurred())
}
