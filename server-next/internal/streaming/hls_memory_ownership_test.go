package streaming

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMemoryOwnershipRefusesUnownedMatchingDirectories(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "no disk duplicate", true: "disk duplicate"}[duplicate], func(t *testing.T) {
			h := newSweepFixture(t.TempDir())
			h.memoryCheck = func(string) error { return nil }
			memory := t.TempDir()
			writeSessionOutput(t, memory, "customer-files", 1)
			if duplicate {
				writeSessionOutput(t, h.root, "customer-files", 1)
			}
			if err := h.ConfigureMemory(memory); err == nil {
				t.Fatal("adopted an unowned tree")
			}
			if !segmentReady(filepath.Join(memory, "customer-files", hlsSegmentFile(0))) {
				t.Fatal("deleted unrelated matching directory")
			}
			if _, err := os.Lstat(filepath.Join(memory, memoryOwnershipMarker)); !os.IsNotExist(err) {
				t.Fatal("minted ownership over existing content", err)
			}
			if h.memoryOutputRoot != nil || len(h.inMemory) > 0 {
				t.Fatal("installed unowned root")
			}
		})
	}
}

func TestMemoryOwnershipRefusesOtherInstallationAndLinkedParents(t *testing.T) {
	h := memoryFixture(t)
	source := writeMemoryOutput(t, h, "owned")
	other := newSweepFixture(t.TempDir())
	other.memoryCheck = func(string) error { return nil }
	if err := other.ConfigureMemory(h.memoryRoot); err == nil {
		t.Fatal("adopted another installation's output")
	}
	if !segmentReady(filepath.Join(source, hlsSegmentFile(0))) {
		t.Fatal("changed another installation's output")
	}
	linked := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(h.memoryRoot, linked); err != nil {
		t.Fatal(err)
	}
	if err := other.ConfigureMemory(filepath.Join(linked, "child")); err == nil {
		t.Fatal("accepted linked parent")
	}
	if _, err := os.Lstat(filepath.Join(h.memoryRoot, "child")); !os.IsNotExist(err) {
		t.Fatal("created output through link", err)
	}
}

func TestMemoryOwnershipRefusesSharedFilesystemRoot(t *testing.T) {
	if _, err := os.Stat("/dev/shm"); err != nil {
		t.Skip("Linux shared memory unavailable")
	}
	h := newSweepFixture(t.TempDir())
	h.memoryCheck = func(string) error { return nil }
	if err := h.ConfigureMemory("/dev/shm"); err == nil {
		t.Fatal("accepted shared filesystem root")
	}
}
