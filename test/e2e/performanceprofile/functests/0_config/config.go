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
	"k8s.io/utils/cpuset"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mcv1 "github.com/openshift/api/machineconfiguration/v1"

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
)

var RunningOnSingleNode bool

var _ = Describe("[performance][config] Performance configuration", Ordered, func() {

	testutils.CustomBeforeAll(func() {
		isSNO, err := cluster.IsSingleNode()
		Expect(err).ToNot(HaveOccurred())
		RunningOnSingleNode = isSNO
	})

	It("Should successfully deploy the performance profile", func() {

		performanceProfile, err := testProfile()
		Expect(err).ToNot(HaveOccurred(), "failed to build performance profile: %v", err)
		profileAlreadyExists := false

		performanceManifest, foundOverride := os.LookupEnv("PERFORMANCE_PROFILE_MANIFEST_OVERRIDE")
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

func testProfile() (*performancev2.PerformanceProfile, error) {
	reserved := performancev2.CPUSet("0")
	isolated := performancev2.CPUSet("1-3")
	profileCpus := &performancev2.CPU{
		Reserved: &reserved,
		Isolated: &isolated,
	}

	customReserved, foundReserved := os.LookupEnv("RESERVED_CPU_SET")
	customIsolated, foundIsolated := os.LookupEnv("ISOLATED_CPU_SET")
	if foundReserved != foundIsolated {
		return nil, fmt.Errorf("overriding default profile CPU setting requires both environment variables RESERVED_CPU_SET and ISOLATED_CPU_SET to be set")
	}

	if foundReserved {
		if _, err := cpuset.Parse(customReserved); err != nil {
			return nil, fmt.Errorf("failed to parse the provided reserved cpu set %s: %v. Using default values", customReserved, err)
		}
		reserved = performancev2.CPUSet(customReserved)
	}

	if foundIsolated {
		if _, err := cpuset.Parse(customIsolated); err != nil {
			return nil, fmt.Errorf("failed to parse the provided isolated cpu set %s: %v. Using default values", customIsolated, err)
		}
		isolated = performancev2.CPUSet(customIsolated)
	}
	customOfflined, foundOfflined := os.LookupEnv("OFFLINED_CPU_SET")
	if foundOfflined {
		if _, err := cpuset.Parse(customOfflined); err != nil {
			return nil, fmt.Errorf("failed to parse the provided offlined cpu set %s: %v. Using default values", customOfflined, err)
		}
		offlined := performancev2.CPUSet(customOfflined)
		profileCpus.Offlined = &offlined
	}

	hugePagesSize := performancev2.HugePageSize("1G")

	// This implements a workaround to prevent CI failures on specific hardware using an Intel E810 network card.
	// When UserLevelNetworking is set to True, tuned attempts to set the combined channel count equal to the reserved CPUs but fails with the following error:
	// tuned.utils.commands: Executing 'ethtool -L ens2f0 combined 1' error: netlink error: Device or resource busy
	// The error occurs because the ice driver: ens2f0: Cannot change channels when RDMA is active.
	// This issue causes the tuned profile to degrade.
	// As a temporary solution, by adding 'module_blacklist=irdma' to the kernel Args we will block RDMA, to avoid these errors.
	// Reference: OCPBUGS-46426

	additionalKernelArgs := []string{
		"module_blacklist=irdma",
	}

	profile := &performancev2.PerformanceProfile{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PerformanceProfile",
			APIVersion: performancev2.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: utils.PerformanceProfileName,
		},
		Spec: performancev2.PerformanceProfileSpec{
			CPU: profileCpus,
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
			AdditionalKernelArgs: additionalKernelArgs,
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
	return profile, nil
}
