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
			expectedOutput: "provider-cloudX",
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

func TestLscpuCheckARM(t *testing.T) {
	lscpuARM := `Architecture:                         aarch64
CPU op-mode(s):                       32-bit, 64-bit
Byte Order:                           Little Endian
CPU(s):                               80
On-line CPU(s) list:                  0-79
Vendor ID:                            ARM
BIOS Vendor ID:                       Ampere(R)
Model name:                           Neoverse-N1
BIOS Model name:                      Ampere(R) Altra(R) Processor Q80-30 CPU @ 3.0GHz
BIOS CPU family:                      257
Model:                                1
Thread(s) per core:                   1
Core(s) per socket:                   80
Socket(s):                            1
Stepping:                             r3p1
Frequency boost:                      disabled
CPU(s) scaling MHz:                   100%
CPU max MHz:                          3000.0000
CPU min MHz:                          1000.0000
BogoMIPS:                             50.00
Flags:                                fp asimd evtstrm aes pmull sha1 sha2 crc32 atomics fphp asimdhp cpuid asimdrdm lrcpc dcpop asimddp
L1d cache:                            5 MiB (80 instances)
L1i cache:                            5 MiB (80 instances)
L2 cache:                             80 MiB (80 instances)
NUMA node(s):                         1
NUMA node0 CPU(s):                    0-79
Vulnerability Gather data sampling:   Not affected
Vulnerability Itlb multihit:          Not affected
Vulnerability L1tf:                   Not affected
Vulnerability Mds:                    Not affected
Vulnerability Meltdown:               Not affected
Vulnerability Mmio stale data:        Not affected
Vulnerability Reg file data sampling: Not affected
Vulnerability Retbleed:               Not affected
Vulnerability Spec rstack overflow:   Not affected
Vulnerability Spec store bypass:      Mitigation; Speculative Store Bypass disabled via prctl
Vulnerability Spectre v1:             Mitigation; __user pointer sanitization
Vulnerability Spectre v2:             Mitigation; CSV2, BHB
Vulnerability Srbds:                  Not affected
Vulnerability Tsx async abort:        Not affected
`

	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr bool
	}{
		{
			name:    "Match ARM Vendor ID",
			args:    []string{"Vendor ID\\:\\s*GenuineIntel", "intel", "Vendor ID\\:\\s*AuthenticAMD", "amd", "Architecture\\:\\s*aarch64", "arm", "unknown"},
			want:    "arm",
			wantErr: false,
		},
		{
			name:    "Match aarch64 architecture",
			args:    []string{"Architecture\\:\\s*x86_64", "x86", "Architecture\\:\\s*aarch64", "aarch64"},
			want:    "aarch64",
			wantErr: false,
		},
		{
			name:    "No match returns fallback",
			args:    []string{"NonExistentPattern", "result", "fallback"},
			want:    "fallback",
			wantErr: false,
		},
		{
			name:    "No match no fallback returns empty string",
			args:    []string{"AuthenticAMD", "amd"},
			want:    "",
			wantErr: false,
		},
		{
			name:    "First match wins",
			args:    []string{"^Architecture", "aarch64", "ARM", "arm", "unknown"},
			want:    "aarch64",
			wantErr: false,
		},
		{
			name:    "One argument returns error",
			args:    []string{"fallback"},
			want:    "",
			wantErr: true,
		},
		{
			name:    "Zero arguments returns error",
			args:    []string{},
			want:    "",
			wantErr: true,
		},
		{
			name:    "Invalid regex returns error",
			args:    []string{"[invalid(regex", "result"},
			want:    "",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := lscpu_check(lscpuARM, tc.args)
			if (err != nil) != tc.wantErr {
				t.Errorf("lscpu_check() error = %v, wantErr %v", err, tc.wantErr)
				return
			}
			if got != tc.want {
				t.Errorf("lscpu_check() = %v, want %v", got, tc.want)
			}
		})
	}
}
