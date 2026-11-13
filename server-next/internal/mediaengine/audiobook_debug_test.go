package mediaengine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Temporary investigation only; remove before review. Retain generated audio, never server auth state.
func keepBookFailure(t *testing.T, sources *[]string) {
	root := os.Getenv("PORTICO_AUDIOBOOK_EVIDENCE")
	if root == "" || !t.Failed() {
		return
	}
	dir, err := os.MkdirTemp(root, t.Name()+"-")
	if err != nil {
		t.Log(err)
		return
	}
	for n, source := range *sources {
		info, err := os.Stat(source)
		if err != nil {
			continue
		}
		target := filepath.Join(dir, fmt.Sprintf("%02d-%s", n, filepath.Base(source)))
		if info.IsDir() {
			err = os.CopyFS(target, os.DirFS(source))
		} else {
			var raw []byte
			if raw, err = os.ReadFile(source); err == nil {
				err = os.WriteFile(target, raw, 0600)
			}
		}
		if err != nil {
			t.Log(err)
		}
	}
}
