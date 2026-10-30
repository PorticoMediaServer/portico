package identity

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
)

// OwnerSwitchCountSigner is supplied by the protected-key networking edge. It
// receives only the captured scalar and preview provenance, in this same Tx.
// It cannot supply an owner, family, setup authority or arbitrary signing kind.
type OwnerSwitchCountSigner func(context.Context, *sql.Tx, string, int64, int) (claimweb.Signed, error)

// CaptureOwnerClaimWithCountTx freezes optional count proof bytes with the
// original/start insertion. Existing captures return the original optional
// proof (including absence), without ever invoking the signer again.
func (s *Service) CaptureOwnerClaimWithCountTx(ctx context.Context, tx *sql.Tx, expected Principal, original, start claimweb.Signed, audience string, sign OwnerSwitchCountSigner) (*claimweb.Signed, error) {
	if tx == nil || expected.Authority != "local" || expected.Role != TierOwner || expected.Grant != "" {
		return nil, ErrForbidden
	}
	claim, _, err := claimweb.VerifyOwnerStart(original, start, audience, time.Now())
	if err != nil {
		return nil, ErrUnauthorized
	}
	var payload, signature []byte
	err = tx.QueryRowContext(ctx, `SELECT switch_count_payload,switch_count_signature FROM identity_owner_completions WHERE operation_id=?`, claim.OperationID).Scan(&payload, &signature)
	existing := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err = s.CaptureOwnerClaimTx(ctx, tx, expected, original, start, audience); err != nil {
		return nil, err
	}
	if existing {
		if len(payload) == 0 || len(signature) == 0 {
			return nil, nil
		}
		proof := claimweb.Signed{Payload: base64.RawURLEncoding.EncodeToString(payload), Signature: base64.RawURLEncoding.EncodeToString(signature)}
		return &proof, nil
	}
	fact, err := s.OwnerSwitchCountFactTx(ctx, tx, expected, claim.OperationID)
	if err != nil {
		return nil, err
	}
	if sign == nil || fact.Count == nil {
		return nil, s.CheckOwnerClaimTx(ctx, tx, expected, claim.OperationID)
	}
	proof, signErr := sign(ctx, tx, fact.PreviewID, fact.PreviewRevision, *fact.Count)
	// Optional metadata never changes genuine owner authority. Recheck that
	// authority after signing I/O even when the informational proof is omitted.
	if err = s.CheckOwnerClaimTx(ctx, tx, expected, claim.OperationID); err != nil {
		return nil, err
	}
	if signErr != nil {
		return nil, nil
	}
	count, err := claimweb.VerifyOwnerSwitchCount(original, start, proof, audience, time.Now())
	if err != nil || count.PreviewID != fact.PreviewID || count.PreviewRevision != fact.PreviewRevision || count.OtherPeopleCount != *fact.Count {
		return nil, nil
	}
	payload, err = base64.RawURLEncoding.Strict().DecodeString(proof.Payload)
	if err != nil {
		return nil, nil
	}
	signature, err = base64.RawURLEncoding.Strict().DecodeString(proof.Signature)
	if err != nil {
		return nil, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE identity_owner_completions SET switch_count_payload=?,switch_count_signature=? WHERE operation_id=? AND phase='prepared' AND switch_count_payload IS NULL AND switch_count_signature IS NULL`, payload, signature, claim.OperationID)
	if err != nil {
		return nil, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if changed != 1 {
		return nil, ErrConflict
	}
	if err = s.CheckOwnerClaimTx(ctx, tx, expected, claim.OperationID); err != nil {
		return nil, err
	}
	return &proof, nil
}
