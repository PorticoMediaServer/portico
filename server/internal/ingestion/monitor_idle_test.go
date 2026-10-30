package ingestion

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestMonitorConfigurationWaitsForActualSourceChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newLoopFixture(t, "movie")
	if _, err := f.db.Exec(`UPDATE library_sources SET enabled=0 WHERE id=?`, f.source); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	dbwork.ProfileStatements(true)
	defer dbwork.ProfileStatements(false)
	go func() { defer close(done); f.s.runMonitor(ctx) }()
	defer func() { cancel(); <-done }()
	reads := func() int64 {
		var n int64
		for _, p := range dbwork.StatementProfile() {
			if strings.HasPrefix(p.Shape, "SELECT s.id,s.library_id,s.root,s.root_identity") {
				n += p.Count
			}
		}
		return n
	}
	await := func(n int64) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for reads() < n && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if reads() != n {
			t.Fatalf("configuration reads=%d, want %d", reads(), n)
		}
	}
	await(1)
	time.Sleep(5200 * time.Millisecond)
	if n := reads(); n != 1 {
		t.Fatalf("idle monitor polled configuration: %d", n)
	}
	write := func(query string, args ...any) {
		t.Helper()
		if err := dbwork.WithWriteTx(ctx, f.db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
			statement, err := tx.PrepareContext(ctx, query)
			if err != nil {
				return err
			}
			defer statement.Close()
			_, err = statement.ExecContext(ctx, args...)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	write(`INSERT INTO telemetry_rollups(bucket_ms,metric,samples,total,status,detail,first_event_ms,last_event_ms) VALUES(0,'cpu',1,1,'available','',0,0)`)
	time.Sleep(1100 * time.Millisecond)
	if n := reads(); n != 1 {
		t.Fatalf("telemetry refreshed source configuration: %d", n)
	}
	write(`UPDATE library_sources SET generation=generation+1 WHERE id=?`, f.source)
	await(2)
	if wait := f.s.nextScheduledScan(ctx); wait != 0 {
		t.Fatalf("disabled source fabricated scheduled work: %v", wait)
	}
}
