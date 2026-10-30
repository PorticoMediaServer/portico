package downloads

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/selectionactions"
)

func TestSelectionTransfersCleanupCompletedAndCancelledDoNotAccumulate(t *testing.T) {
	h := selectionDownloadHarness(t, 0)
	ctx := context.Background()
	for i := 0; i < 43; i++ {
		job := selectionactions.Job{Action: "download", OperationID: fmt.Sprintf("finished-%d", i), State: "complete", Total: 1}
		if i%2 != 0 {
			job.State = "cancelled"
		}
		if err := dbwork.WithWriteTx(ctx, h.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			if err := h.service.OpenSelectionActionTransferTx(ctx, tx, h.viewer, job, selectionDownloadOptions()); err != nil {
				return err
			}
			return h.service.CloseSelectionActionTransferTx(ctx, tx, h.viewer, job, selectionDownloadOptions())
		}); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := h.db.QueryRow(`SELECT count(*) FROM download_action_transfers WHERE operation_id LIKE 'finished-%' AND closed=1`).Scan(&n); err != nil || n != 43 {
		t.Fatal(n, err)
	}
	h.now = h.now.Add(2 * time.Hour)
	for turn := 0; turn < 8; turn++ {
		if err := h.service.cleanupSelectionActionTransfers(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.db.QueryRow(`SELECT count(*) FROM download_action_transfers WHERE operation_id LIKE 'finished-%'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("completed/cancelled empty headers accumulated", n, err)
	}
	if err := h.db.QueryRow(`SELECT count(*) FROM download_action_transfers WHERE closed=0`).Scan(&n); err != nil || n != 3 {
		t.Fatal("active ownership was purged", n, err)
	}
}
func TestSelectionTransferClosureOutlivesHistoryOnlyWhileActualClaimNeedsIt(t *testing.T) {
	h := selectionDownloadHarness(t, 0)
	ctx := context.Background()
	target := selectionDownloadTarget(t, h, h.item, h.itemID)
	if err := h.service.ApplySelectionDownload(ctx, h.viewer, "download", selectionDownloadOptions(), target, nil, selectionDownloadAuthorize, func(*sql.Tx, selectionactions.Effect) error { return nil }); err != nil {
		t.Fatal(err)
	}
	job := selectionactions.Job{Action: "download", OperationID: target.OperationID, State: "complete", Total: 1}
	if err := dbwork.WithWriteTx(ctx, h.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		return h.service.CloseSelectionActionTransferTx(ctx, tx, h.viewer, job, selectionDownloadOptions())
	}); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(2 * time.Hour) // command history can already be gone
	for turn := 0; turn < 3; turn++ {
		if err := h.service.cleanupSelectionActionTransfers(ctx); err != nil {
			t.Fatal(err)
		}
	}
	page, err := h.service.ListSelectionAction(ctx, h.viewer, job.OperationID, "device", "", 20)
	if err != nil || page.Closed == nil || !*page.Closed || len(page.Items) != 1 {
		t.Fatalf("lost closure/preparation after short history: %+v %v", page, err)
	}
	if _, err = h.db.Exec(`DELETE FROM download_preparations WHERE id=?`, page.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	for turn := 0; turn < 3; turn++ {
		if err = h.service.cleanupSelectionActionTransfers(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = h.service.ListSelectionAction(ctx, h.viewer, job.OperationID, "device", "", 20); !errors.Is(err, ErrNotFound) {
		t.Fatal("no-claim header must expire honestly", err)
	}
	var claims int
	if err = h.db.QueryRow(`SELECT count(*) FROM download_action_claims WHERE profile_key=? AND operation_id=?`, operations.ViewerKey(h.viewer), job.OperationID).Scan(&claims); err != nil || claims != 0 {
		t.Fatal("preparation FK did not release actual ownership", claims, err)
	}
}
