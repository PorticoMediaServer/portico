package compactcatalog

import (
	"context"
	"database/sql"
	"testing"
)

func TestSearchBatchPublishesActiveSlotChanges(t *testing.T) {
	db, show := failingFixture(t, "search-publication.sqlite")
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE catalog_entities SET title='Replacement' WHERE id=?`, show)
		return err
	})
	before := cc1Scalar(t, db, `SELECT revision FROM library_revisions WHERE library_id='tv'`)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM api_change_pending`); err != nil {
			return err
		}
		return deriveSearch(ctx, tx, mustJSON([]int64{show}))
	})
	after := cc1Scalar(t, db, `SELECT revision FROM library_revisions WHERE library_id='tv'`)
	hints := cc1Scalar(t, db, `SELECT count(*) FROM api_change_pending WHERE audience='library:tv' AND type='library.changed'`)
	if after <= before || hints != 1 {
		t.Fatalf("active search publication: revision %d -> %d, hints %d", before, after, hints)
	}
	w := NewWorker(db)
	if err := w.Rebuild(context.Background(), DomainSearch); err != nil {
		t.Fatal(err)
	}
	before = cc1Scalar(t, db, `SELECT revision FROM library_revisions WHERE library_id='tv'`)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM api_change_pending`); err != nil {
			return err
		}
		return deriveSearch(ctx, tx, mustJSON([]int64{show}))
	})
	if after = cc1Scalar(t, db, `SELECT revision FROM library_revisions WHERE library_id='tv'`); after != before {
		t.Fatalf("inactive search slot published early: %d -> %d", before, after)
	}
}
