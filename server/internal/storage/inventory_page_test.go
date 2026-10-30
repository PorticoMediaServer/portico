package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestInventoryPagesReplayAndRejectChangedDirectory(t *testing.T) {
	// Portable cursor replay belongs to the retained public producer; direct
	// helper calls with an empty cursor intentionally create a new stream.
	pageFor := LocalInventoryPage
	if runtime.GOOS != "linux" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		client := New(binary)
		client.SourceOperations = NewOperationScope()
		t.Cleanup(func() {
			client.SourceOperations.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := client.SourceOperations.Wait(ctx); err != nil {
				t.Error(err)
			}
		})
		pageFor = func(r InventoryRequest) (InventoryPage, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for {
				p, err := client.InventoryPage(ctx, "replay-fixture", r)
				if !errors.Is(err, ErrBusy) {
					return p, err
				}
			}
		}
	}
	root := t.TempDir()
	for i := 0; i < 137; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("%03d.mp4", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	request := InventoryRequest{Root: root, RelativePath: ".", Limit: 17}
	first, err := pageFor(request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Complete || first.NextCursor == "" || len(first.Entries) != 17 {
		t.Fatal(first)
	}
	request.RootIdentity, request.DirectoryRevision = first.RootIdentity, first.DirectoryRevision
	again, err := pageFor(request)
	if err != nil || !reflect.DeepEqual(first, again) {
		t.Fatal("replaying an uncommitted page changed it", err)
	}
	seen := map[string]bool{}
	page := first
	for turns := 0; ; turns++ {
		if turns > 20 {
			t.Fatal("cursor did not terminate")
		}
		for _, entry := range page.Entries {
			if seen[entry.Name] {
				t.Fatal("duplicate", entry.Name)
			}
			seen[entry.Name] = true
		}
		if page.Complete {
			break
		}
		request.Cursor = page.NextCursor
		page, err = pageFor(request)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 137 {
		t.Fatal("lost page entries", len(seen))
	}
	// Linux before 6.13 stamps directories from the coarse clock (one jiffy,
	// up to 10 ms): on a fast host the whole walk and the new file can share a
	// tick, leaving mtime, ctime and size unchanged. Step past it so the
	// directory really has a new revision to reject.
	time.Sleep(20 * time.Millisecond)
	if err = os.WriteFile(filepath.Join(root, "new.mp4"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	request.Cursor = first.NextCursor
	if _, err = pageFor(request); !errors.Is(err, ErrInventoryChanged) {
		t.Fatal("changed directory admitted stale continuation", err)
	}
}

func TestInventoryRootReplacementAndEscapingSymlinkFailClosed(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "media")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	request := InventoryRequest{Root: root, RelativePath: ".", Limit: 128}
	original, err := LocalInventoryPage(request)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	request.RootIdentity = original.RootIdentity
	if _, err = LocalInventoryPage(request); !errors.Is(err, ErrInventoryChanged) {
		t.Fatal("replacement root admitted", err)
	}
	outside := filepath.Join(parent, "outside.mp4")
	if err = os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(root, "escape.mp4")); err != nil {
		t.Fatal(err)
	}
	request.RootIdentity = ""
	page, err := LocalInventoryPage(request)
	if err != nil || len(page.Entries) != 0 {
		t.Fatal("disabled symlink was inventoried", err)
	}
	request.FollowSymlinks = true
	page, err = LocalInventoryPage(request)
	if err != nil || len(page.Entries) != 0 || len(page.Issues) != 1 || page.Issues[0].Reason != "symlink_outside_root" || !page.Issues[0].Protect {
		t.Fatal("escaping symlink was admitted or not diagnosed", page, err)
	}
}
