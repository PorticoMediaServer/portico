package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStorageGroupsNeverExposeMachineFileNames(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"guide.db", "guide.db-wal", "guide.db-shm", "another.sqlite-wal", "secrets.key", "ffmpeg-abilities.json", "unexpected-folder/file"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("size"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	usage, err := storageUsage(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"database": 16, "keys": 4, "other-files": 8}
	if len(usage.Folders) != len(want) {
		t.Fatal(usage.Folders)
	}
	for _, group := range usage.Folders {
		if n, ok := want[group.Name]; !ok || group.Bytes != n {
			t.Fatal("an ungrouped file or wrong size", usage.Folders)
		}
	}
}
