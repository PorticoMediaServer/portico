package remotesources

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Regression source: callback reads must join the supplied exact snapshot or
// accepting writer, while the source lease keeps the original outside lifetime.
func TestSourceReadCallbacksCarryExactTransactionWithoutRetainingSnapshot(t *testing.T) {
	f, ref := isoObservationFixture(t)
	identities, owner, _ := observationOwner(t, f)
	original, cancel := context.WithCancel(context.Background())
	defer cancel()
	view, err := dbwork.BeginSnapshot(original, f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	var wantClass dbwork.Class
	wantWriter := false
	wantRead := view.Tx()
	lease, err := f.s.CaptureReadSourceTx(original, view.Tx(), SourceSessionAdmission{
		Purpose: SourcePlayback, Observation: ref, OnChanged: func() {},
		CheckTx: func(ctx context.Context, tx *sql.Tx, binding SourceReadBinding) error {
			calls++
			if dbwork.Snapshot(ctx) != tx || wantRead != nil && tx != wantRead || dbwork.InWriteTx(ctx) != wantWriter {
				t.Fatal("source callback read outside its exact supplied transaction")
			}
			if wantWriter {
				if class, ok := dbwork.WriteClass(ctx); !ok || class != wantClass {
					t.Fatal("source callback lost accepting writer class", class, ok)
				}
			}
			return currentLocalOwnerTx(ctx, tx, identities, owner)
		},
	})
	view.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if calls != 1 || dbwork.Snapshot(lease.life) != nil || dbwork.Snapshot(original) != nil {
		t.Fatal("initial callback was detached or snapshot became the lease lifetime", calls)
	}
	// The original first snapshot has ended. A fresh Check must still work and
	// lend only its own current transaction to the callback.
	wantRead = nil
	if err = lease.Check(original); err != nil {
		t.Fatal(err)
	}
	w, err := dbwork.Begin(original, f.s.db, dbwork.ClassEstablishedPlayback)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Rollback()
	wantWriter, wantClass, wantRead = true, dbwork.ClassEstablishedPlayback, w.Tx()
	if err = lease.CheckTx(original, w.Tx()); err != nil {
		t.Fatal(err)
	}
	w.Rollback()
	if calls != 3 || dbwork.Snapshot(lease.life) != nil || len(f.s.active) != 0 || len(f.s.serveLeases) != 0 {
		t.Fatal("callback escaped scope or opened source work", calls)
	}
}
