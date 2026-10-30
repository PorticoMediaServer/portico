package identity

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ReauthorizeTx rechecks an authenticated principal inside the transaction that
// will mutate its resources. It never acquires another database connection.
// Hosted callers must additionally check cached membership/library policy in tx.
// Token-family renewal and rotation are separate, not implied by this check.
func (s *Service) ReauthorizeTx(ctx context.Context, tx *sql.Tx, expected Principal) (Principal, error) {
	return s.reauthorizeTx(ctx, tx, expected, false)
}

// Only the credential-proven Cast renewal parent path may omit token expiry.
func (s *Service) reauthorizeTx(ctx context.Context, tx *sql.Tx, expected Principal, expiredCastParent bool) (Principal, error) {
	if tx == nil || expected.Hash == "" {
		return Principal{}, ErrUnauthorized
	}
	if err := ctx.Err(); err != nil {
		return Principal{}, err
	}
	// Preserve already verified accounting metadata. No authority guard uses
	// DeviceID; identity is still checked against the live token and account.
	current := Principal{Hash: expected.Hash, DeviceID: expected.DeviceID}
	var expiry, horizon string
	var revoked int
	err := tx.QueryRowContext(ctx, `SELECT a.account_id,a.profile_id,a.authority,a.role,a.epoch,a.expires_at,a.revoked,f.server_id,f.authorization_horizon,COALESCE(g.grant_kind,'') FROM authorization_access a JOIN authorization_family_tokens t ON t.token_hash=a.hash JOIN authorization_session_families f ON f.id=t.family_id LEFT JOIN authorization_family_grants g ON g.family_id=f.id WHERE a.hash=?`, expected.Hash).
		Scan(&current.AccountID, &current.ProfileID, &current.Authority, &current.Role, &current.Epoch, &expiry, &revoked, &current.ServerID, &horizon, &current.Grant)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Principal{}, ErrUnauthorized
		}
		return Principal{}, err
	}
	expires, err := time.Parse(time.RFC3339, expiry)
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	authorizedUntil, err := time.Parse(time.RFC3339, horizon)
	if err != nil {
		return Principal{}, ErrUnauthorized
	}
	current.ExpiresAt = expires
	if authorizedUntil.Before(current.ExpiresAt) {
		current.ExpiresAt = authorizedUntil
	}
	if revoked != 0 || !authorizedUntil.After(time.Now()) || (!expiredCastParent && !expires.After(time.Now())) || current.ServerID != s.serverID || current.Viewer != expected.Viewer || current.Epoch != expected.Epoch || current.Grant != expected.Grant {
		return Principal{}, ErrUnauthorized
	}
	switch current.Authority {
	case "local":
		if err = s.checkDirectPrincipalTx(tx, current); err != nil {
			return Principal{}, err
		}
	default:
		return Principal{}, ErrUnauthorized
	}
	if err := ctx.Err(); err != nil {
		return Principal{}, err
	}
	return current, nil
}
