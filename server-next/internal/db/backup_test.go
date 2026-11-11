package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A backup of a large database while people keep writing finishes, holds what was written
// before it ended, and never lets the write-ahead log grow past what the checkpoint policy
// allows: no read it makes lasts long enough to hold the log open. The writes here outpace
// the backup four to one, so a backup made in one long read would let the log reach several
// times the bound.
func TestBackupWhileWritingKeepsTheLogSmall(t *testing.T) {
	if testing.Short() {
		t.Skip("writes about half a gigabyte")
	}
	ctx := context.Background()
	dir := t.TempDir()
	d, err := Open(ctx, dir+"/main.db")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	blob := strings.Repeat("x", 1<<20)
	put := func(p Priority, key string) error {
		return d.write(ctx, p, func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, 0)
				ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, blob)
			return err
		})
	}
	for n := range 96 { // a database of about 100 MB
		if err := put(Background, fmt.Sprintf("fill-%d", n)); err != nil {
			t.Fatal(err)
		}
	}

	var largest, written atomic.Int64
	stop := make(chan struct{})
	writing := make(chan error, 1)
	go func() {
		for n := 0; ; n++ {
			select {
			case <-stop:
				writing <- nil
				return
			default:
			}
			if err := put(User, fmt.Sprintf("during-%d", n%64)); err != nil {
				writing <- err
				return
			}
			written.Add(1 << 20)
			if size, _ := d.walSize(); size > largest.Load() {
				largest.Store(size)
			}
		}
	}()
	started := time.Now()
	if err := d.BackupTo(ctx, dir+"/copy.db", nil); err != nil {
		t.Fatal(err)
	}
	close(stop)
	if err := <-writing; err != nil {
		t.Fatal(err)
	}
	t.Logf("backed up in %s while %d MB were written; the log was at most %d MB",
		time.Since(started).Round(time.Millisecond), written.Load()>>20, largest.Load()>>20)
	bound := int64(walCatchUpBytes + 32<<20)
	if largest.Load() > bound {
		t.Errorf("the log reached %d MB during the backup; the policy keeps it near %d MB", largest.Load()>>20, walCatchUpBytes>>20)
	}
	if written.Load() < 2*bound {
		t.Errorf("only %d MB were written during the backup: not enough to show the log was kept small", written.Load()>>20)
	}
	copied, err := Open(ctx, dir+"/copy.db")
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()
	var fills int
	if err := copied.read.QueryRow(`SELECT count(*) FROM settings WHERE key LIKE 'fill-%'`).Scan(&fills); err != nil || fills != 96 {
		t.Errorf("the copy holds %d of the 96 rows written before it: %v", fills, err)
	}
	var check string
	if err := copied.read.QueryRow(`PRAGMA quick_check`).Scan(&check); err != nil || check != "ok" {
		t.Errorf("the copy's check: %q %v", check, err)
	}
}
