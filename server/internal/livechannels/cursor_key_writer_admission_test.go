package livechannels

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

func cursorKeyDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := persistence.OpenFresh(filepath.Join(t.TempDir(), "cursor.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func cursorKeyAdmissions() uint64 {
	return dbwork.WriteGate().Stats().ByClass[dbwork.ClassInteractive.String()].Acquired
}

func cursorKeyExec(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := dbwork.ExecWrite(context.Background(), db, dbwork.ClassInteractive, statement, args...); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

// TestCursorKeyFirstUseIsAdmittedAndPersisted is the first-use regression: one
// interactive admission generates and stores a 32-byte key, and a second call
// reloads that key with no write and no admission.
func TestCursorKeyFirstUseIsAdmittedAndPersisted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := cursorKeyDB(t)
	before := cursorKeyAdmissions()

	key, err := loadOrCreateCursorKey(db)
	if err != nil {
		t.Fatal(err)
	}
	if got := cursorKeyAdmissions() - before; got != 1 {
		t.Fatalf("first-use cursor key took %d interactive admissions, want 1", got)
	}
	if len(key) != 32 {
		t.Fatalf("generated cursor key is %d bytes, want 32", len(key))
	}
	stored := persistence.Get(db, cursorKeyName)
	decoded, err := hex.DecodeString(stored)
	if err != nil || !bytes.Equal(decoded, key) {
		t.Fatalf("stored cursor key %q does not decode to the generated key (err %v)", stored, err)
	}

	reloaded, err := loadOrCreateCursorKey(db)
	if err != nil {
		t.Fatal(err)
	}
	if got := cursorKeyAdmissions() - before; got != 1 {
		t.Fatalf("valid-key reload added an admission: total %d, want 1", got)
	}
	if !bytes.Equal(reloaded, key) {
		t.Fatal("valid-key reload generated a different key")
	}
	if again := persistence.Get(db, cursorKeyName); again != stored {
		t.Fatalf("valid-key reload rewrote the stored key: %q, want %q", again, stored)
	}
}

// TestCursorKeyReplacesInvalidStoredValue keeps the raw invalid-key path: a
// stored value that is not 32 hex bytes is replaced once, by a fresh key.
func TestCursorKeyReplacesInvalidStoredValue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := cursorKeyDB(t)
	cursorKeyExec(t, db, `INSERT INTO configuration(key,value) VALUES(?,?)`, cursorKeyName, "abcd")
	before := cursorKeyAdmissions()

	key, err := loadOrCreateCursorKey(db)
	if err != nil {
		t.Fatal(err)
	}
	if got := cursorKeyAdmissions() - before; got != 1 {
		t.Fatalf("invalid-key replacement took %d interactive admissions, want 1", got)
	}
	if len(key) != 32 {
		t.Fatalf("replacement cursor key is %d bytes, want 32", len(key))
	}
	stored := persistence.Get(db, cursorKeyName)
	decoded, err := hex.DecodeString(stored)
	if err != nil || !bytes.Equal(decoded, key) {
		t.Fatalf("invalid key was not replaced by the generated key: %q", stored)
	}
	if stored == "abcd" {
		t.Fatal("invalid stored value survived")
	}
}

// TestCursorKeyWaitsForTheWriterGate holds the gate and proves the first-use
// write queues behind it. The raw db.Exec completed instead, so restoring the
// baseline fails here.
func TestCursorKeyWaitsForTheWriterGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := cursorKeyDB(t)
	release, err := dbwork.WriteGate().Acquire(context.Background(), dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	done := make(chan error, 1)
	var key []byte
	go func() {
		generated, e := loadOrCreateCursorKey(db)
		key = generated
		done <- e
	}()

	awaitCursorKeyQueued(t, done)
	if stored := persistence.Get(db, cursorKeyName); stored != "" {
		t.Fatalf("held gate did not block the cursor-key write: %q already stored", stored)
	}
	release()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("cursor-key write did not finish after the gate was released")
	}
	if len(key) != 32 {
		t.Fatalf("admitted cursor key is %d bytes, want 32", len(key))
	}
}

func awaitCursorKeyQueued(t *testing.T, done <-chan error) {
	t.Helper()
	watchdog := time.NewTimer(15 * time.Second)
	defer watchdog.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case err := <-done:
			t.Fatalf("cursor-key write completed while the gate was held (err %v): it bypassed the writer gate", err)
		case <-poll.C:
			if dbwork.WriteGate().Waiting()[dbwork.ClassInteractive.String()] > 0 {
				return
			}
		case <-watchdog.C:
			t.Fatal("timed out waiting for the cursor-key writer to queue at the held gate")
		}
	}
}
