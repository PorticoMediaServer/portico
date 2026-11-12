package db

import (
	"context"
	"database/sql"
	"errors"
)

// QueueNextItem is the title that plays after the current one in a queue, read as the profile
// the queue belongs to; ErrNotFound when nothing follows. Whether someone else may see it is
// the caller's to check.
func (d *DB) QueueNextItem(ctx context.Context, owner Viewer, publicID string) (int64, error) {
	q, err := d.Queue(ctx, owner, publicID)
	if err != nil {
		return 0, err
	}
	if q.NextEntry == 0 || q.NextEntry == q.CurrentEntry {
		return 0, ErrNotFound
	}
	var item int64
	err = d.read.QueryRowContext(ctx, `SELECT item_id FROM queue_entries WHERE id = ? AND queue_id = ?`, q.NextEntry, q.ID).Scan(&item)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return item, err
}
