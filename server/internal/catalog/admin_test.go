package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"testing"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--portico-storage-helper" {
		if storage.Helper(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestAdminDirectoriesCASRestartAndProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	path := filepath.Join(t.TempDir(), "db")
	db, e := persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := New(db)
	root := t.TempDir()
	c := catalogtest.New(t, db)
	c.Library("a", "Library A", "movie", root)
	c.Library("b", "Library B", "tv", "/private-unavailable-source")
	for i := 0; i < 45; i++ {
		_, e = db.Exec(`INSERT INTO jobs(id,library_id,status,processed,created_at) VALUES(?,'a','complete',?,'2026-09-04T00:00:00Z')`, fmt.Sprintf("job%03d", i), i)
		if e != nil {
			t.Fatal(e)
		}
	}
	r := AdminRequest{ServerID: "server", Profile: "p", ViewerFence: "f", Limit: 1}
	out, e := s.AdminLibraries(context.Background(), r)
	if e != nil || len(out.Items) != 1 || out.Items[0].Source.Path != root || out.Items[0].Source.Availability != "unknown" || out.Items[0].LastScan.ID != "job044" || out.NextCursor == "" {
		t.Fatal(out, e)
	}
	r.Cursor = out.NextCursor
	out, e = s.AdminLibraries(context.Background(), r)
	if e != nil || len(out.Items) != 1 || out.Items[0].ID != "b" {
		t.Fatal(out, e)
	}
	r.Cursor = ""
	r.LibraryID = "a"
	r.Limit = 40
	jobs, e := s.AdminJobs(context.Background(), r)
	if e != nil || len(jobs.Items) != 40 || jobs.NextCursor == "" {
		t.Fatal(jobs, e)
	}
	r.Cursor = jobs.NextCursor
	next, e := s.AdminJobs(context.Background(), r)
	if e != nil || len(next.Items) != 5 || next.Items[0].ID != "job004" {
		t.Fatal(next, e)
	}
	if _, e = db.Exec(`UPDATE jobs SET status='cancelled' WHERE id='job044'`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AdminJobs(context.Background(), r); e != nil {
		t.Fatal("directory continuation did not refresh", e)
	}
	allow := func(*sql.Tx) error { return nil }
	if e = s.RenameLibrary(context.Background(), "a", "Renamed", 1, allow); e != nil {
		t.Fatal(e)
	}
	if e = s.RenameLibrary(context.Background(), "a", "Late", 1, allow); !errors.Is(e, ErrLibraryConfigurationConflict) {
		t.Fatal("CAS", e)
	}
	if e = s.RenameLibrary(context.Background(), "a", "Renamed", 2, allow); e != nil {
		t.Fatal("same set", e)
	}
	denied := errors.New("revoked")
	if e = s.RenameLibrary(context.Background(), "a", "Denied", 2, func(*sql.Tx) error { return denied }); !errors.Is(e, denied) {
		t.Fatal("transaction auth", e)
	}
	db.Close()
	db, e = persistence.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s = New(db)
	r.Cursor = ""
	r.Limit = 1
	l, e := s.AdminLibrary(context.Background(), r, true)
	if e != nil || l.Name != "Renamed" || l.Revision != 2 || l.LastScan.Status != "cancelled" || l.LastScan.FinishedAt == nil || l.Source.Availability != "unknown" {
		t.Fatal("restart", l, e)
	}
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	s.SetStorage(storage.New(binary))
	l, e = s.AdminLibrary(context.Background(), r, true)
	if e != nil || l.Source.Availability != "available" || l.Source.ObservedAt == "" {
		t.Fatal("root probe", l, e)
	}
	r.LibraryID = "b"
	l, e = s.AdminLibrary(context.Background(), r, true)
	if e != nil || l.Source.Availability != "unavailable" || l.Source.Message == "" {
		t.Fatal("failed probe", l, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = s.AdminJobs(ctx, r); !errors.Is(e, context.Canceled) {
		t.Fatal("cancelled read", e)
	}
}

// NEW-38: a scan updates its job row continuously, and every update moves the
// admin revision. The owner's first pages (the library list, one library, its
// jobs) are read in one snapshot and never fail because of it; only a cursor
// from an older revision is stale (TestAdminDirectoriesCASRestartAndProbe).
func TestAdminFirstPagesNeverFailDuringAScan(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := New(db)
	c := catalogtest.New(t, db)
	c.Library("tv", "TV", "tv", t.TempDir())
	if _, e = db.Exec(`INSERT INTO jobs(id,library_id,status,processed,created_at) VALUES('scan','tv','running',0,'2026-09-24T00:00:00Z')`); e != nil {
		t.Fatal(e)
	}
	stop := make(chan struct{})
	scanned := make(chan error, 1)
	go func() {
		defer close(scanned)
		for n := 1; ; n++ {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := db.Exec(`UPDATE jobs SET processed=? WHERE id='scan'`, n); err != nil {
				scanned <- err
				return
			}
		}
	}()
	r := AdminRequest{ServerID: "server", Profile: "p", ViewerFence: "f", Limit: 40}
	one := r
	one.LibraryID = "tv"
	for i := 0; i < 300; i++ {
		if _, e = s.AdminLibraries(context.Background(), r); e != nil {
			break
		}
		if _, e = s.AdminLibrary(context.Background(), one, false); e != nil {
			break
		}
		if _, e = s.AdminJobs(context.Background(), one); e != nil {
			break
		}
	}
	close(stop)
	if err := <-scanned; err != nil {
		t.Fatal(err)
	}
	if e != nil {
		t.Fatalf("a first page failed during a scan: %v", e)
	}
	var processed int
	if e = db.QueryRow(`SELECT processed FROM jobs WHERE id='scan'`).Scan(&processed); e != nil || processed < 10 {
		t.Fatalf("the scan barely wrote (%d, %v): the test proves nothing", processed, e)
	}
}

// The owner's library view says how many files need attention (files the
// library couldn't turn into items), and says nothing when none do.
func TestAdminLibraryCountsFilesThatNeedAttention(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	s := New(db)
	c := catalogtest.New(t, db)
	c.Library("tv", "TV", "tv", "/tv")
	c.Library("films", "Films", "movie", "/films")
	for _, a := range []string{"a", "b"} {
		var token string
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			_, value, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: "/tv/" + a + ".mkv", Size: 1, ModifiedNS: 1, Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 60})
			token = value
			return err
		})
		if _, e = db.Exec(`INSERT INTO episodic_sources(library_id,asset_id,manual,issue,source_name) VALUES('tv',?,0,'multipart_episode_requires_assignment',?)`, token, a); e != nil {
			t.Fatal(e)
		}
	}
	c.Drain()
	out, e := s.AdminLibraries(context.Background(), AdminRequest{ServerID: "s", Profile: "p", ViewerFence: "f", Limit: 40})
	if e != nil || len(out.Items) != 2 {
		t.Fatal(out, e)
	}
	for _, l := range out.Items {
		switch l.ID {
		case "tv":
			if l.Attention == nil || l.Attention.Files != 2 || l.Attention.AtLeast {
				t.Fatalf("tv attention %+v", l.Attention)
			}
		case "films":
			if l.Attention != nil {
				t.Fatalf("films attention %+v", l.Attention)
			}
		}
	}
}
