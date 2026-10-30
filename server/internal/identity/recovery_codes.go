package identity

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// RecoveryCodes are returned once; only account-bound hashes are stored.
type RecoveryCodes struct {
	Codes []string `json:"codes"`
}

// ReplaceRecoveryCodes requires the real current password and a live confirmed
// factor proof. The factor's own replay accounting and the complete replacement
// commit together. A refusal retains every previously usable recovery code.
func (s *Service) ReplaceRecoveryCodes(ctx context.Context, bearer, password, code string) (RecoveryCodes, error) {
	if password == "" || len(password) > 72 {
		return RecoveryCodes{}, ErrDirectInput
	}
	proof, err := s.proveCurrentPassword(ctx, bearer, password)
	if err != nil {
		return RecoveryCodes{}, err
	}
	codes, err := newRecoveryCodes()
	if err != nil {
		return RecoveryCodes{}, err
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if err != nil {
		return RecoveryCodes{}, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	caller, err := s.checkCurrentPasswordProofTx(ctx, tx, bearer, proof)
	if err != nil {
		return RecoveryCodes{}, err
	}
	var confirmed bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_account_factors WHERE account_id=? AND confirmed=1)`, caller.account.ID).Scan(&confirmed); err != nil {
		return RecoveryCodes{}, err
	}
	if !confirmed {
		return RecoveryCodes{}, ErrFactorMissing
	}
	if err = s.verifySecondFactorTx(ctx, tx, caller.account.ID, code); err != nil {
		return RecoveryCodes{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM identity_recovery_codes WHERE account_id=?`, caller.account.ID); err != nil {
		return RecoveryCodes{}, err
	}
	for _, value := range codes {
		if _, err = tx.ExecContext(ctx, `INSERT INTO identity_recovery_codes(account_id,code_hash,used) VALUES(?,?,0)`, caller.account.ID, s.setupHash("mfa-recovery:"+caller.account.ID, normalizeRecoveryCode(value))); err != nil {
			return RecoveryCodes{}, err
		}
	}
	if err = s.securityNoticeTx(ctx, tx, caller.account.ID, "security.recovery_codes_replaced"); err != nil {
		return RecoveryCodes{}, err
	}
	if err = gated.Commit(); err != nil {
		return RecoveryCodes{}, err
	}
	return RecoveryCodes{Codes: codes}, nil
}
