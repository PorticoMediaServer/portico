package scan

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
)

type pendingFolder struct {
	Local
	release <-chan struct{}
}

func (s pendingFolder) List(ctx context.Context, rel string) ([]Entry, error) {
	select {
	case <-s.release:
		return s.Local.List(ctx, rel)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestAutomaticScanWaitsForAQueuedFolderScan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	first, second := t.TempDir(), t.TempDir()
	lib, err := d.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetLibraryOptions(ctx, lib.ID, db.LibraryOptions{Depth: db.DepthBasic, RescanMinutes: 15}); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(ScanParams{Folder: &db.RemoteFolder{Path: first}})
	payload, _ := json.Marshal(jobs.Request{Target: jobs.Target{Library: lib.PublicID}, Params: params})
	if _, err := d.StartJob(ctx, db.NewJob{Kind: db.JobScan, Target: lib.PublicID, Payload: string(payload)}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "Heat (1995).mkv"), []byte("film"), 0o644); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	runner := jobs.New(ctx, d, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer func() { cancel(); runner.Wait(context.Background()) }()
	m := NewManager(runner, &Scanner{DB: d, Source: func(root string) Source { return pendingFolder{Local{Root: root}, release} }})
	// The watcher's dispatcher must keep its dirty entry: a queued folder scan is not
	// a queued scan of the whole library. The schedule must likewise keep its request.
	if m.scheduled(ctx, lib.PublicID) {
		t.Fatal("the watcher was told that a queued scan of A covers a change in B")
	}
	m.startDue(ctx, time.Now())
	m.startPending(ctx) // still blocked by the folder-only scan
	close(release)
	runner.Wait(ctx)
	m.startPending(ctx)
	runner.Wait(ctx)
	files, err := d.UnprobedFiles(ctx, lib.ID, 0, 10)
	if err != nil || len(files) != 1 || files[0].Rel != "Heat (1995).mkv" || files[0].Root != second {
		t.Fatalf("the pending scan never walked B: %+v, %v", files, err)
	}
}
