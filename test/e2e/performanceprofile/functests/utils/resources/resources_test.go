package resources

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestTotalCPUsRounded(t *testing.T) {
	tests := []struct {
		name                string
		containersResources []corev1.ResourceList
		expectedTotalCPUs   int
	}{
		{
			name:                "zero containers - empty slice",
			containersResources: []corev1.ResourceList{},
			expectedTotalCPUs:   0,
		},
		{
			name: "single container - no CPU resource",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			},
			expectedTotalCPUs: 0,
		},
		{
			name: "single container - empty ResourceList",
			containersResources: []corev1.ResourceList{
				{},
			},
			expectedTotalCPUs: 0,
		},
		{
			name: "single container - 1 CPU",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			},
			expectedTotalCPUs: 1,
		},
		{
			name: "single container - 2 CPUs",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
			},
			expectedTotalCPUs: 2,
		},
		{
			name: "single container - fractional CPU (500m)",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			},
			expectedTotalCPUs: 1, // 500m (0.5 CPU) rounded up to 1
		},
		{
			name: "single container - fractional CPU (1500m)",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("1500m"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			},
			expectedTotalCPUs: 2, // 1500m (1.5 CPU) rounded up to 2
		},
		{
			name: "two containers - same CPU value",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
			},
			expectedTotalCPUs: 4,
		},
		{
			name: "two containers - different CPU values",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("3"),
					corev1.ResourceMemory: resource.MustParse("300Mi"),
				},
			},
			expectedTotalCPUs: 4,
		},
		{
			name: "two containers - one with no CPU",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
				{
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			},
			expectedTotalCPUs: 2,
		},
		{
			name: "two containers - both with no CPU",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
				{
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
			},
			expectedTotalCPUs: 0,
		},
		{
			name: "three containers - mixed CPU values",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("400Mi"),
				},
			},
			expectedTotalCPUs: 7,
		},
		{
			name: "three containers - with fractional CPUs that sum exactly",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("50Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("50Mi"),
				},
			},
			expectedTotalCPUs: 1, // 500m + 500m = 1000m = 1 CPU
		},
		{
			name: "multiple containers - with fractional CPUs",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("2300m"), // 2.3 CPUs
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("500m"), // 0.5 CPUs
					corev1.ResourceMemory: resource.MustParse("50Mi"),
				},
			},
			expectedTotalCPUs: 4, // 1 + 2.3 + 0.5 = 3.8, rounded up to 4
		},
		{
			name: "four containers - large CPU values",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("8"),
					corev1.ResourceMemory: resource.MustParse("1Gi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("16"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("500Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("32"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
			},
			expectedTotalCPUs: 60,
		},
		{
			name: "multiple containers - all empty ResourceLists",
			containersResources: []corev1.ResourceList{
				{},
				{},
				{},
			},
			expectedTotalCPUs: 0,
		},
		{
			name: "multiple containers - mix of empty and non-empty",
			containersResources: []corev1.ResourceList{
				{},
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
				{},
				{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			},
			expectedTotalCPUs: 3,
		},
		{
			name: "five containers - sequential CPU values",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("3"),
					corev1.ResourceMemory: resource.MustParse("300Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("400Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("5"),
					corev1.ResourceMemory: resource.MustParse("500Mi"),
				},
			},
			expectedTotalCPUs: 15,
		},
		{
			name: "multiple containers - fractional CPUs that sum to whole number",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("250m"),
					corev1.ResourceMemory: resource.MustParse("25Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("250m"),
					corev1.ResourceMemory: resource.MustParse("25Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("250m"),
					corev1.ResourceMemory: resource.MustParse("25Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("250m"),
					corev1.ResourceMemory: resource.MustParse("25Mi"),
				},
			},
			expectedTotalCPUs: 1, // 250m * 4 = 1000m = 1 CPU exactly
		},
		{
			name: "multiple containers - mix of whole and fractional CPUs",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("750m"),
					corev1.ResourceMemory: resource.MustParse("75Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("250m"),
					corev1.ResourceMemory: resource.MustParse("25Mi"),
				},
			},
			expectedTotalCPUs: 4, // 1 + 0.75 + 2 + 0.25 = 4.0 exactly
		},
		{
			name: "multiple containers - some with CPU, some without",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
				{
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("3"),
					corev1.ResourceMemory: resource.MustParse("300Mi"),
				},
				{
					corev1.ResourceMemory: resource.MustParse("150Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			},
			expectedTotalCPUs: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TotalCPUsRounded(tt.containersResources)
			if result != tt.expectedTotalCPUs {
				t.Errorf("TotalCPUsRounded() = %d, want %d", result, tt.expectedTotalCPUs)
			}
		})
	}
}

func TestMaxCPURequestsRounded(t *testing.T) {
	tests := []struct {
		name                string
		containersResources []corev1.ResourceList
		expectedMaxCPUs     int
	}{
		{
			name:                "zero containers - empty slice",
			containersResources: []corev1.ResourceList{},
			expectedMaxCPUs:     0,
		},
		{
			name: "single container - no CPU resource",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			},
			expectedMaxCPUs: 0,
		},
		{
			name: "single container - empty ResourceList",
			containersResources: []corev1.ResourceList{
				{},
			},
			expectedMaxCPUs: 0,
		},
		{
			name: "single container - 1 CPU",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			},
			expectedMaxCPUs: 1,
		},
		{
			name: "single container - 5 CPUs",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("5"),
					corev1.ResourceMemory: resource.MustParse("500Mi"),
				},
			},
			expectedMaxCPUs: 5,
		},
		{
			name: "single container - fractional CPU (500m rounded up to 1)",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			},
			expectedMaxCPUs: 1, // 500m (0.5 CPU) rounded up to 1
		},
		{
			name: "single container - fractional CPU (2300m rounded up to 3)",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("2300m"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
			},
			expectedMaxCPUs: 3, // 2300m (2.3 CPU) rounded up to 3
		},
		{
			name: "two containers - same CPU value",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
			},
			expectedMaxCPUs: 2,
		},
		{
			name: "two containers - first is max",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("5"),
					corev1.ResourceMemory: resource.MustParse("500Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
			},
			expectedMaxCPUs: 5,
		},
		{
			name: "two containers - second is max",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("8"),
					corev1.ResourceMemory: resource.MustParse("800Mi"),
				},
			},
			expectedMaxCPUs: 8,
		},
		{
			name: "two containers - one with no CPU",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("3"),
					corev1.ResourceMemory: resource.MustParse("300Mi"),
				},
				{
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			},
			expectedMaxCPUs: 3,
		},
		{
			name: "two containers - both with no CPU",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
				{
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
			},
			expectedMaxCPUs: 0,
		},
		{
			name: "three containers - middle is max",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("10"),
					corev1.ResourceMemory: resource.MustParse("1Gi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("400Mi"),
				},
			},
			expectedMaxCPUs: 10,
		},
		{
			name: "three containers - last is max",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("16"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
			},
			expectedMaxCPUs: 16,
		},
		{
			name: "multiple containers - with fractional CPUs",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("2300m"), // 2.3 CPUs, rounded up to 3
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("500m"), // 0.5 CPUs, rounded up to 1
					corev1.ResourceMemory: resource.MustParse("50Mi"),
				},
			},
			expectedMaxCPUs: 3, // max of 1, 3, 1
		},
		{
			name: "multiple containers - large CPU values",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("8"),
					corev1.ResourceMemory: resource.MustParse("1Gi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("64"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("32"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("16"),
					corev1.ResourceMemory: resource.MustParse("2Gi"),
				},
			},
			expectedMaxCPUs: 64,
		},
		{
			name: "multiple containers - all empty ResourceLists",
			containersResources: []corev1.ResourceList{
				{},
				{},
				{},
			},
			expectedMaxCPUs: 0,
		},
		{
			name: "multiple containers - mix of empty and non-empty",
			containersResources: []corev1.ResourceList{
				{},
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
				{},
				{
					corev1.ResourceCPU:    resource.MustParse("5"),
					corev1.ResourceMemory: resource.MustParse("500Mi"),
				},
			},
			expectedMaxCPUs: 5,
		},
		{
			name: "five containers - ascending order",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("1"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("3"),
					corev1.ResourceMemory: resource.MustParse("300Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("400Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("5"),
					corev1.ResourceMemory: resource.MustParse("500Mi"),
				},
			},
			expectedMaxCPUs: 5,
		},
		{
			name: "five containers - descending order",
			containersResources: []corev1.ResourceList{
				{
					corev1.ResourceCPU:    resource.MustParse("10"),
					corev1.ResourceMemory: resource.MustParse("1Gi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("8"),
					corev1.ResourceMemory: resource.MustParse("800Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("6"),
					corev1.ResourceMemory: resource.MustParse("600Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("400Mi"),
				},
				{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("200Mi"),
				},
			},
			expectedMaxCPUs: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MaxCPURequestsRounded(tt.containersResources)
			if result != tt.expectedMaxCPUs {
				t.Errorf("MaxCPURequestsRounded() = %d, want %d", result, tt.expectedMaxCPUs)
			}
		})
	}
}
