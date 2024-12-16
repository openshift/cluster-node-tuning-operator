package testing

import (
	apiconfigv1 "github.com/openshift/api/config/v1"
	mcov1 "github.com/openshift/api/machineconfiguration/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
)

const (
	// HugePageSize defines the huge page size used for tests
	HugePageSize = performancev2.HugePageSize("1G")
	// HugePagesCount defines the huge page count used for tests
	HugePagesCount = 4
	// IsolatedCPUs defines the isolated CPU set used for tests
	IsolatedCPUs = performancev2.CPUSet("4-5")
	// ReservedCPUs defines the reserved CPU set used for tests
	ReservedCPUs = performancev2.CPUSet("0-3")
	// OfflinedCPUs defines the Offline CPU set used for tests
	OfflinedCPUs = performancev2.CPUSet("6-7")
	// SharedCPUs defines the shared CPU set used for tests
	SharedCPUs = performancev2.CPUSet("8-9")
	// SingleNUMAPolicy defines the topologyManager policy used for tests
	SingleNUMAPolicy = "single-numa-node"

	// MachineConfigLabelKey defines the MachineConfig label key of the test profile
	MachineConfigLabelKey = "mcKey"
	// MachineConfigLabelValue defines the MachineConfig label vlue of the test profile
	MachineConfigLabelValue = "mcValue"
	// MachineConfigPoolLabelKey defines the MachineConfigPool label key of the test profile
	MachineConfigPoolLabelKey = "mcpKey"
	// MachineConfigPoolLabelValue defines the MachineConfigPool label value of the test profile
	MachineConfigPoolLabelValue = "mcpValue"
)

// Additional Kernel Args
var AdditionalKernelArgs = []string{"audit=0", "processor.max_cstate=1", "idle=poll", "intel_idle.max_cstate=0"}

// NewPerformanceProfile returns new performance profile object that used for tests
func NewPerformanceProfile(name string) *performancev2.PerformanceProfile {
	size := HugePageSize
	isolatedCPUs := IsolatedCPUs
	reservedCPUs := ReservedCPUs
	offlineCPUs := OfflinedCPUs
	sharedCPUs := SharedCPUs
	numaPolicy := SingleNUMAPolicy
	additionalKernelArgs := AdditionalKernelArgs

	return &performancev2.PerformanceProfile{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "performance.openshift.io/v2",
			Kind:       "PerformanceProfile",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			UID:  types.UID("11111111-1111-1111-1111-1111111111111"),
		},
		Spec: performancev2.PerformanceProfileSpec{
			CPU: &performancev2.CPU{
				Isolated: &isolatedCPUs,
				Reserved: &reservedCPUs,
				Offlined: &offlineCPUs,
				Shared:   &sharedCPUs,
			},
			HugePages: &performancev2.HugePages{
				DefaultHugePagesSize: &size,
				Pages: []performancev2.HugePage{
					{
						Count: HugePagesCount,
						Size:  size,
					},
				},
			},
			RealTimeKernel: &performancev2.RealTimeKernel{
				Enabled: ptr.To(true),
			},
			NUMA: &performancev2.NUMA{
				TopologyPolicy: &numaPolicy,
			},
			MachineConfigLabel: map[string]string{
				MachineConfigLabelKey: MachineConfigLabelValue,
			},
			MachineConfigPoolSelector: map[string]string{
				MachineConfigPoolLabelKey: MachineConfigPoolLabelValue,
			},
			NodeSelector: map[string]string{
				"nodekey": "nodeValue",
			},
			WorkloadHints: &performancev2.WorkloadHints{
				HighPowerConsumption:  ptr.To(false),
				RealTime:              ptr.To(true),
				PerPodPowerManagement: ptr.To(false),
				MixedCpus:             ptr.To(true),
			},
			AdditionalKernelArgs: additionalKernelArgs,
		},
	}
}

// NewProfileMCP returns new MCP used for testing
func NewProfileMCP() *mcov1.MachineConfigPool {
	return &mcov1.MachineConfigPool{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "machineconfiguration.openshift.io/v1",
			Kind:       "MachineConfigPool",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
			UID:  "11111111-1111-1111-1111-1111111111111",
			Labels: map[string]string{
				MachineConfigPoolLabelKey: MachineConfigPoolLabelValue,
			},
		},
		Spec: mcov1.MachineConfigPoolSpec{
			NodeSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"nodekey": "nodeValue"},
			},
			MachineConfigSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{MachineConfigLabelKey: MachineConfigLabelValue},
			},
		},
		Status: mcov1.MachineConfigPoolStatus{
			Configuration: mcov1.MachineConfigPoolStatusConfiguration{
				ObjectReference: corev1.ObjectReference{
					Name: "test",
				},
			},
		},
	}
}

func NewProfileMachineConfig(name string, kernelArgs []string) *mcov1.MachineConfig {
	return &mcov1.MachineConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "machineconfiguration.openshift.io/v1",
			Kind:       "MachineConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			UID:  "11111111-1111-1111-1111-1111111111111",
		},
		Spec: mcov1.MachineConfigSpec{
			KernelArguments: kernelArgs,
		},
	}
}

func NewInfraResource(pin bool) *apiconfigv1.Infrastructure {
	pinningMode := apiconfigv1.CPUPartitioningNone
	if pin {
		pinningMode = apiconfigv1.CPUPartitioningAllNodes
	}
	return &apiconfigv1.Infrastructure{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Status: apiconfigv1.InfrastructureStatus{
			CPUPartitioning: pinningMode,
		},
	}
}

func NewClusterOperator() *apiconfigv1.ClusterOperator {
	return &apiconfigv1.ClusterOperator{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-tuning",
		},
		Status: apiconfigv1.ClusterOperatorStatus{
			Versions: []apiconfigv1.OperandVersion{
				{
					Name:    tunedv1.TunedOperandName,
					Version: "",
				},
			},
		},
	}
}

func NewContainerRuntimeConfig(runtime mcov1.ContainerRuntimeDefaultRuntime, mcpSelector map[string]string) *mcov1.ContainerRuntimeConfig {
	return &mcov1.ContainerRuntimeConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "machineconfiguration.openshift.io/v1",
			Kind:       "ContainerRuntimeConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "enable-crun",
		},
		Spec: mcov1.ContainerRuntimeConfigSpec{
			MachineConfigPoolSelector: metav1.SetAsLabelSelector(mcpSelector),
			ContainerRuntimeConfig: &mcov1.ContainerRuntimeConfiguration{
				DefaultRuntime: runtime,
			},
		},
		Status: mcov1.ContainerRuntimeConfigStatus{
			Conditions: []mcov1.ContainerRuntimeConfigCondition{
				{
					Type:   mcov1.ContainerRuntimeConfigSuccess,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
}
