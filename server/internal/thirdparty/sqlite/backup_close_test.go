package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

// Closing a connection that a backup is reading from finishes the backup and
// closes its destination, so a later Step or Finish reports ErrBackupClosed
// instead of running on freed per-connection state.
func TestClosingTheSourceFinishesItsBackup(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "source.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE sample(body BLOB); INSERT INTO sample VALUES(randomblob(65536))`); err != nil {
		t.Fatal(err)
	}
	handle, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var backup *Backup
	var source *conn
	if err = handle.Raw(func(raw any) error {
		source = raw.(*conn)
		backup, err = source.NewBackup(filepath.Join(dir, "copy.sqlite"))
		if err != nil {
			return err
		}
		_, err = backup.Step(1)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	destination := backup.dstConn
	if err = handle.Raw(func(any) error { return source.Close() }); err != nil {
		t.Fatal(err)
	}
	handle.Close()
	if destination.db != 0 {
		t.Fatal("the backup's destination stayed open")
	}
	if _, err = backup.Step(1); !errors.Is(err, ErrBackupClosed) {
		t.Fatalf("step after the source closed: %v", err)
	}
	if err = backup.Finish(); !errors.Is(err, ErrBackupClosed) {
		t.Fatalf("finish after the source closed: %v", err)
	}
}
