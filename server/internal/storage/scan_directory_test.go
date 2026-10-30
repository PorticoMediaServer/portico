package storage

import (
	"os"
	"path/filepath"
	"testing"
)

// The directory authority opens exactly the admitted folder beneath the root,
// and nothing else: a regular file, an escaping relative path and a missing
// folder are all refused before the sandbox is built.
func TestOpenInventoryDirectoryAdmitsOnlyTheFolder(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "Film")
	if err := os.MkdirAll(filepath.Join(folder, "BDMV"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "loose.mkv"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir, err := openInventoryDirectory(InventoryRequest{Root: root, RelativePath: "Film"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := dir.Stat()
	if err != nil || !info.IsDir() {
		t.Fatal(info, err)
	}
	dir.Close()
	for name, relative := range map[string]string{
		"regular file": "loose.mkv",
		"root itself":  ".",
		"missing":      "Elsewhere",
		"escape":       "../outside",
	} {
		if f, err := openInventoryDirectory(InventoryRequest{Root: root, RelativePath: relative}); err == nil {
			f.Close()
			t.Errorf("%s: admitted", name)
		}
	}
}
