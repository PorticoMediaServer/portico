package dbwork

import (
	"context"
	"testing"
	"time"
)

// The read pools follow the host: a one-core VPS keeps a small footprint, a
// large host is not held to six connections, and background loops always have
// more than one connection between them (J-18).
func TestReadPoolsScaleWithTheHost(t *testing.T) {
	for _, tc := range []struct{ cpus, foreground, background int }{
		{1, 4, 2}, {2, 4, 2}, {4, 8, 2}, {6, 12, 3}, {12, 16, 6}, {64, 16, 8},
	} {
		foreground, background := hostReaders(tc.cpus)
		if foreground != tc.foreground || background != tc.background {
			t.Errorf("%d cores: %d foreground and %d background connections, want %d and %d", tc.cpus, foreground, background, tc.foreground, tc.background)
		}
	}
	policy := DefaultPolicy()
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}
	if cap(backgroundSnapshotPermits) < 1 || cap(backgroundSnapshotPermits) >= policy.BackgroundConns && policy.BackgroundConns > 1 {
		t.Fatalf("background snapshots take %d of %d connections", cap(backgroundSnapshotPermits), policy.BackgroundConns)
	}
}

// J-18: a background loop's policy check opened a snapshot on the one
// background connection while the scanner held it, and timed out after half a
// second. Another background reader no longer blocks it.
func TestABackgroundSnapshotOpensWhileAnotherBackgroundReaderWorks(t *testing.T) {
	db := testDB(t)
	ctx := WithClass(context.Background(), ClassBackgroundMedia)
	busy, err := ReadHandle(ctx, db).Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	wait, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	snapshot, err := BeginSnapshot(wait, db)
	if err != nil {
		t.Fatalf("a background snapshot waited on another background reader: %v", err)
	}
	snapshot.Rollback()
}
