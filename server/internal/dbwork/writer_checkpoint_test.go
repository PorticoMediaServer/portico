package dbwork

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// The maintenance controller owns copying; the reader policy remains unchanged.
func TestWriterAutomaticCheckpointIsDisabled(t *testing.T) {
	db, err := OpenHandle(filepath.Join(t.TempDir(), "writer.sqlite"), DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var writer, reader int
	if err = WithWriteTx(context.Background(), db, ClassMaintenance, func(tx *sql.Tx) error {
		return tx.QueryRow(`PRAGMA wal_autocheckpoint`).Scan(&writer)
	}); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`PRAGMA wal_autocheckpoint`).Scan(&reader); err != nil {
		t.Fatal(err)
	}
	if writer != 0 || reader != DefaultPolicy().WALAutocheckpoint {
		t.Fatalf("writer %d reader %d", writer, reader)
	}
	if CheckpointCopyInterval >= CheckpointRegularInterval {
		t.Fatal("new commits must be copied more often than the regular check")
	}
}
