package profilesupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mcv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	profilecontroller "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
)

// UpdateIsolatedReservedCpus Updates the current performance profile with new sets of isolated and reserved cpus, and returns true if the update was successfull and false otherwise
func UpdateIsolatedReservedCpus(profile *performancev2.PerformanceProfile, isolatedSet performancev2.CPUSet, reservedSet performancev2.CPUSet) error {

	profile.Spec.CPU = &performancev2.CPU{
		Isolated: &isolatedSet,
		Reserved: &reservedSet,
	}

	err := ApplyProfile(profile)
	if err == nil {
		testlog.Infof("successfully updated performance profile %q with new isolated cpus set: %q and new reserved cpus set: %q", profile.Name, string(*profile.Spec.CPU.Isolated), string(*profile.Spec.CPU.Reserved))
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
	spec, err := json.Marshal(profile.Spec)
	Expect(err).ToNot(HaveOccurred())
	Expect(testclient.Client.Patch(context.TODO(), profile,
		client.RawPatch(
			types.JSONPatchType,
			[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec", "value": %s }]`, spec)),
		),
	)).ToNot(HaveOccurred())

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
