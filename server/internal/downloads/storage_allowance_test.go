package downloads

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// A4: admission summed every stored claim for each converted item a batch
// admitted. The allowance sums once per transaction and counts its own
// admissions, so it admits and refuses exactly as a fresh sum would: it sums
// again only when it is about to refuse after the transaction ended claims.
func TestStorageAllowanceTracksABatch(t *testing.T) {
	h := newHarness(t)
	if _, e := h.db.Exec(`UPDATE download_settings SET max_prepared_bytes=100`); e != nil {
		t.Fatal(e)
	}
	if _, e := h.db.Exec(`INSERT INTO download_preparations(id,profile_key,authority,account_id,profile_id,item_id,library_id,quality,origin,state,bytes_total,created_ms,updated_ms) VALUES('stored',?,'local','account','profile',?,'lib','720p-high','item','ready',30,1,1)`, operations.ViewerKey(h.viewer), h.itemID); e != nil {
		t.Fatal(e)
	}
	e := dbwork.WithWriteTx(context.Background(), h.db, dbwork.ClassForegroundTransfer, func(tx *sql.Tx) error {
		room := newStorageAllowance(tx)
		if e := room.admit("720p-high", 60); e != nil {
			t.Fatalf("60 of the 70 left: %v", e)
		}
		if e := room.admit("720p-high", 20); e != ErrStorageFull {
			t.Fatalf("20 of the 10 left: %v", e)
		}
		if e := room.admit(QualityOriginal, 1000); e != nil {
			t.Fatalf("an original takes no room: %v", e)
		}
		if e := room.admit("720p-high", 10); e != nil {
			t.Fatalf("the last 10: %v", e)
		}
		// Ending a stored claim frees its room; the allowance sums again rather
		// than refusing on the old total. (Nothing here wrote the admitted claims,
		// so the new sum is the 0 left in the table.)
		if _, e := tx.Exec(`UPDATE download_preparations SET state='cancelled' WHERE id='stored'`); e != nil {
			return e
		}
		room.ended()
		if e := room.admit("720p-high", 90); e != nil {
			t.Fatalf("after a claim ended: %v", e)
		}
		if room.reserved != 90 || !room.exact {
			t.Fatalf("reserved %d exact %v", room.reserved, room.exact)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
