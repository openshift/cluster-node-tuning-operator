package util

import (
	"fmt"
	"os"
	"strings"
)

const (
	pathSeparator = string(os.PathListSeparator)
)

// Delete a file if it exists.  Returns nil if 'file' does not exist.
func Delete(file string) error {
	var err error
	if err = os.Remove(file); os.IsNotExist(err) {
		return nil
	}

	return err
}

// Create a symbolic link.  Returns nil if the link already exists.
func Symlink(target, linkName string) error {
	if _, err := os.Lstat(linkName); err != nil {
		if os.IsNotExist(err) {
			return os.Symlink(target, linkName)
		}
		return err
	}

	return nil
}

// AddToPath adds 'path' to PATH environment variable unless it exists.
// Returns nil unless os.Setenv() call fails.
func AddToPath(path string) error {
	currentPath := os.Getenv("PATH")

	paths := strings.Split(currentPath, pathSeparator)
	for _, p := range paths {
		if p == path {
			// 'path' already exists in PATH
			return nil
		}
	}

	newPath := fmt.Sprintf("%s%s%s", currentPath, pathSeparator, path)

	return os.Setenv("PATH", newPath)
}
