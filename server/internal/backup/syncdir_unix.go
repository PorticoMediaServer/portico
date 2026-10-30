//go:build !windows

package backup

import "os"

func syncDirectory(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func syncDirectoryHandle(file *os.File) error { return file.Sync() }
