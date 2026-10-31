package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// SecurityReturnActorTx is a read-only current-session proof for the distinct
// provider Security purpose. It grants no sign-in or account management itself.
// The caller signs/stores the result in this same accepting transaction.
func (s *Service) SecurityReturnActorTx(ctx context.Context, tx *sql.Tx, bearer string) (account, binding string, expires time.Time, err error) {
	if tx == nil || CredentialShaped(bearer) {
		return "", "", time.Time{}, ErrUnauthorized
	}
	if err = RequireSignInKindTx(ctx, tx, SignInPortico); err != nil {
		return
	}
	c, err := s.directCallerTx(ctx, tx, bearer, true)
	if err != nil {
		return "", "", time.Time{}, err
	}
	record, err := s.familyTokenTx(ctx, tx, Digest(bearer))
	if err != nil {
		return "", "", time.Time{}, err
	}
	if record.TokenGeneration != record.currentGeneration || record.retired {
		return "", "", time.Time{}, ErrUnauthorized
	}
	if err = s.RequireMainProfileTx(ctx, tx, record.principal); err != nil {
		return "", "", time.Time{}, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT hosted_account_id FROM account_portico_links WHERE account_id=?`, c.account.ID).Scan(&account); err != nil {
		return "", "", time.Time{}, ErrUnauthorized
	}
	if !validHostedAccountID(account) {
		return "", "", time.Time{}, ErrUnauthorized
	}
	// The return is bound to the sign-in, never to one token: a routine refresh
	// between prepare and confirm keeps the same binding and deadline (the
	// family's horizon, which a refresh never extends for a Portico sign-in). The
	// pressing call itself still needs the current, unexpired token.
	if !record.tokenExpiry.After(time.Now()) {
		return "", "", time.Time{}, ErrUnauthorized
	}
	expires = record.AuthorizationHorizon
	if !expires.After(time.Now()) {
		return "", "", time.Time{}, ErrUnauthorized
	}
	raw, _ := json.Marshal([]any{c.account.ID, c.profileID, c.familyID, c.epoch, c.account.Revision, c.account.Role, c.deviceID, account, expires.UTC().Format(time.RFC3339Nano)})
	return account, s.setupHash("security-return-actor", string(raw)), expires, nil
}
