package dbwork

import (
	"context"
	"database/sql"
	"sync"
	"testing"
)

// The driver seam holds the copying boundary, not a writer transaction. The
// physical-WAL tests separately exercise real SQLite copying, reader pressure
// and file truncation.
//
// Under hard pressure a writer that crosses the first copy no longer defers
// the reset: the recovery attempt holds new writers, lets the holder finish,
// copies the new tail outside the gate and proves the copy under its pause, so
// the reset has a progress path. What must still never happen is a reset after
// the write generation moved past the proof copy: that would copy a tail under
// the gate.
func TestCheckpointDoesNotResetAcrossAnotherWriter(t *testing.T) {
	for _, activeAtCopy := range []bool{false, true} {
		name := "writer-between-copies"
		if activeAtCopy {
			name = "writer-already-in-flight"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			var release func()
			if activeAtCopy {
				var err error
				release, err = WriteGate().Acquire(ctx, ClassBackgroundMedia)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			}
			var once sync.Once
			conn := &checkpointCleanupConn{busy: 3210, checkpoint: func() {
				once.Do(func() {
					if release != nil {
						release()
						return
					}
					free, err := WriteGate().Acquire(ctx, ClassBackgroundMedia)
					if err != nil {
						t.Fatal(err)
					}
					free()
				})
			}}
			db := sql.OpenDB(checkpointCleanupConnector{conn})
			defer db.Close()
			result := checkpoint(ctx, db, schedulePressureFile(t))
			if result.Err != nil || result.Mode != "truncate" {
				t.Fatalf("a writer before the proof copy stranded the reset: %+v", result)
			}
		})
	}
	t.Run("generation-moved-after-proof-copy", func(t *testing.T) {
		ctx := context.Background()
		calls := 0
		conn := &checkpointCleanupConn{busy: 3210, checkpoint: func() {
			// The first pass, the copy after the reader drain, then the proof
			// copy under the pause. Moving the generation there stands for a
			// writer the pause failed to stop.
			if calls++; calls == 3 {
				gate := WriteGate()
				gate.mu.Lock()
				gate.writeGeneration++
				gate.mu.Unlock()
			}
		}}
		db := sql.OpenDB(checkpointCleanupConnector{conn})
		defer db.Close()
		path := schedulePressureFile(t)
		result := checkpoint(ctx, db, path)
		if result.Err != nil || result.Mode != "passive" {
			t.Fatalf("reset raced a writer after the proof copy: %+v", result)
		}
		// The next pass, with no intervening writer, must still reset: a
		// defensive fence cannot strand permanent physical WAL pressure.
		conn.checkpoint = nil
		result = checkpoint(ctx, db, path)
		if result.Err != nil || result.Mode != "truncate" {
			t.Fatalf("reset did not recover at a clean copy boundary: %+v", result)
		}
	})
}
