package util

import (
	"io"
	"os"
)

// Create a file if it does not exist.  Returns nil if 'file' already exists.
func Create(file string) error {
	if _, err := os.Stat(file); os.IsNotExist(err) {
		file, err := os.Create(file)
		if err != nil {
			return err
		}
		defer file.Close()
	}

	return nil
}

// Delete a file if it exists.  Returns nil if 'file' does not exist.
func Delete(file string) error {
	var err error
	if err = os.Remove(file); os.IsNotExist(err) {
		return nil
	}

	return err
}

// Create a directory 'dir' including its parents.  Returns nil if directory already exists.
func Mkdir(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return os.MkdirAll(dir, os.ModePerm)
	}

	return nil
}

// Copy a file from 'src' to 'dst'.
func Copy(src, dst string) error {
	fin, err := os.Open(src)
	if err != nil {
		return err
	}
	defer fin.Close()

	fout, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer fout.Close()

	_, err = io.Copy(fout, fin)
	if err != nil {
		return err
	}

	return nil
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
