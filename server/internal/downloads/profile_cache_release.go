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

// ProfileCacheRelease reports one bounded release of a profile's prepared
// server copies. Offline permissions (receipts) are untouched: a device keeps
// the copy it already has, and the server prepares it again when needed.
type ProfileCacheRelease struct {
	Claims   int  `json:"claims"`
	Retained int  `json:"retained"`
	More     bool `json:"more"`
}

// ReleaseProfileCache releases a bounded page of this profile's open or ready
// claims without withdrawing offline permissions, and leaves a claim with a
// live transfer grant alone. Repeat with a new operation id while More is true.
func (s *Service) ReleaseProfileCache(ctx context.Context, p identity.Principal, key, operation string, authorize func(*sql.Tx) error) (ProfileCacheRelease, error) {
	out := ProfileCacheRelease{}
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
	scope := "downloads-profile-cache-release:" + operations.ViewerKey(p)
	raw, digest, err := operations.Receipt(tx, scope, operation, key, now)
	if err != nil {
		return out, err
	}
	if raw != "" {
		return out, json.Unmarshal([]byte(raw), &out)
	}
	live := `('queued','running','ready','paused','failed')`
	lease := `EXISTS(SELECT 1 FROM download_grants g WHERE g.preparation_id=p.id AND g.revoked=0 AND g.expires_ms>?)`
	rows, err := tx.QueryContext(ctx, `SELECT id FROM download_preparations p WHERE p.profile_key=? AND p.state IN('queued','running','ready','paused','failed')
 AND NOT EXISTS(SELECT 1 FROM download_grants g WHERE g.preparation_id=p.id AND g.revoked=0 AND g.expires_ms>?)
 ORDER BY p.id LIMIT 100`, key, now)
	if err != nil {
		return out, err
	}
	claims, err := collectIDs(rows)
	if err != nil {
		return out, err
	}
	for _, id := range claims {
		if err = endClaim(ctx, tx, id, StateCancelled, ReasonCacheReleased, now, live); err != nil {
			return out, err
		}
		out.Claims++
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM download_preparations p WHERE p.profile_key=? AND p.state IN('queued','running','ready','paused','failed') AND `+lease, key, now).Scan(&out.Retained); err != nil {
		return out, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM download_preparations p WHERE p.profile_key=? AND p.state IN('queued','running','ready','paused','failed')
 AND NOT EXISTS(SELECT 1 FROM download_grants g WHERE g.preparation_id=p.id AND g.revoked=0 AND g.expires_ms>?))`, key, now).Scan(&out.More); err != nil {
		return out, err
	}
	if err = operations.SaveReceipt(tx, scope, operation, digest, out, now); err != nil {
		return out, err
	}
	if err = operations.Audit(tx, now, operations.AccountKey(p), "downloads.profile.cache_release", key, int64(out.Claims)); err != nil {
		return out, err
	}
	return out, write.Commit()
}
