package scan_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// storage is remote storage for tests: a folder on disk, reached only by listing and by
// range, which counts its listings and says "slow down" once.
type storage struct {
	root     string
	listings atomic.Int64
	slowed   atomic.Bool
}

func (s *storage) List(_ context.Context, dir string) ([]remote.Entry, error) {
	if s.listings.Add(1) == 2 && !s.slowed.Swap(true) {
		return nil, &remote.SlowDown{After: 20 * time.Millisecond}
	}
	listed, err := os.ReadDir(filepath.Join(s.root, dir))
	if err != nil {
		return nil, err
	}
	var out []remote.Entry
	for _, e := range listed {
		info, _ := e.Info()
		out = append(out, remote.Entry{Name: e.Name(), Dir: e.IsDir(), Size: info.Size(), ModifiedNS: info.ModTime().UnixNano()})
	}
	return out, nil
}

type remoteFile struct{ *os.File }

func (f remoteFile) Size() int64 { info, _ := f.Stat(); return info.Size() }

func (s *storage) Open(_ context.Context, path string) (remote.File, error) {
	f, err := os.Open(filepath.Join(s.root, path))
	return remoteFile{f}, err
}

// reader stands in for the prober of files that are not on a disk here.
type reader struct{ calls atomic.Int64 }

func (r *reader) ProbeReader(_ context.Context, file io.ReaderAt, size int64) (scan.Facts, error) {
	r.calls.Add(1)
	if _, err := file.ReadAt(make([]byte, 1), size-1); err != nil {
		return scan.Facts{}, err
	}
	return scan.Facts{Container: "mkv", DurationMS: 1000, Streams: []db.Stream{{Kind: db.StreamVideo, Codec: "h264"}}}, nil
}

// A folder on remote storage is scanned through its source: listed folder by folder within
// the source's limits, waiting when told to slow down, its files read by range and never by
// path. A scan nobody asked for leaves it alone until its source is due, and a file gone
// from a listing is missing exactly as on a local disk.
func TestAFolderOnRemoteStorage(t *testing.T) {
	f := newFixture(t, db.LibraryMovies)
	f.write("Heat (1995)/Heat (1995).mkv")
	f.write("Alien (1979).mkv")
	store := &storage{root: f.root}
	behind := remote.Throttled(store, remote.NewThrottle(remote.Limits{Connections: 2, PerSecond: 200}))
	reads := &reader{}
	f.s.Remote = func(context.Context, int64) (remote.Storage, time.Duration, error) { return behind, time.Hour, nil }
	f.s.ReaderProber = reads
	scanAs := func(scheduled bool, lastScan time.Time) {
		t.Helper()
		libraries, err := f.d.AllLibraries(ctx)
		if err != nil {
			t.Fatal(err)
		}
		f.lib = libraries[0]
		// The folder is on remote source 1, with the path inside that source.
		f.lib.Paths[0].RemoteID, f.lib.Paths[0].Path = 1, ""
		if !lastScan.IsZero() {
			f.lib.Paths[0].LastScanAt = lastScan.UnixMilli()
		}
		run := *f.s
		run.Scheduled = scheduled
		if _, err := run.Scan(ctx, f.lib); err != nil {
			t.Fatal(err)
		}
	}

	scanAs(false, time.Time{})
	if !store.slowed.Load() || reads.calls.Load() != 2 || f.probes.calls.Load() != 0 {
		t.Fatalf("slowed %v, %d files read by range, %d by path", store.slowed.Load(), reads.calls.Load(), f.probes.calls.Load())
	}
	if !f.detail(f.film("Heat").PublicID).Available || !f.detail(f.film("Alien").PublicID).Available {
		t.Fatal("the films on remote storage should be in the library")
	}

	// Scanned a minute ago and due every hour: a scan by itself asks the source nothing.
	f.remove("Alien (1979).mkv")
	listings := store.listings.Load()
	scanAs(true, time.Now().Add(-time.Minute))
	if store.listings.Load() != listings || !f.detail(f.film("Alien").PublicID).Available {
		t.Fatal("a scan that was not due listed the source or marked a file missing")
	}
	// Due again: listed, nothing unchanged is read again, and the file that is gone is missing.
	scanAs(true, time.Now().Add(-2*time.Hour))
	if store.listings.Load() == listings || reads.calls.Load() != 2 || f.detail(f.film("Alien").PublicID).Available {
		t.Fatal("a due scan should list the source, read nothing again and mark the removed film missing")
	}
}
