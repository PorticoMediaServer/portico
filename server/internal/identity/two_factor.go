package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strings"
	"time"
)

var ErrFactorRequired = errors.New("Enter the code from your authenticator app.")
var ErrFactorInvalid = errors.New("That verification code is not correct.")
var ErrFactorEnrolled = errors.New("Two-factor authentication is already enabled for this account.")
var ErrFactorMissing = errors.New("Two-factor authentication is not enabled for this account.")

// A direct server has no mail transport, so the only second factors it can offer
// are ones the account holder carries: a time-based authenticator and a printed
// set of recovery codes. Both are verified here; neither is ever logged.

const totpPeriod = 30
const totpDigits = 6
const totpDrift = 1

var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func newTOTPSecret() (string, error) {
	raw := make([]byte, 20)
	if _, e := rand.Read(raw); e != nil {
		return "", e
	}
	return totpEncoding.EncodeToString(raw), nil
}

func totpCode(key []byte, step uint64) string {
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], step)
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	divisor := uint32(1)
	for i := 0; i < totpDigits; i++ {
		divisor *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, value%divisor)
}

// totpStep verifies a code against the secret and returns the step it matched.
// A step at or below lastStep is refused: a code is usable once, so an observer
// of a code on screen cannot replay it inside its own validity window.
func totpStep(secret, code string, now time.Time, lastStep int64) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return 0, false
	}
	key, e := totpEncoding.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if e != nil || len(key) == 0 {
		return 0, false
	}
	current := now.Unix() / totpPeriod
	for delta := int64(-totpDrift); delta <= totpDrift; delta++ {
		step := current + delta
		if step <= lastStep || step < 0 {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(totpCode(key, uint64(step))), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

func newRecoveryCodes() ([]string, error) {
	out := make([]string, 0, 10)
	for len(out) < 10 {
		raw := make([]byte, 8)
		if _, e := rand.Read(raw); e != nil {
			return nil, e
		}
		text := hex.EncodeToString(raw)
		out = append(out, text[:4]+"-"+text[4:8]+"-"+text[8:12]+"-"+text[12:])
	}
	return out, nil
}

func normalizeRecoveryCode(v string) string {
	return strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(v)))
}

// TwoFactorState is what a client renders on the account security screen.
type TwoFactorState struct {
	Enabled                bool `json:"enabled"`
	PendingEnrolment       bool `json:"pendingEnrolment"`
	RecoveryCodesRemaining int  `json:"recoveryCodesRemaining"`
}

// TwoFactorEnrolment is returned once, at enrolment. The secret and the recovery
// codes are never readable again; a client that loses them must re-enrol.
type TwoFactorEnrolment struct {
	Secret        string   `json:"secret"`
	URI           string   `json:"uri"`
	RecoveryCodes []string `json:"recoveryCodes"`
}

// verifySecondFactorTx accepts either a TOTP code or an unused recovery code when
// a confirmed factor exists, and accepts an empty code when none does. Recovery
// codes are consumed on use.
func (s *Service) verifySecondFactorTx(ctx context.Context, tx *sql.Tx, account, code string) error {
	var secret string
	var lastStep int64
	e := tx.QueryRowContext(ctx, `SELECT secret,last_step FROM identity_account_factors WHERE account_id=? AND confirmed=1`, account)
	if err := e.Scan(&secret, &lastStep); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	var err error
	secret, err = s.openFactorSecret(account, secret)
	if err != nil {
		return err
	}
	if strings.TrimSpace(code) == "" {
		return ErrFactorRequired
	}
	if step, ok := totpStep(secret, code, time.Now().UTC(), lastStep); ok {
		_, err := tx.ExecContext(ctx, `UPDATE identity_account_factors SET last_step=? WHERE account_id=?`, step, account)
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE identity_recovery_codes SET used=1 WHERE account_id=? AND code_hash=? AND used=0`, account, s.setupHash("mfa-recovery:"+account, normalizeRecoveryCode(code)))
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 1 {
		return nil
	}
	return ErrFactorInvalid
}

// TwoFactor reports the account's current factor state.
func (s *Service) TwoFactor(ctx context.Context, bearer string) (TwoFactorState, error) {
	ctx = factorSetupContext(ctx)
	gated, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return TwoFactorState{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	c, e := s.directCallerTx(ctx, tx, bearer, true)
	if errors.Is(e, ErrPasswordChangeRequired) {
		// The existing read-only security route may show the conditional reset
		// choice only after a genuine local owner's file proof. This context
		// never escapes the read and gives no factor mutation authority.
		resetCtx := context.WithValue(ctx, passwordChangeContextKey{}, true)
		candidate, ce := s.directCallerTx(resetCtx, tx, bearer, true)
		if ce == nil && candidate.account.Role == TierOwner {
			kind, ke := resetSessionTx(resetCtx, tx, candidate.familyID)
			if ke != nil {
				return TwoFactorState{}, ke
			}
			if kind == "password" {
				c, e = candidate, nil
			}
		}
	}
	if e != nil {
		return TwoFactorState{}, e
	}
	out := TwoFactorState{}
	var confirmed sql.NullBool
	if e = tx.QueryRowContext(ctx, `SELECT confirmed FROM identity_account_factors WHERE account_id=?`, c.account.ID).Scan(&confirmed); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	out.Enabled = confirmed.Valid && confirmed.Bool
	out.PendingEnrolment = confirmed.Valid && !confirmed.Bool
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM identity_recovery_codes WHERE account_id=? AND used=0`, c.account.ID).Scan(&out.RecoveryCodesRemaining); e != nil {
		return out, e
	}
	return out, nil
}

// EnrolTwoFactor starts enrolment. The factor is not in force until VerifyTwoFactor
// accepts a code from it, so a mistyped secret cannot lock the account holder out.
func (s *Service) EnrolTwoFactor(ctx context.Context, bearer, password string) (TwoFactorEnrolment, error) {
	ctx = factorSetupContext(ctx)
	proof, e := s.proveCurrentPassword(ctx, bearer, password)
	if e != nil {
		return TwoFactorEnrolment{}, e
	}
	secret, e := newTOTPSecret()
	if e != nil {
		return TwoFactorEnrolment{}, e
	}
	codes, e := newRecoveryCodes()
	if e != nil {
		return TwoFactorEnrolment{}, e
	}
	// The issuer name is read before the transaction opens: SQLite serves one
	// connection, so touching s.db while a transaction is held would deadlock.
	issuer := s.Name()
	gated2, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return TwoFactorEnrolment{}, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	c, e := s.checkCurrentPasswordProofTx(ctx, tx, bearer, proof)
	if e != nil {
		return TwoFactorEnrolment{}, e
	}
	var confirmed bool
	if e = tx.QueryRowContext(ctx, `SELECT confirmed FROM identity_account_factors WHERE account_id=?`, c.account.ID).Scan(&confirmed); e != nil && !errors.Is(e, sql.ErrNoRows) {
		return TwoFactorEnrolment{}, e
	} else if e == nil && confirmed {
		return TwoFactorEnrolment{}, ErrFactorEnrolled
	}
	sealed, err := s.sealSetup(secret, "mfa-secret:"+c.account.ID)
	if err != nil {
		return TwoFactorEnrolment{}, err
	}
	storedSecret := base64.RawURLEncoding.EncodeToString(sealed)
	if _, e = tx.ExecContext(ctx, `INSERT INTO identity_account_factors(account_id,secret,confirmed,last_step,enrolled_at) VALUES(?,?,0,0,?) ON CONFLICT(account_id) DO UPDATE SET secret=excluded.secret,confirmed=0,last_step=0,enrolled_at=excluded.enrolled_at`, c.account.ID, storedSecret, time.Now().UTC().Format(time.RFC3339)); e != nil {
		return TwoFactorEnrolment{}, e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM identity_recovery_codes WHERE account_id=?`, c.account.ID); e != nil {
		return TwoFactorEnrolment{}, e
	}
	for _, code := range codes {
		if _, e = tx.ExecContext(ctx, `INSERT INTO identity_recovery_codes(account_id,code_hash,used) VALUES(?,?,0)`, c.account.ID, s.setupHash("mfa-recovery:"+c.account.ID, normalizeRecoveryCode(code))); e != nil {
			return TwoFactorEnrolment{}, e
		}
	}
	label := url.PathEscape(issuer + ":" + c.account.Username)
	uri := "otpauth://totp/" + label + "?secret=" + secret + "&issuer=" + url.QueryEscape(issuer) + "&algorithm=SHA1&digits=6&period=30"
	return TwoFactorEnrolment{Secret: secret, URI: uri, RecoveryCodes: codes}, gated2.Commit()
}

// VerifyTwoFactor puts a pending enrolment into force.
func (s *Service) VerifyTwoFactor(ctx context.Context, bearer, password, code string) (TwoFactorState, error) {
	if err := s.sourceBlocked(ctx); err != nil {
		return TwoFactorState{}, err
	}
	ctx = factorSetupContext(ctx)
	proof, e := s.proveCurrentPassword(ctx, bearer, password)
	if e != nil {
		return TwoFactorState{}, e
	}
	gated3, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return TwoFactorState{}, e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	c, e := s.checkCurrentPasswordProofTx(ctx, tx, bearer, proof)
	if e != nil {
		return TwoFactorState{}, e
	}
	var secret string
	var confirmed bool
	var lastStep int64
	if e = tx.QueryRowContext(ctx, `SELECT secret,confirmed,last_step FROM identity_account_factors WHERE account_id=?`, c.account.ID).Scan(&secret, &confirmed, &lastStep); errors.Is(e, sql.ErrNoRows) {
		return TwoFactorState{}, ErrFactorMissing
	} else if e != nil {
		return TwoFactorState{}, e
	}
	if confirmed {
		return TwoFactorState{}, ErrFactorEnrolled
	}
	secret, e = s.openFactorSecret(c.account.ID, secret)
	if e != nil {
		return TwoFactorState{}, e
	}
	step, ok := totpStep(secret, code, time.Now().UTC(), lastStep)
	if !ok {
		// The refusal ends this transaction, so the source's charge is made in
		// its own, after this one is released.
		if rollbackErr := gated3.Rollback(); rollbackErr != nil {
			return TwoFactorState{}, rollbackErr
		}
		if e = s.RecordSourceFailure(ctx); e != nil {
			return TwoFactorState{}, e
		}
		return TwoFactorState{}, ErrFactorInvalid
	}
	if _, e = tx.ExecContext(ctx, `UPDATE identity_account_factors SET confirmed=1,last_step=? WHERE account_id=?`, step, c.account.ID); e != nil {
		return TwoFactorState{}, e
	}
	var remaining int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM identity_recovery_codes WHERE account_id=? AND used=0`, c.account.ID).Scan(&remaining); e != nil {
		return TwoFactorState{}, e
	}
	if e = s.securityNoticeTx(ctx, tx, c.account.ID, "security.mfa_enabled"); e != nil {
		return TwoFactorState{}, e
	}
	return TwoFactorState{Enabled: true, RecoveryCodesRemaining: remaining}, gated3.Commit()
}

// DisableTwoFactor removes the factor. The account password is required so a
// borrowed signed-in device cannot quietly weaken the account.
func (s *Service) DisableTwoFactor(ctx context.Context, bearer, password, code string) error {
	if err := s.sourceBlocked(ctx); err != nil {
		return err
	}
	proof, e := s.proveCurrentPassword(ctx, bearer, password)
	if e != nil {
		return e
	}
	gated4, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	c, e := s.checkCurrentPasswordProofTx(ctx, tx, bearer, proof)
	if e != nil {
		return e
	}
	if required, e := requiresFactorTx(ctx, tx, c.account.ID); e != nil {
		return e
	} else if required {
		return ErrForbidden
	}
	var exists bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_account_factors WHERE account_id=?)`, c.account.ID).Scan(&exists); e != nil {
		return e
	}
	if !exists {
		return ErrFactorMissing
	}
	if e = s.verifySecondFactorTx(ctx, tx, c.account.ID, code); e != nil {
		if errors.Is(e, ErrFactorInvalid) {
			// A wrong factor code is a guess; the refusal rolls this
			// transaction back, so charge the source in its own.
			if rollbackErr := gated4.Rollback(); rollbackErr != nil {
				return rollbackErr
			}
			if chargeErr := s.RecordSourceFailure(ctx); chargeErr != nil {
				return chargeErr
			}
		}
		return e
	}
	if e = s.revokeDirectWithReasonTx(ctx, tx, c.account.ID, "", "", RevokedPasswordChange); e != nil {
		return e
	}
	if e = s.securityNoticeTx(ctx, tx, c.account.ID, "security.mfa_disabled"); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM identity_account_factors WHERE account_id=?`, c.account.ID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM identity_recovery_codes WHERE account_id=?`, c.account.ID); e != nil {
		return e
	}
	return gated4.Commit()
}

// SignInChallenge is returned instead of an account session when the account has a
// confirmed second factor. The challenge is bound to the account and expires.
type SignInChallenge struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
}

const signInChallengeLifetime = 5 * time.Minute
const signInChallengeAttempts = 5

// issueSignInChallengeTx replaces a completed sign-in with a pending challenge.
func (s *Service) issueSignInChallengeTx(ctx context.Context, tx *sql.Tx, account string) (SignInChallenge, error) {
	now := time.Now().UTC()
	out := SignInChallenge{Token: Token(), ExpiresAt: now.Add(signInChallengeLifetime).Format(time.RFC3339)}
	if _, e := tx.ExecContext(ctx, `DELETE FROM identity_factor_challenges WHERE expires_at<=? OR account_id=?`, now.Format(time.RFC3339), account); e != nil {
		return SignInChallenge{}, e
	}
	if _, e := tx.ExecContext(ctx, `INSERT INTO identity_factor_challenges(token_hash,account_id,expires_at,attempts) VALUES(?,?,?,0)`, Digest(out.Token), account, out.ExpiresAt); e != nil {
		return SignInChallenge{}, e
	}
	return out, nil
}

// AccountFactorRequired reports whether a confirmed factor stands for an account.
func (s *Service) accountFactorRequiredTx(ctx context.Context, tx *sql.Tx, account string) (bool, error) {
	var confirmed bool
	e := tx.QueryRowContext(ctx, `SELECT confirmed FROM identity_account_factors WHERE account_id=?`, account).Scan(&confirmed)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	return confirmed, e
}

// CompleteSignInChallenge exchanges a challenge plus a factor code for the account
// session the password step would otherwise have returned.
func (s *Service) CompleteSignInChallenge(ctx context.Context, token, code string, private bool) (DirectSignIn, error) {
	if len(token) != 43 || len(code) > 64 {
		return DirectSignIn{}, ErrUnauthorized
	}
	if RemoteSignInClosed(ctx) {
		return DirectSignIn{}, ErrRemoteSignIn
	}
	if err := s.sourceBlocked(ctx); err != nil {
		return DirectSignIn{}, err
	}
	gated5, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return DirectSignIn{}, e
	}
	tx := gated5.Tx()
	defer gated5.Rollback()
	var account, expiry string
	var attempts int
	if e = tx.QueryRowContext(ctx, `SELECT account_id,expires_at,attempts FROM identity_factor_challenges WHERE token_hash=?`, Digest(token)).Scan(&account, &expiry, &attempts); e != nil {
		return DirectSignIn{}, ErrUnauthorized
	}
	// A challenge a Portico sign-in raised (the account's own local second
	// factor) finishes as a Portico sign-in: no recovery-password network rule.
	var portico bool
	var signer string
	switch e = tx.QueryRowContext(ctx, `SELECT signer_key_id FROM portico_factor_challenges WHERE token_hash=?`, Digest(token)).Scan(&signer); {
	case e == nil:
		portico = true
	case !errors.Is(e, sql.ErrNoRows):
		return DirectSignIn{}, e
	}
	if portico {
		if err := admitPorticoSignerTx(ctx, tx, signer); err != nil {
			return DirectSignIn{}, err
		}
	}
	at, err := time.Parse(time.RFC3339, expiry)
	if err != nil || !at.After(time.Now().UTC()) || attempts >= signInChallengeAttempts {
		return DirectSignIn{}, ErrUnauthorized
	}
	if e = s.passwordAttemptTx(ctx, tx, account, credentialProbe); e != nil {
		return DirectSignIn{}, e
	}
	if e = s.verifySecondFactorTx(ctx, tx, account, code); e != nil {
		if _, err = tx.ExecContext(ctx, `UPDATE identity_factor_challenges SET attempts=attempts+1 WHERE token_hash=?`, Digest(token)); err != nil {
			return DirectSignIn{}, err
		}
		if err = s.passwordAttemptTx(ctx, tx, account, credentialFailure); err != nil {
			return DirectSignIn{}, err
		}
		if err = gated5.Commit(); err != nil {
			return DirectSignIn{}, err
		}
		return DirectSignIn{}, e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM identity_factor_challenges WHERE token_hash=?`, Digest(token)); e != nil {
		return DirectSignIn{}, e
	}
	c, e := s.directAccountTx(ctx, tx, account)
	if e != nil {
		return DirectSignIn{}, e
	}
	if !portico {
	}
	out, e := s.directSignInTx(ctx, tx, c, private)
	if e != nil {
		return DirectSignIn{}, e
	}
	if portico {
		if e = markPorticoAdmissionTx(ctx, tx, out, signer); e != nil {
			return DirectSignIn{}, e
		}
	}
	if e = s.passwordAttemptTx(ctx, tx, account, credentialSuccess); e != nil {
		return DirectSignIn{}, e
	}
	return out, gated5.Commit()
}

func (s *Service) openFactorSecret(account, stored string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(stored)
	if err != nil {
		return "", ErrFamilyKey
	}
	var secret string
	if err = s.openSetup(raw, "mfa-secret:"+account, &secret); err != nil {
		return "", ErrFamilyKey
	}
	return secret, nil
}
