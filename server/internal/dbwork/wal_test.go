package dbwork

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestWALReportsFramesAndFileSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	db, err := OpenHandle(path, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err = db.ExecContext(ctx, `CREATE TABLE sample(id INTEGER PRIMARY KEY, body TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	// The driver reports the path with symlinks resolved, which is what a stat of
	// the -wal beside it needs; compare the resolved forms.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if file := DatabaseFile(ctx, db); file != resolved {
		t.Fatalf("the handle reported its file as %q, not %q", file, resolved)
	}
	// Write enough to put frames in the log without reaching the autocheckpoint.
	for index := 0; index < 64; index++ {
		if _, err = db.ExecContext(ctx, `INSERT INTO sample(body) VALUES(?)`, "a reasonably sized row of text"); err != nil {
			t.Fatal(err)
		}
	}
	stats := copyWALForTest(t, ctx, db)
	if stats.LastError != "" {
		t.Fatalf("the WAL probe failed: %s", stats.LastError)
	}
	if stats.LogFrames <= 0 {
		t.Fatalf("64 committed rows produced %d log frames", stats.LogFrames)
	}
	if stats.CheckpointedFrames <= 0 {
		t.Fatal("a PASSIVE checkpoint with no competing reader checkpointed nothing")
	}
	if stats.PeakFileBytes < stats.FileBytes {
		t.Fatalf("the peak (%d) is below the current size (%d)", stats.PeakFileBytes, stats.FileBytes)
	}
}

func TestWALOnANilHandleIsEmptyRatherThanAPanic(t *testing.T) {
	if stats := WAL(context.Background(), nil); stats.LogFrames != 0 || stats.FileBytes != 0 {
		t.Fatalf("a nil handle reported %#v", stats)
	}
}

func TestWALSeparatesCopiedBacklogFromReusedPhysicalAllocation(t *testing.T) {
	db := readerFixture(t)
	ctx := context.Background()
	if _, err := ExecWrite(ctx, db, ClassInteractive, `CREATE TABLE payload(body BLOB); WITH RECURSIVE ids(id) AS (VALUES(1) UNION ALL SELECT id+1 FROM ids WHERE id<128) INSERT INTO payload SELECT zeroblob(8192) FROM ids`); err != nil {
		t.Fatal(err)
	}
	copied := copyWALForTest(t, ctx, db)
	if copied.LastError != "" || copied.LogFrames <= 100 || copied.BacklogFrames != 0 || copied.FileBytes <= 1<<20 {
		t.Fatalf("PASSIVE did not expose copied physical allocation: %+v", copied)
	}
	if _, err := ExecWrite(ctx, db, ClassInteractive, `INSERT INTO sample VALUES(3)`); err != nil {
		t.Fatal(err)
	}
	reused := copyWALForTest(t, ctx, db)
	if reused.LastError != "" || reused.LogFrames >= copied.LogFrames || reused.BacklogFrames != 0 || reused.FileBytes != copied.FileBytes {
		t.Fatalf("physical allocation was confused with logical backlog: before%+v after%+v", copied, reused)
	}
}

// Explicit fixture copying used to be a hidden side effect of WAL diagnostics.
// Tests that need a copied/reused native log prepare it here, then inspect the
// exact same cached diagnostic surface production uses.
func copyWALForTest(t *testing.T, ctx context.Context, db *sql.DB) WALStats {
	t.Helper()
	var r CheckpointResult
	if err := db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`).Scan(&r.Busy, &r.LogFrames, &r.Checkpoint); err != nil {
		t.Fatal(err)
	}
	l := ledgerFor(db)
	l.mu.Lock()
	l.lastCheckpoint, l.checkpointKnown = r, true
	l.mu.Unlock()
	return WAL(ctx, db)
}

func TestWALDiagnosticsDoNotCopyOrAcquireWriter(t *testing.T) {
	db := testDB(t)
	before := WriteGate().Stats().Acquired
	ctx, statements := TraceStatements(context.Background())
	first := WAL(ctx, db)
	if first.LastError == "" {
		t.Fatal("an absent owner sample was represented as known")
	}
	if got := statements(); len(got) != 0 {
		t.Fatalf("diagnostic issued SQL: %v", got)
	}
	if WriteGate().Stats().Acquired != before {
		t.Fatal("diagnostic acquired writer")
	}
	copied := copyWALForTest(t, context.Background(), db)
	_ = WAL(ctx, db)
	if got := statements(); len(got) != 0 {
		t.Fatalf("cached diagnostic issued SQL: %v", got)
	}
	if copied.LastError != "" {
		t.Fatal(copied.LastError)
	}
}
