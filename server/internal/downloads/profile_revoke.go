package downloads

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

type ProfileRevocation struct {
	Claims   int  `json:"claims"`
	Receipts int  `json:"receipts"`
	More     bool `json:"more"`
}

// RevokeProfile withdraws a bounded page of this profile's claims and offline
// permissions. Repeat with a new operation id while More is true.
func (s *Service) RevokeProfile(ctx context.Context, p identity.Principal, key, operation string, authorize func(*sql.Tx) error) (ProfileRevocation, error) {
	out := ProfileRevocation{}
	if len(key) == 0 || len(key) > 1024 || strings.ContainsAny(key, "\x00\r\n") || !validID.MatchString(operation) || authorize == nil {
		return out, ErrInput
	}
	write, err := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if err != nil {
		return out, err
	}
	defer write.Rollback()
	tx := write.Tx()
	if err = authorize(tx); err != nil {
		return out, err
	}
	now := s.millis()
	scope := "downloads-profile-revoke:" + operations.ViewerKey(p)
	raw, digest, err := operations.Receipt(tx, scope, operation, key, now)
	if err != nil {
		return out, err
	}
	if raw != "" {
		return out, json.Unmarshal([]byte(raw), &out)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM download_preparations WHERE profile_key=? AND state IN('queued','running','ready','paused','failed') ORDER BY id LIMIT 100`, key)
	if err != nil {
		return out, err
	}
	claims, err := collectIDs(rows)
	if err != nil {
		return out, err
	}
	for _, id := range claims {
		if err = endClaim(ctx, tx, id, StateCancelled, ReasonRevoked, now, `('queued','running','ready','paused','failed')`); err != nil {
			return out, err
		}
		out.Claims++
	}
	rows, err = tx.QueryContext(ctx, `SELECT id FROM download_receipts WHERE profile_key=? AND revoked_ms=0 ORDER BY id LIMIT 100`, key)
	if err != nil {
		return out, err
	}
	receipts, err := collectIDs(rows)
	if err != nil {
		return out, err
	}
	for _, id := range receipts {
		if err = revokeReceipt(tx, id, ReasonRevoked, now); err != nil {
			return out, err
		}
		out.Receipts++
	}
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM download_preparations WHERE profile_key=?1 AND state IN('queued','running','ready','paused','failed')) OR EXISTS(SELECT 1 FROM download_receipts WHERE profile_key=?1 AND revoked_ms=0)`, key).Scan(&out.More); err != nil {
		return out, err
	}
	if err = operations.SaveReceipt(tx, scope, operation, digest, out, now); err != nil {
		return out, err
	}
	if err = operations.Audit(tx, now, operations.AccountKey(p), "downloads.profile.revoke", key, int64(out.Claims+out.Receipts)); err != nil {
		return out, err
	}
	return out, write.Commit()
}
