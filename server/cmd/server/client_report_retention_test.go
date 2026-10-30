package main

import (
	"context"
	"log"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestAdministrationInitializationPrunesExpiredClientReports(t *testing.T) {
	state := t.TempDir()
	db, err := persistence.Open(storagepaths.Path(state, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ident, err := identity.New(db, state)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, entry := range []struct {
		id string
		at time.Time
	}{{"old", now.Add(-31 * 24 * time.Hour)}, {"current", now}} {
		if _, err = db.Exec(`INSERT INTO client_log_uploads(id,account_id,profile_id,device_id,platform,received_ms,bytes,body) VALUES(?,'account','profile','device','test',?,1,'x')`, entry.id, entry.at.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	// Keep today's quota even when old client evidence is removed.
	day := now.UTC().Truncate(24 * time.Hour).UnixMilli()
	if _, err = db.Exec(`INSERT INTO client_log_daily_usage(account_id,day_ms,bytes) VALUES('account',?,100)`, day); err != nil {
		t.Fatal(err)
	}
	console := operations.New(db)
	document, err := console.Settings(context.Background(), operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	values := document.Requested
	values.LogRetention = []operations.LogRetention{{Category: "client", Days: 30}}
	if _, err = console.ApplySettings(context.Background(), identity.Principal{}, operations.AllowServerScope, operations.SettingsChange{ExpectedRevision: document.Revision, IdempotencyKey: "client-retention-setting", Values: values}); err != nil {
		t.Fatal(err)
	}
	writer := log.Writer()
	_, closeArea := initializeAdministration(context.Background(), db, console, ident, state, 32500)
	defer func() { log.SetOutput(writer); closeArea() }()
	var old, current, used int
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM client_log_uploads WHERE id='old'),(SELECT count(*) FROM client_log_uploads WHERE id='current'),(SELECT bytes FROM client_log_daily_usage WHERE account_id='account' AND day_ms=?)`, day).Scan(&old, &current, &used); err != nil {
		t.Fatal(err)
	}
	if old != 0 || current != 1 || used != 100 {
		t.Fatalf("client retention old=%d current=%d today's budget=%d", old, current, used)
	}
}
