package testing

import (
	apiconfigv1 "github.com/openshift/api/config/v1"
	performancev2 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/performanceprofile/v2"
	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	mcov1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
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
	OfflinedCPUs     = performancev2.CPUSet("6-7") // SingleNUMAPolicy defines the topologyManager policy used for tests
	SingleNUMAPolicy = "single-numa-node"

	//MachineConfigLabelKey defines the MachineConfig label key of the test profile
	MachineConfigLabelKey = "mcKey"
	//MachineConfigLabelValue defines the MachineConfig label vlue of the test profile
	MachineConfigLabelValue = "mcValue"
	//MachineConfigPoolLabelKey defines the MachineConfigPool label key of the test profile
	MachineConfigPoolLabelKey = "mcpKey"
	//MachineConfigPoolLabelValue defines the MachineConfigPool label value of the test profile
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
	numaPolicy := SingleNUMAPolicy
	additionalKernelArgs := AdditionalKernelArgs

	return &performancev2.PerformanceProfile{
		TypeMeta: metav1.TypeMeta{Kind: "PerformanceProfile"},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			UID:  types.UID("11111111-1111-1111-1111-1111111111111"),
		},
		Spec: performancev2.PerformanceProfileSpec{
			CPU: &performancev2.CPU{
				Isolated: &isolatedCPUs,
				Reserved: &reservedCPUs,
				Offlined: &offlineCPUs,
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
				Enabled: pointer.BoolPtr(true),
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
				HighPowerConsumption:  pointer.BoolPtr(false),
				RealTime:              pointer.BoolPtr(true),
				PerPodPowerManagement: pointer.BoolPtr(false),
			},
			AdditionalKernelArgs: additionalKernelArgs,
		},
	}
}

// NewProfileMCP returns new MCP used for testing
func NewProfileMCP() *mcov1.MachineConfigPool {
	return &mcov1.MachineConfigPool{
		TypeMeta: metav1.TypeMeta{Kind: "MachineConfigPool"},
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
