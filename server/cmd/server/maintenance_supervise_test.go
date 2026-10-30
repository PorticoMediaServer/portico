package main

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

// restoreMaintenanceSeams resets the focused lifecycle knobs after a test.
func restoreMaintenanceSeams(t *testing.T) {
	t.Helper()
	retention, planner := retentionRun, plannerRefresh
	interval := plannerRefreshInterval
	poll, ancillary := housekeepingPollInterval, housekeepingAncillaryInterval
	tickHook := housekeepingTickHook
	t.Cleanup(func() {
		retentionRun, plannerRefresh = retention, planner
		plannerRefreshInterval = interval
		housekeepingPollInterval, housekeepingAncillaryInterval = poll, ancillary
		housekeepingTickHook = tickHook
	})
}

func openMaintenanceDB(t *testing.T) *sql.DB {
	t.Helper()
	state := t.TempDir()
	db, err := persistence.Open(storagepaths.Path(state, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// Retention owns a never-ending timer loop. Before the fix it ran inline, so the
// goroutine below never reached its ticker, planner refresh or checkpoints. This
// test blocks retention and proves the housekeeping body still advances first.
func TestHousekeepingAdvancesWhileRetentionIsBlocked(t *testing.T) {
	db := openMaintenanceDB(t)
	restoreMaintenanceSeams(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	retentionStarted := make(chan struct{})
	retentionStopped := make(chan struct{})
	retentionRun = func(ctx context.Context, db *sql.DB) {
		close(retentionStarted)
		<-ctx.Done()
		close(retentionStopped)
	}
	plannerRefreshInterval = 0
	housekeepingPollInterval = 5 * time.Millisecond
	housekeepingAncillaryInterval = time.Millisecond
	ticked := make(chan struct{}, 1)
	housekeepingTickHook = func() {
		select {
		case ticked <- struct{}{}:
		default:
		}
	}
	refreshed := make(chan struct{}, 1)
	plannerRefresh = func(ctx context.Context, db *sql.DB) error {
		select {
		case refreshed <- struct{}{}:
		default:
		}
		return nil
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		runDatabaseHousekeeping(ctx, db)
	}()

	waitFor(t, retentionStarted, "retention child start")
	waitFor(t, ticked, "housekeeping checkpoint tick")
	waitFor(t, refreshed, "planner refresh tick")

	cancel()
	waitFor(t, done, "housekeeping exit")
	waitFor(t, retentionStopped, "retention child exit")
}

// The shared lifecycle must cancel both independently supervised loop bodies.
func TestHousekeepingCancellationStopsRetention(t *testing.T) {
	db := openMaintenanceDB(t)
	restoreMaintenanceSeams(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	retentionStarted := make(chan struct{})
	retentionStopped := make(chan struct{})
	retentionRun = func(ctx context.Context, db *sql.DB) {
		close(retentionStarted)
		<-ctx.Done()
		close(retentionStopped)
	}
	housekeepingPollInterval = 5 * time.Millisecond
	housekeepingAncillaryInterval = time.Hour
	plannerRefresh = func(ctx context.Context, db *sql.DB) error { return nil }

	done := make(chan struct{})
	go func() {
		defer close(done)
		runDatabaseHousekeeping(ctx, db)
	}()
	waitFor(t, retentionStarted, "retention child start")

	cancel()
	waitFor(t, done, "housekeeping exit")
	waitFor(t, retentionStopped, "retention child exit")
}

// Exercise the real yielding refresh through its caller, not just a timer hook.
// Startup statistics deliberately describe one row; growth must be noticed
// while the same process and its blocked retention child stay alive.
func TestHousekeepingRefreshesGrownTableWithoutRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := openMaintenanceDB(t)
	restoreMaintenanceSeams(t)
	for _, statement := range []string{
		`CREATE TABLE maintenance_growth(id INTEGER PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO maintenance_growth VALUES(1,'original')`,
		`ANALYZE maintenance_growth`,
		`WITH RECURSIVE n(i) AS (SELECT 2 UNION ALL SELECT i+1 FROM n WHERE i<21) INSERT INTO maintenance_growth SELECT i,'grown' FROM n`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	var before string
	if err := db.QueryRow(`SELECT stat FROM sqlite_stat1 WHERE tbl='maintenance_growth'`).Scan(&before); err != nil || before != "1" {
		t.Fatalf("initial statistics = %q, err=%v", before, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	retentionStopped := make(chan struct{})
	retentionRun = func(ctx context.Context, _ *sql.DB) {
		<-ctx.Done()
		close(retentionStopped)
	}
	plannerRefreshInterval = 0
	housekeepingPollInterval = 5 * time.Millisecond
	housekeepingAncillaryInterval = time.Millisecond
	refreshed := make(chan error, 1)
	plannerRefresh = func(ctx context.Context, db *sql.DB) error {
		err := persistence.RefreshPlannerStatistics(ctx, db)
		select {
		case refreshed <- err:
		default:
		}
		return err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runDatabaseHousekeeping(ctx, db)
	}()
	select {
	case err := <-refreshed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("housekeeping did not complete a real planner refresh")
	}
	var after string
	if err := db.QueryRow(`SELECT stat FROM sqlite_stat1 WHERE tbl='maintenance_growth'`).Scan(&after); err != nil || after != "21" {
		t.Fatalf("refreshed statistics = %q, err=%v", after, err)
	}
	cancel()
	waitFor(t, done, "housekeeping exit after refresh")
	waitFor(t, retentionStopped, "retention exit after refresh")
}

func waitFor(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}
