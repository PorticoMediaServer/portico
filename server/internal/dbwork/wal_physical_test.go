package dbwork

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckpointDatabaseFileUsesReservedMaintenanceConnection(t *testing.T) {
	db := readerFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	maintenance, err := ReadHandle(WithClass(ctx, ClassMaintenance), db).Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer maintenance.Close()
	// Exhaust the foreground pool. Discovering the native filename must use
	// only the maintenance connection already held, including symlink handling.
	var foreground []*sql.Conn
	defer func() {
		for _, conn := range foreground {
			conn.Close()
		}
	}()
	for range db.Stats().MaxOpenConnections {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		foreground = append(foreground, conn)
	}
	file, err := checkpointDatabaseFile(ctx, maintenance)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(file)
	if err != nil || file != resolved {
		t.Fatalf("checkpoint did not receive native filename: file%q resolved%q err%v", file, resolved, err)
	}
}

func TestCheckpointHardPhysicalAllocationWithReusedSmallLogicalLog(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := readerFixture(t)
	ctx := context.Background()
	// One real committed transaction creates >192MiB. Its automatic/PASSIVE
	// checkpoint copies every page but leaves the file allocated. A later
	// single-page write reuses it, reducing logical frames without shrinking it.
	if _, err := ExecWrite(ctx, db, ClassInteractive, `CREATE TABLE large(id INTEGER PRIMARY KEY, body BLOB); WITH RECURSIVE ids(id) AS (VALUES(1) UNION ALL SELECT id+1 FROM ids WHERE id<25000) INSERT INTO large SELECT id,zeroblob(8192) FROM ids`); err != nil {
		t.Fatal(err)
	}
	maintenance, err := ReadHandle(WithClass(ctx, ClassMaintenance), db).Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mainFile, err := checkpointDatabaseFile(ctx, maintenance)
	maintenance.Close()
	if err != nil || mainFile == "" {
		t.Fatalf("native main filename unavailable: %v", err)
	}
	RegisterForegroundProbe(t.Name(), func() bool { return true })
	defer RegisterForegroundProbe(t.Name(), nil)
	for index, name := range []string{"direct-discovery", "cached-native-path", "settled-controller"} {
		t.Run(name, func(t *testing.T) {
			if index != 0 {
				// Fully change every payload byte in the same real database.
				// Random generation is unrelated to checkpoint correctness.
				if _, err := ExecWrite(ctx, db, ClassInteractive, `UPDATE large SET body=?`, bytes.Repeat([]byte{byte(index)}, 8192)); err != nil {
					t.Fatal(err)
				}
			}
			copied := copyWALForTest(t, ctx, db)
			if copied.LastError != "" || copied.FileBytes < CheckpointHardFileBytes || copied.BacklogFrames != 0 {
				t.Fatalf("large fixture was not fully copied: %+v", copied)
			}
			if _, err := ExecWrite(ctx, db, ClassInteractive, `INSERT INTO sample VALUES(?)`, 3+index); err != nil {
				t.Fatal(err)
			}
			reused := copyWALForTest(t, ctx, db)
			if reused.LastError != "" || reused.LogFrames >= CheckpointTruncateFrames || reused.FileBytes < CheckpointHardFileBytes || reused.BacklogFrames != 0 {
				t.Fatalf("fixture did not retain allocation behind small reused log: %+v", reused)
			}
			commits := ChangingCommits()
			var reset CheckpointResult
			if index == 0 {
				reset = Checkpoint(ctx, db)
			} else if index == 1 {
				reset = checkpoint(ctx, db, mainFile+"-wal")
			} else {
				schedule, err := NewCheckpointSchedule(ctx, db)
				if err != nil {
					t.Fatal(err)
				}
				// A prior fully copied result and unchanged commit counter
				// cannot suppress real physical pressure. Construction was
				// just completed, so the regular30s interval is not due.
				schedule.settled, schedule.settledAt = true, commits
				var attempted bool
				reset, attempted = schedule.Tick(ctx)
				if !attempted || !schedule.settled {
					t.Fatalf("settled controller skipped pressure or failed to settle actual shrink: attempted%t settled%t result%+v", attempted, schedule.settled, reset)
				}
				statements := Reads().Statements
				if _, repeated := schedule.Tick(ctx); repeated || Reads().Statements != statements {
					t.Fatal("shrunk settled controller repeated checkpoint SQL without new work")
				}
			}
			if reset.Err != nil || reset.Mode != "truncate" || reset.Outcome != "truncated" || reset.BeforeLogFrames >= CheckpointTruncateFrames || reset.BeforeFileBytes < CheckpointHardFileBytes || reset.LogFrames != 0 || reset.Checkpoint != 0 {
				t.Fatalf("retained physical WAL did not trigger hard reset: %+v", reset)
			}
			if ChangingCommits() != commits {
				t.Fatal("checkpoint needed a new commit to reset retained allocation")
			}
			if size := walFileBytes(ctx, db); size != 0 {
				t.Fatalf("physical hard reset retained%d bytes", size)
			}
			t.Logf("retained%dMiB with%d logical frames reset to0 bytes in%dms without new commits", reset.BeforeFileBytes>>20, reset.BeforeLogFrames, reset.DurationMs)
		})
	}
}
