package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ErrBadSecondStep: the authenticator or recovery code is wrong, or was already used.
var ErrBadSecondStep = errors.New("second step refused")

// challengeAttempts is how many wrong codes one sign-in challenge survives.
const challengeAttempts = 5

// CheckTOTP checks an authenticator code against an account's secret. It returns the time
// step the code belongs to, which must be later than lastStep. It runs inside the
// transaction, so it must only compute.
type CheckTOTP func(secret []byte, lastStep int64) (step int64, ok bool)

// SecondProof is an authenticator code or, failing that, a recovery code.
type SecondProof struct {
	TOTP CheckTOTP
	// RecoveryHash is the typed code hashed as a recovery code would be stored.
	RecoveryHash string
}

// secondProof settles a second step inside the transaction that acts on it. An accepted
// authenticator code's step is recorded and a recovery code is used up, in the same commit.
func secondProof(tx *sql.Tx, userID int64, p SecondProof, now time.Time) (bool, error) {
	var secret []byte
	var enabled bool
	var last sql.NullInt64
	err := tx.QueryRow(`SELECT two_factor_secret, two_factor_enabled_at IS NOT NULL, two_factor_last_step FROM users WHERE id = ?`, userID).Scan(&secret, &enabled, &last)
	if err != nil || !enabled {
		return false, err
	}
	if step, ok := p.TOTP(secret, last.Int64); ok {
		_, err := tx.Exec(`UPDATE users SET two_factor_last_step = ? WHERE id = ?`, step, userID)
		return err == nil, err
	}
	code, err := redeemCode(tx, "recovery", p.RecoveryHash, now)
	if errors.Is(err, ErrBadCredentials) {
		return false, nil
	}
	return err == nil && code.UserID == userID, err
}

// SecondStep finishes a sign-in whose password step left a challenge.
type SecondStep struct {
	ChallengeHash string
	Proof         func(userID int64) SecondProof
	// Limits counts wrong codes against the source.
	Limits  []Limit
	Session NewSession
	Now     time.Time
}

// FinishSignIn checks the second step and starts the session the challenge was for. A
// challenge that is unknown, expired, used or has had too many wrong codes is
// ErrBadCredentials: the person signs in again. A wrong code is ErrBadSecondStep.
func (d *DB) FinishSignIn(ctx context.Context, s SecondStep) (SessionState, error) {
	var out SessionState
	var refused error
	stamp, err := d.writeSessionsStamped(ctx, func(tx *sql.Tx, _ *changes) error {
		refused = nil
		var userID int64
		var payload string
		var attempts int
		at := s.Now.UnixMilli()
		err := tx.QueryRow(`SELECT user_id, payload, attempts FROM auth_codes
			WHERE code_hash = ? AND kind = 'two_factor' AND used_at IS NULL AND expires_at > ?`, s.ChallengeHash, at).Scan(&userID, &payload, &attempts)
		if errors.Is(err, sql.ErrNoRows) {
			refused = ErrBadCredentials
			return nil
		}
		if err != nil {
			return err
		}
		if wait, err := limitWait(ctx, tx, s.Now, s.Limits); err != nil {
			return err
		} else if wait > 0 {
			refused = &LimitedError{Wait: wait}
			return nil
		}
		ok, err := secondProof(tx, userID, s.Proof(userID), s.Now)
		if err != nil {
			return err
		}
		var disabled bool
		if err := tx.QueryRow(`SELECT disabled_at IS NOT NULL FROM users WHERE id = ?`, userID).Scan(&disabled); err != nil {
			return err
		}
		if !ok || disabled {
			refused = ErrBadSecondStep
			query := `UPDATE auth_codes SET attempts = attempts + 1 WHERE code_hash = ?`
			if attempts+1 >= challengeAttempts {
				query = `DELETE FROM auth_codes WHERE code_hash = ?`
			}
			if _, err := tx.Exec(query, s.ChallengeHash); err != nil {
				return err
			}
			return limitFail(tx, s.Now, s.Limits)
		}
		if _, err := tx.Exec(`UPDATE auth_codes SET used_at = ? WHERE code_hash = ?`, at, s.ChallengeHash); err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(payload), &s.Session.Device); err != nil {
			return err
		}
		out, err = startSession(tx, userID, s.Session, s.Now)
		return err
	})
	if err == nil {
		err = refused
	}
	out.Stamp = stamp
	return out, err
}

// TwoFactor is the state of an account's two-step sign-in.
type TwoFactor struct {
	Enabled   bool
	EnabledAt time.Time
	// RecoveryCodesLeft is how many unused recovery codes remain.
	RecoveryCodesLeft int
}

// TwoFactor reads the state of a user's two-step sign-in.
func (d *DB) TwoFactor(ctx context.Context, userID int64) (TwoFactor, error) {
	var t TwoFactor
	var enabledAt sql.NullInt64
	err := d.read.QueryRowContext(ctx, `SELECT two_factor_enabled_at,
		(SELECT count(*) FROM auth_codes WHERE user_id = users.id AND kind = 'recovery' AND used_at IS NULL)
		FROM users WHERE id = ?`, userID).Scan(&enabledAt, &t.RecoveryCodesLeft)
	if enabledAt.Valid {
		t.Enabled, t.EnabledAt = true, time.UnixMilli(enabledAt.Int64)
	}
	return t, err
}

// PasswordProof is a signed-in user's current password, already compared outside any
// transaction against CheckedHash.
type PasswordProof struct {
	UserID      int64
	CheckedHash string
	OK          bool
	Limits      []Limit
}

// BeginTwoFactor stores a new authenticator secret, not yet in force, for a user who has
// proven their password. Starting again replaces an unconfirmed secret. An account that
// already has two-step sign-in is ErrConflict; a wrong password is ErrBadCredentials.
func (d *DB) BeginTwoFactor(ctx context.Context, p PasswordProof, secret []byte, now time.Time) error {
	var refused error
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		refused = nil
		ok, err := proofStands(tx, now, p.Limits, p.OK, p.UserID, p.CheckedHash, &refused)
		if err != nil || !ok {
			return err
		}
		res, err := tx.Exec(`UPDATE users SET two_factor_secret = ?, two_factor_last_step = NULL WHERE id = ? AND two_factor_enabled_at IS NULL`, secret, p.UserID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrConflict
		}
		return nil
	})
	if err == nil {
		err = refused
	}
	return err
}

// recoveryNeverExpires is the expiry given to recovery codes, which last until used or replaced.
const recoveryNeverExpires = 100 * 365 * 24 * time.Hour

func replaceRecoveryCodes(tx *sql.Tx, userID int64, hashes []string, now time.Time) error {
	if _, err := tx.Exec(`DELETE FROM auth_codes WHERE user_id = ? AND kind = 'recovery'`, userID); err != nil {
		return err
	}
	for _, hash := range hashes {
		if err := insertCode(tx, Code{Kind: "recovery", Hash: hash, UserID: userID, ExpiresAt: now.Add(recoveryNeverExpires)}, now); err != nil {
			return err
		}
	}
	return nil
}

// ConfirmTwoFactor turns two-step sign-in on once the person shows a code from the
// authenticator they just set up, and stores their recovery codes. With nothing to confirm
// it is ErrNotFound; already on, ErrConflict; a wrong code, ErrBadSecondStep.
func (d *DB) ConfirmTwoFactor(ctx context.Context, userID int64, check CheckTOTP, recoveryHashes []string, limits []Limit, now time.Time) error {
	var refused error
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		refused = nil
		var secret []byte
		var enabled bool
		if err := tx.QueryRow(`SELECT two_factor_secret, two_factor_enabled_at IS NOT NULL FROM users WHERE id = ?`, userID).Scan(&secret, &enabled); err != nil {
			return err
		}
		switch {
		case enabled:
			return ErrConflict
		case len(secret) == 0:
			return ErrNotFound
		}
		if wait, err := limitWait(ctx, tx, now, limits); err != nil {
			return err
		} else if wait > 0 {
			refused = &LimitedError{Wait: wait}
			return nil
		}
		step, ok := check(secret, 0)
		if !ok {
			refused = ErrBadSecondStep
			return limitFail(tx, now, limits)
		}
		if _, err := tx.Exec(`UPDATE users SET two_factor_enabled_at = ?, two_factor_last_step = ?, updated_at = ? WHERE id = ?`,
			now.UnixMilli(), step, now.UnixMilli(), userID); err != nil {
			return err
		}
		return replaceRecoveryCodes(tx, userID, recoveryHashes, now)
	})
	if err == nil {
		err = refused
	}
	return err
}

// provenTwice runs change for a user who proves both their password and a second step.
// Without two-step sign-in it is ErrNotFound.
func (d *DB) provenTwice(ctx context.Context, p PasswordProof, second SecondProof, now time.Time, change func(tx *sql.Tx) error) error {
	var refused error
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		refused = nil
		var enabled bool
		if err := tx.QueryRow(`SELECT two_factor_enabled_at IS NOT NULL FROM users WHERE id = ?`, p.UserID).Scan(&enabled); err != nil {
			return err
		}
		if !enabled {
			return ErrNotFound
		}
		ok, err := proofStands(tx, now, p.Limits, p.OK, p.UserID, p.CheckedHash, &refused)
		if err != nil || !ok {
			return err
		}
		if ok, err = secondProof(tx, p.UserID, second, now); err != nil {
			return err
		} else if !ok {
			refused = ErrBadSecondStep
			return limitFail(tx, now, p.Limits)
		}
		return change(tx)
	})
	if err == nil {
		err = refused
	}
	return err
}

// ReplaceRecoveryCodes swaps a user's recovery codes for new ones.
func (d *DB) ReplaceRecoveryCodes(ctx context.Context, p PasswordProof, second SecondProof, hashes []string, now time.Time) error {
	return d.provenTwice(ctx, p, second, now, func(tx *sql.Tx) error {
		return replaceRecoveryCodes(tx, p.UserID, hashes, now)
	})
}

// DisableTwoFactor turns two-step sign-in off and forgets the secret and recovery codes.
func (d *DB) DisableTwoFactor(ctx context.Context, p PasswordProof, second SecondProof, now time.Time) error {
	return d.provenTwice(ctx, p, second, now, func(tx *sql.Tx) error {
		return clearTwoFactor(tx, p.UserID, now)
	})
}

func clearTwoFactor(tx *sql.Tx, userID int64, now time.Time) error {
	if _, err := tx.Exec(`UPDATE users SET two_factor_secret = NULL, two_factor_enabled_at = NULL, two_factor_last_step = NULL, updated_at = ?
		WHERE id = ?`, now.UnixMilli(), userID); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM auth_codes WHERE user_id = ? AND kind IN ('recovery', 'two_factor')`, userID)
	return err
}
