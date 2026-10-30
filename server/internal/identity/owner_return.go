package identity

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
)

// OwnerClaimOperationTx restores the one captured operation for this exact
// token after a restart. It grants no ordinary session or management scope.
func (s *Service) OwnerClaimOperationTx(ctx context.Context, tx *sql.Tx, expected Principal) (string, error) {
	operation, err := ownerCompletionOperationTx(ctx, tx, expected.Hash)
	if err != nil {
		return "", err
	}
	if err = s.CheckOwnerClaimTx(ctx, tx, expected, operation); err != nil {
		return "", err
	}
	return operation, nil
}

// CheckOwnerReturnOriginTx checks the original, immutable signed callback
// context. It is only an extra proof fence; the caller must also check current
// owner/attempt authority in this same snapshot. No HTTP origin is normalized.
func CheckOwnerReturnOriginTx(ctx context.Context, tx *sql.Tx, operation, origin, audience string) error {
	var claimPayload, claimSignature, startPayload, startSignature []byte
	if err := tx.QueryRowContext(ctx, `SELECT claim_payload,claim_signature,start_payload,start_signature FROM identity_owner_completions WHERE operation_id=?`, operation).Scan(&claimPayload, &claimSignature, &startPayload, &startSignature); err != nil {
		return err
	}
	original := claimweb.Signed{Payload: base64.RawURLEncoding.EncodeToString(claimPayload), Signature: base64.RawURLEncoding.EncodeToString(claimSignature)}
	start := claimweb.Signed{Payload: base64.RawURLEncoding.EncodeToString(startPayload), Signature: base64.RawURLEncoding.EncodeToString(startSignature)}
	claim, _, err := claimweb.VerifyOwnerStart(original, start, audience, time.Now())
	if err != nil || origin == "" || claim.Address != origin {
		return ErrUnauthorized
	}
	return nil
}

// OwnerReturnRoute is a private, current resolver observation. Networking must
// obtain it through its installed credential and recheck actual serving TLS in
// the same transaction as these writers. Neither a callback nor a browser may
// supply a registration or treat the original signed Address as this origin.
type OwnerReturnRoute struct {
	Registration serverreturn.Registration
	Generation   int64
}

func ownerReturnRouteTx(ctx context.Context, tx *sql.Tx, c ownerCompletion, route OwnerReturnRoute) error {
	if !serverreturn.Origin(route.Registration.Origin) || route.Generation < 1 {
		return ErrUnauthorized
	}
	var server, claim, credential string
	var generation int64
	var enabled bool
	err := tx.QueryRowContext(ctx, `SELECT i.server_id,i.claim_generation,i.credential_generation,r.generation,m.enabled FROM networking_claim_intents i JOIN networking_remote_state r ON r.singleton=1 JOIN identity_managed_route m ON m.singleton=1 WHERE i.operation_id=? AND i.stage='installed' AND i.installation_acknowledged=1`, c.operation).Scan(&server, &claim, &credential, &generation, &enabled)
	if err != nil {
		return err
	}
	claimGeneration, ce := strconv.ParseInt(claim, 10, 64)
	credentialGeneration, ke := strconv.ParseInt(credential, 10, 64)
	if ce != nil || ke != nil || claimGeneration < 1 || credentialGeneration < 1 || !enabled || generation != route.Generation || route.Registration.ID != serverreturn.RegistrationID(server, c.operation, claimGeneration, credentialGeneration, generation, route.Registration.Origin) {
		return ErrUnauthorized
	}
	return nil
}

// BindOwnerReturnRegistrationTx freezes the first genuine installed resolver
// result. An actual later route/registration change closes this continuation;
// false,nil must be committed and shown as a fresh ordinary sign-in, never
// rolled back or silently retargeted to the new registration.
func (s *Service) BindOwnerReturnRegistrationTx(ctx context.Context, tx *sql.Tx, expected Principal, operation string, route OwnerReturnRoute) (bool, error) {
	c, _, err := s.currentOwnerCompletionTx(ctx, tx, expected, operation, "")
	if err != nil {
		return false, err
	}
	if c.phase != "installed" || c.transaction == "" {
		return false, ErrForbidden
	}
	if err = ownerReturnRouteTx(ctx, tx, c, route); err != nil {
		return false, err
	}
	if c.registration != "" {
		if c.registration == route.Registration.ID && c.origin == route.Registration.Origin && c.routeGeneration == route.Generation {
			return true, nil
		}
		return false, s.endOwnerReturnTx(ctx, tx, c, "superseded")
	}
	result, err := tx.ExecContext(ctx, `UPDATE identity_owner_completions SET registration_id=?,registration_origin=?,route_generation=? WHERE operation_id=? AND phase='installed' AND registration_id=''`, route.Registration.ID, route.Registration.Origin, route.Generation, operation)
	if err != nil {
		return false, err
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		return false, ErrConflict
	}
	return true, s.CheckOwnerClaimTx(ctx, tx, expected, operation)
}

func ownerReturnSecret32(value string) bool {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return err == nil && len(raw) == 32 && base64.RawURLEncoding.EncodeToString(raw) == value
}

func ownerReturnBindingTx(ctx context.Context, tx *sql.Tx, c ownerCompletion, q serverreturn.OwnerExchange, route OwnerReturnRoute) error {
	if !q.Valid() || !ownerReturnSecret32(q.State) || !ownerReturnSecret32(q.Verifier) || !validInstallationID(q.InstallationID) || c.registration == "" || c.registration != q.RegistrationID || c.transaction != q.TransactionID || c.installation != q.InstallationID || c.challenge != Digest(q.ServerChallenge) || c.origin != route.Registration.Origin || c.registration != route.Registration.ID || c.routeGeneration != route.Generation {
		return ErrUnauthorized
	}
	if err := ownerReturnRouteTx(ctx, tx, c, route); err != nil {
		return err
	}
	var stateHash, codeChallenge string
	if err := tx.QueryRowContext(ctx, `SELECT state_hash,code_challenge FROM identity_owner_completions WHERE operation_id=?`, c.operation).Scan(&stateHash, &codeChallenge); err != nil {
		return err
	}
	pkce := sha256.Sum256([]byte(q.Verifier))
	if subtle.ConstantTimeCompare([]byte(stateHash), []byte(Digest(q.State))) != 1 || subtle.ConstantTimeCompare([]byte(codeChallenge), []byte(base64.RawURLEncoding.EncodeToString(pkce[:]))) != 1 {
		return ErrUnauthorized
	}
	return nil
}

// BeginOwnerReturnTx consumes the local attempt BEFORE the installed-server
// network exchange. A crash/lost response cannot send a second exchange. The
// returned attempt is private to the executing request and is never a token.
func (s *Service) BeginOwnerReturnTx(ctx context.Context, tx *sql.Tx, expected Principal, q serverreturn.OwnerExchange, route OwnerReturnRoute) (operation, attempt string, err error) {
	operation, err = ownerCompletionOperationTx(ctx, tx, expected.Hash)
	if err != nil {
		return "", "", err
	}
	c, _, err := s.currentOwnerCompletionTx(ctx, tx, expected, operation, "")
	if err != nil {
		return "", "", err
	}
	if c.phase != "installed" {
		return "", "", ErrForbidden
	}
	if err = ownerReturnBindingTx(ctx, tx, c, q, route); err != nil {
		return "", "", err
	}
	attempt = Token()
	result, err := tx.ExecContext(ctx, `UPDATE identity_owner_completions SET phase='consumed',consumed_ms=?,attempt_id=? WHERE operation_id=? AND phase='installed' AND consumed_ms=0 AND attempt_id=''`, time.Now().UnixMilli(), attempt, operation)
	if err != nil {
		return "", "", err
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		return "", "", ErrConflict
	}
	_, _, err = s.currentOwnerCompletionTx(ctx, tx, expected, operation, attempt)
	return operation, attempt, err
}

// CheckOwnerReturnAttemptTx is for the one in-flight request only. No public
// request may restore a consumed attempt or turn it into generic owner rights.
func (s *Service) CheckOwnerReturnAttemptTx(ctx context.Context, tx *sql.Tx, expected Principal, operation, attempt string, q serverreturn.OwnerExchange, route OwnerReturnRoute) error {
	c, _, err := s.currentOwnerCompletionTx(ctx, tx, expected, operation, attempt)
	if err != nil {
		return err
	}
	if c.phase != "consumed" || attempt == "" {
		return ErrForbidden
	}
	return ownerReturnBindingTx(ctx, tx, c, q, route)
}

// CompleteOwnerReturnTx is called only with an unchanged Hosted-signed identity
// verified by Hosted's trust service. Challenge/nonce spending, admission and
// retirement of the initiating family share this final writer transaction.
func (s *Service) CompleteOwnerReturnTx(ctx context.Context, tx *sql.Tx, expected Principal, operation, attempt string, q serverreturn.OwnerExchange, route OwnerReturnRoute, who PorticoIdentity, nonce string) (DirectSignIn, error) {
	var none DirectSignIn
	if err := s.CheckOwnerReturnAttemptTx(ctx, tx, expected, operation, attempt, q, route); err != nil {
		return none, err
	}
	c, err := ownerCompletionTx(ctx, tx, operation)
	if err != nil {
		return none, err
	}
	r := who.Return
	if r == nil || r.Purpose != "switch" || r.CurrentOwnerAccountID != "" || r.PreviousOwnerRole != "" || r.OperationID != operation || r.InstallationID != q.InstallationID || r.RegistrationID != q.RegistrationID || who.Consent == "" || who.SignerKeyID == "" || len(nonce) < 22 || len(nonce) > 128 {
		return none, ErrUnauthorized
	}
	var approvedAccount, approvedUsername string
	if err = tx.QueryRowContext(ctx, `SELECT account_id,approved_username FROM networking_claim_intents WHERE operation_id=?`, operation).Scan(&approvedAccount, &approvedUsername); err != nil {
		return none, err
	}
	if who.AccountID != approvedAccount || who.Username != approvedUsername {
		return none, ErrUnauthorized
	}
	// Final local CAS before anything is consumed: the stored signed start's mode
	// must still equal this confirmed move's own start_over. A legacy start has no
	// mode and is an ordinary switch only.
	var startPayload []byte
	if err = tx.QueryRowContext(ctx, `SELECT start_payload FROM identity_owner_completions WHERE operation_id=?`, operation).Scan(&startPayload); err != nil {
		return none, err
	}
	var storedStart claimweb.OwnerStart
	if json.Unmarshal(startPayload, &storedStart) != nil {
		return none, ErrUnauthorized
	}
	var startOver bool
	if err = tx.QueryRowContext(ctx, `SELECT start_over FROM identity_moves WHERE id=?`, c.move).Scan(&startOver); err != nil {
		return none, err
	}
	want := "switch"
	if startOver {
		want = "start-over"
	}
	if storedStart.TransitionMode != want && !(storedStart.TransitionMode == "" && !startOver) {
		return none, ErrUnauthorized
	}
	if err = CheckPorticoReturnTx(ctx, tx, who); err != nil {
		return none, err
	}
	now := time.Now().UnixMilli()
	result, err := tx.ExecContext(ctx, `DELETE FROM hosted_identity_challenges WHERE challenge_hash=? AND installation_id=? AND expires_ms>?`, c.challenge, c.installation, now)
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
	var device string
	if err = tx.QueryRowContext(ctx, `SELECT b.device_id FROM identity_device_families b JOIN identity_devices d ON d.id=b.device_id WHERE b.family_id=? AND d.authority='local' AND d.account_id=? AND d.installation_id=? AND d.platform='web'`, c.family, c.owner, c.installation).Scan(&device); err != nil {
		return none, err
	}
	out, err := s.PorticoSignInTx(withExistingDevice(ctx, device), tx, who)
	if err != nil {
		return none, err
	}
	if _, err = s.revokeWhereTx(ctx, tx, RevokedExplicitSignout, `id=?`, c.family); err != nil {
		return none, err
	}
	result, err = tx.ExecContext(ctx, `UPDATE identity_owner_completions SET phase='completed',completed_ms=? WHERE operation_id=? AND phase='consumed' AND attempt_id=?`, now, operation, attempt)
	if err != nil {
		return none, err
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		return none, ErrConflict
	}
	if err = CheckPorticoReturnTx(ctx, tx, who); err != nil {
		return none, err
	}
	if time.Now().UnixMilli() >= c.expires {
		return none, ErrUnauthorized
	}
	return out, ctx.Err()
}

func (s *Service) endOwnerReturnTx(ctx context.Context, tx *sql.Tx, c ownerCompletion, phase string) error {
	if phase != "superseded" && phase != "cancelled" && phase != "expired" {
		return ErrDirectInput
	}
	if _, err := tx.ExecContext(ctx, `UPDATE identity_owner_completions SET phase=? WHERE operation_id=? AND phase IN('installed','consumed')`, phase, c.operation); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM hosted_identity_challenges WHERE challenge_hash=? AND installation_id=?`, c.challenge, c.installation); err != nil {
		return err
	}
	_, err := s.revokeWhereTx(ctx, tx, RevokedExplicitSignout, `id=?`, c.family)
	return err
}

// AbandonOwnerReturnTx is a deny-only cleanup after a consumed network attempt
// failed or its response was lost. It cannot cancel the installed central
// claim, change kind, erase again or authorize any positive continuation.
func (s *Service) AbandonOwnerReturnTx(ctx context.Context, tx *sql.Tx, operation, attempt string) error {
	c, err := ownerCompletionTx(ctx, tx, operation)
	if err != nil {
		return err
	}
	if c.phase != "consumed" || attempt == "" || c.attempt != attempt {
		return ErrForbidden
	}
	return s.endOwnerReturnTx(ctx, tx, c, "superseded")
}

// CancelOwnerReturnTx ends only this live browser continuation. The installed
// claim, managed route, linked owner and move data remain installed. Reversing
// that installation still requires the explicit signed-leave operation.
func (s *Service) CancelOwnerReturnTx(ctx context.Context, tx *sql.Tx, expected Principal, operation string) error {
	c, _, err := s.currentOwnerCompletionTx(ctx, tx, expected, operation, "")
	if err != nil {
		return err
	}
	if c.phase != "installed" {
		return ErrForbidden
	}
	return s.endOwnerReturnTx(ctx, tx, c, "cancelled")
}

// RetireOwnerCompletionForClaimTx composes the existing deny-only claim hook.
// The caller has already fenced this exact claim in the same writer. It cannot
// retire a live claim or touch a successor's family; no positive owner proof or
// unexpired browser challenge is needed to finish an already authorized denial.
func (s *Service) RetireOwnerCompletionForClaimTx(ctx context.Context, tx *sql.Tx, operation string) error {
	c, err := ownerCompletionTx(ctx, tx, operation)
	if errors.Is(err, ErrUnauthorized) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.phase != "prepared" && c.phase != "installed" && c.phase != "consumed" {
		return nil
	}
	var denied bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM networking_claim_intents i LEFT JOIN networking_claim_authority a ON a.operation_id=i.operation_id WHERE i.operation_id=? AND i.server_id=? AND (i.stage IN('cancel_pending','cancelled') OR a.state IN('revoked','account_deleted','unclaimed','reassigned')))`, operation, s.ID()).Scan(&denied); err != nil {
		return err
	}
	if !denied {
		return ErrForbidden
	}
	phase := "superseded"
	if c.phase == "prepared" {
		phase = "cancelled"
		if _, err = tx.ExecContext(ctx, `UPDATE identity_moves SET stage='cancelled',revision=revision+1 WHERE id=? AND owner_id=? AND revision=? AND stage='preview'`, c.move, c.owner, c.moveRevision); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE identity_owner_completions SET phase='cancelled' WHERE operation_id=? AND phase='prepared'`, operation); err != nil {
			return err
		}
	}
	return s.endOwnerReturnTx(ctx, tx, c, phase)
}
