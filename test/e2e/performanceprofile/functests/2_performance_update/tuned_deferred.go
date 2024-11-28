package __performance_update

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"gopkg.in/ini.v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"

	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profilesupdate"
	tunedutil "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/tuned"
)

var _ = Describe("Tuned Deferred tests of performance profile", Ordered, Label(string(label.TunedDeferred)), func() {
	var (
		workerCNFNodes []corev1.Node
		err            error
		poolName       string
		tuned          *tunedv1.Tuned
		initialPolicy  *string
	)

	type TunedProfileConfig struct {
		DeferMode         string
		ProfileChangeType string // "first-time" or "in-place"
		ExpectedBehavior  string // "deferred" or "immediate"
		KernelSHMMNI      string // 4096 or 8192
	}

	name := "performance-patch"

	BeforeAll(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}
		workerCNFNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		workerCNFNodes, err = nodes.MatchingOptionalSelector(workerCNFNodes)
		Expect(err).ToNot(HaveOccurred(), "error looking for the optional selector: %v", err)
		Expect(workerCNFNodes).ToNot(BeEmpty())

		profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		initialPolicy = profile.Spec.NUMA.TopologyPolicy

		poolName, err = mcps.GetByProfile(profile)
		Expect(err).ToNot(HaveOccurred())
		testlog.Infof("using performanceMCP: %q", poolName)
	})

	Context("Tuned Deferred status", func() {
		DescribeTable("Validate Tuned DeferMode behavior",
			func(tc TunedProfileConfig) {

				if tc.ProfileChangeType == "first-time" {
					tuned = getTunedProfile(name)

					// If profile is present
					if tuned != nil {

						// Delete the profile
						testlog.Infof("Deleting Tuned Profile Patch: %s", name)
						Expect(testclient.ControlPlaneClient.Delete(context.TODO(), tuned)).To(Succeed())
						Eventually(func() bool {
							profile, _ := tunedutil.GetProfile(context.TODO(), testclient.ControlPlaneClient, components.NamespaceNodeTuningOperator, workerCNFNodes[0].Name)
							return profile.Spec.Config.TunedProfile == "openshift-node-performance-performance"
						}, 2*time.Minute, 10*time.Second).Should(BeTrue(), "Tuned profile deletion did not complete in time")
					}
					tuned, err = createTunedObject(tc.DeferMode, tc.KernelSHMMNI, name)
					Expect(err).NotTo(HaveOccurred())

				} else {
					tuned := getTunedProfile(name)
					tunedData := []byte(*tuned.Spec.Profile[0].Data)

					cfg, err := ini.Load(tunedData)
					Expect(err).ToNot(HaveOccurred())

					sysctlSection, err := cfg.GetSection("sysctl")
					Expect(err).ToNot(HaveOccurred())

					sysctlSection.Key("kernel.shmmni").SetValue(tc.KernelSHMMNI)
					Expect(sysctlSection.Key("kernel.shmmni").String()).To(Equal(tc.KernelSHMMNI))

					var updatedTunedData bytes.Buffer
					_, err = cfg.WriteTo(&updatedTunedData)
					Expect(err).ToNot(HaveOccurred())

					updatedTunedDataBytes := updatedTunedData.Bytes()
					tuned.Spec.Profile[0].Data = stringPtr(string(updatedTunedDataBytes))

					testlog.Infof("Updating Tuned Profile Patch: %s", name)
					Expect(testclient.ControlPlaneClient.Update(context.TODO(), tuned)).To(Succeed())
					Eventually(func() string {
						patch := getTunedProfile(name)
						return *patch.Spec.Profile[0].Data
					}, 1*time.Minute, 5*time.Second).Should(ContainSubstring(tc.KernelSHMMNI), "The updated tuned profile section did not contain the expected value")
				}

				switch tc.ExpectedBehavior {
				case "immediate":
					verifyImmediateApplication(tuned, workerCNFNodes, tc.KernelSHMMNI)
				case "deferred":
					Eventually(func() bool {
						result, err := verifyDeferredApplication(tuned, workerCNFNodes, name)
						Expect(err).To(BeNil())
						return result
					}, 1*time.Minute, 5*time.Second).Should(BeTrue(), "Timed out checking deferred condition message")
					rebootNode(poolName)
					verifyImmediateApplication(tuned, workerCNFNodes, tc.KernelSHMMNI)
				}
			},
			Entry("[test_id:78115] Verify deferred Always with first-time profile change", TunedProfileConfig{
				DeferMode:         "always",
				ProfileChangeType: "first-time",
				ExpectedBehavior:  "deferred",
				KernelSHMMNI:      "8192",
			}),
			Entry("[test_id:78116] Verify deferred Always with in-place profile update", TunedProfileConfig{
				DeferMode:         "always",
				ProfileChangeType: "in-place",
				ExpectedBehavior:  "deferred",
				KernelSHMMNI:      "4096",
			}),
			Entry("[test_id:78117] Verify deferred Update with first-time profile change", TunedProfileConfig{
				DeferMode:         "update",
				ProfileChangeType: "first-time",
				ExpectedBehavior:  "immediate",
				KernelSHMMNI:      "8192",
			}),
			Entry("[test_id:78118] Verify deferred Update with in-place profile update", TunedProfileConfig{
				DeferMode:         "update",
				ProfileChangeType: "in-place",
				ExpectedBehavior:  "deferred",
				KernelSHMMNI:      "4096",
			}),
			Entry("[test_id:78119] Verify deferred with No annotation with first-time profile change", TunedProfileConfig{
				DeferMode:         "",
				ProfileChangeType: "first-time",
				ExpectedBehavior:  "immediate",
				KernelSHMMNI:      "8192",
			}),
			Entry("[test_id:78120] Verify deferred Never mode with in-place profile update", TunedProfileConfig{
				DeferMode:         "never",
				ProfileChangeType: "in-place",
				ExpectedBehavior:  "immediate",
				KernelSHMMNI:      "4096",
			}),
		)
	})

	AfterAll(func() {
		tuned = getTunedProfile(name)
		if tuned != nil {
			Expect(testclient.ControlPlaneClient.Delete(context.TODO(), tuned)).To(Succeed())
		}
		profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Unable to fetch latest performance profile err: %v", err))
		if *profile.Spec.NUMA.TopologyPolicy != *initialPolicy {
			testlog.Infof("current policy: ", *profile.Spec.NUMA.TopologyPolicy)
			testlog.Infof("initial policy: ", *initialPolicy)
			profile.Spec.NUMA = &performancev2.NUMA{
				TopologyPolicy: initialPolicy,
			}

			By("Updating the performance profile")
			profiles.UpdateWithRetry(profile)

			By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
			profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

			By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
			profilesupdate.WaitForTuningUpdated(context.TODO(), profile)
		}
	})

})

func stringPtr(s string) *string {
	return &s
}

func getTunedProfile(tunedName string) *tunedv1.Tuned {
	GinkgoHelper()

	tunedList := &tunedv1.TunedList{}
	var matchedTuned *tunedv1.Tuned

	Eventually(func(g Gomega) {
		g.Expect(testclient.DataPlaneClient.List(context.TODO(), tunedList, &client.ListOptions{
			Namespace: components.NamespaceNodeTuningOperator,
		})).To(Succeed())

		g.Expect(tunedList.Items).ToNot(BeEmpty())

		for _, tuned := range tunedList.Items {
			if tuned.Name == tunedName {
				matchedTuned = &tuned
				break
			}
		}
	}).WithTimeout(time.Minute * 3).WithPolling(time.Second * 10).Should(Succeed())

	if matchedTuned == nil {
		return nil
	}
	return matchedTuned
}

func createTunedObject(deferMode string, KernelSHMMNI string, name string) (*tunedv1.Tuned, error) {
	GinkgoHelper()

	tunedName := name
	ns := components.NamespaceNodeTuningOperator
	priority := uint64(19)
	data := fmt.Sprintf(`
	[main]
	summary=Configuration changes profile inherited from performance created tuned
	include=openshift-node-performance-performance
	[sysctl]
	kernel.shmmni=%s
	`, KernelSHMMNI)

	// Create a Tuned object
	tuned := &tunedv1.Tuned{
		TypeMeta: metav1.TypeMeta{
			APIVersion: tunedv1.SchemeGroupVersion.String(),
			Kind:       "Tuned",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      tunedName,
			Namespace: ns,
			Annotations: map[string]string{
				"tuned.openshift.io/deferred": deferMode,
			},
		},
		Spec: tunedv1.TunedSpec{
			Profile: []tunedv1.TunedProfile{
				{
					Name: &tunedName,
					Data: &data,
				},
			},
			Recommend: []tunedv1.TunedRecommend{
				{
					MachineConfigLabels: map[string]string{"machineconfiguration.openshift.io/role": testutils.RoleWorkerCNF},
					Priority:            &priority,
					Profile:             &tunedName,
				},
			},
		},
	}

	testlog.Infof("Creating Tuned Profile Patch: %s", name)
	Expect(testclient.ControlPlaneClient.Create(context.TODO(), tuned)).To(Succeed())
	return tuned, nil
}

func execSysctlOnWorkers(ctx context.Context, workerNodes []corev1.Node, sysctlMap map[string]string) {
	GinkgoHelper()

	var err error
	var out []byte
	isSNO := false
	for _, node := range workerNodes {
		for param, expected := range sysctlMap {
			Eventually(func() bool {
				By(fmt.Sprintf("executing the command \"sysctl -n %s\"", param))
				tunedCmd := []string{"/bin/sh", "-c", "chroot /host sysctl -a | grep shmmni"}
				tunedPod := nodes.TunedForNode(&node, isSNO)
				out, err = pods.WaitForPodOutput(ctx, testclient.K8sClient, tunedPod, tunedCmd)
				Expect(err).ToNot(HaveOccurred())
				res := strings.TrimSpace(strings.SplitN(string(out), "=", 2)[1])
				if res != expected {
					testlog.Errorf("parameter %s value is not %s.", out, expected)
				}
				return res == expected
			}, 2*time.Minute, 10*time.Second).Should(BeTrue(), "Timed out verifying kernel.shmmni on worker node")
		}
	}
}

func verifyImmediateApplication(tuned *tunedv1.Tuned, workerCNFNodes []corev1.Node, KernelSHMMNI string) {
	// Use Eventually to wait until the performance profile status is updated
	Eventually(func() bool {
		profile, _ := tunedutil.GetProfile(context.TODO(), testclient.ControlPlaneClient, components.NamespaceNodeTuningOperator, workerCNFNodes[0].Name)
		return profile.Spec.Config.TunedProfile == tuned.Name
	}, 2*time.Minute, 10*time.Second).Should(BeTrue(), "Timed out waiting for profile.Spec.Config.TunedProfile to match tuned.Name")

	sysctlMap := map[string]string{
		"kernel.shmmni": KernelSHMMNI,
	}
	execSysctlOnWorkers(context.TODO(), workerCNFNodes, sysctlMap)
}

func verifyDeferredApplication(tuned *tunedv1.Tuned, workerCNFNodes []corev1.Node, name string) (bool, error) {
	// Use Eventually to wait until the performance profile status is updated
	Eventually(func() bool {
		profile, _ := tunedutil.GetProfile(context.TODO(), testclient.ControlPlaneClient, components.NamespaceNodeTuningOperator, workerCNFNodes[0].Name)
		return profile.Spec.Config.TunedProfile == tuned.Name
	}, 2*time.Minute, 10*time.Second).Should(BeTrue(), "Timed out waiting for profile.Spec.Config.TunedProfile to match tuned.Name")

	expectedMessage := fmt.Sprintf("The TuneD daemon profile is waiting for the next node restart: %s", name)
	profile, err := tunedutil.GetProfile(context.TODO(), testclient.ControlPlaneClient, components.NamespaceNodeTuningOperator, workerCNFNodes[0].Name)
	if err != nil {
		return false, fmt.Errorf("failed to get profile: %w", err)
	}

	for _, condition := range profile.Status.Conditions {
		if condition.Message == expectedMessage {
			return true, nil
		}
	}

	return false, nil
}

func rebootNode(poolName string) {
	profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
	if err != nil {
		testlog.Errorf("Unable to fetch latest performance profile err: %v", err)
	}
	testlog.Info("Rebooting the node")
	// reboot the node, for that we change the numa policy to best-effort
	// Note: this is used only to trigger reboot
	policy := "best-effort"
	// Need to make some changes to pp , causing system reboot
	currentPolicy := profile.Spec.NUMA.TopologyPolicy
	if *currentPolicy == "best-effort" {
		policy = "single-numa-node"
	}
	profile.Spec.NUMA = &performancev2.NUMA{
		TopologyPolicy: &policy,
	}

	By("Updating the performance profile")
	profiles.UpdateWithRetry(profile)

	By(fmt.Sprintf("Applying changes in performance profile and waiting until %s will start updating", poolName))
	profilesupdate.WaitForTuningUpdating(context.TODO(), profile)

	By(fmt.Sprintf("Waiting when %s finishes updates", poolName))
	profilesupdate.WaitForTuningUpdated(context.TODO(), profile)
}
