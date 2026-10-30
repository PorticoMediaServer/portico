package networking

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// SignSetupStart is deliberately separate from Sign's fixed-kind allowlist.
// It signs only the complete canonical start beside its original signed claim,
// for this current prepared, initial setup. No caller gets a generic
// identity-key signing capability or a Local identity grant.
func (k *ProtectedKeys) SignSetupStart(ctx context.Context, binding Binding, original claimweb.Signed, start claimweb.SetupStart) (claimweb.Signed, error) {
	return k.signSetupStart(ctx, binding, original, start, false)
}

// SignRenewedSetupStart signs a fresh start for the same setup once its claim has INSTALLED
// (a slow certificate outlived the original start). The binding is the claim's original,
// unbound one; the installed operation must be this one, now bound to a real account.
func (k *ProtectedKeys) SignRenewedSetupStart(ctx context.Context, binding Binding, original claimweb.Signed, start claimweb.SetupStart) (claimweb.Signed, error) {
	return k.signSetupStart(ctx, binding, original, start, true)
}

func (k *ProtectedKeys) signSetupStart(ctx context.Context, binding Binding, original claimweb.Signed, start claimweb.SetupStart, renewal bool) (claimweb.Signed, error) {
	var none claimweb.Signed
	if k == nil || k.db == nil || k.root == nil || !validBinding(binding) || binding.AccountID != UnboundAccount {
		return none, ErrInvalid
	}
	if _, err := claimAuthority(ctx); err != nil {
		return none, err
	}
	g, err := dbwork.BeginSnapshot(ctx, k.db)
	if err != nil {
		return none, err
	}
	defer g.Rollback()
	tx := g.Tx()
	if err = guardClaimRequestTx(ctx, tx); err != nil {
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
	// A renewal's intent is installed and bound to its approver; it is otherwise the same binding.
	unbound := intent.Binding
	if renewal {
		unbound.AccountID = UnboundAccount
	}
	stage := (!renewal && !installed.Valid && intent.Stage == Prepared) ||
		(renewal && installed.Valid && installed.String == binding.OperationID && intent.Stage == Installed && validID(intent.AccountID) && intent.AccountID != UnboundAccount)
	if current.ServerID != binding.ServerID || current.ResetGeneration != binding.LocalGeneration || !bytes.Equal(current.PublicKey, binding.PublicKey) || !active.Valid || active.String != binding.OperationID || !stage || pending != "" || !sameBinding(unbound, binding) || intent.Purpose != "setup" || intent.CurrentOwnerAccountID != "" || intent.PreviousOwnerRole != "" || intent.ReturnRegistrationID != "" || intent.owner.SetupOperationID != start.SetupRequestID || intent.owner.Epoch != start.SetupRevision {
		return none, ErrStale
	}
	if renewal {
		// Defence in depth: the signer itself requires an expired, unconsumed start of this setup
		// and a server nobody owns yet, whatever its caller decided.
		var lapsed bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_setup_completions c JOIN onboarding_state_v1 o ON o.singleton=1 AND o.ready=0
 WHERE c.operation_id=? AND c.phase IN('prepared','installed') AND c.attempt_id='' AND c.consumed_ms=0 AND c.expires_ms<=? AND c.setup_request_id=? AND c.setup_revision=?)`,
			binding.OperationID, time.Now().UnixMilli(), start.SetupRequestID, start.SetupRevision).Scan(&lapsed); err != nil {
			return none, err
		}
		if !lapsed {
			return none, ErrStale
		}
	}
	key, err := k.read(current.KeyIncarnation)
	if err != nil {
		return none, err
	}
	defer clear(key)
	if !bytes.Equal(key.Public().(ed25519.PublicKey), current.PublicKey) {
		return none, ErrStale
	}
	raw, err := json.Marshal(start)
	if err != nil || len(raw) > 8192 {
		return none, ErrInvalid
	}
	signed := claimweb.Signed{Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, raw))}
	claim, _, err := claimweb.VerifySetupStart(original, signed, start.Audience, time.Now())
	if err != nil || claim.AccountID != "" || claim.CurrentOwnerAccountID != "" || claim.PreviousOwnerRole != "" || claim.ReturnRegistrationID != "" || claim.OperationID != binding.OperationID || claim.ServerID != binding.ServerID || claim.LocalGeneration != binding.LocalGeneration || claim.PublicKey != base64.RawURLEncoding.EncodeToString(binding.PublicKey) {
		return none, ErrInvalid
	}
	if err = guardClaimRequestTx(ctx, tx); err != nil {
		return none, err
	}
	if _, err = claimAuthority(ctx); err != nil {
		return none, err
	}
	return signed, nil
}
