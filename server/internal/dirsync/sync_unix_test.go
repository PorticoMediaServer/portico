//go:build !windows

package dirsync

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectorySyncPropagatesFailures(t *testing.T) {
	directory := t.TempDir()
	if err := Sync(directory); err != nil {
		t.Fatal(err)
	}
	// A publisher must not acknowledge durability when its directory is gone
	// or the resolved path is no longer a directory.
	missing := filepath.Join(directory, "missing")
	if err := Sync(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing directory: %v", err)
	}
	file := filepath.Join(directory, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Sync(filepath.Join(file, "child")); err == nil {
		t.Fatal("invalid directory accepted")
	}
}
