// Assisted-by: Claude Code IDE; model: claude-4.5-sonnet

package sync

import (
	"os"
	"testing"
)

func TestIsReady(t *testing.T) {
	testCases := []struct {
		name                   string
		mcpName                string
		readyPools             map[string]string
		expectedBootcmdlineDep string
		releaseVersion         string
		expected               bool
	}{
		{
			name:                   "matching release version and tuned dependency",
			mcpName:                "worker",
			readyPools:             map[string]string{"worker": "0.0.1,tuned1:1"},
			expectedBootcmdlineDep: "tuned1:1",
			releaseVersion:         "0.0.1",
			expected:               true,
		},
		{
			name:                   "wrong release version but correct tuned dependency",
			mcpName:                "worker",
			readyPools:             map[string]string{"worker": "0.0.1,tuned1:1"},
			expectedBootcmdlineDep: "tuned1:1",
			releaseVersion:         "0.0.2",
			expected:               false,
		},
		{
			name:                   "matching release version but wrong tuned dependency",
			mcpName:                "worker",
			readyPools:             map[string]string{"worker": "0.0.1,tuned1:1"},
			expectedBootcmdlineDep: "tuned2:1",
			releaseVersion:         "0.0.1",
			expected:               false,
		},
		{
			name:                   "unset worker readyPools entry, but different (master) pool exists that matches",
			mcpName:                "worker",
			readyPools:             map[string]string{"master": "4.22.0,tuned1:1"},
			expectedBootcmdlineDep: "tuned1:1",
			releaseVersion:         "4.22.0",
			expected:               false,
		},
		{
			name:                   "multiple tuned dependencies, expected match at start",
			mcpName:                "worker",
			readyPools:             map[string]string{"worker": "4.22.0,tuned1:1,tuned2:5,tuned3:10"},
			expectedBootcmdlineDep: "tuned1:1",
			releaseVersion:         "4.22.0",
			expected:               true,
		},
		{
			name:                   "multiple tuned deps, expected match in middle",
			mcpName:                "worker",
			readyPools:             map[string]string{"worker": "4.22.0,tuned1:1,tuned2:5,tuned3:10"},
			expectedBootcmdlineDep: "tuned2:5",
			releaseVersion:         "4.22.0",
			expected:               true,
		},
		{
			name:                   "multiple tuned deps, expected match at end",
			mcpName:                "worker",
			readyPools:             map[string]string{"worker": "4.22.0,tuned1:1,tuned2:5,tuned3:10"},
			expectedBootcmdlineDep: "tuned3:10",
			releaseVersion:         "4.22.0",
			expected:               true,
		},
		{
			name:                   "multiple tuned deps: not found",
			mcpName:                "worker",
			readyPools:             map[string]string{"worker": "4.22.0,tuned1:1,tuned2:5,tuned3:10"},
			expectedBootcmdlineDep: "tuned4:1",
			releaseVersion:         "4.22.0",
			expected:               false,
		},
		{
			name:                   "partial match of tuned name: should not match",
			mcpName:                "worker",
			readyPools:             map[string]string{"worker": "4.22.0,tuned1:1"},
			expectedBootcmdlineDep: "tuned1",
			releaseVersion:         "4.22.0",
			expected:               false,
		},
		{
			name:                   "different generation should not match",
			mcpName:                "worker",
			readyPools:             map[string]string{"worker": "4.22.0,tuned1:1"},
			expectedBootcmdlineDep: "tuned1:2",
			releaseVersion:         "4.22.0",
			expected:               false,
		},
		{
			name:                   "empty expected bootcmdline dependency",
			mcpName:                "worker",
			readyPools:             map[string]string{"worker": "4.22.0,tuned1:1"},
			expectedBootcmdlineDep: "",
			releaseVersion:         "4.22.0",
			expected:               false,
		},
		{
			name:                   "release version mismatch, empty in the environment",
			mcpName:                "worker",
			readyPools:             map[string]string{"worker": "4.22.0,tuned1:1"},
			expectedBootcmdlineDep: "tuned1:1",
			releaseVersion:         "",
			expected:               false,
		},
		{
			name:                   "empty release version in readyPools",
			mcpName:                "worker",
			readyPools:             map[string]string{"worker": ",tuned1:1"},
			expectedBootcmdlineDep: "tuned1:1",
			releaseVersion:         "",
			expected:               true,
		},
		{
			name:                   "unset readyPools entry",
			mcpName:                "worker",
			readyPools:             map[string]string{},
			expectedBootcmdlineDep: "tuned1:1",
			releaseVersion:         "4.22.0",
			expected:               false,
		},
		{
			name:                   "empty string in readyPools",
			mcpName:                "worker",
			readyPools:             map[string]string{"worker": ""},
			expectedBootcmdlineDep: "tuned1:1",
			releaseVersion:         "4.22.0",
			expected:               false,
		},
		{
			name:                   "only release version in readyPools, no tuned dependencies",
			mcpName:                "worker",
			readyPools:             map[string]string{"worker": "0.0.1"},
			expectedBootcmdlineDep: "tuned1:1",
			releaseVersion:         "0.0.1",
			expected:               false,
		},
		{
			name:                   "complex release version string",
			mcpName:                "worker",
			readyPools:             map[string]string{"worker": "4.22.0-rc.1+git.abc123,tuned1:1"},
			expectedBootcmdlineDep: "tuned1:1",
			releaseVersion:         "4.22.0-rc.1+git.abc123",
			expected:               true,
		},
		{
			name:                   "tuned name with special characters",
			mcpName:                "worker",
			readyPools:             map[string]string{"worker": "4.22.0,my-tuned-profile_v2:123"},
			expectedBootcmdlineDep: "my-tuned-profile_v2:123",
			releaseVersion:         "4.22.0",
			expected:               true,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new bootcmdlineSync instance for each test to avoid interference
			b := &bootcmdlineSync{
				readyPools: tt.readyPools,
			}

			// Use t.Setenv to automatically restore environment after subtest
			if tt.releaseVersion != "" {
				t.Setenv("RELEASE_VERSION", tt.releaseVersion)
			} else {
				t.Setenv("RELEASE_VERSION", "")
			}

			got := b.IsReady(tt.mcpName, tt.expectedBootcmdlineDep)
			if got != tt.expected {
				t.Errorf("IsReady() = %v, expected %v", got, tt.expected)
			}
		})
	}
}

func TestIsReady_ConcurrentAccess(t *testing.T) {
	// Test that IsReady can be called concurrently with SignalReady
	b := &bootcmdlineSync{
		readyPools: make(map[string]string),
	}

	// Set RELEASE_VERSION for the test
	oldReleaseVersion := os.Getenv("RELEASE_VERSION")
	defer func() {
		if oldReleaseVersion != "" {
			os.Setenv("RELEASE_VERSION", oldReleaseVersion)
		} else {
			os.Unsetenv("RELEASE_VERSION")
		}
	}()
	os.Setenv("RELEASE_VERSION", "4.22.0")

	done := make(chan bool)

	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			b.SignalReady("worker", "4.22.0,tuned1:1")
			b.ClearCacheForPool("worker")
		}
		done <- true
	}()

	// Reader goroutines
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = b.IsReady("worker", "tuned1:1")
			}
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 6; i++ {
		<-done
	}

	// After concurrent operations, verify correctness with known state
	if got := b.IsReady("worker", "tuned1:1"); got != false {
		t.Fatalf("IsReady() after ClearCacheForPool = %v, expected false", got)
	}

	// Signal ready with known values and verify
	b.SignalReady("worker", "4.22.0,tuned1:1")
	if got := b.IsReady("worker", "tuned1:1"); got != true {
		t.Fatalf("IsReady() with matching deps = %v, expected true", got)
	}

	// Verify with wrong tuned dependency
	if got := b.IsReady("worker", "tuned2:2"); got != false {
		t.Fatalf("IsReady() with non-matching tuned dep = %v, expected false", got)
	}

	// Verify with wrong pool name
	if got := b.IsReady("master", "tuned1:1"); got != false {
		t.Fatalf("IsReady() with non-matching pool = %v, expected false", got)
	}
}
