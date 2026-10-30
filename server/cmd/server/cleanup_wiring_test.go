package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestMinuteCleanupWakesForItsInputsAndUnknownWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "cleanup.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO server_settings(singleton) VALUES(1) ON CONFLICT(singleton) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO metadata_owner_history(kind,entity_id,base_revision,revision,trigger,actor,observed_at,before_json,after_json) VALUES('item',1,'before','after','upload','owner','2020-01-01T00:00:00Z','{}','{}')`); err != nil {
		t.Fatal(err)
	}
	wake, stop := minuteCleanupWake()
	defer stop()
	write := func(query string) {
		t.Helper()
		if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
			_, err := tx.Exec(query)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	write(`INSERT INTO telemetry_rollups(bucket_ms,metric,samples,total,status,detail,first_event_ms,last_event_ms) VALUES(0,'cpu',1,1,'available','',0,0)`)
	if wake.Take() {
		t.Fatal("unrelated known telemetry publication woke retention")
	}
	write(`UPDATE server_settings SET updated_ms=updated_ms+1 WHERE singleton=1`)
	if !wake.Take() {
		t.Fatal("changed missing-title settings did not wake retention")
	}
	write(`DELETE FROM metadata_owner_history WHERE kind='item' AND entity_id=1`)
	if !wake.Take() {
		t.Fatal("history-only removal did not wake artwork collection")
	}
	write(`WITH changed(value) AS (SELECT 1) UPDATE telemetry_rollups SET total=total+(SELECT value FROM changed) WHERE bucket_ms=0`)
	if !wake.Take() {
		t.Fatal("unknown write scope lost the conservative retention wake")
	}
	stop()
	write(`UPDATE server_settings SET updated_ms=updated_ms+1 WHERE singleton=1`)
	if wake.Take() {
		t.Fatal("retired retention subscription was woken")
	}
}
