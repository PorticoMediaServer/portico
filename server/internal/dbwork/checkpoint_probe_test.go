package dbwork

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"
)

func TestCheckpointCopyFenceIgnoresReadOnlyReadinessProbes(t *testing.T) {
	realDB := readerFixture(t)
	ctx := context.Background()
	var copied sync.Once
	conn := &checkpointCleanupConn{busy: 3210, checkpoint: func() {
		// These use the real SQLite writer and interactive-priority gate, as
		// repeated public readiness requests do during a slow copy pass.
		copied.Do(func() {
			for range 10 {
				probeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
				err := CheckWriteReady(probeCtx, realDB)
				cancel()
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}}
	db := sql.OpenDB(checkpointCleanupConnector{conn})
	defer db.Close()
	result := checkpoint(ctx, db, schedulePressureFile(t))
	if result.Err != nil || result.Mode != "truncate" {
		t.Fatalf("read-only readiness probes invalidated copied WAL: %+v", result)
	}
}

func TestHardPressureDefersBeforeDrainingReadersForBusyWriter(t *testing.T) {
	db := readerFixture(t)
	ctx := context.Background()
	_, finishRead, err := BeginRead(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer finishRead()
	held, err := Begin(ctx, db, ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	result := checkpoint(short, db, schedulePressureFile(t))
	if result.Err != nil || result.DrainMs != 0 || result.BackoffMs != 0 {
		t.Fatalf("busy writer caused an unnecessary reader drain/failure backoff: %+v", result)
	}
	if scope := readerScopeForTest(t, db); scope.stats().AdmissionPaused || scope.pressureBackoff() != 0 {
		t.Fatal("normal writer deferral changed reader admission or its failure policy")
	}
}
