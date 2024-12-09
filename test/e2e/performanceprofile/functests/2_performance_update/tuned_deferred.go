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

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"

	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/pods"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profilesupdate"
)

var _ = Describe("Tuned Deferred tests of performance profile", Ordered, Label(string("Tier5")), func() {
	var (
		workerCNFNodes []corev1.Node
		err            error
		poolName       string
		np             *hypershiftv1beta1.NodePool
		tuned          *tunedv1.Tuned
	)

	type TunedProfileConfig struct {
		DeferMode         string
		ProfileChangeType string // "first-time" or "in-place"
		ExpectedBehavior  string // "deferred" or "immediate"
		Kernel_shmmni     string // 4096 or 8192
	}

	name := "performance-patch"

	BeforeEach(func() {
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
						time.Sleep(2 * time.Minute)
					}
					tuned, err = createTunedObject(tc.DeferMode, tc.Kernel_shmmni, np, name)
					Expect(err).NotTo(HaveOccurred())

				} else {
					tuned := getTunedProfile(name)
					tunedData := []byte(*tuned.Spec.Profile[0].Data)

					cfg, err := ini.Load(tunedData)
					Expect(err).ToNot(HaveOccurred())

					sysctlSection, err := cfg.GetSection("sysctl")
					Expect(err).ToNot(HaveOccurred())

					sysctlSection.Key("kernel.shmmni").SetValue(tc.Kernel_shmmni)
					Expect(sysctlSection.Key("kernel.shmmni").String()).To(Equal(tc.Kernel_shmmni))

					var updatedTunedData bytes.Buffer
					_, err = cfg.WriteTo(&updatedTunedData)
					Expect(err).ToNot(HaveOccurred())

					updatedTunedDataBytes := updatedTunedData.Bytes()
					tuned.Spec.Profile[0].Data = stringPtr(string(updatedTunedDataBytes))

					testlog.Infof("Updating Tuned Profile Patch: %s", name)
					Expect(testclient.ControlPlaneClient.Update(context.TODO(), tuned)).To(Succeed())
				}

				switch tc.ExpectedBehavior {
				case "immediate":
					verifyImmediateApplication(tuned, workerCNFNodes, tc.Kernel_shmmni)
				case "deferred":
					out, err := verifyDeferredApplication(tuned, workerCNFNodes, tc.Kernel_shmmni, name)
					Expect(err).To(BeNil())
					Expect(out).To(BeTrue())
					rebootNode(poolName)
					verifyImmediateApplication(tuned, workerCNFNodes, tc.Kernel_shmmni)
				}
			},
			Entry("[test_id:78115] Verify deferred Always with first-time profile change", TunedProfileConfig{
				DeferMode:         "always",
				ProfileChangeType: "first-time",
				ExpectedBehavior:  "deferred",
				Kernel_shmmni:     "8192",
			}),
			Entry("[test_id:78116] Verify deferred Always with in-place profile update", TunedProfileConfig{
				DeferMode:         "always",
				ProfileChangeType: "in-place",
				ExpectedBehavior:  "deferred",
				Kernel_shmmni:     "4096",
			}),
			Entry("[test_id:78117] Verify deferred Update with first-time profile change", TunedProfileConfig{
				DeferMode:         "update",
				ProfileChangeType: "first-time",
				ExpectedBehavior:  "immediate",
				Kernel_shmmni:     "8192",
			}),
			Entry("[test_id:78118] Verify deferred Update with in-place profile update", TunedProfileConfig{
				DeferMode:         "update",
				ProfileChangeType: "in-place",
				ExpectedBehavior:  "deferred",
				Kernel_shmmni:     "4096",
			}),
			Entry("[test_id:78119] Verify deferred with No annotation with first-time profile change", TunedProfileConfig{
				DeferMode:         "",
				ProfileChangeType: "first-time",
				ExpectedBehavior:  "immediate",
				Kernel_shmmni:     "8192",
			}),
			Entry("[test_id:78120] Verify deferred Never mode with in-place profile update", TunedProfileConfig{
				DeferMode:         "never",
				ProfileChangeType: "in-place",
				ExpectedBehavior:  "immediate",
				Kernel_shmmni:     "4096",
			}),
		)
	})

	AfterAll(func() {
		tuned = getTunedProfile(name)
		if tuned != nil {
			Expect(testclient.ControlPlaneClient.Delete(context.TODO(), tuned)).To(Succeed())
		}
	})

})

func stringPtr(s string) *string {
	return &s
}

func getTunedProfile(tunedName string) *tunedv1.Tuned {
	tunedList := &tunedv1.TunedList{}
	var matchedTuned *tunedv1.Tuned

	Eventually(func(g Gomega) {
		g.Expect(testclient.DataPlaneClient.List(context.TODO(), tunedList, &client.ListOptions{
			Namespace: components.NamespaceNodeTuningOperator,
		})).To(Succeed())

		g.Expect(len(tunedList.Items)).To(BeNumerically(">", 1))

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

func createTunedObject(deferMode string, kernel_shmmni string, np *hypershiftv1beta1.NodePool, name string) (*tunedv1.Tuned, error) {
	tunedName := name
	ns := components.NamespaceNodeTuningOperator
	priority := uint64(19)
	data := fmt.Sprintf(`
	[main]
	summary=Configuration changes profile inherited from performance created tuned
	include=openshift-node-performance-performance
	[sysctl]
	kernel.shmmni=%s
	`, kernel_shmmni)

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
	var err error
	var out []byte
	isSNO := false
	for _, node := range workerNodes {
		for param, expected := range sysctlMap {
			By(fmt.Sprintf("executing the command \"sysctl -n %s\"", param))
			tunedCmd := []string{"/bin/sh", "-c", "chroot /host sysctl -a | grep shmmni"}
			tunedPod := nodes.TunedForNode(&node, isSNO)
			out, err = pods.WaitForPodOutput(context.TODO(), testclient.K8sClient, tunedPod, tunedCmd)
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.TrimSpace(strings.SplitN(string(out), "=", 2)[1])).Should(Equal(expected), "parameter %s value is not %s.", out, expected)
		}
	}
}

func verifyImmediateApplication(tuned *tunedv1.Tuned, workerCNFNodes []corev1.Node, kernel_shmmni string) {
	time.Sleep(40 * time.Second)
	sysctlMap := map[string]string{
		"kernel.shmmni": kernel_shmmni,
	}
	execSysctlOnWorkers(context.TODO(), workerCNFNodes, sysctlMap)
}

func verifyDeferredApplication(tuned *tunedv1.Tuned, workerCNFNodes []corev1.Node, kernel_shmmni string, name string) (bool, error) {
	time.Sleep(20 * time.Second)
	expectedMessage := fmt.Sprintf("The TuneD daemon profile is waiting for the next node restart: %s", name)
	profile := &tunedv1.Profile{}
	node := workerCNFNodes[0]
	err := testclient.DataPlaneClient.Get(context.TODO(), client.ObjectKey{Name: node.Name, Namespace: tuned.Namespace}, profile)
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
