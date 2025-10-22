package util

import (
	"os"
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
