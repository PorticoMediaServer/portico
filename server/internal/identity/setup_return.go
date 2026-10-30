package identity

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
)

type setupCompletion struct {
	operation, phase, transaction, installation, challenge, attempt string
	expires                                                         int64
}

func readSetupCompletionTx(ctx context.Context, tx *sql.Tx, operation string) (c setupCompletion, err error) {
	c.operation = operation
	err = tx.QueryRowContext(ctx, `SELECT phase,transaction_id,installation_id,server_challenge_hash,expires_ms,attempt_id FROM identity_setup_completions WHERE operation_id=?`, operation).Scan(&c.phase, &c.transaction, &c.installation, &c.challenge, &c.expires, &c.attempt)
	return
}
func (s *Service) SetupClaimOperationTx(ctx context.Context, tx *sql.Tx, pending PendingAccountSetup, proof string) (string, error) {
	var operation string
	if err := tx.QueryRowContext(ctx, `SELECT operation_id FROM identity_setup_completions WHERE setup_request_id=? AND setup_revision=? AND setup_proof=? AND phase IN('prepared','installed')`, pending.RequestID, pending.Revision, proof).Scan(&operation); err != nil {
		return "", err
	}
	return operation, s.CheckSetupContinuationTx(ctx, tx, pending, proof, operation, "")
}
func CheckSetupReturnOriginTx(ctx context.Context, tx *sql.Tx, operation, origin, audience string) error {
	actual, err := setupReturnOriginTx(ctx, tx, operation, audience)
	if err != nil {
		return err
	}
	if origin == "" || actual != origin {
		return ErrUnauthorized
	}
	return ctx.Err()
}

// CapturedSetupReturnOriginTx recovers only an existing frozen continuation's
// signed origin. The persisted proof remains bound to current pending setup;
// this does not reconstruct a setup token or grant an owner session.
func (s *Service) CapturedSetupReturnOriginTx(ctx context.Context, tx *sql.Tx, pending PendingAccountSetup, proof, operation, audience string) (string, error) {
	if err := s.CheckSetupContinuationTx(ctx, tx, pending, proof, operation, ""); err != nil {
		return "", err
	}
	return setupReturnOriginTx(ctx, tx, operation, audience)
}

func setupReturnOriginTx(ctx context.Context, tx *sql.Tx, operation, audience string) (string, error) {
	var cp, cs, sp, ss []byte
	if err := tx.QueryRowContext(ctx, `SELECT claim_payload,claim_signature,start_payload,start_signature FROM identity_setup_completions WHERE operation_id=?`, operation).Scan(&cp, &cs, &sp, &ss); err != nil {
		return "", err
	}
	claim, _, err := claimweb.VerifySetupStart(claimweb.Signed{Payload: base64.RawURLEncoding.EncodeToString(cp), Signature: base64.RawURLEncoding.EncodeToString(cs)}, claimweb.Signed{Payload: base64.RawURLEncoding.EncodeToString(sp), Signature: base64.RawURLEncoding.EncodeToString(ss)}, audience, time.Now())
	if err != nil || claim.Address == "" {
		return "", ErrUnauthorized
	}
	return claim.Address, ctx.Err()
}

// MarkSetupInstalledTx records that the approved claim has installed: owner admission no
// longer waits for a certificate or an HTTPS return registration (O-163). The caller has
// checked the installed, acknowledged setup intent in this same writer; the exchange and
// completion later fence the installed claim and credential generations themselves.
func (s *Service) MarkSetupInstalledTx(ctx context.Context, tx *sql.Tx, pending PendingAccountSetup, proof, operation string) error {
	if err := s.CheckSetupContinuationTx(ctx, tx, pending, proof, operation, ""); err != nil {
		return err
	}
	c, err := readSetupCompletionTx(ctx, tx, operation)
	if err != nil {
		return err
	}
	if c.transaction == "" {
		return ErrUnauthorized
	}
	if c.phase == "installed" {
		return nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE identity_setup_completions SET phase='installed',installed_ms=? WHERE operation_id=? AND phase='prepared' AND transaction_id<>'' AND attempt_id='' AND consumed_ms=0`, time.Now().UnixMilli(), operation)
	if err != nil {
		return err
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		return ErrConflict
	}
	return s.CheckSetupContinuationTx(ctx, tx, pending, proof, operation, "")
}
func setupReturnBindingTx(ctx context.Context, tx *sql.Tx, c setupCompletion, q serverreturn.SetupExchange) error {
	if !q.Valid() || !ownerReturnSecret32(q.State) || !ownerReturnSecret32(q.Verifier) || !validInstallationID(q.InstallationID) || c.transaction != q.TransactionID || c.installation != q.InstallationID || c.challenge != Digest(q.ServerChallenge) {
		return ErrUnauthorized
	}
	var state, challenge string
	if err := tx.QueryRowContext(ctx, `SELECT state_hash,code_challenge FROM identity_setup_completions WHERE operation_id=?`, c.operation).Scan(&state, &challenge); err != nil {
		return err
	}
	pkce := sha256.Sum256([]byte(q.Verifier))
	if subtle.ConstantTimeCompare([]byte(state), []byte(Digest(q.State))) != 1 || subtle.ConstantTimeCompare([]byte(challenge), []byte(base64.RawURLEncoding.EncodeToString(pkce[:]))) != 1 {
		return ErrUnauthorized
	}
	return ctx.Err()
}
func (s *Service) BeginSetupReturnTx(ctx context.Context, tx *sql.Tx, pending PendingAccountSetup, proof string, q serverreturn.SetupExchange) (operation, attempt string, err error) {
	operation, err = s.SetupClaimOperationTx(ctx, tx, pending, proof)
	if err != nil {
		return "", "", err
	}
	c, err := readSetupCompletionTx(ctx, tx, operation)
	if err != nil {
		return "", "", err
	}
	if c.phase != "installed" {
		return "", "", ErrUnauthorized
	}
	if err = setupReturnBindingTx(ctx, tx, c, q); err != nil {
		return "", "", err
	}
	attempt = Token()
	result, err := tx.ExecContext(ctx, `UPDATE identity_setup_completions SET phase='consumed',consumed_ms=?,attempt_id=? WHERE operation_id=? AND phase='installed' AND attempt_id='' AND consumed_ms=0`, time.Now().UnixMilli(), attempt, operation)
	if err != nil {
		return "", "", err
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		return "", "", ErrConflict
	}
	return operation, attempt, s.CheckSetupReturnAttemptTx(ctx, tx, pending, proof, operation, attempt, q)
}
func (s *Service) CheckSetupReturnAttemptTx(ctx context.Context, tx *sql.Tx, pending PendingAccountSetup, proof, operation, attempt string, q serverreturn.SetupExchange) error {
	if attempt == "" {
		return ErrUnauthorized
	}
	if err := s.CheckSetupContinuationTx(ctx, tx, pending, proof, operation, attempt); err != nil {
		return err
	}
	c, err := readSetupCompletionTx(ctx, tx, operation)
	if err != nil {
		return err
	}
	if c.phase != "consumed" || c.attempt != attempt {
		return ErrUnauthorized
	}
	return setupReturnBindingTx(ctx, tx, c, q)
}

// Completion spends the challenge and nonce with the sole existing account
// setup admission boundary; approval or a return alone never grants a session.
func (s *Service) CompleteSetupReturnTx(ctx context.Context, tx *sql.Tx, pending PendingAccountSetup, proof, operation, attempt string, q serverreturn.SetupExchange, who PorticoIdentity, nonce string) (DirectSignIn, error) {
	var none DirectSignIn
	if err := s.CheckSetupReturnAttemptTx(ctx, tx, pending, proof, operation, attempt, q); err != nil {
		return none, err
	}
	c, err := readSetupCompletionTx(ctx, tx, operation)
	if err != nil {
		return none, err
	}
	r := who.Return
	if r == nil || r.Purpose != "setup" || r.CurrentOwnerAccountID != "" || r.PreviousOwnerRole != "" || r.OperationID != operation || r.InstallationID != q.InstallationID || r.RegistrationID != "" || len(nonce) < 22 || len(nonce) > 128 {
		return none, ErrUnauthorized
	}
	if err = CheckPorticoReturnTx(ctx, tx, who); err != nil {
		return none, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM hosted_identity_challenges WHERE challenge_hash=? AND installation_id=? AND expires_ms>?`, c.challenge, c.installation, time.Now().UnixMilli())
	if err != nil {
		return none, err
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		return none, ErrUnauthorized
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO hosted_identity_nonces(nonce,expires_ms) VALUES(?,?) ON CONFLICT(nonce) DO NOTHING`, nonce, r.ExpiresAt.UnixMilli())
	if err != nil {
		return none, err
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		return none, ErrUnauthorized
	}
	out, err := s.completeAccountSetupTx(ctx, tx, pending, proof, who)
	if err != nil {
		return none, err
	}
	result, err = tx.ExecContext(ctx, `UPDATE identity_setup_completions SET phase='completed',completed_ms=? WHERE operation_id=? AND phase='consumed' AND attempt_id=? AND expires_ms>?`, time.Now().UnixMilli(), operation, attempt, time.Now().UnixMilli())
	if err != nil {
		return none, err
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		return none, ErrUnauthorized
	}
	if err = CheckPorticoReturnTx(ctx, tx, who); err != nil {
		return none, err
	}
	if time.Now().UnixMilli() >= c.expires {
		return none, ErrUnauthorized
	}
	return out, ctx.Err()
}
func (s *Service) endSetupReturnTx(ctx context.Context, tx *sql.Tx, c setupCompletion, phase string) error {
	if phase != "cancelled" && phase != "superseded" && phase != "expired" {
		return ErrDirectInput
	}
	if _, err := tx.ExecContext(ctx, `UPDATE identity_setup_completions SET phase=? WHERE operation_id=? AND phase IN('prepared','installed','consumed')`, phase, c.operation); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM hosted_identity_challenges WHERE challenge_hash=? AND installation_id=?`, c.challenge, c.installation)
	return err
}
func (s *Service) AbandonSetupReturnTx(ctx context.Context, tx *sql.Tx, operation, attempt string) error {
	c, err := readSetupCompletionTx(ctx, tx, operation)
	if err != nil {
		return err
	}
	if c.phase != "consumed" || c.attempt != attempt || attempt == "" {
		return ErrUnauthorized
	}
	return s.endSetupReturnTx(ctx, tx, c, "cancelled")
}
