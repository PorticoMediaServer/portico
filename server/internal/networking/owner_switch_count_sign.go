package networking

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
)

// SignOwnerSwitchCountTx is a fixed-kind preview attestation, not a generic
// signing capability. Its caller already holds the original/start capture Tx;
// no nested snapshot or extra ownership authority is created here.
func (k *ProtectedKeys) SignOwnerSwitchCountTx(ctx context.Context, tx *sql.Tx, binding Binding, audience string, original, start claimweb.Signed, count claimweb.OwnerSwitchCount) (claimweb.Signed, error) {
	var none claimweb.Signed
	if k == nil || k.db == nil || k.root == nil || tx == nil || !validBinding(binding) || binding.AccountID != UnboundAccount || count.Kind != claimweb.OwnerSwitchCountKind || count.Purpose != "switch" {
		return none, ErrInvalid
	}
	if _, err := claimAuthority(ctx); err != nil {
		return none, err
	}
	if err := guardClaimRequestTx(ctx, tx); err != nil {
		return none, err
	}
	current, active, installed, err := readCurrentIdentityTx(ctx, tx)
	if err != nil {
		return none, err
	}
	var pending string
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(pending_operation_id,'') FROM networking_claim_identity WHERE singleton=1`).Scan(&pending); err != nil {
		return none, err
	}
	intent, err := loadIntentTx(ctx, tx, binding.OperationID)
	if err != nil {
		return none, err
	}
	if current.ServerID != binding.ServerID || current.ResetGeneration != binding.LocalGeneration || !bytes.Equal(current.PublicKey, binding.PublicKey) || active.Valid || installed.Valid || pending != binding.OperationID || !sameBinding(intent.Binding, binding) || intent.Stage != Prepared || intent.Purpose != "switch" || intent.CurrentOwnerAccountID != "" || intent.PreviousOwnerRole != "" || intent.ReturnRegistrationID != "" {
		return none, ErrStale
	}
	claim, _, err := claimweb.VerifyOwnerStart(original, start, audience, time.Now())
	if err != nil || claim.AccountID != "" || claim.CurrentOwnerAccountID != "" || claim.PreviousOwnerRole != "" || claim.ReturnRegistrationID != "" || claim.OperationID != binding.OperationID || claim.ServerID != binding.ServerID || claim.LocalGeneration != binding.LocalGeneration || claim.PublicKey != base64.RawURLEncoding.EncodeToString(binding.PublicKey) {
		return none, ErrInvalid
	}
	claimRaw, _ := base64.RawURLEncoding.DecodeString(original.Payload)
	startRaw, _ := base64.RawURLEncoding.DecodeString(start.Payload)
	var matches bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_owner_completions c JOIN identity_moves m ON m.id=c.move_id AND m.owner_id=c.owner_id AND m.revision=c.move_revision WHERE c.operation_id=? AND c.phase='prepared' AND c.claim_payload=? AND c.start_payload=? AND c.switch_count_payload IS NULL AND c.switch_count_signature IS NULL AND m.id=? AND m.revision=? AND m.other_people_count=? AND m.stage='preview' AND m.owner_confirmed=1 AND m.from_kind='local' AND m.to_kind='portico')`, binding.OperationID, claimRaw, startRaw, count.PreviewID, count.PreviewRevision, count.OtherPeopleCount).Scan(&matches)
	if err != nil {
		return none, err
	}
	if !matches {
		return none, ErrStale
	}
	key, err := k.read(current.KeyIncarnation)
	if err != nil {
		return none, err
	}
	defer clear(key)
	if !bytes.Equal(key.Public().(ed25519.PublicKey), current.PublicKey) {
		return none, ErrStale
	}
	raw, err := json.Marshal(count)
	if err != nil || len(raw) > 8192 {
		return none, ErrInvalid
	}
	proof := claimweb.Signed{Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, raw))}
	if _, err = claimweb.VerifyOwnerSwitchCount(original, start, proof, claim.Audience, time.Now()); err != nil {
		return none, ErrInvalid
	}
	if err = guardClaimRequestTx(ctx, tx); err != nil {
		return none, err
	}
	if _, err = claimAuthority(ctx); err != nil {
		return none, err
	}
	return proof, nil
}
