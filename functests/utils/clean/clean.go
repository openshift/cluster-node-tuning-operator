package clean

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components/profile"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	performancev2 "github.com/openshift-kni/performance-addon-operators/api/v2"
	"github.com/openshift-kni/performance-addon-operators/functests/utils"
	testclient "github.com/openshift-kni/performance-addon-operators/functests/utils/client"
	testlog "github.com/openshift-kni/performance-addon-operators/functests/utils/log"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/mcps"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/profiles"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components"
	mcv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

var cleanPerformance bool

func init() {
	clean, found := os.LookupEnv("CLEAN_PERFORMANCE_PROFILE")
	if !found || clean != "false" {
		cleanPerformance = true
	}
}

// All deletes any leftovers created when running the performance tests.
func All() {
	if !cleanPerformance {
		testlog.Info("Performance cleaning disabled, skipping")
		return
	}

	perfProfile := performancev2.PerformanceProfile{}
	err := testclient.Client.Get(context.TODO(), types.NamespacedName{Name: utils.PerformanceProfileName}, &perfProfile)
	if errors.IsNotFound(err) {
		return
	}
	Expect(err).ToNot(HaveOccurred(), "Failed to find perf profile")
	mcpLabel := profile.GetMachineConfigLabel(&perfProfile)
	key, value := components.GetFirstKeyAndValue(mcpLabel)
	mcpsByLabel, err := mcps.GetByLabel(key, value)
	Expect(err).ToNot(HaveOccurred(), "Failed getting MCP")
	Expect(len(mcpsByLabel)).To(Equal(1), fmt.Sprintf("Unexpected number of MCPs found: %v", len(mcpsByLabel)))

	performanceMCP := &mcpsByLabel[0]

	err = testclient.Client.Delete(context.TODO(), &perfProfile)
	Expect(err).ToNot(HaveOccurred(), "Failed to delete perf profile")

	By("Waiting for MCP starting to update")
	mcps.WaitForCondition(performanceMCP.Name, mcv1.MachineConfigPoolUpdating, corev1.ConditionTrue)

	By("Waiting for MCP being updated")
	mcps.WaitForCondition(performanceMCP.Name, mcv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
	profileKey := types.NamespacedName{
		Name:      perfProfile.Name,
		Namespace: perfProfile.Namespace,
	}
	err = profiles.WaitForDeletion(profileKey, 60*time.Second)
	Expect(err).ToNot(HaveOccurred(), "Failed to wait for perf profile deletion")
}
