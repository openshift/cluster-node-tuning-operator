package __performance_status

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onsi/gomega/gcustom"
	types2 "github.com/onsi/gomega/types"
//	tunedutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/tuned"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
//	nodev1 "k8s.io/api/node/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
//	"sigs.k8s.io/controller-runtime/pkg/client"

	ign2types "github.com/coreos/ignition/config/v2_2/types"
	machineconfigv1 "github.com/openshift/api/machineconfiguration/v1"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	hypershiftutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/hypershift"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/label"
//	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodepools"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/nodes"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	v1 "github.com/openshift/custom-resource-status/conditions/v1"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/util"
)

var _ = Describe("Status testing of performance profile", Ordered, func() {
	var (
		workerCNFNodes []corev1.Node
		err            error
	)

	BeforeEach(func() {
		if discovery.Enabled() && testutils.ProfileNotFound {
			Skip("Discovery mode enabled, performance profile not found")
		}
		workerCNFNodes, err = nodes.GetByLabels(testutils.NodeSelectorLabels)
		Expect(err).ToNot(HaveOccurred())
		workerCNFNodes, err = nodes.MatchingOptionalSelector(workerCNFNodes)
		Expect(err).ToNot(HaveOccurred(), "error looking for the optional selector: %v", err)
		Expect(workerCNFNodes).ToNot(BeEmpty())
	})

	Context("[rfe_id:28881][performance] Performance Addons detailed status", Label(string(label.Tier1)), func() {
//		It("[test_id:30894] Tuned status name tied to Performance Profile", func() {
//			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
//			Expect(err).ToNot(HaveOccurred())
//			tunedList := &tunedv1.TunedList{}
//			tunedName, err := tunedutils.GetName(context.TODO(), testclient.ControlPlaneClient, profile.Name)
//			Expect(err).ToNot(HaveOccurred())
//			tunedNamespacedName := types.NamespacedName{
//				Name:      tunedName,
//				Namespace: components.NamespaceNodeTuningOperator,
//			}
//			// on hypershift platform, we're getting the tuned object that was mirrored by NTO to the hosted cluster,
//			// hence we're using the DataPlaneClient here.
//			Eventually(func(g Gomega) {
//				g.Expect(testclient.DataPlaneClient.List(context.TODO(), tunedList, &client.ListOptions{
//					Namespace: tunedNamespacedName.Namespace,
//				})).To(Succeed())
//				Expect(tunedList.Items).To(MatchTunedName(tunedName))
//			}).WithTimeout(time.Minute).WithPolling(time.Second * 10).Should(Succeed())
//			Expect(*profile.Status.Tuned).ToNot(BeNil())
//			Expect(*profile.Status.Tuned).To(Equal(tunedNamespacedName.String()))
//		})
//
//		It("[test_id:33791] Should include the generated runtime class name", func() {
//			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
//			Expect(err).ToNot(HaveOccurred())
//
//			key := types.NamespacedName{
//				Name:      components.GetComponentName(profile.Name, components.ComponentNamePrefix),
//				Namespace: metav1.NamespaceAll,
//			}
//			runtimeClass := &nodev1.RuntimeClass{}
//			err = testclient.GetWithRetry(context.TODO(), testclient.DataPlaneClient, key, runtimeClass)
//			Expect(err).ToNot(HaveOccurred(), "cannot find the RuntimeClass object "+key.String())
//
//			Expect(profile.Status.RuntimeClass).NotTo(BeNil())
//			Expect(*profile.Status.RuntimeClass).To(Equal(runtimeClass.Name))
//		})
//
//		It("[test_id:29673] Machine config pools status tied to Performance Profile", Label(string(label.OpenShift)), func() {
//			// Creating bad MC that leads to degraded state
//			By("Creating bad MachineConfig")
//			badMC := createBadMachineConfig("bad-mc")
//			err = testclient.ControlPlaneClient.Create(context.TODO(), badMC)
//			Expect(err).ToNot(HaveOccurred())
//
//			By("Wait for MCP condition to be Degraded")
//			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
//			Expect(err).ToNot(HaveOccurred())
//			performanceMCP, err := mcps.GetByProfile(profile)
//			Expect(err).ToNot(HaveOccurred())
//			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolDegraded, corev1.ConditionTrue)
//			mcpConditionReason := mcps.GetConditionReason(performanceMCP, machineconfigv1.MachineConfigPoolDegraded)
//			profileConditionMessage := profiles.GetConditionMessage(testutils.NodeSelectorLabels, v1.ConditionDegraded)
//			// Verify the status reason of performance profile
//			Expect(profileConditionMessage).To(ContainSubstring(mcpConditionReason))
//
//			By("Deleting bad MachineConfig and waiting when Degraded state is removed")
//			err = testclient.ControlPlaneClient.Delete(context.TODO(), badMC)
//			Expect(err).ToNot(HaveOccurred())
//
//			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
//		})

		It("[test_id:40402] Tuned profile status tied to Performance Profile", func() {
			// During this test we're creating additional synthetic tuned CR by invoking the createrBadTuned function.
			// This synthetic tuned will look for a tuned profile which doesn't exist.
			// This tuned CR will be applied on the profiles.tuned.openshift.io CR (there is such profile per node)
			// which is associate to the node object with the same name.
			// The connection between the node object and the tuned object is via the MachineConfigLables, worker-cnf in our case.
			tunedName := "openshift-cause-tuned-failure"
			// on hypershift this is the namespace name on the hosted cluster where the tuned object is mirrored to.
			// on openshift this is the namespace where NTO/PAO creates tuned objects
			ns := components.NamespaceNodeTuningOperator

			By("1) ------------")
			_, _, err = util.ExecAndLogCommand("oc", "get", "tuned", "-n", ns)
			_, _, err = util.ExecAndLogCommand("oc", "get", "profile", "-n", ns)
//			_, _, err = util.ExecAndLogCommand("oc", "get", "performanceprofile/performance", "-n", ns, "-o", "yaml")

			// Creating a bad Tuned object that leads to degraded state
			cleanupFunc := createBadTuned(tunedName, ns)
			defer func() {
				By("2b) ------------")
				_, _, err = util.ExecAndLogCommand("oc", "get", "tuned", "-n", ns)
				_, _, err = util.ExecAndLogCommand("oc", "get", "profile", "-n", ns)

				By("Deleting bad Tuned and waiting when Degraded state is removed")
				cleanupFunc()

				By("3) Sleeping 30")
				time.Sleep(30 * time.Second)

				_, _, err = util.ExecAndLogCommand("oc", "get", "tuned", "-n", ns)
				_, _, err = util.ExecAndLogCommand("oc", "get", "profile", "-n", ns)
//				_, _, err = util.ExecAndLogCommand("oc", "get", "performanceprofile/performance", "-n", ns, "-o", "yaml")
				Expect(err).To(HaveOccurred())	// intentionally fail

				profiles.WaitForCondition(testutils.NodeSelectorLabels, v1.ConditionAvailable, corev1.ConditionTrue)
			}()

			By("Waiting for performance profile condition to be Degraded")
			profiles.WaitForCondition(testutils.NodeSelectorLabels, v1.ConditionDegraded, corev1.ConditionTrue)

			By("2) ------------")
			_, _, err = util.ExecAndLogCommand("oc", "get", "tuned", "-n", ns)
			_, _, err = util.ExecAndLogCommand("oc", "get", "profile", "-n", ns)
//			_, _, err = util.ExecAndLogCommand("oc", "get", "performanceprofile/performance", "-n", ns, "-o", "yaml")
		})
	})
})

func createBadMachineConfig(name string) *machineconfigv1.MachineConfig {
	rawIgnition, _ := json.Marshal(
		&ign2types.Config{
			Ignition: ign2types.Ignition{
				Version: ign2types.MaxVersion.String(),
			},
			Storage: ign2types.Storage{
				Disks: []ign2types.Disk{
					{
						Device: "/one",
					},
				},
			},
		},
	)

	return &machineconfigv1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: machineconfigv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{"machineconfiguration.openshift.io/role": testutils.RoleWorkerCNF},
			UID:    types.UID(utilrand.String(5)),
		},
		Spec: machineconfigv1.MachineConfigSpec{
			OSImageURL: "",
			Config: runtime.RawExtension{
				Raw: rawIgnition,
			},
		},
	}
}

// createBadTuned creates bad tuned that should ended up in a degraded state
// and return a cleanup function that can be called to wipe out the bad tuned object
func createBadTuned(name, ns string) func() {
	priority := uint64(20)
	// include=profile-does-not-exist
	// points to tuned profile which doesn't exist
	data := "[main]\nsummary=A Tuned daemon profile that does not exist\ninclude=profile-does-not-exist"

	badTuned := &tunedv1.Tuned{
		TypeMeta: metav1.TypeMeta{
			APIVersion: tunedv1.SchemeGroupVersion.String(),
			Kind:       "Tuned",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			UID:       types.UID(utilrand.String(5)),
		},
		Spec: tunedv1.TunedSpec{
			Profile: []tunedv1.TunedProfile{
				{
					Name: &name,
					Data: &data,
				},
			},
			Recommend: []tunedv1.TunedRecommend{
				{
					MachineConfigLabels: map[string]string{"machineconfiguration.openshift.io/role": testutils.RoleWorkerCNF},
					Priority:            &priority,
					Profile:             &name,
				},
			},
		},
	}

	Expect(testclient.ControlPlaneClient.Create(context.TODO(), badTuned)).To(Succeed())
	if hypershiftutils.IsHypershiftCluster() {
		Expect(nodepools.AttachTuningObject(context.TODO(), testclient.ControlPlaneClient, badTuned)).To(Succeed())
	}

	return func() {
		GinkgoHelper()
		if hypershiftutils.IsHypershiftCluster() {
			Expect(nodepools.DeattachTuningObject(context.TODO(), testclient.ControlPlaneClient, badTuned)).ToNot(HaveOccurred(), "failed to de-attach tuned %q from NodePool", badTuned.Name)
		}
		Expect(testclient.ControlPlaneClient.Delete(context.TODO(), badTuned)).ToNot(HaveOccurred(), "failed to delete tuned %q", badTuned.Name)
	}
}

func MatchTunedName(expected interface{}) types2.GomegaMatcher {
	return gcustom.MakeMatcher(func(tuneds []tunedv1.Tuned) (bool, error) {
		for _, tuned := range tuneds {
			if tuned.Name == expected {
				return true, nil
			}
		}
		return false, nil
	}).WithTemplate("Expected:\n{{.FormattedActual}}\n{{.To}} contain Tuned object with the name\n{{format .Data 1}}").WithTemplateData(expected)
}
