package util

import (
	"os"
)

// Create a directory including its parents.  Returns nil if directory already exists.
func Mkdir(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, os.ModePerm)
		if err != nil {
			return err
		}
	}

	return nil
}
