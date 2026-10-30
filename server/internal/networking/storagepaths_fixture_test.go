package networking

import (
	"os"
	"path/filepath"
)

func writePathFixture(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}
