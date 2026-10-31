package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
)

// InstallOwnerClaimTx is called only by the networking installation hook. The
// current credential and installed pointers already exist in this transaction;
// none is visible until this owner transition and the final claim guard commit.
// It does not admit a session from an identity assertion or claim approval.
func (s *Service) InstallOwnerClaimTx(ctx context.Context, tx *sql.Tx, operation string) error {
	c, family, err := s.ownerCompletionAuthorityTx(ctx, tx, nil, operation, "", true)
	if err != nil {
		return err
	}
	if c.transaction == "" {
		return ErrUnauthorized
	}
	var account, username string
	if err = tx.QueryRowContext(ctx, `SELECT account_id,approved_username FROM networking_claim_intents WHERE operation_id=? AND owner_id=? AND owner_profile_id=? AND owner_epoch=? AND purpose='switch' AND current_owner_account_id='' AND owner_change_reserved=1`, operation, c.owner, c.profile, c.epoch).Scan(&account, &username); err != nil {
		return err
	}
	// Final CAS: the stored signed start's mode must still be the confirmed move's
	// mode. A legacy start with no mode is an ordinary switch only.
	var startPayload []byte
	if err = tx.QueryRowContext(ctx, `SELECT start_payload FROM identity_owner_completions WHERE operation_id=?`, operation).Scan(&startPayload); err != nil {
		return err
	}
	var storedStart claimweb.OwnerStart
	if json.Unmarshal(startPayload, &storedStart) != nil {
		return ErrUnauthorized
	}
	var startOver bool
	if err = tx.QueryRowContext(ctx, `SELECT start_over FROM identity_moves WHERE id=?`, c.move).Scan(&startOver); err != nil {
		return err
	}
	want := "switch"
	if startOver {
		want = "start-over"
	}
	if storedStart.TransitionMode != want && !(storedStart.TransitionMode == "" && !startOver) {
		return ErrUnauthorized
	}
	if !validHostedAccountID(account) || !directName(username) || len(username) < 3 || len(username) > 64 || username != strings.ToLower(strings.TrimSpace(username)) || strings.ContainsAny(username, " \t\r\n@") {
		return ErrUnauthorized
	}
	var collision bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_portico_links WHERE hosted_account_id=? AND account_id<>?) OR EXISTS(SELECT 1 FROM accounts WHERE username=? COLLATE NOCASE AND id<>?)`, account, c.owner, username, c.owner).Scan(&collision); err != nil {
		return err
	}
	if collision {
		return ErrMoveCollision
	}
	move, err := readMoveTx(ctx, tx, c.move)
	if err != nil {
		return err
	}
	var confirmation string
	if err = tx.QueryRowContext(ctx, `SELECT confirmation FROM identity_moves WHERE id=?`, c.move).Scan(&confirmation); err != nil {
		return err
	}
	caller := directCaller{account: DirectAccount{ID: c.owner, PrimaryProfileID: c.profile, Role: TierOwner}, epoch: int(c.epoch), familyID: c.family}
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM direct_memberships WHERE account_id=? AND role='owner' AND disabled=0`, c.owner).Scan(&caller.account.Revision); err != nil {
		return err
	}
	if move, err = s.confirmMoveTx(ctx, tx, caller, MoveConfirmRequest{MoveID: c.move, ExpectedRevision: c.moveRevision, Confirmation: confirmation}, SignInPortico); err != nil {
		return err
	}
	if err = s.prepareMoveMembersKeepingFamilyTx(ctx, tx, caller, move, family.ID); err != nil {
		return err
	}
	// The reservation triggers also protect the target from this writer. Release
	// it only here, after the final collision/owner checks, while the installation
	// writer still holds the lock. Any later failure restores it with the move.
	released, err := tx.ExecContext(ctx, `UPDATE networking_claim_intents SET owner_change_reserved=0 WHERE operation_id=? AND owner_change_reserved=1`, operation)
	if err != nil {
		return err
	}
	if n, e := released.RowsAffected(); e != nil || n != 1 {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE identity_state SET sign_in_kind='portico',revision=revision+1 WHERE singleton=1 AND sign_in_kind='local'`); err != nil {
		return err
	}
	// The approved account is bound now. Consent for discovery arrives only with
	// the later unchanged Hosted-signed assertion; a claim is not identity consent.
	if _, err = tx.ExecContext(ctx, `INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES(?,?)`, c.owner, account); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE accounts SET username=? WHERE id=?`, username, c.owner); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE identity_managed_route SET enabled=1,revision=revision+1 WHERE singleton=1`); err != nil {
		return err
	}
	stage := "moving_to_portico"
	if move.StartOver || move.People == 0 {
		stage = "complete"
	}
	now := time.Now().UnixMilli()
	if _, err = tx.ExecContext(ctx, `UPDATE identity_moves SET stage=?,revision=revision+1,confirmed_ms=? WHERE id=?`, stage, now, c.move); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO authorization_family_grants(family_id,grant_kind) VALUES(?,?)`, family.ID, GrantOwnerClaimCompletion); err != nil {
		return err
	}
	// The retained token no longer keeps playback, queue or reader authority.
	// Retire those consumers through the same hook as sign-out, without ending
	// the one narrowly scoped operation token which must finish this transition.
	if s.OnFamilyRevokedTx != nil {
		if err = s.OnFamilyRevokedTx(ctx, tx, family.FamilyState); err != nil {
			return err
		}
	}
	deadline := time.UnixMilli(c.expires).UTC().Truncate(time.Second).Format(time.RFC3339)
	// Compare instants in Go, not differently precise RFC3339 text. The guard
	// proved both existing limits still live; narrowing never extends either.
	if family.AuthorizationHorizon.After(time.UnixMilli(c.expires)) {
		if _, err = tx.ExecContext(ctx, `UPDATE authorization_session_families SET authorization_horizon=? WHERE id=?`, deadline, family.ID); err != nil {
			return err
		}
	}
	if family.tokenExpiry.After(time.UnixMilli(c.expires)) {
		// The family's current token, which the move's own refreshes may have rotated.
		if _, err = tx.ExecContext(ctx, `UPDATE authorization_family_tokens SET expires_at=? WHERE family_id=? AND generation=?`, deadline, family.ID, family.TokenGeneration); err != nil {
			return err
		}
	}
	for _, query := range []string{`DELETE FROM identity_refresh_credentials WHERE family_id=?`, `DELETE FROM identity_refresh_receipts WHERE family_id=?`, `DELETE FROM identity_refresh_predecessors WHERE family_id=?`, `DELETE FROM authorization_family_renewals WHERE family_id=?`} {
		if _, err = tx.ExecContext(ctx, query, family.ID); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE identity_owner_completions SET phase='installed',installed_ms=? WHERE operation_id=? AND phase='prepared' AND expires_ms>?`, now, operation, now)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return ErrConflict
	}
	_, _, err = s.ownerCompletionAuthorityTx(ctx, tx, nil, operation, "", false)
	return err
}
