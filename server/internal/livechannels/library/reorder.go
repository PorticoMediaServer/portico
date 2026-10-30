package librarychannels

import (
	"context"
	"database/sql"
	"errors"
)

type OrderEntry struct {
	ID               string `json:"id"`
	ExpectedRevision int64  `json:"expectedRevision"`
}
type ReorderInput struct {
	RequestID string       `json:"requestId"`
	Channels  []OrderEntry `json:"channels"`
}

// Reorder is an atomic compare-and-swap of the entire small channel directory.
// It does not perturb seeds, programme identity or accepted playback entries.
func (s *Store) Reorder(ctx context.Context, a Authority, in ReorderInput) error {
	if !validID(in.RequestID) || len(in.Channels) > MaxChannels {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, v := range in.Channels {
		if !validID(v.ID) || seen[v.ID] || v.ExpectedRevision < 1 {
			return ErrInvalid
		}
		seen[v.ID] = true
	}
	hash := digest("reorder", encode(in))
	return s.transaction(ctx, a, true, func(tx *sql.Tx, scope Scope) error {
		var prior string
		e := tx.QueryRowContext(ctx, `SELECT digest FROM lc_receipts WHERE request_id=?`, in.RequestID).Scan(&prior)
		if e == nil {
			if prior != hash {
				return ErrConflict
			}
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return unavailable(e)
		}
		var count int
		if cause := tx.QueryRowContext(ctx, `SELECT count(*) FROM lc_channels WHERE removed=0`).Scan(&count); cause != nil {
			return unavailable(cause)
		}
		if count != len(in.Channels) {
			return ErrConflict
		}
		for position, v := range in.Channels {
			c, e := readChannel(ctx, tx, v.ID)
			if e != nil {
				return e
			}
			if c.Revision != v.ExpectedRevision {
				return ErrConflict
			}
			c.Config.Position = position
			if _, e = tx.ExecContext(ctx, `UPDATE lc_channels SET position=?,config_json=?,revision=revision+1 WHERE id=? AND revision=?`, position, encode(c.Config), v.ID, v.ExpectedRevision); e != nil {
				return unavailable(e)
			}
			// Only the ordering field changed. The worker sees this exact revision under
			// the same transaction, with candidate/rule cursors left intact.
			if _, e = tx.ExecContext(ctx, `UPDATE lc_generations SET config_revision=?,config_json=? WHERE channel_id=? AND status='pending'`, v.ExpectedRevision+1, encode(c.Config), v.ID); e != nil {
				return unavailable(e)
			}
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO lc_receipts VALUES(?,?,'{}')`, in.RequestID, hash)
		return e
	})
}
