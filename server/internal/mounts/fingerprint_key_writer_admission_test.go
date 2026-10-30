package mounts

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func fingerprintKeyDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := persistence.OpenFresh(filepath.Join(t.TempDir(), "mounts.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func fingerprintKeyAdmissions() uint64 {
	return dbwork.WriteGate().Stats().ByClass[dbwork.ClassInteractive.String()].Acquired
}

func fingerprintKeyExec(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := dbwork.ExecWrite(context.Background(), db, dbwork.ClassInteractive, statement, args...); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

// TestFingerprintKeyFirstUseIsAdmittedAndPersisted is the first-use regression:
// one interactive admission generates and stores a 32-byte key, and a second
// call reloads that key with no write and no admission.
func TestFingerprintKeyFirstUseIsAdmittedAndPersisted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := fingerprintKeyDB(t)
	before := fingerprintKeyAdmissions()

	var key [32]byte
	if err := loadOrCreateFingerprintKey(db, &key); err != nil {
		t.Fatal(err)
	}
	if got := fingerprintKeyAdmissions() - before; got != 1 {
		t.Fatalf("first-use fingerprint key took %d interactive admissions, want 1", got)
	}
	stored := persistence.Get(db, fingerprintKeyName)
	decoded, err := hex.DecodeString(stored)
	if err != nil || len(decoded) != 32 || !bytes.Equal(decoded, key[:]) {
		t.Fatalf("stored fingerprint key %q does not decode to the generated key (err %v)", stored, err)
	}

	var reloaded [32]byte
	if err := loadOrCreateFingerprintKey(db, &reloaded); err != nil {
		t.Fatal(err)
	}
	if got := fingerprintKeyAdmissions() - before; got != 1 {
		t.Fatalf("valid-key reload added an admission: total %d, want 1", got)
	}
	if reloaded != key {
		t.Fatal("valid-key reload produced a different fingerprint key")
	}
	if again := persistence.Get(db, fingerprintKeyName); again != stored {
		t.Fatalf("valid-key reload rewrote the stored key: %q, want %q", again, stored)
	}
}

// TestFingerprintKeyReplacesInvalidStoredValue keeps the raw invalid-key path: a
// stored value that is not 32 hex bytes is replaced once, by a fresh key.
func TestFingerprintKeyReplacesInvalidStoredValue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := fingerprintKeyDB(t)
	fingerprintKeyExec(t, db, `INSERT INTO configuration(key,value) VALUES(?,?)`, fingerprintKeyName, "abcd")
	before := fingerprintKeyAdmissions()

	var key [32]byte
	if err := loadOrCreateFingerprintKey(db, &key); err != nil {
		t.Fatal(err)
	}
	if got := fingerprintKeyAdmissions() - before; got != 1 {
		t.Fatalf("invalid-key replacement took %d interactive admissions, want 1", got)
	}
	stored := persistence.Get(db, fingerprintKeyName)
	decoded, err := hex.DecodeString(stored)
	if err != nil || len(decoded) != 32 || !bytes.Equal(decoded, key[:]) {
		t.Fatalf("invalid key was not replaced by the generated key: %q", stored)
	}
	if stored == "abcd" {
		t.Fatal("invalid stored value survived")
	}
}

// TestFingerprintKeyWaitsForTheWriterGate holds the gate and proves the first-use
// write queues behind it. The raw db.Exec completed instead, so restoring the
// baseline fails here.
func TestFingerprintKeyWaitsForTheWriterGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := fingerprintKeyDB(t)
	release, err := dbwork.WriteGate().Acquire(context.Background(), dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	done := make(chan error, 1)
	var key [32]byte
	go func() {
		done <- loadOrCreateFingerprintKey(db, &key)
	}()

	awaitFingerprintKeyQueued(t, done)
	if stored := persistence.Get(db, fingerprintKeyName); stored != "" {
		t.Fatalf("held gate did not block the fingerprint-key write: %q already stored", stored)
	}
	release()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("fingerprint-key write did not finish after the gate was released")
	}
	if key == ([32]byte{}) {
		t.Fatal("admitted fingerprint key was not generated")
	}
}

func awaitFingerprintKeyQueued(t *testing.T, done <-chan error) {
	t.Helper()
	watchdog := time.NewTimer(15 * time.Second)
	defer watchdog.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case err := <-done:
			t.Fatalf("fingerprint-key write completed while the gate was held (err %v): it bypassed the writer gate", err)
		case <-poll.C:
			if dbwork.WriteGate().Waiting()[dbwork.ClassInteractive.String()] > 0 {
				return
			}
		case <-watchdog.C:
			t.Fatal("timed out waiting for the fingerprint-key writer to queue at the held gate")
		}
	}
}
