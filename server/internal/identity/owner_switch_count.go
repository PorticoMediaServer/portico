package identity

import (
	"context"
	"database/sql"
)

// ownerSwitchCountSQL visits at most 10001 other account keys through the
// primary-key index. The two ranges exclude the owner without joining profiles,
// memberships or invitations: retained disabled/Waiting accounts still count.
const ownerSwitchCountSQL = `SELECT count(*) FROM (
 SELECT id FROM accounts WHERE id<?1
 UNION ALL
 SELECT id FROM accounts WHERE id>?1
 LIMIT 10001
)`

// captureOwnerSwitchCountTx must use the authenticated switch preview's own
// transaction. Nil means the exact informational count exceeds the bound; it
// must be omitted, never truncated or interpreted as zero.
func captureOwnerSwitchCountTx(ctx context.Context, tx *sql.Tx, owner string) (*int, error) {
	var count int
	if err := tx.QueryRowContext(ctx, ownerSwitchCountSQL, owner).Scan(&count); err != nil {
		return nil, err
	}
	if count > 10000 {
		return nil, nil
	}
	return &count, nil
}

// OwnerSwitchCountFact is internal signing input from an already-authenticated
// owner capture. It is never an unsigned API response or an authority grant.
type OwnerSwitchCountFact struct {
	PreviewID       string
	PreviewRevision int64
	Count           *int
}

// OwnerSwitchCountFactTx is read inside the same transaction that freezes the
// original claim/start. The existing current-family/owner/main/epoch/operation
// guard runs before the captured preview scalar can reach its fixed-kind signer.
func (s *Service) OwnerSwitchCountFactTx(ctx context.Context, tx *sql.Tx, expected Principal, operation string) (OwnerSwitchCountFact, error) {
	var out OwnerSwitchCountFact
	capture, _, err := s.currentOwnerCompletionTx(ctx, tx, expected, operation, "")
	if err != nil {
		return out, err
	}
	if capture.phase != "prepared" {
		return out, ErrForbidden
	}
	var count sql.NullInt64
	if err = tx.QueryRowContext(ctx, `SELECT other_people_count FROM identity_moves WHERE id=? AND owner_id=? AND revision=? AND stage='preview'`, capture.move, capture.owner, capture.moveRevision).Scan(&count); err != nil {
		return out, err
	}
	out.PreviewID, out.PreviewRevision = capture.move, capture.moveRevision
	if count.Valid && count.Int64 >= 0 && count.Int64 <= 10000 {
		value := int(count.Int64)
		out.Count = &value
	}
	return out, nil
}
