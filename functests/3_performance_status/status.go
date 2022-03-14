package __performance_status

import (
	"context"
	"encoding/json"

	ign2types "github.com/coreos/ignition/config/v2_2/types"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	v1 "github.com/openshift/custom-resource-status/conditions/v1"
	machineconfigv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"k8s.io/apimachinery/pkg/runtime"

	testutils "github.com/openshift-kni/performance-addon-operators/functests/utils"
	testclient "github.com/openshift-kni/performance-addon-operators/functests/utils/client"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/discovery"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/mcps"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/nodes"
	"github.com/openshift-kni/performance-addon-operators/functests/utils/profiles"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components"

	corev1 "k8s.io/api/core/v1"
	nodev1beta1 "k8s.io/api/node/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
)

var _ = Describe("Status testing of performance profile", func() {
	var (
		workerCNFNodes []corev1.Node
		err            error
		clean          func() error
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
		// initialized clean function handler to be nil on every It execution
		clean = nil
	})

	AfterEach(func() {
		if clean != nil {
			clean()
		}

	})

	Context("[rfe_id:28881][performance] Performance Addons detailed status", func() {

		It("[test_id:30894] Tuned status name tied to Performance Profile", func() {
			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			key := types.NamespacedName{
				Name:      components.GetComponentName(profile.Name, components.ProfileNamePerformance),
				Namespace: components.NamespaceNodeTuningOperator,
			}
			tuned := &tunedv1.Tuned{}
			err = testclient.GetWithRetry(context.TODO(), key, tuned)
			Expect(err).ToNot(HaveOccurred(), "cannot find the Cluster Node Tuning Operator Tuned object "+key.String())
			tunedNamespacedname := types.NamespacedName{
				Name:      components.GetComponentName(profile.Name, components.ProfileNamePerformance),
				Namespace: components.NamespaceNodeTuningOperator,
			}
			tunedStatus := tunedNamespacedname.String()
			Expect(profile.Status.Tuned).NotTo(BeNil())
			Expect(*profile.Status.Tuned).To(Equal(tunedStatus))
		})

		It("[test_id:33791] Should include the generated runtime class name", func() {
			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())

			key := types.NamespacedName{
				Name:      components.GetComponentName(profile.Name, components.ComponentNamePrefix),
				Namespace: metav1.NamespaceAll,
			}
			runtimeClass := &nodev1beta1.RuntimeClass{}
			err = testclient.GetWithRetry(context.TODO(), key, runtimeClass)
			Expect(err).ToNot(HaveOccurred(), "cannot find the RuntimeClass object "+key.String())

			Expect(profile.Status.RuntimeClass).NotTo(BeNil())
			Expect(*profile.Status.RuntimeClass).To(Equal(runtimeClass.Name))
		})

		It("[test_id:29673] Machine config pools status tied to Performance Profile", func() {
			// Creating bad MC that leads to degraded state
			By("Creating bad MachineConfig")
			badMC := createBadMachineConfig("bad-mc")
			err = testclient.Client.Create(context.TODO(), badMC)
			Expect(err).ToNot(HaveOccurred())

			By("Wait for MCP condition to be Degraded")
			profile, err := profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred())
			performanceMCP, err := mcps.GetByProfile(profile)
			Expect(err).ToNot(HaveOccurred())
			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolDegraded, corev1.ConditionTrue)
			mcpConditionReason := mcps.GetConditionReason(performanceMCP, machineconfigv1.MachineConfigPoolDegraded)
			profileConditionMessage := profiles.GetConditionMessage(testutils.NodeSelectorLabels, v1.ConditionDegraded)
			// Verify the status reason of performance profile
			Expect(profileConditionMessage).To(ContainSubstring(mcpConditionReason))

			By("Deleting bad MachineConfig and waiting when Degraded state is removed")
			err = testclient.Client.Delete(context.TODO(), badMC)
			Expect(err).ToNot(HaveOccurred())

			mcps.WaitForCondition(performanceMCP, machineconfigv1.MachineConfigPoolUpdated, corev1.ConditionTrue)
		})

		It("[test_id:40402] Tuned profile status tied to Performance Profile", func() {
			// During this test we're creating additional synthetic tuned CR by invoking the createrBadTuned function.
			// This synthetic tuned will look for a tuned profile which doesn't exist.
			// This tuned CR will be applied on the profiles.tuned.openshift.io CR (there is such profile per node)
			// which is associate to the node object with the same name.
			// The connection between the node object and the tuned object is via the MachineConfigLables, worker-cnf in our case.
			ns := "openshift-cluster-node-tuning-operator"
			tunedName := "openshift-cause-tuned-failure"

			// Make sure to clean badTuned object even if the It threw an error
			clean = func() error {
				key := types.NamespacedName{
					Name:      tunedName,
					Namespace: ns,
				}
				runtimeClass := &tunedv1.Tuned{}
				err := testclient.Client.Get(context.TODO(), key, runtimeClass)
				// if err != nil probably the resource were already deleted
				if err == nil {
					testclient.Client.Delete(context.TODO(), runtimeClass)
				}
				return err
			}

			// Creating bad Tuned object that leads to degraded state
			badTuned := createBadTuned(tunedName, ns)
			err = testclient.Client.Create(context.TODO(), badTuned)
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for performance profile condition to be Degraded")
			profiles.WaitForCondition(testutils.NodeSelectorLabels, v1.ConditionDegraded, corev1.ConditionTrue)

			By("Deleting bad Tuned and waiting when Degraded state is removed")
			err = testclient.Client.Delete(context.TODO(), badTuned)
			profiles.WaitForCondition(testutils.NodeSelectorLabels, v1.ConditionAvailable, corev1.ConditionTrue)
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

func createBadTuned(name, ns string) *tunedv1.Tuned {
	priority := uint64(20)
	// include=profile-does-not-exist
	// points to tuned profile which doesn't exist
	data := "[main]\nsummary=A Tuned daemon profile that does not exist\ninclude=profile-does-not-exist"

	return &tunedv1.Tuned{
		TypeMeta: metav1.TypeMeta{
			APIVersion: tunedv1.SchemeGroupVersion.String(),
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

}
