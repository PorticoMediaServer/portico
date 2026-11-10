package scan_test

import (
	"io"
	"log/slog"
	"runtime"
	"testing"
	"time"

	"context"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/jobs"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// With the schedule running, a library is scanned when the server starts, and a film put into
// one of its folders afterwards, also into a folder made afterwards, turns up by itself.
func TestAWatchedFolderIsScannedWhenItChanges(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("folders are watched on Linux only")
	}
	f := newFixture(t, db.LibraryMovies)
	f.write("Heat (1995).mkv")
	run, stop := context.WithCancel(ctx)
	defer stop()
	runner := jobs.New(run, f.d, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m := scan.NewManager(runner, f.s)
	go m.Run(run)
	appears := func(title string) {
		t.Helper()
		for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(100 * time.Millisecond) {
			libraries, err := f.d.AllLibraries(ctx)
			if err != nil {
				t.Fatal(err)
			}
			f.lib = libraries[0]
			if _, ok := f.titles(db.KindMovie)[title]; ok {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s did not turn up by itself; have %v", title, keys(f.titles(db.KindMovie)))
			}
		}
	}
	appears("Heat")
	f.write("Alien (1979).mkv")
	appears("Alien")
	f.write("Ronin (1998)/Ronin (1998).mkv")
	appears("Ronin")
	// The folder made since is watched too.
	f.write("Ronin (1998)/Ronin (1998) - 4K.mkv")
	for deadline := time.Now().Add(30 * time.Second); len(f.detail(f.film("Ronin").PublicID).Versions) != 2; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("a file put into a folder made after the server started was not picked up: %v, %+v", keys(f.titles(db.KindMovie)), f.detail(f.film("Ronin").PublicID).Versions)
		}
	}
}
