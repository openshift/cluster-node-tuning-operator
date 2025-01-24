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
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/infrastructure"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
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

var _ = Describe("[Performance] Last Level Cache", Label(string(label.OpenShift)), func() {
	var (
		workerRTNodes      []corev1.Node
		perfProfile        *performancev2.PerformanceProfile
		performanceMCP     string
		isAMD              bool
		ctx                context.Context = context.Background()
		err                error
		profileAnnotations = make(map[string]string)
		poolName           string
	)
	testutils.CustomBeforeAll(func() {

		workerRTNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		workerRTNodes, err = nodes.MatchingOptionalSelector(workerRTNodes)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("error looking for the optional selector: %v", err))

		isAMD, err = infrastructure.IsAMD(ctx, &workerRTNodes[0])
		Expect(err).ToNot(HaveOccurred(), "Unable to fetch Vendor ID")
		if !isAMD {
			Skip("AMD LLC Tests can only run on AMD Hardware")
		}

		perfProfile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())

		performanceMCP, err = mcps.GetByProfile(perfProfile)
		Expect(err).ToNot(HaveOccurred())

		poolName = poolname.GetByProfile(ctx, perfProfile)

		mc, err := createMachineConfig(perfProfile)
		Expect(err).ToNot(HaveOccurred())

		By("Enabling AMD LLC Uncore cache feature")
		err = testclient.Client.Create(context.TODO(), mc)

		Expect(err).ToNot(HaveOccurred(), "Unable to apply machine config for enabling AMD llc")

		mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
		By("Waiting when mcp finishes updates")

		mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
	})

	Context("Configuration Tests", Ordered, func() {
		llcPolicy := "{\"cpuManagerPolicyOptions\":{\"prefer-align-cpus-by-uncorecache\":\"true\", \"full-pcpus-only\":\"true\"}}"
		profileAnnotations["kubeletconfig.experimental"] = llcPolicy

		It("[test_id: 77722] Kubelet is configured appropriately when align-cpus-by-uncorecache is enabled through Performance profile", func() {

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
				Expect(kubeletconfig.CPUManagerPolicyOptions["prefer-align-cpus-by-uncorecache"]).To(Equal("true"))
			}
		})

		It("[test_id: 77723] Removing the kubelet annotation should disable align-cpus-by-uncorecache policy", func() {

			// Delete the Annotations
			if perfProfile.Annotations != nil {
				perfProfile.Annotations = nil
			}
			fmt.Println("AFter updating ", perfProfile.Annotations)

			By("updating performance profile")
			profiles.UpdateWithRetry(perfProfile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(ctx, perfProfile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(ctx, perfProfile)

			for _, node := range workerRTNodes {
				kubeletconfig, err := nodes.GetKubeletConfig(ctx, &node)
				Expect(err).ToNot(HaveOccurred())
				Expect(kubeletconfig.CPUManagerPolicyOptions).ToNot(HaveKey("prefer-align-cpus-by-uncorecache"))
			}
		})

		It("[test_id: 77724] disabling align-cpus-by-uncorecache explicitly through annotation", func() {

			llcDisablePolicy := "{\"cpuManagerPolicyOptions\":{\"prefer-align-cpus-by-uncorecache\":\"false\", \"full-pcpus-only\":\"true\"}}"
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
				Expect(kubeletconfig.CPUManagerPolicyOptions["prefer-align-cpus-by-uncorecache"]).To(Equal("false"))
			}
		})
	})
})

// create Machine config to create text file required to enable prefer-align-cpus-by-uncorecache policy
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
