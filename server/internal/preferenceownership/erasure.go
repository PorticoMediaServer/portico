// Package preferenceownership provides the identity erasure seam without an
// identity/operations import cycle. Callers authorize and own the transaction.
package preferenceownership

import (
	"context"
	"database/sql"
	"errors"
)

// EraseProfile removes only this authority/account/profile's overrides. Values
// and ordered language items are foreign-key children of the scope rows.
func EraseProfile(ctx context.Context, tx *sql.Tx, authority, account, profile string) error {
	if authority == "" || account == "" || profile == "" {
		return errors.New("preference erasure requires a complete owner")
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM preference_scopes WHERE authority=? AND account_id=? AND profile_id=?`, authority, account, profile)
	return err
}

// EraseAccount is called only when the account itself is erased, never during
// a sign-in-kind move or while an account is Waiting, disabled or Left.
func EraseAccount(ctx context.Context, tx *sql.Tx, authority, account string) error {
	if authority == "" || account == "" {
		return errors.New("preference erasure requires a complete owner")
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM preference_scopes WHERE authority=? AND account_id=?`, authority, account)
	return err
}
