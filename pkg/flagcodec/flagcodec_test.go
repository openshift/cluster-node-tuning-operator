/*
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2021 Red Hat, Inc.
 */

package flagcodec

import (
	"reflect"
	"testing"
)

func TestParseStringRoundTrip(t *testing.T) {
	type testCase struct {
		name     string
		argv     []string
		expected []string
	}

	testCases := []testCase{
		{
			name: "nil",
		},
		{
			name: "empty",
			argv: []string{},
		},
		{
			name: "only command",
			argv: []string{
				"/bin/true",
			},
			expected: []string{
				"/bin/true",
			},
		},
		{
			name: "simple",
			argv: []string{
				"/bin/resource-topology-exporter",
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
			},
			expected: []string{
				"/bin/resource-topology-exporter",
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
			},
		},
	}

	for _, normFlag := range []bool{false, true} {
		for _, tc := range testCases {
			name := tc.name
			if normFlag {
				name += "-norm-flag"
			}
			t.Run(name, func(t *testing.T) {
				var fl *Flags
				if normFlag {
					fl = ParseArgvKeyValue(tc.argv, WithFlagNormalization)
				} else {
					fl = ParseArgvKeyValue(tc.argv)
				}
				got := fl.Argv()
				if !reflect.DeepEqual(tc.expected, got) {
					t.Errorf("expected %v got %v", tc.expected, got)
				}
			})
		}
	}
}

func TestParseStringRoundTripWithCommand(t *testing.T) {
	type testCase struct {
		name     string
		command  string
		args     []string
		expected []string
	}

	testCases := []testCase{
		{
			name: "nil",
		},
		{
			name: "empty",
			args: []string{},
		},
		{
			name:    "only command",
			command: "/bin/true",
			expected: []string{
				"/bin/true",
			},
		},
		{
			name:    "simple",
			command: "/bin/resource-topology-exporter",
			args: []string{
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
			},
			expected: []string{
				"/bin/resource-topology-exporter",
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
			},
		},
	}

	for _, normFlag := range []bool{false, true} {
		for _, tc := range testCases {
			name := tc.name
			if normFlag {
				name += "-norm-flag"
			}
			t.Run(name, func(t *testing.T) {
				var fl *Flags
				if normFlag {
					fl = ParseArgvKeyValue(tc.args, WithCommand(tc.command), WithFlagNormalization)
				} else {
					fl = ParseArgvKeyValue(tc.args, WithCommand(tc.command))
				}
				got := fl.Argv()
				if !reflect.DeepEqual(tc.expected, got) {
					t.Errorf("expected %v got %v", tc.expected, got)
				}
			})
		}
	}
}

func TestAddFlags(t *testing.T) {
	type testOpt struct {
		name  string
		value string
	}

	type testCase struct {
		name     string
		command  string
		args     []string
		options  []testOpt
		expected []string
	}

	testCases := []testCase{
		{
			name:    "add-mixed",
			command: "/bin/resource-topology-exporter",
			args: []string{
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
			},
			options: []testOpt{
				{
					name:  "--hostname",
					value: "host.test.net",
				},
				{
					name:  "--v",
					value: "2",
				},
			},
			expected: []string{
				"/bin/resource-topology-exporter",
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
				"--hostname=host.test.net",
				"--v=2",
			},
		},
		{
			name:    "add-mixed-norm",
			command: "/bin/resource-topology-exporter",
			args: []string{
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
			},
			options: []testOpt{
				{
					name:  "--hostname",
					value: "host.test.net",
				},
				{
					name:  "-v",
					value: "2",
				},
			},
			expected: []string{
				"/bin/resource-topology-exporter",
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
				"--hostname=host.test.net",
				"-v=2",
			},
		},
	}

	for _, tc := range testCases {
		name := tc.name
		t.Run(name, func(t *testing.T) {
			fl := ParseArgvKeyValue(tc.args, WithCommand(tc.command))
			for _, opt := range tc.options {
				fl.SetOption(opt.name, opt.value)
			}
			got := fl.Argv()
			if !reflect.DeepEqual(tc.expected, got) {
				t.Errorf("expected %v got %v", tc.expected, got)
			}
		})
	}
}

func TestAddFlagsNormalized(t *testing.T) {
	type testOpt struct {
		name  string
		value string
	}

	type testCase struct {
		name     string
		command  string
		args     []string
		options  []testOpt
		expected []string
	}

	testCases := []testCase{
		{
			name:    "add-mixed",
			command: "/bin/resource-topology-exporter",
			args: []string{
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
			},
			options: []testOpt{
				{
					name:  "--hostname",
					value: "host.test.net",
				},
				{
					name:  "--v",
					value: "2",
				},
			},
			expected: []string{
				"/bin/resource-topology-exporter",
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
				"--hostname=host.test.net",
				"-v=2",
			},
		},
		{
			name:    "add-mixed-norm",
			command: "/bin/resource-topology-exporter",
			args: []string{
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
			},
			options: []testOpt{
				{
					name:  "--hostname",
					value: "host.test.net",
				},
				{
					name:  "-v",
					value: "2",
				},
			},
			expected: []string{
				"/bin/resource-topology-exporter",
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
				"--hostname=host.test.net",
				"-v=2",
			},
		},
	}

	for _, tc := range testCases {
		name := tc.name
		t.Run(name, func(t *testing.T) {
			fl := ParseArgvKeyValue(tc.args, WithCommand(tc.command), WithFlagNormalization)
			for _, opt := range tc.options {
				fl.SetOption(opt.name, opt.value)
			}
			got := fl.Argv()
			if !reflect.DeepEqual(tc.expected, got) {
				t.Errorf("expected %v got %v", tc.expected, got)
			}
		})
	}
}

func TestDeleteFlags(t *testing.T) {

	type testCase struct {
		name     string
		command  string
		args     []string
		options  []string
		expected []string
	}

	testCases := []testCase{
		{
			name:    "remove-option",
			command: "/bin/resource-topology-exporter",
			args: []string{
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
			},
			options: []string{
				"--sleep-interval",
			},
			expected: []string{
				"/bin/resource-topology-exporter",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
			},
		},
		{
			name:    "remove-option-inexistent",
			command: "/bin/resource-topology-exporter",
			args: []string{
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
			},
			options: []string{
				"--foo=bar",
			},
			expected: []string{
				"/bin/resource-topology-exporter",
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
			},
		},
		{
			name:    "remove-toggle",
			command: "/bin/resource-topology-exporter",
			args: []string{
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
				"--pods-fingerprint",
			},
			options: []string{
				"--pods-fingerprint",
			},
			expected: []string{
				"/bin/resource-topology-exporter",
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
			},
		},
		{
			name:    "remove-toggle-inexistent",
			command: "/bin/resource-topology-exporter",
			args: []string{
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
				"--pods-fingerprint",
			},
			options: []string{
				"--fizz-buzz",
			},
			expected: []string{
				"/bin/resource-topology-exporter",
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
				"--pods-fingerprint",
			},
		},
	}

	for _, tc := range testCases {
		name := tc.name
		t.Run(name, func(t *testing.T) {
			fl := ParseArgvKeyValue(tc.args, WithCommand(tc.command))
			for _, opt := range tc.options {
				fl.Delete(opt)
			}
			got := fl.Argv()
			if !reflect.DeepEqual(tc.expected, got) {
				t.Errorf("expected %v got %v", tc.expected, got)
			}
		})
	}
}

func TestDeleteFlagsNormalized(t *testing.T) {

	type testCase struct {
		name     string
		command  string
		args     []string
		options  []string
		expected []string
	}

	testCases := []testCase{
		{
			name:    "remove-option-double-dash",
			command: "/bin/resource-topology-exporter",
			args: []string{
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
				"--pods-fingerprint",
				"-v=2",
			},
			options: []string{
				"--v",
			},
			expected: []string{
				"/bin/resource-topology-exporter",
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
				"--pods-fingerprint",
			},
		},
		{
			name:    "remove-option-single-dash",
			command: "/bin/resource-topology-exporter",
			args: []string{
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
				"--pods-fingerprint",
				"-v=2",
			},
			options: []string{
				"-v",
			},
			expected: []string{
				"/bin/resource-topology-exporter",
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
				"--pods-fingerprint",
			},
		},
	}

	for _, tc := range testCases {
		name := tc.name
		t.Run(name, func(t *testing.T) {
			fl := ParseArgvKeyValue(tc.args, WithCommand(tc.command), WithFlagNormalization)
			for _, opt := range tc.options {
				fl.Delete(opt)
			}
			got := fl.Argv()
			if !reflect.DeepEqual(tc.expected, got) {
				t.Errorf("expected %v got %v", tc.expected, got)
			}
		})
	}
}

func TestGetFlags(t *testing.T) {
	type testOpt struct {
		value Val
		found bool
	}

	type testCase struct {
		name     string
		command  string
		args     []string
		params   []string
		expected []testOpt
	}

	testCases := []testCase{
		{
			name:    "get-option-missing",
			command: "/bin/resource-topology-exporter",
			args: []string{
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
			},
			params: []string{
				"--blah",
			},
			expected: []testOpt{
				{
					value: Val{},
					found: false,
				},
			},
		},
		{
			name:    "get-option-checking-dashes",
			command: "/bin/resource-topology-exporter",
			args: []string{
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
				"-v=2",
			},
			params: []string{
				"--v",
			},
			expected: []testOpt{
				{
					value: Val{},
					found: false,
				},
			},
		},
		{
			name:    "get-flag-option-existing",
			command: "/bin/resource-topology-exporter",
			args: []string{
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
			},
			params: []string{
				"--sysfs",
			},
			expected: []testOpt{
				{
					value: Val{
						Kind: FlagOption,
						Data: "/host-sys",
					},
					found: true,
				},
			},
		},
		{
			name:    "get-toggle-option-existing",
			command: "/bin/resource-topology-exporter",
			args: []string{
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
				"--pods-fingerprint",
			},
			params: []string{
				"--pods-fingerprint",
			},
			expected: []testOpt{
				{
					value: Val{
						Kind: FlagToggle,
						Data: "",
					},
					found: true,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fl := ParseArgvKeyValue(tc.args, WithCommand(tc.command))
			for idx := range tc.params {
				param := tc.params[idx]
				exp := tc.expected[idx]
				got, ok := fl.GetFlag(param)
				if ok != exp.found {
					t.Fatalf("flag %q found %v expected %v", param, ok, exp.found)
				}
				if !reflect.DeepEqual(got, exp.value) {
					t.Errorf(" flag %q got %+v expected %+v", param, got, exp.value)
				}
			}
		})
	}
}

func TestGetFlagsNormalized(t *testing.T) {
	type testOpt struct {
		value Val
		found bool
	}

	type testCase struct {
		name     string
		command  string
		args     []string
		params   []string
		expected []testOpt
	}

	testCases := []testCase{
		{
			name:    "get-option-ignoring-dashes",
			command: "/bin/resource-topology-exporter",
			args: []string{
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
				"-v=2",
			},
			params: []string{
				"--v",
			},
			expected: []testOpt{
				{
					value: Val{
						Kind: FlagOption,
						Data: "2",
					},
					found: true,
				},
			},
		},
		{
			name:    "get-option-matching-dashes",
			command: "/bin/resource-topology-exporter",
			args: []string{
				"--sleep-interval=10s",
				"--sysfs=/host-sys",
				"--kubelet-state-dir=/host-var/lib/kubelet",
				"--podresources-socket=unix:///host-var/lib/kubelet/pod-resources/kubelet.sock",
				"-v=2",
			},
			params: []string{
				"-v",
			},
			expected: []testOpt{
				{
					value: Val{
						Kind: FlagOption,
						Data: "2",
					},
					found: true,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fl := ParseArgvKeyValue(tc.args, WithCommand(tc.command), WithFlagNormalization)
			for idx := range tc.params {
				param := tc.params[idx]
				exp := tc.expected[idx]
				got, ok := fl.GetFlag(param)
				if ok != exp.found {
					t.Fatalf("flag %q found %v expected %v", param, ok, exp.found)
				}
				if !reflect.DeepEqual(got, exp.value) {
					t.Errorf(" flag %q got %+v expected %+v", param, got, exp.value)
				}
			}
		})
	}
}
