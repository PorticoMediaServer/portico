package preparedmedia

import (
	"os"
	"path/filepath"
	"testing"
)

// Production requires a private physical path; macOS TempDir may use /var's alias.
func canonicalFixtureDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	return root
}
