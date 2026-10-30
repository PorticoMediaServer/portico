package recordingaccess

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// BatchCachedPolicy reads current policy once in the caller's actual snapshot.
// Unrecognised compositions retain the ordinary per-library authority path.
type BatchCachedPolicy interface {
	AllowedSetTxContext(context.Context, identity.Principal, []string, *sql.Tx) (map[string]bool, error)
}

func (p Policy) AllowedSetTx(ctx context.Context, tx *sql.Tx, who identity.Principal, libraries []string) (map[string]bool, error) {
	out := map[string]bool{}
	if tx == nil {
		return nil, identity.ErrUnauthorized
	}
	batch, ok := p.Cached.(BatchCachedPolicy)
	if who.Authority != "local" || p.Cached != nil && !ok {
		for _, id := range libraries {
			err := p.AllowedTxContext(ctx, who, id, tx)
			if err == nil {
				out[id] = true
			} else if errors.Is(err, identity.ErrUnauthorized) {
				out[id] = false
			} else {
				return nil, err
			}
		}
		return out, nil
	}
	if _, err := localAccess(ctx, tx, p.Schema, who, ""); err != nil {
		if errors.Is(err, identity.ErrUnauthorized) {
			return out, nil
		}
		return nil, err
	}
	targets := append(append([]string{}, libraries...), "")
	var err error
	if p.Cached != nil {
		out, err = batch.AllowedSetTxContext(ctx, who, targets, tx)
	} else {
		var all bool
		var ids []string
		all, ids, _, _, err = identity.DirectAccess(tx, who)
		grants := map[string]bool{}
		for _, id := range ids {
			grants[id] = true
		}
		for _, id := range targets {
			out[id] = id == "" || all || grants[id]
		}
	}
	if err != nil {
		if errors.Is(err, identity.ErrUnauthorized) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	raw, _ := json.Marshal(libraries)
	rows, err := tx.QueryContext(ctx, `SELECT library_id,authority,account_id,profile_id FROM dvr_private_libraries WHERE library_id IN(SELECT value FROM json_each(?))`, string(raw))
	if err != nil {
		return nil, err
	}
	type private struct{ id, authority, account, profile string }
	var privateRows []private
	for rows.Next() {
		var r private
		if err = rows.Scan(&r.id, &r.authority, &r.account, &r.profile); err != nil {
			break
		}
		privateRows = append(privateRows, r)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	allowDVR := false
	if len(privateRows) > 0 {
		restrictions, e := identity.RestrictionsForViewerTx(ctx, tx, who.Viewer)
		if e != nil {
			return nil, e
		}
		allowDVR = restrictions.AllowDVR
	}
	for _, r := range privateRows {
		out[r.id] = r.authority == who.Authority && r.account == who.AccountID && r.profile == who.ProfileID && allowDVR && out[""]
	}
	delete(out, "")
	return out, ctx.Err()
}
