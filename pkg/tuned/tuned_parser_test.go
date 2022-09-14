package tuned

import (
	"testing"
)

func TestBuiltinExpansion(t *testing.T) {
	var tests = []struct {
		input          string
		expectedOutput string
	}{
		// Basic expansion.
		{
			input:          "provider-cloudX",
			expectedOutput: "provider-cloudY",
		},
		{
			input:          "provider-${f:exec:printf:cloudX}",
			expectedOutput: "provider-cloudX",
		},
		{
			input:          "provider-$cloudX",
			expectedOutput: "provider-$cloudX",
		},
		{
			input:          "provider-${f:exec:printf:cloudX}_$$_${}cl'oudX${f:",
			expectedOutput: "provider-cloudX_$$_${}cl'oudX${f:",
		},
		// Deeper nesting in functions and its arguments.
		{
			input:          "provider-${f:${f:exec:printf:exec}:printf:cloudX}",
			expectedOutput: "provider-cloudX",
		},
		{
			input:          "provider-${f:${f:${f:exec:printf:exec}:printf:exec}:printf:cloudX}",
			expectedOutput: "provider-cloudX",
		},
		{
			input:          "provider-${f:exec:${f:exec:printf:printf}:cloudX}",
			expectedOutput: "provider-cloudX",
		},
		{
			input:          "provider-${f:exec:${f:exec:printf:print}f:cloudX}",
			expectedOutput: "provider-cloudX",
		},
		{
			input:          "provider-${f:exec:${f:${f:exec:printf:exec}:printf:printf}:cloudX}",
			expectedOutput: "provider-cloudX",
		},
		{
			input:          "provider-${f:exec:printf:cl${f:exec:printf:o}udX}",
			expectedOutput: "provider-cloudX",
		},
	}

	for i, tc := range tests {
		actual := expandTuneDBuiltin(tc.input)

		if actual != tc.expectedOutput {
			t.Errorf(
				"failed test case %d:\n\t  in: %s\n\twant: %s\n\thave: %s",
				i+1,
				tc.input,
				tc.expectedOutput,
				actual,
			)
		}
	}
}
