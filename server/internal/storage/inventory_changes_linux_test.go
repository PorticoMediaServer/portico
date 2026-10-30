//go:build linux

package storage

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

func TestInventoryChangesWatchesTheAnchoredInode(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "directory")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	watch, err := watchInventoryChanges(f)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	if watch.Changed() {
		t.Fatal("unchanged directory rejected")
	}
	// No sleep: a mutation must invalidate even inside one timestamp tick.
	if err = os.WriteFile(filepath.Join(path, "entry"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if !watch.Changed() || !watch.Changed() {
		t.Fatal("directory change was not latched")
	}
	fd := watch.fd
	watch.Close()
	if _, err = unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != unix.EBADF {
		t.Fatal("watch descriptor still open", err)
	}
	if !watch.Changed() {
		t.Fatal("closed watch accepted a continuation")
	}

	moved := filepath.Join(root, "moved")
	if err = os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	watch, err = watchInventoryChanges(f)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Close()
	if err = os.WriteFile(filepath.Join(path, "unrelated"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if watch.Changed() {
		t.Fatal("watch followed the replacement pathname")
	}
	if err = os.WriteFile(filepath.Join(moved, "anchored"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if !watch.Changed() {
		t.Fatal("watch lost the anchored inode")
	}
}

func TestRetainedInventoryWatchClosesOnCacheEviction(t *testing.T) {
	clearRetainedNameFixture(t)
	var watches []*inventoryChanges
	for range 33 {
		f, err := os.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		_, _, done, err := retainedInventoryNames(f, "", 1)
		f.Close()
		if err != nil || !done {
			t.Fatal("empty listing", done, err)
		}
		inventoryNameCache.Lock()
		for _, stream := range inventoryNameCache.entries {
			if stream.changes != nil {
				watches = append(watches, stream.changes)
			}
		}
		inventoryNameCache.Unlock()
	}
	closed := false
	for _, watch := range watches {
		if watch.fd == -1 {
			closed = true
		}
	}
	if !closed {
		t.Fatal("eviction retained every watch descriptor")
	}
}

// Exercise the real retained stream repeatedly without timestamp sleeps.
func TestInventoryRetainedNamesSameTickMutations(t *testing.T) {
	for i := range 100 {
		t.Run(strconv.Itoa(i), TestRetainedNamesRejectChangedDirectoryAndLostCursor)
	}
}
