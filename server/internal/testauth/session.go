// Package testauth installs explicit test credentials in the same device-bound
// authorization tables used by the server. It is for fixtures that exercise a
// subsystem without constructing the full identity service.
package testauth

import (
	"database/sql"
	"testing"
)

func InsertSession(t testing.TB, db *sql.DB, hash, account, profile, authority, role, expiry string) {
	t.Helper()
	var server string
	if err := db.QueryRow(`SELECT value FROM configuration WHERE key='id'`).Scan(&server); err != nil || server == "" {
		server = "fixture-server"
	}
	device, family := "fixture-device-"+hash, "fixture-family-"+hash
	for _, step := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO identity_devices(id,authority,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES(?,?,?,?,?,'test','portico','1','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, []any{device, authority, account, device, "Fixture"}},
		{`INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation) VALUES(?,?,?,?,?,?,1,?,1)`, []any{family, server, account, profile, authority, role, expiry}},
		{`INSERT INTO identity_device_families(family_id,device_id) VALUES(?,?)`, []any{family, device}},
		{`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES(?,?,1,?)`, []any{hash, family, expiry}},
	} {
		if _, err := db.Exec(step.query, step.args...); err != nil {
			t.Fatalf("install test session: %v", err)
		}
	}
}
