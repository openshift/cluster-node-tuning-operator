package __performance_config

import (
	"context"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mcv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components"
	"github.com/openshift/cluster-node-tuning-operator/pkg/performanceprofile/controller/performanceprofile/components/profile"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testutils "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils"
	testclient "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/client"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/cluster"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/discovery"
	testlog "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/log"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/mcps"
	"github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/utils/profiles"
	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

var RunningOnSingleNode bool

var _ = Describe("[performance][config] Performance configuration", Ordered, func() {

	testutils.CustomBeforeAll(func() {
		isSNO, err := cluster.IsSingleNode()
		Expect(err).ToNot(HaveOccurred())
		RunningOnSingleNode = isSNO
	})

	It("should remove OLM artifacts for performance-addon-operator", func() {
		csvs := &olmv1alpha1.ClusterServiceVersionList{}
		if err := testclient.Client.List(context.TODO(), csvs); err != nil {
			if !errors.IsNotFound(err) {
				Expect(err).ToNot(HaveOccurred())
			}
		}
		for i := range csvs.Items {
			csv := &csvs.Items[i]
			deploymentSpecs := csv.Spec.InstallStrategy.StrategySpec.DeploymentSpecs
			if deploymentSpecs != nil {
				for _, deployment := range deploymentSpecs {
					Expect(deployment.Name).ToNot(Equal("performance-operator"), fmt.Sprintf("CSV %s for performance-operator should have been removed", csv.Name))
				}
			}
		}
	})

	It("Should successfully deploy the performance profile", func() {

		performanceProfile := testProfile()
		profileAlreadyExists := false

		performanceManifest, foundOverride := os.LookupEnv("PERFORMANCE_PROFILE_MANIFEST_OVERRIDE")
		var err error
		if foundOverride {
			performanceProfile, err = externalPerformanceProfile(performanceManifest)
			Expect(err).ToNot(HaveOccurred(), "Failed overriding performance profile", performanceManifest)
			testlog.Warningf("Consuming performance profile from %s", performanceManifest)
		}
		if discovery.Enabled() {
			performanceProfile, err = profiles.GetByNodeLabels(testutils.NodeSelectorLabels)
			Expect(err).ToNot(HaveOccurred(), "Failed finding a performance profile in discovery mode using selector %v", testutils.NodeSelectorLabels)
			testlog.Info("Discovery mode: consuming a deployed performance profile from the cluster")
			profileAlreadyExists = true
		}

		By("Getting MCP for profile")
		mcpLabel := profile.GetMachineConfigLabel(performanceProfile)
		key, value := components.GetFirstKeyAndValue(mcpLabel)
		mcpsByLabel, err := mcps.GetByLabel(key, value)
		Expect(err).ToNot(HaveOccurred(), "Failed getting MCP by label key %v value %v", key, value)
		Expect(len(mcpsByLabel)).To(Equal(1), fmt.Sprintf("Unexpected number of MCPs found: %v", len(mcpsByLabel)))
		performanceMCP := &mcpsByLabel[0]

		if !discovery.Enabled() {
			By("Creating the PerformanceProfile")
			// this might fail while the operator is still being deployed and the CRD does not exist yet
			Eventually(func() error {
				err := testclient.Client.Create(context.TODO(), performanceProfile)
				if errors.IsAlreadyExists(err) {
					testlog.Warning(fmt.Sprintf("A PerformanceProfile with name %s already exists! If created externally, tests might have unexpected behaviour", performanceProfile.Name))
					profileAlreadyExists = true
					return nil
				}
				return err
			}, cluster.ComputeTestTimeout(15*time.Minute, RunningOnSingleNode), 15*time.Second).ShouldNot(HaveOccurred(), "Failed creating the performance profile")
		}

		if !performanceMCP.Spec.Paused {
			By("MCP is already unpaused")
		} else {
			By("Unpausing the MCP")
			Expect(testclient.Client.Patch(context.TODO(), performanceMCP,
				client.RawPatch(
					types.JSONPatchType,
					[]byte(fmt.Sprintf(`[{ "op": "replace", "path": "/spec/paused", "value": %v }]`, false)),
				),
			)).ToNot(HaveOccurred(), "Failed unpausing MCP")
		}

		By("Waiting for the MCP to pick the PerformanceProfile's MC")
		mcps.WaitForProfilePickedUp(performanceMCP.Name, performanceProfile)

		// If the profile is already there, it's likely to have been through the updating phase, so we only
		// wait for updated.
		if !profileAlreadyExists {
			By("Waiting for MCP starting to update")
			mcps.WaitForCondition(performanceMCP.Name, mcv1.MachineConfigPoolUpdating, corev1.ConditionTrue)
		}
		By("Waiting for MCP being updated")
		mcps.WaitForCondition(performanceMCP.Name, mcv1.MachineConfigPoolUpdated, corev1.ConditionTrue)

		Expect(testclient.Client.Get(context.TODO(), client.ObjectKeyFromObject(performanceProfile), performanceProfile))
		By("Printing the updated profile")
		format.Object(performanceProfile, 2)
	})

})

func externalPerformanceProfile(performanceManifest string) (*performancev2.PerformanceProfile, error) {
	performanceScheme := runtime.NewScheme()
	err := performancev2.AddToScheme(performanceScheme)
	if err != nil {
		return nil, err
	}

	decode := serializer.NewCodecFactory(performanceScheme).UniversalDeserializer().Decode
	manifest, err := os.ReadFile(performanceManifest)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s file", performanceManifest)
	}
	obj, _, err := decode(manifest, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to read the manifest file %s", performanceManifest)
	}
	profile, ok := obj.(*performancev2.PerformanceProfile)
	if !ok {
		return nil, fmt.Errorf("failed to convert manifest file to profile")
	}
	return profile, nil
}

func testProfile() *performancev2.PerformanceProfile {
	reserved := performancev2.CPUSet("0")
	isolated := performancev2.CPUSet("1-3")
	hugePagesSize := performancev2.HugePageSize("1G")

	profile := &performancev2.PerformanceProfile{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PerformanceProfile",
			APIVersion: performancev2.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: utils.PerformanceProfileName,
		},
		Spec: performancev2.PerformanceProfileSpec{
			CPU: &performancev2.CPU{
				Reserved: &reserved,
				Isolated: &isolated,
			},
			HugePages: &performancev2.HugePages{
				DefaultHugePagesSize: &hugePagesSize,
				Pages: []performancev2.HugePage{
					{
						Size:  "1G",
						Count: 1,
						Node:  pointer.Int32(0),
					},
					{
						Size:  "2M",
						Count: 128,
					},
				},
			},
			NodeSelector: testutils.NodeSelectorLabels,
			RealTimeKernel: &performancev2.RealTimeKernel{
				Enabled: pointer.Bool(true),
			},
			NUMA: &performancev2.NUMA{
				TopologyPolicy: pointer.String("single-numa-node"),
			},
			Net: &performancev2.Net{
				UserLevelNetworking: pointer.Bool(true),
			},
			WorkloadHints: &performancev2.WorkloadHints{
				RealTime:              pointer.Bool(true),
				HighPowerConsumption:  pointer.Bool(false),
				PerPodPowerManagement: pointer.Bool(false),
			},
		},
	}
	// If the machineConfigPool is master, the automatic selector from PAO won't work
	// since the machineconfiguration.openshift.io/role label is not applied to the
	// master pool, hence we put an explicit selector here.
	if utils.RoleWorkerCNF == "master" {
		profile.Spec.MachineConfigPoolSelector = map[string]string{
			"pools.operator.machineconfiguration.openshift.io/master": "",
		}
	}
	return profile
}
