package mounts

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

// fixtureExecutable copies the native helper with immutable permissions regardless
// of the umask used by go test. Never change the running test executable.
func fixtureExecutable(t *testing.T) string {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rclone-fixture")
	if err = os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
