package livechannels_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestEmptyPhysicalReconcileDoesNotPublishAChange(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	locks, err := livechannels.NewPhysicalLocks(filepath.Join(root, "locks"))
	if err != nil {
		t.Fatal(err)
	}
	before := dbwork.ChangingCommits()
	for n := 0; n < 3; n++ {
		count, err := locks.ReconcileExpired(context.Background(), db, time.Now())
		if err != nil || count != 0 {
			t.Fatalf("idle reconcile %d: %d, %v", n, count, err)
		}
	}
	if got := dbwork.ChangingCommits(); got != before {
		t.Fatalf("idle reconciliation published %d false changes", got-before)
	}
}
