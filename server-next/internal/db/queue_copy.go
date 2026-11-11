package db

import (
	"context"
	"database/sql"
	"errors"
)

// QueueToCopy is another device's queue as a new device of the same account takes it on, when
// playback moves between them.
type QueueToCopy struct {
	// Items are the titles in the order they play, only those the new viewer may see.
	Items []int64
	// Start is where the copied queue's current entry stands in Items.
	Start       int
	ShuffleSeed *int64
	Selection   string
}

// CopyableQueue reads a queue of any profile of the viewer's account for the viewer to take
// on: its titles in play order, without those the viewer may not see. A queue of another
// account is not found.
func (d *DB) CopyableQueue(ctx context.Context, v Viewer, publicID string) (QueueToCopy, error) {
	var out QueueToCopy
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return out, err
	}
	var queueID, current int64
	var seed sql.NullInt64
	var selection sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT q.id, ifnull(q.current_entry_id, 0), q.shuffle_seed, q.selection FROM play_queues q
		JOIN profiles p ON p.id = q.profile_id WHERE q.public_id = ? AND p.user_id = ?`, publicID, v.UserID).
		Scan(&queueID, &current, &seed, &selection)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if seed.Valid {
		out.ShuffleSeed = &seed.Int64
	}
	out.Selection = selection.String
	rows, err := tx.QueryContext(ctx, `SELECT e.id, e.item_id FROM queue_entries e JOIN items i ON i.id = e.item_id
		WHERE e.queue_id = @queue AND `+acc.visible("i")+` ORDER BY e.position LIMIT @limit`,
		append(v.args(), sql.Named("queue", queueID), sql.Named("limit", QueueMax))...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var entry, item int64
		if err := rows.Scan(&entry, &item); err != nil {
			return out, err
		}
		if entry == current {
			out.Start = len(out.Items)
		}
		out.Items = append(out.Items, item)
	}
	return out, rows.Err()
}
