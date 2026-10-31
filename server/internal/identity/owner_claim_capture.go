package identity

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
)

// CaptureOwnerClaimTx freezes both complete signed envelopes beside the actual
// initiating owner family and fresh local challenge, before any pending POST.
// It is a private claim-operation writer, not an identity or ownership grant.
// The caller owns the transaction and the trusted browser-origin prerequisite.
func (s *Service) CaptureOwnerClaimTx(ctx context.Context, tx *sql.Tx, expected Principal, original, signedStart claimweb.Signed, audience string) error {
	if tx == nil || expected.Authority != "local" || expected.Role != TierOwner || expected.Grant != "" {
		return ErrForbidden
	}
	now := time.Now()
	claim, start, err := claimweb.VerifyOwnerStart(original, signedStart, audience, now)
	if err != nil || claim.AccountID != "" || claim.CurrentOwnerAccountID != "" || claim.PreviousOwnerRole != "" || claim.ReturnRegistrationID != "" || claim.ServerID != s.ID() || start.IssuedAt.After(now) || start.ExpiresAt.After(now.Add(claimweb.OwnerStartLifetime)) {
		return ErrUnauthorized
	}
	state, stateErr := base64.RawURLEncoding.Strict().DecodeString(start.State)
	if stateErr != nil || len(state) != 32 || base64.RawURLEncoding.EncodeToString(state) != start.State || !validInstallationID(start.InstallationID) {
		return ErrUnauthorized
	}
	// The canonical verifier already required exact RawURL re-encoding, both
	// signatures, original-byte claim hash, field order and complete tuple.
	claimRaw, _ := base64.RawURLEncoding.DecodeString(original.Payload)
	claimSignature, _ := base64.RawURLEncoding.DecodeString(original.Signature)
	startRaw, _ := base64.RawURLEncoding.DecodeString(signedStart.Payload)
	startSignature, _ := base64.RawURLEncoding.DecodeString(signedStart.Signature)
	key, _ := base64.RawURLEncoding.DecodeString(claim.PublicKey)
	var storedClaim, storedClaimSignature, storedStart, storedStartSignature []byte
	err = tx.QueryRowContext(ctx, `SELECT claim_payload,claim_signature,start_payload,start_signature FROM identity_owner_completions WHERE operation_id=?`, claim.OperationID).Scan(&storedClaim, &storedClaimSignature, &storedStart, &storedStartSignature)
	if err == nil {
		if !bytes.Equal(storedClaim, claimRaw) || !bytes.Equal(storedClaimSignature, claimSignature) || !bytes.Equal(storedStart, startRaw) || !bytes.Equal(storedStartSignature, startSignature) {
			return ErrConflict
		}
		return s.CheckOwnerClaimTx(ctx, tx, expected, claim.OperationID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err = s.RequireMainProfileTx(ctx, tx, expected); err != nil {
		return err
	}
	if err = RequireSignInKindTx(ctx, tx, SignInLocal); err != nil {
		return err
	}
	family, err := s.familyTokenTx(ctx, tx, expected.Hash)
	if err != nil {
		return err
	}
	if _, err = s.sessionFamilyRecordTx(ctx, tx, expected, family); err != nil {
		return err
	}
	if family.principal.Grant != "" || start.ExpiresAt.After(family.AuthorizationHorizon) || start.ExpiresAt.After(family.tokenExpiry) {
		return ErrUnauthorized
	}
	if err = refuseResetDescendantTx(ctx, tx, family.ID); err != nil {
		return err
	}
	var sameBrowser bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_device_families b JOIN identity_devices d ON d.id=b.device_id WHERE b.family_id=? AND d.authority='local' AND d.account_id=? AND d.installation_id=? AND d.platform='web')`, family.ID, expected.AccountID, start.InstallationID).Scan(&sameBrowser); err != nil {
		return err
	}
	if !sameBrowser {
		return ErrForbidden
	}
	var eligible bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM networking_claim_intents i
 JOIN networking_claim_identity k ON k.singleton=1 AND k.pending_operation_id=i.operation_id AND k.active_operation_id IS NULL AND k.installed_operation_id IS NULL
 JOIN networking_server_identities keys ON keys.server_id=k.server_id AND keys.public_key=k.public_key
 WHERE i.operation_id=? AND i.server_id=? AND i.public_key=? AND i.local_generation=? AND k.server_id=i.server_id AND k.public_key=i.public_key AND k.reset_generation=i.local_generation
 AND i.stage='prepared' AND i.purpose='switch' AND i.account_id='account-unbound' AND i.current_owner_account_id='' AND i.previous_owner_role='' AND i.return_registration_id=''
 AND i.owner_id=? AND i.owner_profile_id=? AND i.owner_epoch=?
 AND NOT EXISTS(SELECT 1 FROM account_portico_links WHERE account_id=?))`, claim.OperationID, claim.ServerID, key, claim.LocalGeneration, expected.AccountID, expected.ProfileID, expected.Epoch, expected.AccountID).Scan(&eligible)
	if err != nil {
		return err
	}
	if !eligible {
		return ErrUnauthorized
	}
	var move, confirmation string
	var revision, memberRevision int64
	err = tx.QueryRowContext(ctx, `SELECT m.id,m.revision,m.confirmation,a.revision FROM identity_moves m JOIN direct_memberships a ON a.account_id=m.owner_id WHERE m.owner_id=? AND m.from_kind='local' AND m.to_kind='portico' AND m.stage='preview' AND m.owner_confirmed=1`, expected.AccountID).Scan(&move, &revision, &confirmation, &memberRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	// The signed start's mode must match the server's own confirmed move; a legacy
	// v1 start has no mode and is therefore an ordinary switch only.
	var startOver bool
	if err = tx.QueryRowContext(ctx, `SELECT start_over FROM identity_moves WHERE id=?`, move).Scan(&startOver); err != nil {
		return ErrUnauthorized
	}
	want := "switch"
	if startOver {
		want = "start-over"
	}
	if start.TransitionMode != want && !(start.TransitionMode == "" && !startOver) {
		return ErrUnauthorized
	}
	caller := directCaller{account: DirectAccount{ID: expected.AccountID, PrimaryProfileID: expected.ProfileID, Revision: memberRevision, Role: TierOwner}, epoch: expected.Epoch, familyID: family.ID}
	if _, err = s.confirmMoveTx(ctx, tx, caller, MoveConfirmRequest{MoveID: move, ExpectedRevision: revision, Confirmation: confirmation}, SignInPortico); err != nil {
		return err
	}
	// R-02: only a live attempt of this family blocks a new one: prepared and
	// unexpired, or installed onward. One that expired unconsumed, or ended
	// cancelled, expired or superseded, is replaced (family_id is unique).
	var liveFamily, challengeExists bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_owner_completions WHERE family_id=? AND phase NOT IN('cancelled','expired','superseded') AND NOT (phase='prepared' AND expires_ms<=?)),EXISTS(SELECT 1 FROM hosted_identity_challenges WHERE challenge_hash=?)`, family.ID, now.UnixMilli(), Digest(start.ServerChallenge)).Scan(&liveFamily, &challengeExists)
	if err != nil {
		return err
	}
	if liveFamily || challengeExists {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM hosted_identity_challenges WHERE challenge_hash IN(SELECT server_challenge_hash FROM identity_owner_completions WHERE family_id=?)`, family.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM identity_owner_completions WHERE family_id=?`, family.ID); err != nil {
		return err
	}
	// The challenge and immutable operation capture become visible together.
	// No relaxed public Local identity-challenge endpoint is involved.
	if _, err = tx.ExecContext(ctx, `INSERT INTO hosted_identity_challenges(challenge_hash,installation_id,expires_ms) VALUES(?,?,?)`, Digest(start.ServerChallenge), start.InstallationID, start.ExpiresAt.UnixMilli()); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO identity_owner_completions(operation_id,move_id,family_id,token_hash,token_generation,owner_id,owner_profile_id,owner_epoch,phase,claim_hash,claim_payload,claim_signature,start_payload,start_signature,state_hash,code_challenge,installation_id,server_challenge_hash,created_ms,expires_ms,move_revision)
 VALUES(?,?,?,?,?,?,?,?,'prepared',?,?,?,?,?,?,?,?,?,?,?,?)`, claim.OperationID, move, family.ID, expected.Hash, family.TokenGeneration, expected.AccountID, expected.ProfileID, expected.Epoch, start.ClaimHash, claimRaw, claimSignature, startRaw, startSignature, Digest(start.State), start.CodeChallenge, start.InstallationID, Digest(start.ServerChallenge), now.UnixMilli(), start.ExpiresAt.UnixMilli(), revision)
	if err != nil {
		return err
	}
	return s.CheckOwnerClaimTx(ctx, tx, expected, claim.OperationID)
}

// BindOwnerClaimTransactionTx binds the genuine pending response only after
// its network await and the same-operation live guard. A different response
// cannot silently retarget the frozen browser's operation.
func (s *Service) BindOwnerClaimTransactionTx(ctx context.Context, tx *sql.Tx, expected Principal, operation, transaction string) error {
	if !validFamilyID(transaction) {
		return ErrUnauthorized
	}
	c, _, err := s.currentOwnerCompletionTx(ctx, tx, expected, operation, "")
	if err != nil {
		return err
	}
	if c.phase != "prepared" || c.transaction != "" && c.transaction != transaction {
		return ErrConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE identity_owner_completions SET transaction_id=? WHERE operation_id=? AND phase='prepared' AND (transaction_id='' OR transaction_id=?) AND expires_ms>?`, transaction, operation, transaction, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		return ErrConflict
	}
	return s.CheckOwnerClaimTx(ctx, tx, expected, operation)
}
