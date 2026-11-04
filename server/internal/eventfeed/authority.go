package eventfeed

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// authoritySinceTx projects only the latest own-account membership revision.
// It can accompany a role-bound cursor's resync without replaying old authority.
func (h *Hub) authoritySinceTx(ctx context.Context, tx *sql.Tx, p identity.Principal, after, maximum int64, next string) (Event, bool, error) {
	var revision string
	var at int64
	err := tx.QueryRowContext(ctx, `SELECT revision,at_ms FROM api_events
 WHERE audience=? AND id>? AND id<=? AND type='authority.changed'
 AND resource_kind='account' AND resource_id='self' AND data=''
 AND length(CAST(revision AS BLOB)) BETWEEN 1 AND ? ORDER BY id DESC LIMIT 1`,
		apievents.AuthorityAudience(p.Authority, p.AccountID), after, maximum, eventFieldBytes).Scan(&revision, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, false, nil
	}
	if err != nil {
		return Event{}, false, err
	}
	return Event{ID: next, Type: "authority.changed", At: time.UnixMilli(at).UTC().Format("2006-01-02T15:04:05.000Z"), Resource: Resource{Kind: "account", ID: "self"}, Revision: revision}, true, nil
}

// AuthoritySince is the final safe projection for an authenticated reader whose
// old private capacity admission no longer holds after a role change. The caller
// must authenticate live first. No ordinary event or capacity data is read.
func (h *Hub) AuthoritySince(ctx context.Context, p identity.Principal, cursor string) (Page, error) {
	out := Page{Events: []Event{}}
	gate, err := dbwork.BeginSnapshot(ctx, h.DB)
	if err != nil {
		return out, err
	}
	defer gate.Rollback()
	tx := gate.Tx()
	var device string
	if err := tx.QueryRowContext(ctx, `SELECT device_id FROM identity_device_families b JOIN authorization_family_tokens t ON t.family_id=b.family_id WHERE t.token_hash=?`, p.Hash).Scan(&device); err != nil {
		return out, err
	}
	admin := p.Role == "owner" || p.Role == "admin"
	bound := cursorBinding(p, device, admin)
	after, opened := h.keys.open(cursor, bound)
	if !opened {
		after, opened = h.keys.open(cursor, cursorBinding(p, device, !admin))
	}
	if !opened {
		return out, gate.Commit()
	}
	var minimum, maximum int64
	if err := tx.QueryRowContext(ctx, eventBoundsSQL).Scan(&minimum, &maximum); err != nil {
		return out, err
	}
	out.NextAfter = h.keys.seal(maximum, bound)
	event, found, err := h.authoritySinceTx(ctx, tx, p, after, maximum, out.NextAfter)
	if err != nil {
		return out, err
	}
	if found {
		out.Events = append(out.Events, event)
	}
	return out, gate.Commit()
}
