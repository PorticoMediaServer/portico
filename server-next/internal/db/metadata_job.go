package db

import (
	"context"
	"database/sql"
)

// Metadata selection jobs act as their account's main profile, including at each write.
func metadataJobViewer(ctx context.Context, tx *sql.Tx, jobID int64) (Viewer, access, error) {
	var v Viewer
	err := tx.QueryRowContext(ctx, `SELECT p.user_id, p.id FROM jobs j JOIN profiles p
		ON p.user_id = j.owner_user_id AND p.is_primary = 1 WHERE j.id = ?`, jobID).Scan(&v.UserID, &v.ProfileID)
	if err != nil {
		return v, access{}, err
	}
	a, err := loadAccess(ctx, tx, v)
	return v, a, err
}

// metadataJobItem clears an earlier failure in the same transaction as a successful save.
// A title hidden since the page was read cannot be changed by that job.
func metadataJobItem(tx *sql.Tx, jobID, itemID int64) error {
	if jobID == 0 {
		return nil
	}
	// The transaction already carries the write request's cancellation.
	v, a, err := metadataJobViewer(context.Background(), tx, jobID)
	if err != nil {
		return err
	}
	var visible bool
	err = tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM items i WHERE i.id = @item AND `+a.visible("i")+`)`,
		append(v.args(), sql.Named("item", itemID))...).Scan(&visible)
	if err != nil {
		return err
	}
	if !visible {
		return ErrNotFound
	}
	_, err = tx.Exec(`DELETE FROM job_item_failures WHERE job_id = ? AND item_id = ?`, jobID, itemID)
	return err
}

// RecordMetadataJobOutcome records a refusal without copying its underlying error. A title
// gone or hidden since the attempt is skipped; a no-op success clears its earlier refusal.
func (d *DB) RecordMetadataJobOutcome(ctx context.Context, jobID int64, item, code string) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		v, a, err := metadataJobViewer(ctx, tx, jobID)
		if err != nil {
			return err
		}
		var visible bool
		err = tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM items i WHERE i.public_id = @item AND `+a.visible("i")+`)`,
			append(v.args(), sql.Named("item", item))...).Scan(&visible)
		if err != nil {
			return err
		}
		if !visible {
			code = ""
		}
		return jobItemOutcome(tx, jobID, item, code)
	})
}

// JobItemFailed keeps a partially refreshed title retryable even if its root details were
// saved before a later provider request for its children failed.
func (d *DB) JobItemFailed(ctx context.Context, jobID, itemID int64) (bool, error) {
	var failed bool
	err := d.read.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM job_item_failures WHERE job_id = ? AND item_id = ?)`, jobID, itemID).Scan(&failed)
	return failed, err
}
