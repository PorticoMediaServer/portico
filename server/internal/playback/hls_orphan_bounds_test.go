package playback

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestOrphanSweepDoesNotAllocateForTheWholeDirectory(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Recent directories require no database lookups, isolating directory
	// enumeration. A large backlog must not allocate a listing of all of them.
	for i := range 2000 {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("session-%06d", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	h := &HLS{db: db, root: root}
	defer h.closeOrphanDirectory()
	allocations := testing.AllocsPerRun(1, func() { h.sweepOrphanDirectories(context.Background()) })
	if allocations > 2000 {
		t.Fatalf("one bounded orphan checkpoint allocated %.0f objects for a 2000-directory backlog", allocations)
	}
}

func TestOrphanSweepResumesAndPreservesOwnedAndRecentDirectories(t *testing.T) {
	_, h, owned := conversionOutputFixture(t)
	defer h.closeOrphanDirectory()
	old := time.Now().Add(-time.Hour)
	for i := range 300 {
		path := filepath.Join(h.root, fmt.Sprintf("orphan-%06d", i))
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	recent := filepath.Join(h.root, "recent")
	if err := os.Mkdir(recent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(h.root, owned), old, old); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		h.sweepOrphanDirectories(context.Background())
	}
	entries, err := os.ReadDir(h.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("checkpoint cursor stranded or removed directories: %v", entries)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatal("recent directory removed", err)
	}
	if _, err := os.Stat(filepath.Join(h.root, owned)); err != nil {
		t.Fatal("owned directory removed", err)
	}
	// Closing a partly consumed scan must release its directory descriptor.
	h.nextOrphanEntries(context.Background())
	if err := h.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.orphanDir != nil {
		t.Fatal("shutdown retained the directory scan")
	}
}
