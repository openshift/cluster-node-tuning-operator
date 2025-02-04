package __llc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	igntypes "github.com/coreos/ignition/v2/config/v3_2/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	profilecomponent "github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"

	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"

	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/poolname"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profilesupdate"
)

const (
	llcEnableFileName            = "/etc/kubernetes/openshift-llc-alignment"
	defaultIgnitionContentSource = "data:text/plain;charset=utf-8;base64"
	defaultIgnitionVersion       = "3.2.0"
	fileMode                     = 0420
)

var _ = Describe("[rfe_id:77446] LLC-aware cpu pinning", Label(string(label.OpenShift)), Ordered, func() {
	var (
		workerRTNodes      []corev1.Node
		perfProfile        *performancev2.PerformanceProfile
		performanceMCP     string
		err                error
		profileAnnotations map[string]string
		poolName           string
		llcPolicy          string
		mc                 *machineconfigv1.MachineConfig
	)

	BeforeAll(func() {
		var ctx context.Context
		profileAnnotations = make(map[string]string)
		ctx = context.Background()

		workerRTNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("error looking for the optional selector: %v", err))

		perfProfile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		performanceMCP, err = mcps.GetByProfile(perfProfile)
		Expect(err).ToNot(HaveOccurred())

		poolName = poolname.GetByProfile(ctx, perfProfile)

		mc, err = createMachineConfig(perfProfile)
		Expect(err).ToNot(HaveOccurred())

		llcPolicy = `{"cpuManagerPolicyOptions":{"prefer-align-cpus-by-uncorecache":"true", "full-pcpus-only":"true"}}`
		profileAnnotations["kubeletconfig.experimental"] = llcPolicy

		// Create machine config to create file /etc/kubernetes/openshift-llc-alignment
		// required to enable align-cpus-by-uncorecache cpumanager policy

		By("Enabling Uncore cache feature")
		Expect(testclient.Client.Create(context.TODO(), mc)).To(Succeed(), "Unable to apply machine config for enabling uncore cache")

		Expect(err).ToNot(HaveOccurred(), "Unable to apply machine config for enabling uncore cache")

		mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
		By("Waiting when mcp finishes updates")

		mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

		// Apply Annotation to enable align-cpu-by-uncorecache cpumanager policy option
		if perfProfile.Annotations == nil || perfProfile.Annotations["kubeletconfig.experimental"] != llcPolicy {
			testlog.Info("Enable align-cpus-by-uncorecache cpumanager policy")
			perfProfile.Annotations = profileAnnotations

			By("updating performance profile")
			profiles.UpdateWithRetry(perfProfile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(ctx, perfProfile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(ctx, perfProfile)
		}

	})

	AfterAll(func() {

		// Delete machine config created to enable uncocre cache cpumanager policy option
		// first make sure the profile doesn't have the annotation
		var ctx context.Context
		perfProfile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		perfProfile.Annotations = nil
		By("updating performance profile")
		profiles.UpdateWithRetry(perfProfile)

		By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
		profilesupdate.WaitForTuningUpdating(ctx, perfProfile)

		By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
		profilesupdate.WaitForTuningUpdated(ctx, perfProfile)

		// delete the machine config pool
		Expect(testclient.Client.Delete(ctx, mc)).To(Succeed())

		mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
		By("Waiting when mcp finishes updates")

		mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
	})

	Context("Configuration Tests", func() {
		When("align-cpus-by-uncorecache cpumanager policy option is enabled", func() {
			It("[test_id:77722] kubelet is configured appropriately", func() {
				for _, node := range workerRTNodes {
					kubeletconfig, err := nodes.GetKubeletConfig(ctx, &node)
					Expect(err).ToNot(HaveOccurred())
					Expect(kubeletconfig.CPUManagerPolicyOptions).To(HaveKeyWithValue("prefer-align-cpus-by-uncorecache", "true"))
				}
			})
		})

		When("align-cpus-by-uncorecache annotations is removed", func() {
			It("[test_id:77723] should disable align-cpus-by-uncorecache cpumanager policy option", func() {
				var ctx context.Context
				// Delete the Annotations
				if perfProfile.Annotations != nil {
					perfProfile.Annotations = nil

					By("updating performance profile")
					profiles.UpdateWithRetry(perfProfile)

					By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
					profilesupdate.WaitForTuningUpdating(ctx, perfProfile)

					By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
					profilesupdate.WaitForTuningUpdated(ctx, perfProfile)
				}

				for _, node := range workerRTNodes {
					kubeletconfig, err := nodes.GetKubeletConfig(ctx, &node)
					Expect(err).ToNot(HaveOccurred())
					Expect(kubeletconfig.CPUManagerPolicyOptions).ToNot(HaveKey("prefer-align-cpus-by-uncorecache"))
				}
			})
		})

		When("align-cpus-by-uncorecache cpumanager policy option is disabled", func() {
			It("[test_id:77724] cpumanager Policy option in kubelet is configured appropriately", func() {
				var ctx context.Context
				llcDisablePolicy := `{"cpuManagerPolicyOptions":{"prefer-align-cpus-by-uncorecache":"false", "full-pcpus-only":"true"}}`
				profileAnnotations["kubeletconfig.experimental"] = llcDisablePolicy
				perfProfile.Annotations = profileAnnotations

				By("updating performance profile")
				profiles.UpdateWithRetry(perfProfile)

				By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
				profilesupdate.WaitForTuningUpdating(ctx, perfProfile)

				By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
				profilesupdate.WaitForTuningUpdated(ctx, perfProfile)

				for _, node := range workerRTNodes {
					kubeletconfig, err := nodes.GetKubeletConfig(ctx, &node)
					Expect(err).ToNot(HaveOccurred())
					Expect(kubeletconfig.CPUManagerPolicyOptions).To(HaveKeyWithValue("prefer-align-cpus-by-uncorecache", "false"))
				}
			})
		})
	})
})

// create Machine config to create text file required to enable prefer-align-cpus-by-uncorecache policy option
func createMachineConfig(perfProfile *performancev2.PerformanceProfile) (*machineconfigv1.MachineConfig, error) {
	mcName := "openshift-llc-alignment"
	mc := &machineconfigv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: machineconfigv1.GroupVersion.String(),
			Kind:       "MachineConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:   mcName,
			Labels: profilecomponent.GetMachineConfigLabel(perfProfile),
		},
		Spec: machineconfigv1.MachineConfigSpec{},
	}
	ignitionConfig := &igntypes.Config{
		Ignition: igntypes.Ignition{
			Version: defaultIgnitionVersion,
		},
		Storage: igntypes.Storage{
			Files: []igntypes.File{},
		},
	}
	addContent(ignitionConfig, []byte("enabled"), llcEnableFileName, fileMode)
	rawIgnition, err := json.Marshal(ignitionConfig)
	if err != nil {
		return nil, err
	}
	mc.Spec.Config = runtime.RawExtension{Raw: rawIgnition}
	return mc, nil
}

// creates the ignitionConfig file
func addContent(ignitionConfig *igntypes.Config, content []byte, dst string, mode int) {
	contentBase64 := base64.StdEncoding.EncodeToString(content)
	ignitionConfig.Storage.Files = append(ignitionConfig.Storage.Files, igntypes.File{
		Node: igntypes.Node{
			Path: dst,
		},
		FileEmbedded1: igntypes.FileEmbedded1{
			Contents: igntypes.Resource{
				Source: ptr.To(fmt.Sprintf("%s,%s", defaultIgnitionContentSource, contentBase64)),
			},
			Mode: &mode,
		},
	})
}
