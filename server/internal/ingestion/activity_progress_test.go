package ingestion

import (
	"context"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/scanevents"
	"sync"
	"testing"
	"time"
)

func TestActivityFileIsActualRelativeAndCancellationFenced(t *testing.T) {
	s := &Service{}
	for _, bad := range []string{"/private/movie.mkv", "../movie.mkv", "."} {
		clear := s.activityFile(context.Background(), "job", bad)
		if s.CurrentActivityFile("job") != "" {
			t.Fatal("unsafe relative path published")
		}
		clear()
	}
	ctx, cancel := context.WithCancel(context.Background())
	first := s.activityFile(ctx, "job", "Movies/Film.mkv")
	if s.CurrentActivityFile("job") != "Movies/Film.mkv" {
		t.Fatal("actual file missing")
	}
	cancel()
	if s.CurrentActivityFile("job") != "" {
		t.Fatal("cancelled worker still visible")
	}
	second := s.activityFile(context.Background(), "job", "Movies/Film.mkv")
	first()
	if s.CurrentActivityFile("job") == "" {
		t.Fatal("old cleanup removed replacement observation")
	}
	second()
	if s.CurrentActivityFile("job") != "" {
		t.Fatal("finished file retained")
	}
}
func TestActivityFileConcurrentWorkersAndReads(t *testing.T) {
	s := &Service{}
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				clear := s.activityFile(context.Background(), "job", "Movie.mkv")
				_ = s.CurrentActivityFile("job")
				clear()
			}
		}()
	}
	wg.Wait()
	if s.CurrentActivityFile("job") != "" {
		t.Fatal("file observation leaked after work")
	}
}

// Real in-process observations invalidate the owner summary on the existing
// publisher loop even when no inventory/analysis counter or phase changes.
func TestActivityFileChangesWakeSummaryWithStableCounters(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO libraries(id,name,kind,root) VALUES('lib','Movies','movie','/private-root')`,
		`INSERT INTO jobs(id,library_id,status,created_at) VALUES('job','lib','running','2026-10-22')`,
		`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision,phase,discovered,analyzed) VALUES('job','lib',1,'inc','root',1,'basic_running',10,2)`,
		`INSERT INTO inventory_source_active VALUES('lib','job')`,
	} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	s := &Service{}
	work, cancelWork := context.WithCancel(context.Background())
	defer cancelWork()
	first := s.activityFile(work, "job", "Movies/first-private.mkv")
	defer first()
	p := scanevents.New(db, func(context.Context, string) (catalog.InventoryStatus, error) {
		return catalog.InventoryStatus{LibraryID: "lib", Sources: []catalog.InventorySourceStatus{{ID: "lib", Status: "running", Phase: "basic_running", Discovered: 10, Analyzed: 2}}}, nil
	}, nil, s)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("publisher did not stop")
		}
	}()
	waitHints := func(want int) {
		t.Helper()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
		for {
			var n int
			if e := db.QueryRow(`SELECT count(*) FROM api_events WHERE audience='admin' AND type='job.updated' AND data=''`).Scan(&n); e != nil {
				t.Fatal(e)
			}
			if n >= want {
				if n != want {
					t.Fatalf("extra file hints: %d, want %d", n, want)
				}
				return
			}
			select {
			case <-ticker.C:
			case <-deadline.C:
				t.Fatalf("missing current-file invalidation: %d, want %d", n, want)
			}
		}
	}
	waitHints(1)
	second := s.activityFile(work, "job", "Movies/second-private.mkv")
	defer second()
	first() // The superseded observation must not clear the replacement.
	waitHints(2)
	cancelWork()
	waitHints(3)
	var viewer, leaked int
	if e = db.QueryRow(`SELECT count(*) FROM api_events WHERE audience='library:lib'`).Scan(&viewer); e != nil || viewer != 1 {
		t.Fatal("private observations changed viewer feed", viewer, e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM api_events WHERE data LIKE '%private%' OR resource_id LIKE '%private%' OR revision LIKE '%private%'`).Scan(&leaked); e != nil || leaked != 0 {
		t.Fatal("private observation leaked", leaked, e)
	}
}
