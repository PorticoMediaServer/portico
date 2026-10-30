package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

// Sparse blank files exercise actual stat/inventory boundaries, not playback.
func TestInventorySampleSizeBoundaryAndExplicitIgnore(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "Samples"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, size := range map[string]int64{
		"Movie-sample.mkv":    naming.SampleLimitBytes - 1,
		"Boundary-sample.mkv": naming.SampleLimitBytes,
		"Large-sample.mkv":    naming.SampleLimitBytes + 1,
		"Samples/small.mkv":   naming.SampleLimitBytes - 1,
		"Samples/full.mkv":    naming.SampleLimitBytes,
		"Explicit-sample.mkv": naming.SampleLimitBytes + 1,
	} {
		f, err := os.Create(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		err = f.Truncate(size)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".plexignore"), []byte("Explicit-sample.mkv\n"), 0600); err != nil {
		t.Fatal(err)
	}
	page, err := LocalInventoryPage(InventoryRequest{Root: root, RelativePath: ".", Limit: 128})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range page.Entries {
		seen[entry.Name] = true
	}
	if !seen["Boundary-sample.mkv"] || !seen["Large-sample.mkv"] || !seen["Samples"] || seen["Movie-sample.mkv"] || seen["Explicit-sample.mkv"] {
		t.Fatal("sample size/user rule boundary", seen, page.Issues)
	}
	child, err := LocalInventoryPage(InventoryRequest{Root: root, RelativePath: "Samples", RootIdentity: page.RootIdentity, Limit: 128})
	if err != nil || len(child.Entries) != 1 || child.Entries[0].Name != "full.mkv" {
		t.Fatal("sample folder was not classified per file", child, err)
	}
}
