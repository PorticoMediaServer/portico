package tableretention

import (
	"context"
	"fmt"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
	"time"
)

func TestSweepBoundsReceiptsAndPreservesLiveWork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for n := 0; n < 7; n++ {
		if _, err = db.Exec(`INSERT INTO admin_receipts VALUES('server',?,'digest','{}',0)`, fmt.Sprintf("old%d", n)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`UPDATE maintenance_retention_rows SET created_ms=0 WHERE table_name='admin_receipts'`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO admin_receipts VALUES('server','fresh','digest','{}',0)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO prepared_media_jobs(id,item_id,asset_id,library_id,profile_id,target_id,selection_json,source_revision,principal_json,state,phase,created_ms,updated_ms) VALUES('active',1,'a','l','p','t','{}','r','{}','running','copy',0,0),('retired',1,'a','l','p','t','{}','r','{}','failed','failed',0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE maintenance_retention_rows SET created_ms=0 WHERE table_name='prepared_media_jobs'`); err != nil {
		t.Fatal(err)
	}
	more, err := Sweep(context.Background(), db, time.Now(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if !more {
		t.Fatal("backlog not reported")
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM admin_receipts`).Scan(&count); err != nil || count != 5 {
		t.Fatalf("batch count %d: %v", count, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM prepared_media_jobs WHERE id='active'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("active work deleted: %v", err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM prepared_media_jobs WHERE id='retired'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("terminal work retained: %v", err)
	}
	for more {
		more, err = Sweep(context.Background(), db, time.Now(), 3)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = db.QueryRow(`SELECT count(*) FROM admin_receipts`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("fresh receipt not retained: %v", err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM maintenance_retention_rows WHERE table_name='admin_receipts'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("age ledger leaked: %v", err)
	}
}

func TestPermanentBoundKeepsExactReplayAndUpdateAtTheCeiling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`UPDATE maintenance_table_bounds SET maximum=2 WHERE table_name='saved_operation_fences'`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		if _, err = db.Exec(`INSERT INTO saved_operation_fences VALUES('p','personal',?,'hash','now')`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT OR IGNORE INTO saved_operation_fences VALUES('p','personal','one','hash','now')`); err != nil {
		t.Fatalf("exact retry refused: %v", err)
	}
	if _, err = db.Exec(`INSERT INTO saved_operation_fences VALUES('p','personal','three','hash','now')`); err == nil {
		t.Fatal("bound bypassed")
	}
	if _, err = db.Exec(`UPDATE saved_operation_fences SET request_hash='changed' WHERE operation_id='one'`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT rows FROM maintenance_table_bounds WHERE table_name='saved_operation_fences'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("count %d: %v", count, err)
	}
	if _, err = db.Exec(`DELETE FROM saved_operation_fences WHERE operation_id='one'`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO saved_operation_fences VALUES('p','personal','three','hash','now')`); err != nil {
		t.Fatalf("released capacity not usable: %v", err)
	}
	for table, maximum := range ResourceBounds {
		var got int
		if err = db.QueryRow(`SELECT maximum FROM maintenance_table_bounds WHERE table_name=?`, table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if table != "saved_operation_fences" && got != maximum {
			t.Fatalf("%s ceiling %d want %d", table, got, maximum)
		}
	}
}

func TestBackgroundSpeedControlsRetentionQuantum(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for n := 0; n < 130; n++ {
		if _, err = db.Exec(`INSERT INTO admin_receipts VALUES('server',?,'digest','{}',0)`, fmt.Sprintf("old%d", n)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`UPDATE maintenance_retention_rows SET created_ms=0`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE maintenance_settings SET background_speed='gentle' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	more, err := Sweep(context.Background(), db, time.Now(), 256)
	if err != nil || !more {
		t.Fatalf("gentle sweep %v %v", more, err)
	}
	var left int
	if err = db.QueryRow(`SELECT count(*) FROM admin_receipts`).Scan(&left); err != nil || left != 66 {
		t.Fatalf("gentle quantum: %d remaining: %v", left, err)
	}
	if _, err = db.Exec(`UPDATE maintenance_settings SET background_speed='fast' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	_, err = Sweep(context.Background(), db, time.Now(), 256)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM admin_receipts`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("fast quantum: %d remaining: %v", left, err)
	}
}

func TestExpiredGroupHistoryIsDismantledBeforeTheParent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO social_groups(id,name,authority,account_id,profile_id,host_member_id,host_authority,state,created_at_ms,updated_at_ms) VALUES('old','Old','local','a','p','host','host-only','ended',0,0)`); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 10; n++ {
		if _, err = db.Exec(`INSERT INTO social_group_events VALUES('old',?,'ended','{}',0)`, n); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`UPDATE maintenance_retention_rows SET created_ms=0 WHERE table_name='social_groups'`); err != nil {
		t.Fatal(err)
	}
	more, err := Sweep(context.Background(), db, time.Now(), 3)
	if err != nil || !more {
		t.Fatalf("first quantum %v %v", more, err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM social_group_events`).Scan(&count); err != nil || count != 7 {
		t.Fatalf("unbounded cascade: %d events left: %v", count, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM social_groups`).Scan(&count); err != nil || count != 1 {
		t.Fatal("parent removed before bounded child cleanup", err)
	}
	for more {
		more, err = Sweep(context.Background(), db, time.Now(), 3)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = db.QueryRow(`SELECT count(*) FROM social_groups`).Scan(&count); err != nil || count != 0 {
		t.Fatal("retired parent remained", err)
	}
}

func TestBoundAllowsInterningAndIntentReplayThroughNaturalKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"catalog_attribute_fields", "playback_personal_intents"} {
		if _, err = db.Exec(`UPDATE maintenance_table_bounds SET maximum=rows+1 WHERE table_name=?`, table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO catalog_attribute_fields(field) VALUES('example')`); err != nil {
		t.Fatal(err)
	}
	var id int
	if err = db.QueryRow(`INSERT INTO catalog_attribute_fields(field) VALUES('example') ON CONFLICT(field) DO UPDATE SET field=excluded.field RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("existing interned field refused: %v", err)
	}
	if _, err = db.Exec(`INSERT INTO catalog_attribute_fields(field) VALUES('another')`); err == nil {
		t.Fatal("intern bound bypassed")
	}
	if _, err = db.Exec(`INSERT INTO playback_personal_intents(request_key) VALUES('same-play')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT OR IGNORE INTO playback_personal_intents(request_key) VALUES('same-play')`); err != nil {
		t.Fatalf("existing intent refused: %v", err)
	}
	if _, err = db.Exec(`INSERT INTO playback_personal_intents(request_key) VALUES('new-play')`); err == nil {
		t.Fatal("intent bound bypassed")
	}
}
