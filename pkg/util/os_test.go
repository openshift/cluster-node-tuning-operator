package util

import (
	"os"
	"testing"
)

// TestAddToPath tests basic functionality of AddToPath.
func TestAddToPath(t *testing.T) {
	tests := []struct {
		name        string
		initialPath string
		pathToAdd   string
		expected    string
	}{
		{
			name:        "Add new path to existing PATH",
			initialPath: "/usr/bin" + pathSeparator + "/usr/local/bin",
			pathToAdd:   "/opt/bin",
			expected:    "/usr/bin" + pathSeparator + "/usr/local/bin" + pathSeparator + "/opt/bin",
		},
		{
			name:        "Path already exists in PATH",
			initialPath: "/usr/bin" + pathSeparator + "/usr/local/bin" + pathSeparator + "/opt/bin",
			pathToAdd:   "/usr/bin",
			expected:    "/usr/bin" + pathSeparator + "/usr/local/bin" + pathSeparator + "/opt/bin",
		},
		{
			name:        "Path exists as substring but not as the exact match",
			initialPath: "/usr/bin" + pathSeparator + "/usr/local/bin",
			pathToAdd:   "/usr",
			expected:    "/usr/bin" + pathSeparator + "/usr/local/bin" + pathSeparator + "/usr",
		},
		// A zero-length (null) directory name in the value of PATH indicates the current directory.
		// Examples: PATH="", PATH=/usr/bin:, PATH=/usr/bin::/usr/local/bin
		{
			name:        "Add path to empty PATH",
			initialPath: "",
			pathToAdd:   "/usr/bin",
			expected:    pathSeparator + "/usr/bin",
		},
		{
			name:        "Add empty path",
			initialPath: "/usr/bin",
			pathToAdd:   "",
			expected:    "/usr/bin" + pathSeparator,
		},
		{
			name:        "Single empty path exists in the middle of non-empty PATH",
			initialPath: "/usr/bin" + pathSeparator + pathSeparator + "/usr/local/bin",
			pathToAdd:   "",
			expected:    "/usr/bin" + pathSeparator + pathSeparator + "/usr/local/bin",
		},
		{
			name:        "Single empty path exists at the end of in non-empty PATH",
			initialPath: "/usr/bin" + pathSeparator,
			pathToAdd:   "",
			expected:    "/usr/bin" + pathSeparator,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use t.Setenv instead of os.Setenv() to restore the original PATH automatically.
			t.Setenv("PATH", tt.initialPath)
			err := AddToPath(tt.pathToAdd)
			if err != nil {
				t.Errorf("Expected no error from AddToPath() but got: %v", err)
			}

			newPath := os.Getenv("PATH")
			if newPath != tt.expected {
				t.Errorf("Expected PATH to be %q, but got %q", tt.expected, newPath)
			}
		})
	}
}
