package scan_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

func TestRestartScansAnAddedFolderThatNeverHadItsFirstScan(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "main.db")
	d, err := db.Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if d != nil {
			d.Close()
		}
	}()
	first, added := t.TempDir(), t.TempDir()
	lib, err := d.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{first})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetLibraryOptions(ctx, lib.ID, db.LibraryOptions{Depth: db.DepthBasic, NoWatch: true}); err != nil {
		t.Fatal(err)
	}
	libraries, err := d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&scan.Scanner{DB: d}).Scan(ctx, libraries[0]); err != nil {
		t.Fatal(err)
	}
	paths := []string{first, added}
	if _, _, err := d.EditLibrary(ctx, lib.PublicID, db.LibraryEdit{Folders: &paths}); err != nil {
		t.Fatal(err)
	}
	// Restart after the addition is saved but before any scan of it has started.
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d = nil
	d, err = db.Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	run, stop := context.WithCancel(ctx)
	runner := jobs.New(run, d, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m := scan.NewManager(runner, &scan.Scanner{DB: d})
	done := make(chan struct{})
	go func() { m.Run(run); close(done) }()
	defer func() { stop(); <-done; runner.Wait(ctx) }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		read, err := d.AdminLibraries(ctx, lib.PublicID)
		if err != nil {
			t.Fatal(err)
		}
		if len(read[0].Folders) == 2 && read[0].Folders[1].LastScanAt != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the saved Never folder was forgotten at restart")
		}
	}
}
