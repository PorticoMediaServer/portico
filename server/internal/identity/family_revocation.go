package identity

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"
)

type RevocationReason string

const (
	RevokedRefreshReuse      RevocationReason = "refresh_reuse"
	RevokedExplicitSignout   RevocationReason = "explicit_signout"
	RevokedAdminRevoke       RevocationReason = "admin_revoke"
	RevokedMembershipRemoved RevocationReason = "membership_removed"
	RevokedPasswordChange    RevocationReason = "password_change"
	// RevokedSigningKeyRevoked: the Hosted signing key whose assertion admitted
	// the session was revoked.
	RevokedSigningKeyRevoked RevocationReason = "signing_key_revoked"
	// RevokedRestored: the server's data was restored from a backup, which
	// signs everyone out.
	RevokedRestored RevocationReason = "restored"
)

func validRevocationReason(reason RevocationReason) bool {
	switch reason {
	case RevokedRefreshReuse, RevokedExplicitSignout, RevokedAdminRevoke, RevokedMembershipRemoved, RevokedPasswordChange, RevokedSigningKeyRevoked, RevokedRestored:
		return true
	}
	return false
}

// RevokeFamilyTx stamps one active family and logs its identifier prefix. It
// never logs a bearer, refresh credential, or full family identifier.
func RevokeFamilyTx(ctx context.Context, tx *sql.Tx, id string, reason RevocationReason) error {
	if !validRevocationReason(reason) {
		return errors.New("invalid revocation reason")
	}
	var revoked string
	err := tx.QueryRowContext(ctx, `UPDATE authorization_session_families SET revoked=1,revoked_reason=?,revoked_at=? WHERE id=? AND revoked=0 RETURNING id`, string(reason), time.Now().UTC().Format(time.RFC3339), id).Scan(&revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	prefix := revoked
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	log.Printf("authorization family revoked family=%s reason=%s", prefix, reason)
	return nil
}

// RevokeFamiliesMatchingTx is for trusted, static internal predicates; values
// must always be passed as args. Each family gets its own reason and log line.
func RevokeFamiliesMatchingTx(ctx context.Context, tx *sql.Tx, reason RevocationReason, predicate string, args ...any) error {
	if !validRevocationReason(reason) {
		return errors.New("invalid revocation reason")
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM authorization_session_families WHERE revoked=0 AND (`+predicate+`)`, args...)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = RevokeFamilyTx(ctx, tx, id, reason); err != nil {
			return err
		}
	}
	return nil
}

// RetierFamiliesTx carries a change of an account's tier to the sessions it
// already holds, in the transaction that changes the tier, so nobody is signed
// out for being promoted or demoted. A session's tier is stored with it and
// checked against the membership on every request; a session left claiming a
// tier the account no longer has would be refused, which is what used to make
// a tier change end every sign-in.
//
// Only sessions of the account's primary profile carry its tier. Sessions of
// its other profiles are members whatever the account is, the account session
// a device holds between sign-in and choosing a profile has no tier at all,
// and a session that was issued narrower than its account on purpose (a
// viewer-only device, a Cast target) stays as narrow as it was issued.
func RetierFamiliesTx(ctx context.Context, tx *sql.Tx, account, role string) error {
	if !ValidTier(role) {
		return errors.New("invalid tier")
	}
	_, err := tx.ExecContext(ctx, `UPDATE authorization_session_families SET role=?
 WHERE authority='local' AND account_id=? AND revoked=0 AND role IN('owner','admin','member') AND role<>?
 AND profile_id=(SELECT profile_id FROM accounts WHERE id=?)
 AND NOT EXISTS(SELECT 1 FROM authorization_family_grants g WHERE g.family_id=authorization_session_families.id)`, role, account, role, account)
	return err
}
