//go:build !windows

package ingestion

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Building a library from nothing must be quick: the time from an empty
// server to every title browsable and searchable, with the real scanner
// (names only, no probing) and the derived worker running beside it as the
// server runs them. Half the files are films, half episodes (twenty to a
// show). It reports when the first title, half the titles and every title
// could be browsed, when the scan finished and when derived work was idle,
// with wall and CPU time.
//
// PORTICO_PERFORMANCE_TIER=release|deep runs it; PORTICO_BUILD_FILES sizes it
// (default 4,000). TMPDIR places the media tree and database;
// PORTICO_BUILD_PROFILE prints the costliest statements.
func TestBuildLibraryFromEmpty(t *testing.T) {
	switch os.Getenv("PORTICO_PERFORMANCE_TIER") {
	case "release", "deep":
	default:
		t.Skip("scale proof: runs with PORTICO_PERFORMANCE_TIER=release or deep")
	}
	files := envCount("PORTICO_BUILD_FILES", 4000)
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := catalog.New(db)
	s := New(db, c, assets.Probe{})
	// Folders are listed through the storage helper, a child process, as the
	// server lists them (PORTICO_BUILD_IN_PROCESS lists them in the test).
	if os.Getenv("PORTICO_BUILD_IN_PROCESS") == "" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		s.SetStorage(storage.New(binary))
	}
	type library struct{ id, root string }
	var libraries []library
	for _, kind := range []string{"movie", "tv"} {
		root, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		lib, err := c.Create(kind, kind, root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`UPDATE library_scan_policies SET tier='file_list_only',operations_json='[]' WHERE library_id=?`, lib.ID); err != nil {
			t.Fatal(err)
		}
		libraries = append(libraries, library{lib.ID, root})
	}
	write := func(root, relative string) {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("media"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	movies, episodes := files/2, files-files/2
	for n := 0; n < movies; n++ {
		year := 1950 + n%70
		write(libraries[0].root, fmt.Sprintf("Film %06d (%d)/Film %06d (%d).mkv", n, year, n, year))
	}
	for n := 0; n < episodes; n++ {
		show, episode := n/20, n%20
		season := episode/10 + 1
		write(libraries[1].root, fmt.Sprintf("Show %05d (2000)/Season %02d/Show %05d - S%02dE%02d.mkv", show, season, show, season, episode%10+1))
	}
	count := func(query string) int {
		var n int
		if err := db.QueryRow(query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if os.Getenv("PORTICO_BUILD_PROFILE") != "" {
		dbwork.ProfileStatements(true)
		defer dbwork.ProfileStatements(false)
	}
	// The server's maintenance loop copies committed pages into the database
	// every second or two; without it the log grows and every commit pays
	// for copying it.
	checkpoints, err := dbwork.NewCheckpointSchedule(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	maintained, stopMaintenance := context.WithCancel(ctx)
	maintenanceDone := make(chan struct{})
	go func() {
		defer close(maintenanceDone)
		ticker := time.NewTicker(dbwork.CheckpointPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-maintained.Done():
				return
			case <-ticker.C:
				checkpoints.Tick(maintained)
			}
		}
	}()
	defer func() { stopMaintenance(); <-maintenanceDone }()
	var cpuStart, helpersStart syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &cpuStart)
	_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &helpersStart)
	start := time.Now()
	scanCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	var (
		mu       sync.Mutex
		scanDone bool
		units    int
		busy     time.Duration
		byDomain = map[int]int{}
		domainMS = map[int]time.Duration{}
		workErr  error
	)
	idle := make(chan struct{})
	go func() {
		defer close(idle)
		w := compactcatalog.NewWorker(db)
		w.PaceToHold(compactcatalog.BatchHoldTarget)
		// PORTICO_BUILD_SCAN_FIRST holds derived work until the scan is done,
		// to read the scan's own cost.
		for os.Getenv("PORTICO_BUILD_SCAN_FIRST") != "" {
			mu.Lock()
			done := scanDone
			mu.Unlock()
			if done {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		for {
			began := time.Now()
			n, err := w.Step(scanCtx, compactcatalog.BatchMax)
			took := time.Since(began)
			if err != nil {
				mu.Lock()
				workErr = err
				mu.Unlock()
				return
			}
			mu.Lock()
			units += n
			busy += took
			if domain, batch, held := w.LastBatch(); n > 0 {
				byDomain[domain] += batch
				domainMS[domain] += held
			}
			done := scanDone
			mu.Unlock()
			if n == 0 {
				if done {
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()
	// Visibility is sampled from the published rows: a title is browsable when
	// its browse row is, searchable when its search document is.
	titles := movies + episodes
	var firstVisible, halfVisible, allVisible, allSearchable time.Duration = -1, -1, -1, -1
	sample := func() {
		elapsed := time.Since(start)
		browsable := count(`SELECT count(*) FROM catalog_browse_rows WHERE item_id IS NOT NULL`)
		if firstVisible < 0 && browsable > 0 {
			firstVisible = elapsed
		}
		if halfVisible < 0 && browsable >= titles/2 {
			halfVisible = elapsed
		}
		if allVisible < 0 && browsable >= titles {
			allVisible = elapsed
		}
		if allSearchable < 0 && count(`SELECT count(*) FROM catalog_search_documents d JOIN catalog_entities e ON e.id=d.entity_id JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1`) >= titles {
			allSearchable = elapsed
		}
	}
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-idle:
				return
			case <-ticker.C:
				sample()
			}
		}
	}()
	// The scanner runs as the server runs it: its own loop, one quantum after
	// another, pacing itself against foreground work.
	scanning, stopScan := context.WithCancel(ctx)
	scanStopped := make(chan struct{})
	go func() { s.Run(scanning); close(scanStopped) }()
	var scanWall time.Duration
	for _, lib := range libraries {
		job, err := s.Queue(lib.id)
		if err != nil {
			t.Fatal(err)
		}
		for waited := 0; ; waited++ {
			time.Sleep(50 * time.Millisecond)
			state, err := s.Get(job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if state.Status == "complete" || state.Status == "complete_with_warnings" {
				break
			}
			if state.Status == "failed" {
				t.Fatalf("scan failed: %+v", state)
			}
			if waited > 40*files {
				t.Fatal("scan did not finish")
			}
		}
	}
	stopScan()
	<-scanStopped
	scanWall = time.Since(start)
	mu.Lock()
	scanDone = true
	mu.Unlock()
	<-idle
	<-sampled
	sample()
	total := time.Since(start)
	var cpuEnd, helpersEnd syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &cpuEnd)
	_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &helpersEnd)
	if workErr != nil {
		t.Fatal(workErr)
	}
	if n := count(`SELECT count(*) FROM catalog_dirty`); n != 0 {
		t.Fatalf("derived work left queued: %d", n)
	}
	if n := count(`SELECT count(*) FROM catalog_browse_rows WHERE item_id IS NOT NULL`); n != titles {
		t.Fatalf("%d titles browsable, want %d", n, titles)
	}
	cpu := func(r syscall.Rusage) time.Duration {
		return time.Duration(r.Utime.Nano() + r.Stime.Nano())
	}
	seconds := func(d time.Duration) float64 { return d.Seconds() }
	for domain, n := range byDomain {
		t.Logf("BUILD_DOMAIN domain=%d units=%d held_s=%.1f", domain, n, domainMS[domain].Seconds())
	}
	for i, entry := range dbwork.StatementProfile() {
		if i == 40 {
			break
		}
		t.Logf("BUILD_COST ms=%.0f calls=%d %.300s", entry.Millis(), entry.Count, entry.Shape)
	}
	t.Logf("BUILD files=%d titles=%d scan_s=%.1f derive_busy_s=%.1f first_visible_s=%.1f half_visible_s=%.1f all_visible_s=%.1f all_searchable_s=%.1f idle_s=%.1f cpu_s=%.1f helper_cpu_s=%.1f derived_units=%d ms_per_file=%.2f",
		files, titles, seconds(scanWall), seconds(busy), seconds(firstVisible), seconds(halfVisible), seconds(allVisible), seconds(allSearchable), seconds(total), seconds(cpu(cpuEnd)-cpu(cpuStart)), seconds(cpu(helpersEnd)-cpu(helpersStart)), units, float64(total)/float64(time.Millisecond)/float64(files))
}
