package playback

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Used only after authentication/transactional authority. A token generation
// change within one durable family preserves an occurrence; a different family
// (including another device for the same profile) is not an ownership match.
// Both placeholders are the caller's authenticated token hash.
const sessionFamilyMatch = `(session_hash=? OR EXISTS(
 SELECT 1 FROM authorization_family_tokens original
 JOIN authorization_family_tokens caller ON caller.family_id=original.family_id
 JOIN authorization_session_families family ON family.id=caller.family_id
 WHERE original.token_hash=playback_sessions.session_hash AND caller.token_hash=?
 AND caller.retired=0 AND family.revoked=0 AND caller.generation=family.current_generation
))`

// Stored idempotency keys are authority-qualified without changing the public
// request ID or exposing the viewer tuple in a delivery URL.
func sessionRequestKey(p identity.Principal, request string) string {
	return "scoped:" + identity.Digest(identity.PersonalKey(p.Viewer)+"\x00"+request)
}

// CheckStopOccurrenceTx attenuates an already strict-authenticated caller to its
// original legacy occurrence. It grants no access and never adopts another
// device family or generation. The accepting writer repeats this check.
func (s *Service) CheckStopOccurrenceTx(ctx context.Context, tx *sql.Tx, p identity.Principal, id string, generation int, item string) error {
	if tx == nil || dbwork.Snapshot(ctx) != tx || generation < 1 {
		return ErrOccurrenceChanged
	}
	var found int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM playback_sessions WHERE id=? AND generation=? AND account_id=? AND profile_id=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND `+sessionFamilyMatch, id, generation, p.AccountID, p.ProfileID, item, p.Hash, p.Hash).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrOccurrenceChanged
	}
	return err
}
