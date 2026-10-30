package persistence_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

const (
	pruneChallengesStatement = `DELETE FROM identity_factor_challenges WHERE rowid IN(SELECT rowid FROM identity_factor_challenges WHERE expires_at<=? LIMIT 64)`
	pruneTokensStatement     = `DELETE FROM topshelf_tokens WHERE rowid IN(SELECT rowid FROM topshelf_tokens WHERE expires_at<=? LIMIT 64)`
)

func writerAdmissionDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := persistence.OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func classAdmissions(class dbwork.Class) uint64 {
	return dbwork.WriteGate().Stats().ByClass[class.String()].Acquired
}

// holdWriterGate takes the single writer slot so a correctly admitted writer has
// to queue behind it. The returned release is idempotent.
func holdWriterGate(t *testing.T) func() {
	t.Helper()
	release, err := dbwork.WriteGate().Acquire(context.Background(), dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return release
}

// awaitQueued proves the writer was blocked by the held gate rather than merely
// slow: it must appear in the gate's wait queue while the hold is outstanding. A
// raw writer completes instead, which fails the test.
func awaitQueued(t *testing.T, class dbwork.Class, done <-chan error) {
	t.Helper()
	watchdog := time.NewTimer(15 * time.Second)
	defer watchdog.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case err := <-done:
			t.Fatalf("%s writer completed while the gate was held (err %v): it bypassed the writer gate", class, err)
		case <-poll.C:
			if dbwork.WriteGate().Waiting()[class.String()] > 0 {
				return
			}
		case <-watchdog.C:
			t.Fatalf("timed out waiting for a %s writer to queue at the held gate", class)
		}
	}
}

// finishAdmittedWrite releases the hold and waits for the queued writer.
func finishAdmittedWrite(t *testing.T, class dbwork.Class, done <-chan error, release func()) {
	t.Helper()
	release()
	watchdog := time.NewTimer(15 * time.Second)
	defer watchdog.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s admitted write failed after release: %v", class, err)
		}
	case <-watchdog.C:
		t.Fatalf("%s writer did not finish after the gate was released", class)
	}
}

func mustExec(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := dbwork.ExecWrite(context.Background(), db, dbwork.ClassInteractive, statement, args...); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

func seedProofRows(t *testing.T, db *sql.DB, now, future string) {
	t.Helper()
	mustExec(t, db, `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('acct-writer','writer-user',x'00','prof-writer')`)
	for _, row := range []struct {
		hash, expiry string
	}{
		{"challenge-expired-at-now", now},
		{"challenge-future", future},
	} {
		mustExec(t, db, `INSERT INTO identity_factor_challenges(token_hash,account_id,expires_at) VALUES(?,?,?)`, row.hash, "acct-writer", row.expiry)
	}
	for _, row := range []struct {
		hash, expiry string
	}{
		{"token-expired-at-now", now},
		{"token-future", future},
	} {
		mustExec(t, db, `INSERT INTO topshelf_tokens(token_hash,device_id,account_id,profile_id,expires_at,account_epoch,pin_revision) VALUES(?,?,?,?,?,1,1)`, row.hash, "device-writer", "acct-writer", "prof-writer", row.expiry)
	}
}

func proofHashExists(t *testing.T, db *sql.DB, table, hash string) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM `+table+` WHERE token_hash=?`, hash).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count == 1
}

// TestSetAdmitsConfigurationWriteThroughTheWriterGate is the regression for
// persistence.Set: the real function stores the value, issues the upsert once,
// and takes exactly one interactive admission. A raw db.Exec would complete
// while the gate is held instead of queueing.
func TestSetAdmitsConfigurationWriteThroughTheWriterGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := writerAdmissionDB(t)
	// Set has no context to trace, so count the row effects of its one
	// statement instead: an insert, then exactly one update per later call.
	mustExec(t, db, `CREATE TABLE test_configuration_writes(operation TEXT NOT NULL)`)
	mustExec(t, db, `CREATE TRIGGER test_configuration_insert AFTER INSERT ON configuration BEGIN INSERT INTO test_configuration_writes(operation) VALUES('insert'); END`)
	mustExec(t, db, `CREATE TRIGGER test_configuration_update AFTER UPDATE ON configuration BEGIN INSERT INTO test_configuration_writes(operation) VALUES('update'); END`)

	before := classAdmissions(dbwork.ClassInteractive)
	release := holdWriterGate(t)
	done := make(chan error, 1)
	go func() {
		done <- persistence.Set(db, "writer_gate_key", "first")
	}()
	if value := persistence.Get(db, "writer_gate_key"); value != "" {
		t.Fatalf("held gate did not block Set: value already %q", value)
	}
	awaitQueued(t, dbwork.ClassInteractive, done)
	if value := persistence.Get(db, "writer_gate_key"); value != "" {
		t.Fatalf("held gate did not block Set: value already %q", value)
	}
	finishAdmittedWrite(t, dbwork.ClassInteractive, done, release)

	if got := classAdmissions(dbwork.ClassInteractive) - before; got != 1 {
		t.Fatalf("Set took %d interactive admissions, want 1", got)
	}
	if value := persistence.Get(db, "writer_gate_key"); value != "first" {
		t.Fatalf("Set stored %q, want first", value)
	}

	// The same key is an upsert: one statement, one admission, new value.
	if err := persistence.Set(db, "writer_gate_key", "second"); err != nil {
		t.Fatal(err)
	}
	if got := classAdmissions(dbwork.ClassInteractive) - before; got != 2 {
		t.Fatalf("insert plus upsert took %d interactive admissions, want 2", got)
	}
	if value := persistence.Get(db, "writer_gate_key"); value != "second" {
		t.Fatalf("upsert stored %q, want second", value)
	}
	var operations []string
	rows, err := db.Query(`SELECT operation FROM test_configuration_writes ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var operation string
		if err = rows.Scan(&operation); err != nil {
			t.Fatal(err)
		}
		operations = append(operations, operation)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if got := strings.Join(operations, ","); got != "insert,update" {
		t.Fatalf("configuration row effects = %q, want one insert then one update: the upsert ran once per call", got)
	}
}

// TestPruneIdentityProofsAdmitsBothDeletesAtMaintenanceClass keeps the exact
// statement order, the inclusive expiry boundary and the maintenance admission
// class. Expiry itself is enforced on read; only reclamation is tested here.
func TestPruneIdentityProofsAdmitsBothDeletesAtMaintenanceClass(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := writerAdmissionDB(t)
	const now = "2026-10-27T00:00:00Z"
	const future = "2026-10-27T00:00:01Z"
	seedProofRows(t, db, now, future)

	before := classAdmissions(dbwork.ClassMaintenance)
	traceCtx, statements := dbwork.TraceQueryPlans(context.Background())
	if err := persistence.PruneIdentityProofs(traceCtx, db, now); err != nil {
		t.Fatal(err)
	}
	if got := classAdmissions(dbwork.ClassMaintenance) - before; got != 2 {
		t.Fatalf("PruneIdentityProofs took %d maintenance admissions, want 2", got)
	}

	// expires_at <= now is reclaimed; one second later survives.
	if proofHashExists(t, db, "identity_factor_challenges", "challenge-expired-at-now") {
		t.Fatal("challenge expiring exactly at now survived")
	}
	if !proofHashExists(t, db, "identity_factor_challenges", "challenge-future") {
		t.Fatal("future challenge was reclaimed")
	}
	if proofHashExists(t, db, "topshelf_tokens", "token-expired-at-now") {
		t.Fatal("token expiring exactly at now survived")
	}
	if !proofHashExists(t, db, "topshelf_tokens", "token-future") {
		t.Fatal("future token was reclaimed")
	}

	challengesAt, tokensAt := -1, -1
	for i, line := range statements() {
		switch line {
		case pruneChallengesStatement:
			if challengesAt >= 0 {
				t.Fatalf("challenge DELETE issued more than once at %d and %d", challengesAt, i)
			}
			challengesAt = i
		case pruneTokensStatement:
			if tokensAt >= 0 {
				t.Fatalf("token DELETE issued more than once at %d and %d", tokensAt, i)
			}
			tokensAt = i
		}
	}
	if challengesAt < 0 || tokensAt < 0 {
		t.Fatalf("prune statements missing (challenges %d, tokens %d):\n%s", challengesAt, tokensAt, strings.Join(statements(), "\n"))
	}
	if challengesAt > tokensAt {
		t.Fatalf("prune order changed: challenges %d, tokens %d", challengesAt, tokensAt)
	}
}

// A backlog larger than one batch is reclaimed in bounded batches, each its own
// maintenance admission, until a batch comes back short.
func TestPruneIdentityProofsReclaimsABacklogInBatches(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := writerAdmissionDB(t)
	const now = "2026-10-27T00:00:00Z"
	seedProofRows(t, db, now, "2026-10-27T00:00:01Z")
	for i := 0; i < 140; i++ {
		mustExec(t, db, `INSERT INTO identity_factor_challenges(token_hash,account_id,expires_at) VALUES(?,?,?)`, fmt.Sprintf("backlog-%d", i), "acct-writer", now)
	}
	before := classAdmissions(dbwork.ClassMaintenance)
	if err := persistence.PruneIdentityProofs(context.Background(), db, now); err != nil {
		t.Fatal(err)
	}
	// 141 expired challenges: 64 + 64 + 13, then one short token batch.
	if got := classAdmissions(dbwork.ClassMaintenance) - before; got != 4 {
		t.Fatalf("PruneIdentityProofs took %d maintenance admissions, want 4", got)
	}
	var left int
	if err := db.QueryRow(`SELECT count(*) FROM identity_factor_challenges WHERE expires_at<=?`, now).Scan(&left); err != nil || left != 0 {
		t.Fatalf("%d expired challenges left: %v", left, err)
	}
	if !proofHashExists(t, db, "identity_factor_challenges", "challenge-future") {
		t.Fatal("future challenge was reclaimed")
	}
}

// TestPruneIdentityProofsWaitsForTheWriterGate holds the gate and proves the
// retention deletes queue behind it. The raw db.ExecContext removed expired rows
// while the gate was held, so restoring the baseline fails here.
func TestPruneIdentityProofsWaitsForTheWriterGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := writerAdmissionDB(t)
	const now = "2026-10-27T00:00:00Z"
	seedProofRows(t, db, now, "2026-10-27T00:00:01Z")

	release := holdWriterGate(t)
	done := make(chan error, 1)
	go func() {
		done <- persistence.PruneIdentityProofs(context.Background(), db, now)
	}()
	awaitQueued(t, dbwork.ClassMaintenance, done)
	if !proofHashExists(t, db, "identity_factor_challenges", "challenge-expired-at-now") {
		t.Fatal("held gate did not block the first prune statement")
	}
	if !proofHashExists(t, db, "topshelf_tokens", "token-expired-at-now") {
		t.Fatal("held gate did not block the second prune statement")
	}
	finishAdmittedWrite(t, dbwork.ClassMaintenance, done, release)
	if proofHashExists(t, db, "identity_factor_challenges", "challenge-expired-at-now") ||
		proofHashExists(t, db, "topshelf_tokens", "token-expired-at-now") {
		t.Fatal("expired rows survived after the gate was released")
	}
}

// TestPruneIdentityProofsFirstStatementErrorSkipsTheSecond keeps the raw
// error-first shape: a refused first DELETE returns immediately and the second
// statement is not attempted.
func TestPruneIdentityProofsFirstStatementErrorSkipsTheSecond(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := writerAdmissionDB(t)
	const now = "2026-10-27T00:00:00Z"
	seedProofRows(t, db, now, "2026-10-27T00:00:01Z")
	mustExec(t, db, `CREATE TRIGGER test_writer_prune_first_fails BEFORE DELETE ON identity_factor_challenges BEGIN SELECT RAISE(ABORT,'injected first prune failure'); END`)

	before := classAdmissions(dbwork.ClassMaintenance)
	traceCtx, statements := dbwork.TraceQueryPlans(context.Background())
	if err := persistence.PruneIdentityProofs(traceCtx, db, now); err == nil {
		t.Fatal("PruneIdentityProofs returned nil after the first DELETE was refused")
	}
	if got := classAdmissions(dbwork.ClassMaintenance) - before; got != 1 {
		t.Fatalf("refused first prune took %d maintenance admissions, want 1 (no retry, no second statement)", got)
	}
	if !proofHashExists(t, db, "identity_factor_challenges", "challenge-expired-at-now") {
		t.Fatal("refused first DELETE still removed the challenge row")
	}
	if !proofHashExists(t, db, "topshelf_tokens", "token-expired-at-now") {
		t.Fatal("second DELETE ran after the first failed")
	}
	for _, line := range statements() {
		if line == pruneTokensStatement {
			t.Fatalf("second prune statement was attempted after the first failed:\n%s", strings.Join(statements(), "\n"))
		}
	}
}

// TestPruneIdentityProofsSecondStatementErrorLeavesFirstCommitted preserves the
// actual autocommit parity: the two DELETEs are separate writes, so a refused
// second one must not roll back the first.
func TestPruneIdentityProofsSecondStatementErrorLeavesFirstCommitted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := writerAdmissionDB(t)
	const now = "2026-10-27T00:00:00Z"
	seedProofRows(t, db, now, "2026-10-27T00:00:01Z")
	mustExec(t, db, `CREATE TRIGGER test_writer_prune_second_fails BEFORE DELETE ON topshelf_tokens BEGIN SELECT RAISE(ABORT,'injected second prune failure'); END`)

	before := classAdmissions(dbwork.ClassMaintenance)
	if err := persistence.PruneIdentityProofs(context.Background(), db, now); err == nil {
		t.Fatal("PruneIdentityProofs returned nil after the second DELETE was refused")
	}
	if got := classAdmissions(dbwork.ClassMaintenance) - before; got != 2 {
		t.Fatalf("partial prune took %d maintenance admissions, want 2", got)
	}
	if proofHashExists(t, db, "identity_factor_challenges", "challenge-expired-at-now") {
		t.Fatal("first DELETE was rolled back when the second failed: the raw shape is two autocommit writes")
	}
	if !proofHashExists(t, db, "topshelf_tokens", "token-expired-at-now") {
		t.Fatal("refused second DELETE still removed the token row")
	}
	if !proofHashExists(t, db, "identity_factor_challenges", "challenge-future") ||
		!proofHashExists(t, db, "topshelf_tokens", "token-future") {
		t.Fatal("future rows were reclaimed")
	}
}
