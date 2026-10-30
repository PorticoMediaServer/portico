//go:build linux || darwin

package playbackdisc

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// These normalized rows exercise stored presentation currentness only. The
// plain fixture checker is not authentication or a production family grant.
func seedDiscFamilyContinuation(t *testing.T, f *discFixture, snap Snapshot, nextHash string) {
	t.Helper()
	until := time.UnixMilli(f.until).UTC().Format(time.RFC3339Nano)
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,revoked) VALUES(?,?,?,?,?,'member',?,?,?,0)`, []any{snap.Actor.FamilyID, snap.Actor.ServerID, snap.Actor.AccountID, snap.Actor.ProfileID, snap.Actor.Authority, snap.Actor.Epoch, until, snap.Actor.TokenGeneration + 1}},
		{`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at,retired) VALUES(?,?,?,?,1)`, []any{snap.Actor.SessionHash, snap.Actor.FamilyID, snap.Actor.TokenGeneration, until}},
		{`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at,retired) VALUES(?,?,?,?,0)`, []any{nextHash, snap.Actor.FamilyID, snap.Actor.TokenGeneration + 1, until}},
		{`INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES(?,?,'disc-fixture-installation','Fixture','test','test','test',?,?)`, []any{snap.Actor.DeviceID, snap.Actor.AccountID, until, until}},
		{`INSERT INTO identity_device_families(family_id,device_id) VALUES(?,?)`, []any{snap.Actor.FamilyID, snap.Actor.DeviceID}},
		{`UPDATE playback_sessions SET session_hash=? WHERE id=?`, []any{nextHash, snap.SessionID}},
	}
	for _, statement := range statements {
		if err := f.exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiscActiveReaderKeepsItsOriginalSnapshotAcrossSameFamilyRotation(t *testing.T) {
	f := newDiscFixture(t)
	snap := f.snapshot
	snap.Actor.Authority = "local"
	if err := f.publish(snap, plainCheck()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.activate(snap.key(), snap.ID, plainCheck()); err != nil {
		t.Fatal(err)
	}
	before := mustInt(t, f.db, `SELECT expires_ms FROM playback_disc_lifecycle WHERE snapshot_id=?`, snap.ID)
	seedDiscFamilyContinuation(t, f, snap, "rotated-disc-token")
	got, err := f.resolve(snap.key(), snap.ID, plainCheck())
	if err != nil || got.Actor != snap.Actor || got.Source != snap.Source || got.Operation != snap.Operation {
		t.Fatalf("same-family read changed or lost the original snapshot: %v", err)
	}
	if after := mustInt(t, f.db, `SELECT expires_ms FROM playback_disc_lifecycle WHERE snapshot_id=?`, snap.ID); after != before {
		t.Fatal("token rotation renewed the immutable presentation deadline")
	}
	refused := errors.New("ordinary presentation authority refused")
	if _, err := f.resolve(snap.key(), snap.ID, func(context.Context, *sql.Tx, Snapshot) error { return refused }); !errors.Is(err, refused) {
		t.Fatal("family currentness replaced the mandatory ordinary authority proof", err)
	}
}

func TestDiscPendingActivationStillRequiresOriginalToken(t *testing.T) {
	f := newDiscFixture(t)
	snap := f.snapshot
	snap.Actor.Authority = "local"
	if err := f.publish(snap, plainCheck()); err != nil {
		t.Fatal(err)
	}
	seedDiscFamilyContinuation(t, f, snap, "rotated-before-activation")
	if _, err := f.activate(snap.key(), snap.ID, plainCheck()); !errors.Is(err, ErrCurrent) {
		t.Fatal("pending activation adopted a newer token", err)
	}
	if state := mustText(t, f.db, `SELECT state FROM playback_disc_lifecycle WHERE snapshot_id=?`, snap.ID); state != "pending" {
		t.Fatal("refused rotation published an active presentation")
	}
}

func TestDiscActiveFamilyContinuationRefusesRetiredOrForeignBindings(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for name, drift := range map[string]string{
		"revoked family":       `UPDATE authorization_session_families SET revoked=1`,
		"retired token":        `UPDATE authorization_family_tokens SET retired=1 WHERE token_hash='rotated-disc-token'`,
		"moved generation":     `UPDATE authorization_session_families SET current_generation=current_generation+1`,
		"backwards generation": `UPDATE authorization_family_tokens SET generation=2 WHERE token_hash='rotated-disc-token'; UPDATE authorization_session_families SET current_generation=2`,
		"original generation":  `UPDATE authorization_family_tokens SET generation=1 WHERE token_hash='session-hash-test'`,
		"viewer changed":       `UPDATE authorization_session_families SET profile_id='another-profile'`,
		"epoch changed":        `UPDATE authorization_session_families SET epoch=epoch+1`,
		"expired family":       `UPDATE authorization_session_families SET authorization_horizon='2000-01-01T00:00:00Z'`,
		"expired token":        `UPDATE authorization_family_tokens SET expires_at='2000-01-01T00:00:00Z' WHERE token_hash='rotated-disc-token'`,
		"missing device":       `DELETE FROM identity_device_families`,
		"unknown current hash": `UPDATE playback_sessions SET session_hash='foreign-token'`,
		"another family for same viewer and device": `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,revoked)
 SELECT 'another-family',server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,1,0 FROM authorization_session_families WHERE id='family-disc-test';
 INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at,retired)
 SELECT 'foreign-token','another-family',1,expires_at,0 FROM authorization_family_tokens WHERE token_hash='rotated-disc-token';
 INSERT INTO identity_device_families(family_id,device_id) SELECT 'another-family',device_id FROM identity_device_families WHERE family_id='family-disc-test';
 UPDATE playback_sessions SET session_hash='foreign-token'`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newDiscFixture(t)
			snap := f.snapshot
			snap.Actor.Authority = "local"
			if err := f.publish(snap, plainCheck()); err != nil {
				t.Fatal(err)
			}
			if _, err := f.activate(snap.key(), snap.ID, plainCheck()); err != nil {
				t.Fatal(err)
			}
			seedDiscFamilyContinuation(t, f, snap, "rotated-disc-token")
			if err := f.exec(drift); err != nil {
				t.Fatal(err)
			}
			if _, err := f.resolve(snap.key(), snap.ID, plainCheck()); !errors.Is(err, ErrCurrent) {
				t.Fatalf("%s was adopted: %v", name, err)
			}
		})
	}
}
