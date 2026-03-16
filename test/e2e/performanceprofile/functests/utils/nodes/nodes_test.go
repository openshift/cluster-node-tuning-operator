package nodes

import (
	"testing"

	"k8s.io/utils/cpuset"
)

func TestGetTwoSiblingsFromCPUSet(t *testing.T) {
	tests := []struct {
		name        string
		siblings    map[int]map[int][]int
		cpuSet      cpuset.CPUSet
		wantCPUSet  cpuset.CPUSet
		expectError bool
	}{
		{
			name:        "empty siblings map returns error",
			siblings:    map[int]map[int][]int{},
			cpuSet:      cpuset.New(0, 1, 2, 3),
			wantCPUSet:  cpuset.New(),
			expectError: true,
		},
		{
			name:        "nil siblings map returns error",
			siblings:    nil,
			cpuSet:      cpuset.New(0, 1),
			wantCPUSet:  cpuset.New(),
			expectError: true,
		},
		{
			name: "two siblings both in cpuSet returns that sibling set",
			siblings: map[int]map[int][]int{
				0: {0: {0, 16}}, // NUMA 0, core 0: CPUs 0 and 16 (HT pair)
			},
			cpuSet:      cpuset.New(0, 1, 2, 16),
			wantCPUSet:  cpuset.New(0, 16),
			expectError: false,
		},
		{
			name: "two siblings only one in cpuSet returns error",
			siblings: map[int]map[int][]int{
				0: {0: {0, 16}},
			},
			cpuSet:      cpuset.New(0, 1, 2), // 16 not in set
			wantCPUSet:  cpuset.New(),
			expectError: true,
		},
		{
			name: "first core not subset second core is subset returns second",
			siblings: map[int]map[int][]int{
				0: {
					0: {0, 16}, // not in cpuSet
					1: {2, 18}, // both in cpuSet
				},
			},
			cpuSet:      cpuset.New(2, 18, 4, 5),
			wantCPUSet:  cpuset.New(2, 18),
			expectError: false,
		},
		{
			name: "single CPU sibling returns error (wanted 2)",
			siblings: map[int]map[int][]int{
				0: {0: {5}}, // single CPU - core must have exactly 2 siblings
			},
			cpuSet:      cpuset.New(1, 2, 5, 6),
			wantCPUSet:  cpuset.New(),
			expectError: true,
		},
		{
			name: "multiple NUMA nodes first matching core returned",
			siblings: map[int]map[int][]int{
				0: {0: {0, 16}}, // not in cpuSet (no 16)
				1: {0: {1, 17}}, // both in cpuSet
			},
			cpuSet:      cpuset.New(1, 17, 2, 18),
			wantCPUSet:  cpuset.New(1, 17),
			expectError: false,
		},
		{
			name: "no core's siblings are subset of cpuSet returns error",
			siblings: map[int]map[int][]int{
				0: {0: {0, 16}, 1: {2, 18}},
			},
			cpuSet:      cpuset.New(10, 11, 12),
			wantCPUSet:  cpuset.New(),
			expectError: true,
		},
		{
			name: "empty cpuSet returns error",
			siblings: map[int]map[int][]int{
				0: {0: {0, 16}},
			},
			cpuSet:      cpuset.New(),
			wantCPUSet:  cpuset.New(),
			expectError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetTwoSiblingsFromCPUSet(tt.siblings, tt.cpuSet)
			if (err != nil) != tt.expectError {
				t.Errorf("GetTwoSiblingsFromCPUSet() error = %v, expectError %v", err, tt.expectError)
				return
			}
			if !got.Equals(tt.wantCPUSet) {
				t.Errorf("GetTwoSiblingsFromCPUSet() got = %v, want %v", got.String(), tt.wantCPUSet.String())
			}
		})
	}
}
