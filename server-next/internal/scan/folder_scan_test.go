package scan_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

func TestOneFolderScanLeavesOtherFoldersUntouched(t *testing.T) {
	f := newFixture(t, db.LibraryMovies)
	first := f.root
	f.write("Heat (1995).mkv")
	second := t.TempDir()
	f.root = second
	f.write("Alien (1979).mkv")
	f.write("Ronin (1998).mkv")
	libraries, err := f.d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{first, second}
	if _, _, err := f.d.EditLibrary(ctx, libraries[0].PublicID, db.LibraryEdit{Folders: &paths}); err != nil {
		t.Fatal(err)
	}
	f.scan()
	if f.probes.calls.Load() != 3 {
		t.Fatal("the initial scan did not read all three files")
	}
	// One folder has a new file; the other has both a new file and a removed file.
	f.write("Arrival (2016).mkv")
	f.remove("Ronin (1998).mkv")
	f.root = first
	f.write("Thief (1981).mkv")
	var selected, other db.LibraryPath
	for _, p := range f.lib.Paths {
		if p.Path == first {
			selected = p
		} else {
			other = p
		}
	}
	h := held{listing: make(chan struct{}), release: make(chan struct{})}
	f.s.Source = func(root string) scan.Source { h.Local = scan.Local{Root: root}; return h }
	runCtx, cancel := context.WithCancel(ctx)
	runner := jobs.New(runCtx, f.d, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { cancel(); runner.Wait(ctx) })
	m := scan.NewManager(runner, f.s)
	job, err := m.ScanFolder(ctx, f.lib.PublicID, 0, true, selected)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.listing:
	case <-time.After(10 * time.Second):
		t.Fatal("the folder scan did not start")
	}
	joined, err := m.ScanFolder(ctx, f.lib.PublicID, 0, true, selected)
	if err != nil || joined.PublicID != job.PublicID {
		t.Fatalf("the same folder did not join: %+v, %v", joined, err)
	}
	if _, err := m.ScanFolder(ctx, f.lib.PublicID, 0, false, other); !errors.Is(err, scan.ErrScanBusy) {
		t.Fatalf("a different folder was falsely acknowledged: %v", err)
	}
	if _, err := m.Scan(ctx, f.lib.PublicID, 0, false); !errors.Is(err, scan.ErrScanBusy) {
		t.Fatalf("a whole-library request joined a narrower scan: %v", err)
	}
	close(h.release)
	runner.Wait(ctx)
	ended, err := f.d.Job(ctx, job.PublicID)
	if err != nil || ended.State != db.JobDone {
		t.Fatalf("folder scan ended %+v, %v", ended, err)
	}
	stats, _ := m.Stats(f.lib.PublicID)
	if stats.Files != 2 || stats.Probed != 2 || f.probes.calls.Load() != 5 {
		t.Fatalf("scan all files escaped its folder: %+v, %d total probes", stats, f.probes.calls.Load())
	}
	got := f.titles(db.KindMovie)
	if len(got) != 4 || got["Thief"].PublicID == "" || got["Arrival"].PublicID != "" || !f.detail(got["Ronin"].PublicID).Available {
		t.Fatalf("the selected folder changed another folder's titles: %v", keys(got))
	}
	// A later whole-library scan still finds the other folder's changes. Alien's old
	// measurements were not cleared by Scan all files on the first folder.
	f.s.Source = nil
	f.scan()
	got = f.titles(db.KindMovie)
	if got["Arrival"].PublicID == "" || f.detail(got["Ronin"].PublicID).Available || f.probes.calls.Load() != 6 {
		t.Fatalf("whole-library rescan: %v, %d total probes", keys(got), f.probes.calls.Load())
	}
}
