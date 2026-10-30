package dbwork

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	sqlite "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

// P2-13 with P2-12: an online backup steps on the writer connection as
// writer-gated maintenance tickets, and holds nothing between steps. A
// hard-pressure WAL recovery therefore cancels the owner-long read that pins
// the log (ErrReadPressure) but only makes the backup wait: its steps queue
// behind the hold and the pause like any writer, the reset happens, and the
// backup then finishes with a copy that passes quick_check.
func TestHardPressureCancelsTheLongReadButABackupOnlyWaits(t *testing.T) {
	db := readerFixture(t)
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		if _, err := ExecWrite(ctx, db, ClassInteractive, `INSERT INTO sample SELECT value FROM json_each('[1,2,3,4,5,6,7,8]')`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ExecWrite(ctx, db, ClassInteractive, `CREATE TABLE bulk(body BLOB); INSERT INTO bulk SELECT randomblob(4000) FROM json_each('[`+repeatOnes(256)+`]')`); err != nil {
		t.Fatal(err)
	}

	long, release, err := AcquireLongRead(ctx, LongReadIntegrity)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	started := make(chan struct{})
	ended := make(chan error, 1)
	go func() {
		ended <- WithReadSnapshot(long, db, func(snapshot context.Context) error {
			var n int
			if err := QueryRow(snapshot, db, `SELECT count(*) FROM sample`).Scan(&n); err != nil {
				return err
			}
			close(started)
			<-snapshot.Done()
			return QueryRow(snapshot, db, `SELECT count(*) FROM sample`).Scan(&n)
		})
	}()
	<-started
	background := ReadHandle(long, db)
	if background == db || background.Stats().MaxOpenConnections != 1 || background.Stats().InUse != 1 {
		t.Fatalf("long snapshot did not pin the sole background connection: %+v", background.Stats())
	}

	// The backup: one page per writer ticket, with a pause between steps so
	// it spans the recovery.
	dest := filepath.Join(t.TempDir(), "copy.sqlite")
	var steps atomic.Int64
	var finished atomic.Bool
	backupDone := make(chan error, 1)
	go func() {
		var backup *sqlite.Backup
		for {
			stepCtx, conn, releaseTicket, err := ReserveWriterConn(WithClass(ctx, ClassMaintenance), db, ClassMaintenance)
			if err != nil {
				backupDone <- err
				return
			}
			more := true
			err = conn.Raw(func(raw any) error {
				if backup == nil {
					b, err := RawConn(raw).(interface {
						NewBackup(string) (*sqlite.Backup, error)
					}).NewBackup(dest)
					if err != nil {
						return err
					}
					backup = b
				}
				var stepErr error
				more, stepErr = backup.Step(1)
				if stepErr == nil && !more {
					stepErr = backup.Finish()
				}
				return stepErr
			})
			_ = stepCtx
			releaseTicket()
			steps.Add(1)
			if err != nil || !more {
				finished.Store(true)
				backupDone <- err
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	for steps.Load() < 20 {
		time.Sleep(time.Millisecond)
	}
	if _, err = ExecWrite(ctx, db, ClassInteractive, `INSERT INTO sample VALUES(99)`); err != nil {
		t.Fatal(err)
	}
	recoveryStarted := time.Now()
	result := checkpoint(ctx, db, schedulePressureFile(t))
	if result.Err != nil || result.Outcome != "truncated" || time.Since(recoveryStarted) > CheckpointDeadline {
		t.Fatalf("hard pressure did not reset within its budget with a backup running: %+v", result)
	}
	spanned := !finished.Load()
	select {
	case err = <-ended:
		if !errors.Is(err, ErrReadPressure) {
			t.Fatalf("the long read ended with %v, want the retryable read-pressure refusal", err)
		}
	case <-time.After(CheckpointDeadline):
		t.Fatal("hard recovery left the long background snapshot running")
	}
	select {
	case err = <-backupDone:
		if err != nil {
			t.Fatalf("the backup failed instead of waiting: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the backup did not finish after the reset")
	}
	if !spanned {
		t.Fatal("the backup did not span the recovery; enlarge the fixture")
	}
	copy, err := OpenCandidate(filepath.Dir(dest), filepath.Base(dest))
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	lines, err := copy.Check(ctx, true, 1)
	if err != nil || !Healthy(lines) {
		t.Fatalf("copy after the reset: %v %v", lines, err)
	}
	var n int
	if err = copy.QueryRowContext(ctx, `SELECT count(*) FROM sample WHERE id=99`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("copy is missing a write made before it finished: %d %v", n, err)
	}
}

func repeatOnes(n int) string {
	out := make([]byte, 0, 2*n)
	for i := 0; i < n; i++ {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, '1')
	}
	return string(out)
}
