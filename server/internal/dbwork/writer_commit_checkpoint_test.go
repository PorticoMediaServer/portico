package dbwork

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// This is a real WAL commit above the former 16,000-frame writer threshold.
// Checking the database file before any PASSIVE probe distinguishes appending
// the scan's commit from silently copying its payload inside the write gate.
func TestScanCommitLeavesCheckpointCopyToMaintenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scan.sqlite")
	db, err := OpenHandle(path, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err = ExecWrite(ctx, db, ClassBackgroundMedia, `CREATE TABLE scan_payload(id INTEGER PRIMARY KEY, body BLOB)`); err != nil {
		t.Fatal(err)
	}
	if err = WithWriteTx(ctx, db, ClassBackgroundMedia, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `WITH RECURSIVE ids(id) AS (VALUES(1) UNION ALL SELECT id+1 FROM ids WHERE id<8200) INSERT INTO scan_payload SELECT id,zeroblob(8192) FROM ids`)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	main, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	wal, err := os.Stat(path + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	if wal.Size() < int64(CheckpointTruncateFrames)*4096 {
		t.Fatalf("scan did not reach the former automatic checkpoint threshold: %d bytes", wal.Size())
	}
	if main.Size() > 1<<20 {
		t.Fatalf("scan commit copied payload into main database while holding writer: %d bytes", main.Size())
	}
	result := Checkpoint(ctx, db)
	if result.Err != nil || result.Checkpoint != result.LogFrames {
		t.Fatalf("maintenance could not copy committed scan: %+v", result)
	}
	main, err = os.Stat(path)
	if err != nil || main.Size() < 64<<20 {
		t.Fatalf("maintenance did not copy payload: %v %v", main, err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM scan_payload`).Scan(&count); err != nil || count != 8200 {
		t.Fatalf("checkpoint lost committed scan rows: count %d error %v", count, err)
	}
}
