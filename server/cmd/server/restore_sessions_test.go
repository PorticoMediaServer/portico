package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/backup"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func writeRestoreResult(t *testing.T, state, outcome, at string) {
	t.Helper()
	body := `{"at":"` + at + `","outcome":"` + outcome + `"}` + "\n"
	if err := writePathFixture(storagepaths.Path(state, backup.RestoreResultFile), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

// What the first start after a restore does to sign-ins, and what a start
// that follows no restore, or a restore that was rolled back, leaves alone.
func TestStartAfterARestoreSignsEveryoneOutAndScrubsTheSetAsideCopy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	state := t.TempDir()
	db, err := persistence.Open(storagepaths.Path(state, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ident, err := identity.New(db, state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',X'00','profile',1)`); err != nil {
		t.Fatal(err)
	}
	session, err := ident.Issue("account", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	// The copy of the previous database a restore sets aside: a real database
	// with a live session in it, in a "-pre-restore" backup folder.
	setAside := storagepaths.Path(state, backup.BackupsDir, "2026-10-13T100000Z-pre-restore")
	if err = os.MkdirAll(filepath.Join(setAside, "Database"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`VACUUM INTO ?`, filepath.Join(setAside, backup.DatabaseName)); err != nil {
		t.Fatal(err)
	}
	writePreRestoreManifest(t, setAside)
	liveIn := func(path string) int {
		t.Helper()
		copyDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		defer copyDB.Close()
		var n int
		if err = copyDB.QueryRow(`SELECT count(*) FROM authorization_session_families WHERE revoked=0`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// No restore has happened.
	if err = signOutAfterRestore(ctx, ident, state); err != nil {
		t.Fatal(err)
	}
	if _, err = ident.Authenticate(session.AccessToken); err != nil {
		t.Fatalf("a start with no restore signed someone out: %v", err)
	}
	// A restore that was rolled back: the database in place is the one that
	// was already running, and its sessions stand.
	writeRestoreResult(t, state, "rolled_back", "2026-10-13T10:00:00Z")
	if err = signOutAfterRestore(ctx, ident, state); err != nil {
		t.Fatal(err)
	}
	if _, err = ident.Authenticate(session.AccessToken); err != nil {
		t.Fatalf("a rolled-back restore signed someone out: %v", err)
	}
	if liveIn(filepath.Join(setAside, backup.DatabaseName)) != 1 {
		t.Fatal("the set-aside copy was changed before any restore was applied")
	}
	// A restore that was applied.
	writeRestoreResult(t, state, "restored", "2026-10-13T10:05:00Z")
	if err = signOutAfterRestore(ctx, ident, state); err != nil {
		t.Fatal(err)
	}
	if _, err = ident.Authenticate(session.AccessToken); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("a session survived a restore: %v", err)
	}
	if n := liveIn(filepath.Join(setAside, backup.DatabaseName)); n != 0 {
		t.Fatalf("the copy of the previous database still holds %d live sessions", n)
	}
	svc := backup.New(state, db, ident.ID(), "test", persistence.SchemaVersion())
	for _, source := range []backup.Source{{BackupID: filepath.Base(setAside)}, {Path: setAside}} {
		if _, err = svc.Stage(ctx, source); err != nil {
			t.Fatalf("scrubbed pre-restore backup cannot be staged (%+v): %v", source, err)
		}
		if err = os.RemoveAll(storagepaths.Path(state, backup.RestoreStagedDir)); err != nil {
			t.Fatal(err)
		}
	}
	// The next ordinary start leaves a new sign-in alone.
	again, err := ident.Issue("account", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = signOutAfterRestore(ctx, ident, state); err != nil {
		t.Fatal(err)
	}
	if _, err = ident.Authenticate(again.AccessToken); err != nil {
		t.Fatalf("a later start signed out someone who signed in after the restore: %v", err)
	}
}

func TestUnreadableRestoreResultStopsStartup(t *testing.T) {
	state := t.TempDir()
	for _, body := range []string{"broken", `{}`, `{"outcome":"restored","at":""}`, `{"outcome":"unexpected","at":"2026-10-13T10:05:00Z"}`} {
		if err := writePathFixture(storagepaths.Path(state, backup.RestoreResultFile), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if err := signOutAfterRestore(context.Background(), nil, state); err == nil {
			t.Fatalf("invalid restore result %q was treated as no restore", body)
		}
	}
}

func TestRestoreScrubbingRetriesAFailedSetAsideCopy(t *testing.T) {
	ctx := context.Background()
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
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',X'00','profile',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err = ident.Issue("account", "profile", "local", "owner", 1); err != nil {
		t.Fatal(err)
	}
	intact := storagepaths.Path(state, "intact.db")
	if _, err = db.Exec(`VACUUM INTO ?`, intact); err != nil {
		t.Fatal(err)
	}
	folder := storagepaths.Path(state, backup.BackupsDir, "2026-10-13T100000Z-pre-restore")
	if err = os.MkdirAll(filepath.Join(folder, "Database"), 0700); err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(folder, backup.DatabaseName)
	if err = os.WriteFile(copyPath, []byte("broken database"), 0600); err != nil {
		t.Fatal(err)
	}
	writeRestoreResult(t, state, "restored", "2026-10-13T10:05:00Z")
	if err = signOutAfterRestore(ctx, ident, state); err == nil {
		t.Error("a failed backup scrub did not stop startup")
	}
	raw, err := os.ReadFile(intact)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(copyPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	writePreRestoreManifest(t, folder)
	if err = signOutAfterRestore(ctx, ident, state); err != nil {
		t.Fatal(err)
	}
	copyDB, err := dbwork.OpenHandle(copyPath, dbwork.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	var live int
	if err = copyDB.QueryRow(`SELECT count(*) FROM authorization_session_families WHERE revoked=0`).Scan(&live); err != nil || live != 0 {
		t.Fatalf("failed scrub was not retried: %d live sessions, %v", live, err)
	}
}

func writePreRestoreManifest(t *testing.T, folder string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(folder, backup.DatabaseName))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	manifest, err := json.Marshal(backup.Manifest{Format: backup.Format, Version: backup.StreamVersion, SchemaVersion: persistence.SchemaVersion(), Kind: backup.KindPreRestore, CreatedAt: "2026-10-13T10:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	writeBackupStream(t, folder, manifest, []backup.FileEntry{{Name: backup.DatabaseName, Bytes: int64(len(raw)), SHA256: hex.EncodeToString(digest[:])}})
}

func TestRealRestoreKeepsItsScrubbedRollbackBackupRestorable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	state := t.TempDir()
	db, err := persistence.Open(storagepaths.Path(state, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	ident, err := identity.New(db, state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',X'00','profile',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err = ident.Issue("account", "profile", "local", "owner", 1); err != nil {
		t.Fatal(err)
	}
	svc := backup.New(state, db, ident.ID(), "test", persistence.SchemaVersion())
	id, err := svc.Create(ctx, backup.KindManual)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Stage(ctx, backup.Source{BackupID: id}); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = backup.ApplyStaged(ctx, state, nil); err != nil {
		t.Fatal(err)
	}
	db, err = persistence.Open(storagepaths.Path(state, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	ident, err = identity.New(db, state)
	if err != nil {
		t.Fatal(err)
	}
	if err = signOutAfterRestore(ctx, ident, state); err != nil {
		t.Fatal(err)
	}
	svc = backup.New(state, db, ident.ID(), "test", persistence.SchemaVersion())
	entries, err := svc.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.Kind != backup.KindPreRestore {
			continue
		}
		found = true
		for _, source := range []backup.Source{{BackupID: entry.ID}, {Path: storagepaths.Path(state, backup.BackupsDir, entry.ID)}} {
			if _, err = svc.Stage(ctx, source); err != nil {
				t.Fatalf("real scrubbed rollback backup cannot stage: %v", err)
			}
			if err = os.RemoveAll(storagepaths.Path(state, backup.RestoreStagedDir)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !found {
		t.Fatal("no automatic rollback backup")
	}
}
