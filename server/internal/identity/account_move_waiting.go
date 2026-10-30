package identity

import (
	"context"
	"database/sql"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// MoveWaiting is one account that still needs its link or code for the active
// move: the id the codes route accepts, the username the owner recognises, and
// the expiry of a code that already exists and still works. It never carries a
// code, hash or password material.
type MoveWaiting struct {
	AccountID string `json:"accountId"`
	Username  string `json:"username"`
	// ExpiresAt is the existing receipt's own expiry; absent when this person has
	// no working code yet, never a guess or a fresh issue.
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// MoveWaitingList is bounded and ordered by username. Truncated says the waiting
// population is larger than this read lists; the list is never presented as
// complete when it is not.
type MoveWaitingList struct {
	Items     []MoveWaiting `json:"items"`
	Truncated bool          `json:"truncated"`
}

// MoveWaiting reads the accounts waiting on the active move, from the same
// owner-fenced rows the codes writer validates against. The list is descriptive:
// it issues no code and admits nobody. Because a revocation or demotion during
// the read cannot be observed inside the read's own snapshot, the initiating
// bearer is proved again in a fresh short snapshot immediately before delivery.
func (s *Service) MoveWaiting(ctx context.Context, bearer string) (out MoveWaitingList, err error) {
	if s == nil || s.db == nil {
		return MoveWaitingList{Items: []MoveWaiting{}}, ErrDirectInput
	}
	items, err := s.moveWaitingRows(ctx, bearer)
	if err != nil {
		return MoveWaitingList{Items: []MoveWaiting{}}, err
	}
	truncated := false
	if len(items) > 2000 {
		// One extra row proves more people are waiting than one sheet lists.
		items = items[:2000]
		truncated = true
	}
	// The read's rows and snapshot are closed by now; prove the same owner again.
	if err = s.proveMoveOwner(ctx, bearer); err != nil {
		return MoveWaitingList{Items: []MoveWaiting{}}, err
	}
	return MoveWaitingList{Items: items, Truncated: truncated}, nil
}

// moveWaitingRows reads and closes its own rows and snapshot before returning.
func (s *Service) moveWaitingRows(ctx context.Context, bearer string) (items []MoveWaiting, err error) {
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return nil, err
	}
	defer snapshot.Rollback()
	if _, err = s.moveWebOwnerTx(ctx, snapshot.Tx(), bearer); err != nil {
		return nil, err
	}
	// The left join reads each waiting person's existing code expiry from the same
	// move's real receipt; an expired code is not reported because it no longer works.
	rows, err := snapshot.Tx().QueryContext(ctx, `SELECT a.id,a.username,c.expires_ms FROM identity_move_waiting w JOIN accounts a ON a.id=w.account_id JOIN identity_moves m ON m.id=w.move_id LEFT JOIN identity_move_codes c ON c.account_id=w.account_id AND c.move_id=w.move_id AND c.expires_ms>? WHERE w.state='waiting' AND m.stage IN('moving_to_local','moving_to_portico') ORDER BY a.username,a.id LIMIT 2001`, time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items = make([]MoveWaiting, 0, 16)
	for rows.Next() {
		var item MoveWaiting
		var expires sql.NullInt64
		if err = rows.Scan(&item.AccountID, &item.Username, &expires); err != nil {
			return nil, err
		}
		if expires.Valid {
			item.ExpiresAt = time.UnixMilli(expires.Int64).UTC().Format(time.RFC3339)
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// proveMoveOwner is the short, fresh owner proof the delivery depends on.
func (s *Service) proveMoveOwner(ctx context.Context, bearer string) error {
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return err
	}
	if _, err = s.moveWebOwnerTx(ctx, snapshot.Tx(), bearer); err != nil {
		_ = snapshot.Rollback()
		return err
	}
	// End the proving snapshot before the caller delivers anything.
	return snapshot.Rollback()
}
