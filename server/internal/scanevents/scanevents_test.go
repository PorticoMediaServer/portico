package scanevents

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

type scanEventRow struct {
	id           int64
	audience     string
	typ          string
	resourceKind string
	resourceID   string
	revision     string
	data         map[string]any
}

func readScanEvents(t *testing.T, db *sql.DB) []scanEventRow {
	t.Helper()
	rows, err := db.Query(`SELECT id,audience,type,resource_kind,resource_id,revision,data FROM api_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []scanEventRow
	for rows.Next() {
		var r scanEventRow
		var raw string
		if err := rows.Scan(&r.id, &r.audience, &r.typ, &r.resourceKind, &r.resourceID, &r.revision, &raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(raw), &r.data); err != nil {
			t.Fatalf("event data is not JSON: %q: %v", raw, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func setupScanLibrary(t *testing.T, db *sql.DB, lib, src, job string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES(?,?,?,?)`, lib, lib, "movie", "/"+lib); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,?,'running','2026-09-24T00:00:00Z')`, job, lib); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO library_sources(id,library_id,configured_root,root) VALUES(?,?,?,?)`, src, lib, "/"+src, "/"+src); err != nil {
		t.Fatal(err)
	}
}

func insertActive(t *testing.T, db *sql.DB, src, job string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO inventory_source_active(source_id,job_id) VALUES(?,?)`, src, job); err != nil {
		t.Fatal(err)
	}
}

func deleteActive(t *testing.T, db *sql.DB, src string) {
	t.Helper()
	if _, err := db.Exec(`DELETE FROM inventory_source_active WHERE source_id=?`, src); err != nil {
		t.Fatal(err)
	}
}

func statusWith(sources ...catalog.InventorySourceStatus) catalog.InventoryStatus {
	return catalog.InventoryStatus{LibraryID: "lib", Sources: sources}
}

func TestScanEventsStartedProgressFinished(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	setupScanLibrary(t, db, "lib1", "src1", "job1")
	insertActive(t, db, "src1", "job1")
	var found atomic.Int64
	var scanning atomic.Bool
	found.Store(100)
	scanning.Store(true)
	var calls atomic.Int64
	status := func(ctx context.Context, library string) (catalog.InventoryStatus, error) {
		calls.Add(1)
		if library != "lib1" {
			t.Fatalf("unexpected library %q", library)
		}
		st := "completed"
		if scanning.Load() {
			st = "running"
		}
		return statusWith(catalog.InventorySourceStatus{ID: "src1", Status: st, Discovered: found.Load()}), nil
	}
	p := New(db, status, nil)
	base := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	if _, err := p.tick(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	events := readScanEvents(t, db)
	if len(events) != 1 {
		t.Fatalf("started events: %+v", events)
	}
	if events[0].audience != "library:lib1" || events[0].typ != "library.scan.updated" || events[0].resourceKind != "library" || events[0].resourceID != "lib1" {
		t.Fatalf("started envelope: %+v", events[0])
	}
	if events[0].data["state"] != "started" || int64(events[0].data["found"].(float64)) != 100 || len(events[0].data) != 2 {
		t.Fatalf("started data: %+v", events[0].data)
	}
	// Two found changes within one second: no event.
	found.Store(120)
	if _, err := p.tick(context.Background(), base.Add(100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	found.Store(140)
	if _, err := p.tick(context.Background(), base.Add(200*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if got := readScanEvents(t, db); len(got) != 1 {
		t.Fatalf("throttled changes emitted: %+v", got)
	}
	// At one second: exactly one progress with the latest count.
	if _, err := p.tick(context.Background(), base.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	events = readScanEvents(t, db)
	if len(events) != 2 {
		t.Fatalf("progress events: %+v", events)
	}
	if events[1].data["state"] != "progress" || int64(events[1].data["found"].(float64)) != 140 || len(events[1].data) != 2 {
		t.Fatalf("progress data: %+v", events[1].data)
	}
	// End the scan: one finished.
	deleteActive(t, db, "src1")
	scanning.Store(false)
	if _, err := p.tick(context.Background(), base.Add(1100*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	events = readScanEvents(t, db)
	if len(events) != 3 {
		t.Fatalf("finished events: %+v", events)
	}
	if events[2].data["state"] != "finished" || len(events[2].data) != 2 {
		t.Fatalf("finished data: %+v", events[2].data)
	}
	if events[2].audience != "library:lib1" {
		t.Fatalf("finished audience: %+v", events[2])
	}
}

func TestScanEventsNoWorkWhileIdle(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var calls atomic.Int64
	status := func(ctx context.Context, library string) (catalog.InventoryStatus, error) {
		calls.Add(1)
		return catalog.InventoryStatus{}, nil
	}
	p := New(db, status, nil)
	base := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		if _, err := p.tick(context.Background(), base.Add(time.Duration(i*15)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	// A simulated minute with nothing scanning: no status calls, no events.
	if _, err := p.tick(context.Background(), base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatalf("idle loop called status %d times", calls.Load())
	}
	if got := readScanEvents(t, db); len(got) != 0 {
		t.Fatalf("idle loop wrote events: %+v", got)
	}
}

func TestScanEventsRestartRepublishesStarted(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	setupScanLibrary(t, db, "lib1", "src1", "job1")
	insertActive(t, db, "src1", "job1")
	status := func(ctx context.Context, library string) (catalog.InventoryStatus, error) {
		return statusWith(catalog.InventorySourceStatus{ID: "src1", Status: "running", Discovered: 7}), nil
	}
	p := New(db, status, nil)
	base := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	if _, err := p.tick(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	events := readScanEvents(t, db)
	if len(events) != 1 || events[0].data["state"] != "started" {
		t.Fatalf("restart started: %+v", events)
	}
	if _, err := p.tick(context.Background(), base.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := readScanEvents(t, db); len(got) != 1 {
		t.Fatalf("restart emitted started more than once: %+v", got)
	}
}

func TestScanEventsRevisionIncreases(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	setupScanLibrary(t, db, "lib1", "src1", "job1")
	insertActive(t, db, "src1", "job1")
	var found atomic.Int64
	var scanning atomic.Bool
	found.Store(10)
	scanning.Store(true)
	status := func(ctx context.Context, library string) (catalog.InventoryStatus, error) {
		st := "completed"
		if scanning.Load() {
			st = "running"
		}
		return statusWith(catalog.InventorySourceStatus{ID: "src1", Status: st, Discovered: found.Load()}), nil
	}
	p := New(db, status, nil)
	when := time.Date(2026, 9, 24, 12, 0, 0, 123*1000*1000, time.UTC)
	if _, err := p.tick(context.Background(), when); err != nil {
		t.Fatal(err)
	}
	deleteActive(t, db, "src1")
	scanning.Store(false)
	if _, err := p.tick(context.Background(), when); err != nil {
		t.Fatal(err)
	}
	events := readScanEvents(t, db)
	if len(events) != 2 {
		t.Fatalf("revision events: %+v", events)
	}
	first, err := strconv.ParseInt(events[0].revision, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	second, err := strconv.ParseInt(events[1].revision, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if second <= first {
		t.Fatalf("revisions did not increase in the same millisecond: %q then %q", events[0].revision, events[1].revision)
	}
}
