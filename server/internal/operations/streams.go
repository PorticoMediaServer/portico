package operations

import (
	"context"
	"database/sql"
)

type StreamOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type StreamOptions struct {
	Libraries []StreamOption `json:"libraries"`
	Viewers   []StreamOption `json:"viewers"`
}

// StreamOptions facets were driven by /v2 occurrences. Active streams are now
// listed by the v1 admin sessions API; facets return empty until that UI moves.
func (s *Store) StreamOptions(ctx context.Context, auth Authorize) (out StreamOptions, err error) {
	out.Libraries = []StreamOption{}
	out.Viewers = []StreamOption{}
	err = s.snapshot(ctx, auth, "", func(tx *sql.Tx) error { return nil })
	return out, err
}

// The console owner can select local accounts without copying opaque identifiers.
func (s *Store) LocalAccountOptions(ctx context.Context, auth Authorize) (out []StreamOption, err error) {
	out = []StreamOption{}
	err = s.snapshot(ctx, auth, "", func(tx *sql.Tx) error {
		rows, e := tx.QueryContext(ctx, `SELECT a.id,a.username FROM accounts a JOIN direct_memberships m ON m.account_id=a.id WHERE m.disabled=0 ORDER BY a.username LIMIT 1000`)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var v StreamOption
			if e = rows.Scan(&v.ID, &v.Label); e != nil {
				return e
			}
			out = append(out, v)
		}
		return rows.Err()
	})
	return
}
