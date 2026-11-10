package scan

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
)

// A folder that is not watched is due every hour, or as often as the owner set; and a title
// that has been missing for the library's number of days is removed without a scan.
func TestIntervalsAndTheTrashWithoutAScan(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	root := filepath.Join(t.TempDir(), "films")
	film := filepath.Join(root, "Heat (1995).mkv")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{film, filepath.Join(root, "Alien (1979).mkv")} {
		if err := os.WriteFile(name, make([]byte, 100+len(name)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{root}); err != nil {
		t.Fatal(err)
	}
	library := func() db.Library {
		t.Helper()
		libraries, err := d.AllLibraries(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return libraries[0]
	}
	scanner := &Scanner{DB: d}
	m := NewManager(jobs.New(ctx, d, slog.New(slog.NewTextHandler(io.Discard, nil))), scanner)
	if _, err := scanner.Scan(ctx, library()); err != nil {
		t.Fatal(err)
	}
	lib, now := library(), time.Now()
	if m.due(ctx, lib, lib.Paths[0], now.Add(59*time.Minute)) || !m.due(ctx, lib, lib.Paths[0], now.Add(61*time.Minute)) {
		t.Fatal("a folder that is not watched should be due an hour after its last scan")
	}
	lib.Options.RescanMinutes = 15
	if m.due(ctx, lib, lib.Paths[0], now.Add(14*time.Minute)) || !m.due(ctx, lib, lib.Paths[0], now.Add(16*time.Minute)) {
		t.Fatal("the owner's interval should be kept")
	}
	m.watched[lib.PublicID], lib.Options.RescanMinutes = true, 0
	if m.due(ctx, lib, lib.Paths[0], now.Add(48*time.Hour)) {
		t.Fatal("a watched folder needs no interval")
	}

	// A film goes missing. Its 30 days pass with no scan.
	if err := os.Remove(film); err != nil {
		t.Fatal(err)
	}
	if _, err := scanner.Scan(ctx, library()); err != nil {
		t.Fatal(err)
	}
	missing := func() int {
		t.Helper()
		_, total, err := d.MissingItems(ctx, lib.ID, 1)
		if err != nil {
			t.Fatal(err)
		}
		return total
	}
	if m.emptyTrash(ctx, now.AddDate(0, 0, 29)); missing() != 1 {
		t.Fatal("the missing film should wait in the trash for 30 days")
	}
	if m.emptyTrash(ctx, now.AddDate(0, 0, 31)); missing() != 0 {
		t.Fatal("the missing film should be removed when its days are up, without a scan")
	}
}
