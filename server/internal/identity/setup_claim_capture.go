package identity

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
)

// CaptureSetupClaimTx freezes the original server's setup authority beside its
// distinct signed continuation. It cannot manufacture a Local account/family.
func (s *Service) CaptureSetupClaimTx(ctx context.Context, tx *sql.Tx, pending PendingAccountSetup, proof string, original, signed claimweb.Signed, audience string) error {
	if tx == nil {
		return ErrUnauthorized
	}
	if err := s.CheckSetupClaimTx(ctx, tx, pending.RequestID, pending.Revision, proof); err != nil {
		return err
	}
	claim, start, err := claimweb.VerifySetupStart(original, signed, audience, time.Now())
	if err != nil || claim.AccountID != "" || claim.ServerID != s.ID() || start.IssuedAt.After(time.Now()) || start.ExpiresAt.After(time.Now().Add(claimweb.SetupStartLifetime)) || start.SetupRequestID != pending.RequestID || start.SetupRevision != pending.Revision || !ownerReturnSecret32(start.State) || !validInstallationID(start.InstallationID) {
		return ErrUnauthorized
	}
	claimRaw, _ := base64.RawURLEncoding.DecodeString(original.Payload)
	claimSig, _ := base64.RawURLEncoding.DecodeString(original.Signature)
	startRaw, _ := base64.RawURLEncoding.DecodeString(signed.Payload)
	startSig, _ := base64.RawURLEncoding.DecodeString(signed.Signature)
	key, _ := base64.RawURLEncoding.DecodeString(claim.PublicKey)
	var oldClaim, oldClaimSig, oldStart, oldStartSig []byte
	err = tx.QueryRowContext(ctx, `SELECT claim_payload,claim_signature,start_payload,start_signature FROM identity_setup_completions WHERE operation_id=?`, claim.OperationID).Scan(&oldClaim, &oldClaimSig, &oldStart, &oldStartSig)
	if err == nil {
		if !bytes.Equal(oldClaim, claimRaw) || !bytes.Equal(oldClaimSig, claimSig) || !bytes.Equal(oldStart, startRaw) || !bytes.Equal(oldStartSig, startSig) {
			return ErrConflict
		}
		return s.CheckSetupContinuationTx(ctx, tx, pending, proof, claim.OperationID, "")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var eligible bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM networking_claim_intents i JOIN networking_claim_identity k ON k.singleton=1 AND k.active_operation_id=i.operation_id AND k.installed_operation_id IS NULL AND k.pending_operation_id IS NULL
 WHERE i.operation_id=? AND i.server_id=? AND i.public_key=? AND i.local_generation=? AND k.server_id=i.server_id AND k.public_key=i.public_key AND k.reset_generation=i.local_generation
 AND i.stage='prepared' AND i.purpose='setup' AND i.account_id='account-unbound' AND i.setup_operation_id=? AND i.owner_epoch=? AND i.owner_id='' AND i.owner_profile_id='' AND i.current_owner_account_id='' AND i.previous_owner_role='' AND i.return_registration_id='')`, claim.OperationID, claim.ServerID, key, claim.LocalGeneration, pending.RequestID, pending.Revision).Scan(&eligible)
	if err != nil {
		return err
	}
	if !eligible {
		return ErrUnauthorized
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO hosted_identity_challenges(challenge_hash,installation_id,expires_ms) VALUES(?,?,?)`, Digest(start.ServerChallenge), start.InstallationID, start.ExpiresAt.UnixMilli()); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO identity_setup_completions(operation_id,setup_request_id,setup_revision,setup_proof,phase,claim_hash,claim_payload,claim_signature,start_payload,start_signature,state_hash,code_challenge,installation_id,server_challenge_hash,created_ms,expires_ms) VALUES(?,?,?,?,'prepared',?,?,?,?,?,?,?,?,?,?,?)`, claim.OperationID, pending.RequestID, pending.Revision, proof, start.ClaimHash, claimRaw, claimSig, startRaw, startSig, Digest(start.State), start.CodeChallenge, start.InstallationID, Digest(start.ServerChallenge), time.Now().UnixMilli(), start.ExpiresAt.UnixMilli())
	if err != nil {
		return err
	}
	return s.CheckSetupContinuationTx(ctx, tx, pending, proof, claim.OperationID, "")
}

// CheckSetupContinuationTx rechecks both pending setup and the exact current
// server identity/operation. Only an in-flight attempt may read consumed state.
func (s *Service) CheckSetupContinuationTx(ctx context.Context, tx *sql.Tx, pending PendingAccountSetup, proof, operation, attempt string) error {
	if err := s.CheckSetupClaimTx(ctx, tx, pending.RequestID, pending.Revision, proof); err != nil {
		return err
	}
	var live bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_setup_completions c
 JOIN networking_claim_intents i ON i.operation_id=c.operation_id AND i.setup_operation_id=c.setup_request_id AND i.owner_epoch=c.setup_revision
 JOIN networking_claim_identity k ON k.singleton=1 AND k.active_operation_id=i.operation_id AND k.server_id=i.server_id AND k.public_key=i.public_key AND k.reset_generation=i.local_generation
 WHERE c.operation_id=? AND c.setup_request_id=? AND c.setup_revision=? AND c.setup_proof=? AND c.expires_ms>?
 AND i.purpose='setup' AND i.stage IN('prepared','approved','finalizing','retrieving','installed') AND i.current_owner_account_id='' AND i.previous_owner_role=''
 AND ((c.phase IN('prepared','installed') AND ?='') OR(c.phase='consumed' AND c.attempt_id=? AND ?<>'')))`, operation, pending.RequestID, pending.Revision, proof, time.Now().UnixMilli(), attempt, attempt, attempt).Scan(&live)
	if err != nil {
		return err
	}
	if !live {
		return ErrUnauthorized
	}
	return ctx.Err()
}

func (s *Service) BindSetupClaimTransactionTx(ctx context.Context, tx *sql.Tx, pending PendingAccountSetup, proof, operation, transaction string) error {
	if !validFamilyID(transaction) {
		return ErrUnauthorized
	}
	if err := s.CheckSetupContinuationTx(ctx, tx, pending, proof, operation, ""); err != nil {
		return err
	}
	// A renewed start of an installed setup is bound again in place (RenewSetupClaimTx).
	result, err := tx.ExecContext(ctx, `UPDATE identity_setup_completions SET transaction_id=? WHERE operation_id=? AND (phase='prepared' OR phase='installed' AND attempt_id='') AND (transaction_id='' OR transaction_id=?)`, transaction, operation, transaction)
	if err != nil {
		return err
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		return ErrConflict
	}
	return s.CheckSetupContinuationTx(ctx, tx, pending, proof, operation, "")
}

// RenewSetupClaimTx replaces the expired, unconsumed start of this same pending setup once
// its claim has INSTALLED (a slow certificate outlived the start). The new claim must equal
// the captured one except for its time bounds, and the new start carries this browser's
// fresh bindings. The installed registration, if any, stays. It grants nothing: owner
// admission remains the setup return and completeAccountSetupTx.
func (s *Service) RenewSetupClaimTx(ctx context.Context, tx *sql.Tx, pending PendingAccountSetup, proof string, original, signed claimweb.Signed, audience string) error {
	if tx == nil {
		return ErrUnauthorized
	}
	if err := s.CheckSetupClaimTx(ctx, tx, pending.RequestID, pending.Revision, proof); err != nil {
		return err
	}
	now := time.Now()
	claim, start, err := claimweb.VerifySetupStart(original, signed, audience, now)
	if err != nil || claim.AccountID != "" || claim.ServerID != s.ID() || start.IssuedAt.After(now) || start.ExpiresAt.After(now.Add(claimweb.SetupStartLifetime)) || start.SetupRequestID != pending.RequestID || start.SetupRevision != pending.Revision || !ownerReturnSecret32(start.State) || !validInstallationID(start.InstallationID) {
		return ErrUnauthorized
	}
	var oldClaim []byte
	var phase, attempt, request, oldProof, oldChallenge, oldInstallation string
	var revision, expires, consumed int64
	err = tx.QueryRowContext(ctx, `SELECT claim_payload,phase,attempt_id,setup_request_id,setup_revision,setup_proof,expires_ms,consumed_ms,server_challenge_hash,installation_id FROM identity_setup_completions WHERE operation_id=?`, claim.OperationID).Scan(&oldClaim, &phase, &attempt, &request, &revision, &oldProof, &expires, &consumed, &oldChallenge, &oldInstallation)
	if err != nil {
		return ErrUnauthorized
	}
	if (phase != "prepared" && phase != "installed") || attempt != "" || consumed != 0 || expires > now.UnixMilli() || request != pending.RequestID || revision != pending.Revision || oldProof != proof {
		return ErrUnauthorized
	}
	var before claimweb.Request
	if json.Unmarshal(oldClaim, &before) != nil || !claim.IssuedAt.After(before.IssuedAt) {
		return ErrUnauthorized
	}
	before.IssuedAt, before.ExpiresAt = claim.IssuedAt, claim.ExpiresAt
	same, _ := json.Marshal(before)
	claimRaw, _ := base64.RawURLEncoding.DecodeString(original.Payload)
	if !bytes.Equal(same, claimRaw) {
		return ErrUnauthorized
	}
	// The claim is installed for this very setup, and nobody owns the server yet.
	var eligible bool
	key, _ := base64.RawURLEncoding.DecodeString(claim.PublicKey)
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM networking_claim_intents i JOIN networking_claim_identity k ON k.singleton=1 AND k.active_operation_id=i.operation_id AND k.installed_operation_id=i.operation_id AND k.pending_operation_id IS NULL
 WHERE i.operation_id=? AND i.server_id=? AND i.public_key=? AND i.local_generation=? AND k.server_id=i.server_id AND k.public_key=i.public_key AND k.reset_generation=i.local_generation
 AND i.stage='installed' AND i.purpose='setup' AND i.setup_operation_id=? AND i.owner_epoch=? AND i.current_owner_account_id='' AND i.previous_owner_role='')`, claim.OperationID, claim.ServerID, key, claim.LocalGeneration, pending.RequestID, pending.Revision).Scan(&eligible)
	if err != nil {
		return err
	}
	if !eligible {
		return ErrUnauthorized
	}
	claimSig, _ := base64.RawURLEncoding.DecodeString(original.Signature)
	startRaw, _ := base64.RawURLEncoding.DecodeString(signed.Payload)
	startSig, _ := base64.RawURLEncoding.DecodeString(signed.Signature)
	if _, err = tx.ExecContext(ctx, `DELETE FROM hosted_identity_challenges WHERE challenge_hash=? AND installation_id=?`, oldChallenge, oldInstallation); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO hosted_identity_challenges(challenge_hash,installation_id,expires_ms) VALUES(?,?,?)`, Digest(start.ServerChallenge), start.InstallationID, start.ExpiresAt.UnixMilli()); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE identity_setup_completions SET claim_hash=?,claim_payload=?,claim_signature=?,start_payload=?,start_signature=?,state_hash=?,code_challenge=?,installation_id=?,server_challenge_hash=?,created_ms=?,expires_ms=?,transaction_id=''
 WHERE operation_id=? AND phase=? AND attempt_id='' AND consumed_ms=0 AND expires_ms<=? AND setup_request_id=? AND setup_revision=? AND setup_proof=?`,
		start.ClaimHash, claimRaw, claimSig, startRaw, startSig, Digest(start.State), start.CodeChallenge, start.InstallationID, Digest(start.ServerChallenge), now.UnixMilli(), start.ExpiresAt.UnixMilli(),
		claim.OperationID, phase, now.UnixMilli(), pending.RequestID, pending.Revision, proof)
	if err != nil {
		return err
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		return ErrConflict
	}
	return s.CheckSetupContinuationTx(ctx, tx, pending, proof, claim.OperationID, "")
}
