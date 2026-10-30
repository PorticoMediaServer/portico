//go:build linux || darwin

package mounts

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestCacheVolumeKeySharesFilesystemAcrossDirectories(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "owned-artifacts")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	parentKey, err := CacheVolumeKey(root)
	if err != nil {
		t.Fatal(err)
	}
	childKey, err := CacheVolumeKey(child)
	if err != nil || parentKey != childKey {
		t.Fatalf("same filesystem did not share its growth key: %v", err)
	}
	if raw, err := hex.DecodeString(parentKey); err != nil || len(raw) != 32 {
		t.Fatal("volume key is not an opaque SHA-256 identity")
	}
	if _, err := CacheVolumeKey(filepath.Join(root, "missing")); err == nil {
		t.Fatal("unobserved filesystem received a key")
	}
}
