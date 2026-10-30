package apievents

import (
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestAppendIsTransactionalAndTrimsCanonicalRing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = Append(tx, DeviceAudience("device"), "session.updated", "session", "one", "1", map[string]string{"state": "playing"}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM api_events`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rolled-back event: %d %v", count, err)
	}
	// Give the next append id 20,096 so its in-transaction ring trim removes
	// the old marker without having to write twenty thousand test rows.
	if _, err = db.Exec(`INSERT INTO api_events(id,audience,type,resource_kind,resource_id,revision,data,at_ms) VALUES(1,'admin','operation.updated','operation','old','1','',1),(20095,'admin','operation.updated','operation','recent','1','',1)`); err != nil {
		t.Fatal(err)
	}
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = Append(tx, AdminAudience, "operation.updated", "operation", "new", "2", nil); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM api_events WHERE resource_id='old'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("ring kept old event: %d %v", count, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM api_events WHERE resource_id IN('recent','new')`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("ring discarded recent event: %d %v", count, err)
	}
}
