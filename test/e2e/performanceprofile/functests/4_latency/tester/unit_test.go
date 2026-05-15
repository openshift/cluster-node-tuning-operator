package tester

import (
	"os"
	"testing"

	latency "github.com/openshift/cluster-node-tuning-operator/test/e2e/performanceprofile/functests/4_latency"
)

func TestGetLatencyTestMemory(t *testing.T) {
	testcases := []struct {
		testName       string
		envVarValue    string
		cpus           int
		expectedMemory string
	}{
		{
			testName:       "no env var set should default to 1Gi - low count of CPUs",
			envVarValue:    "",
			cpus:           4,
			expectedMemory: "1Gi",
		},
		{
			testName:       "no env var set should default to 1Gi - high count of CPUs",
			envVarValue:    "",
			cpus:           50,
			expectedMemory: "1Gi",
		},
		{
			testName:       "dynamic memory should be 32Mi per CPU with high count of CPUs",
			envVarValue:    "dynamic",
			cpus:           50,
			expectedMemory: "1600Mi",
		},
		{
			testName:       "explicitly set to 100Mi despite the CPUs count",
			envVarValue:    "100Mi",
			cpus:           50,
			expectedMemory: "100Mi",
		},
		{
			testName:       "2 CPUs should default to 1Gi",
			envVarValue:    "dynamic",
			cpus:           2,
			expectedMemory: "1Gi",
		},
		{
			testName:       "unset env var and unset cpus should default to 1Gi",
			envVarValue:    "",
			cpus:           -1,
			expectedMemory: "1Gi",
		},
		{
			testName:       "dynamic memory and unset cpus should default to 1Gi",
			envVarValue:    "dynamic",
			cpus:           -1,
			expectedMemory: "1Gi",
		},
		{
			testName:       "default memory and 0 cpus should default to 1Gi",
			envVarValue:    "",
			cpus:           0,
			expectedMemory: "1Gi",
		},
	}
	for _, testcase := range testcases {
		t.Run(testcase.testName, func(t *testing.T) {
			if testcase.envVarValue != "" {
				os.Setenv("LATENCY_TEST_MEMORY", testcase.envVarValue)
			}
			defer os.Unsetenv("LATENCY_TEST_MEMORY")

			memory, err := latency.GetLatencyTestMemory(testcase.cpus)
			if err != nil {
				t.Fatalf("failed to get latency test memory: %v", err)
			}
			if memory != testcase.expectedMemory {
				t.Fatalf("expected memory %s, got %s", testcase.expectedMemory, memory)
			}
		})
	}
}
