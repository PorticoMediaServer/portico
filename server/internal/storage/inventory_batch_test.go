package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Several folders are listed and checked by one helper, each answer matching
// what the one-folder operations give, and a folder that fails answers with
// its own error without failing the others.
func TestInventoryBatchListsAndChecksSeveralFoldersInOneHelper(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := New(binary)
	root := t.TempDir()
	for _, name := range []string{"Film A (2001)/Film A (2001).mkv", "Film B (2002)/Film B (2002).mkv", "Film B (2002)/Film B (2002).en.srt"} {
		path := filepath.Join(root, name)
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, []byte("media"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	rootID, err := c.InspectRoot(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	requests := []InventoryRequest{
		{Root: root, RelativePath: "Film A (2001)", RootIdentity: RootIdentity(rootID), Limit: 64},
		{Root: root, RelativePath: "Missing", RootIdentity: RootIdentity(rootID), Limit: 64},
		{Root: root, RelativePath: "Film B (2002)", RootIdentity: RootIdentity(rootID), Limit: 64},
	}
	pages, errs, err := c.InventoryPages(context.Background(), "batch", requests)
	if err != nil {
		t.Fatal(err)
	}
	if errs[0] != nil || errs[2] != nil || !errors.Is(errs[1], os.ErrNotExist) {
		t.Fatalf("folder errors %v", errs)
	}
	for i, want := range []int{1, 0, 2} {
		if len(pages[i].Entries) != want {
			t.Fatalf("folder %d listed %d entries, want %d", i, len(pages[i].Entries), want)
		}
	}
	single, err := c.InventoryPage(context.Background(), "single", requests[2])
	if err != nil || single.DirectoryRevision != pages[2].DirectoryRevision || single.DirectoryIdentity != pages[2].DirectoryIdentity || len(single.Entries) != len(pages[2].Entries) {
		t.Fatalf("batch page %+v differs from the single listing %+v (%v)", pages[2], single, err)
	}
	values, errs, err := c.InventoryDirectories(context.Background(), "batch", requests)
	if err != nil {
		t.Fatal(err)
	}
	if errs[0] != nil || errs[2] != nil || errs[1] == nil {
		t.Fatalf("directory errors %v", errs)
	}
	one, err := c.InventoryDirectory(context.Background(), "single", requests[0])
	if err != nil || InventoryRevision(one) != InventoryRevision(values[0]) {
		t.Fatalf("batch check %+v differs from the single check %+v (%v)", values[0], one, err)
	}
	// Folders of different roots, or too many, are not one batch.
	if _, _, err = c.InventoryPages(context.Background(), "batch", append(requests, InventoryRequest{Root: t.TempDir(), RelativePath: ".", Limit: 64})); err == nil {
		t.Fatal("a batch across roots was accepted")
	}
	if _, _, err = c.InventoryDirectories(context.Background(), "batch", make([]InventoryRequest, MaxInventoryBatch+1)); err == nil {
		t.Fatal("an oversized batch was accepted")
	}
}
