package identity

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"github.com/PorticoMediaServer/portico/server/internal/networkpolicy"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/notify"
)

var ErrPasswordChangeRequired = errors.New("Choose a new password before continuing.")
var ErrAccountLocked = errors.New("Too many sign-in attempts. Try again later.")
var ErrCurrentPasswordIncorrect = errors.New("The current password is incorrect.")

type AccountLockError struct{ RetryAfter int64 }

func (e *AccountLockError) Error() string { return ErrAccountLocked.Error() }
func (e *AccountLockError) Unwrap() error { return ErrAccountLocked }

type passwordChangeContextKey struct{}
type signInSourceKey struct{}

// WithSignInSource carries the verified network peer into password and MFA
// accounting, independently of whether this request includes a device claim.
func WithSignInSource(ctx context.Context, peer string) context.Context {
	return context.WithValue(ctx, signInSourceKey{}, peer)
}

func passwordChangeRequired(ctx context.Context, tx *sql.Tx, account string) (bool, error) {
	var required bool
	err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT must_change FROM identity_credential_state WHERE account_id=?),0)`, account).Scan(&required)
	return required, err
}

type credentialOutcome uint8

const (
	credentialProbe credentialOutcome = iota
	credentialFailure
	credentialSuccess
)

func credentialSource(ctx context.Context) (string, bool) {
	peer, _ := ctx.Value(signInSourceKey{}).(string)
	if peer == "" {
		claim, _ := ctx.Value(issuingDeviceKey{}).(issuingDevice)
		peer = claim.peer
	}
	host, _, err := net.SplitHostPort(peer)
	if err != nil {
		host = peer
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown", false
	}
	addr = addr.Unmap()
	bits := 64
	if addr.Is4() {
		bits = 24
	}
	return netip.PrefixFrom(addr, bits).Masked().String(), networkpolicy.Home(ctx)
}

// A bucket in cool-off is refused before password hashing, with the exact
// Retry-After (C66). A refusal evaluates nothing, so it neither extends the
// cool-off nor touches the account-wide budget: a correct password always gets
// in once its own bucket's bounded cool-off (at most 60 s) has passed, and a
// peer that cannot guess the password cannot hold that bucket closed by
// sending refused requests.
func (s *Service) credentialCooloff(ctx context.Context, account string) error {
	if account == "" {
		return nil
	}
	read, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return err
	}
	until, err := credentialLockedUntilTx(ctx, read.Tx(), account)
	if closeErr := read.Commit(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if now := time.Now().Unix(); until > now {
		return &AccountLockError{RetryAfter: max(1, until-now)}
	}
	return nil
}

// credentialBucket is who a cool-off applies to (C66). A request from a device
// that has signed in to this account before (an approved device with the same
// installation ID) has its own bucket, so wrong guesses from its network,
// shared or not, never delay it. Anything else counts against its network
// (/24 for IPv4, /64 for IPv6).
func credentialBucketTx(ctx context.Context, tx *sql.Tx, account string) (string, error) {
	claim, _ := ctx.Value(issuingDeviceKey{}).(issuingDevice)
	if id := claim.registration.InstallationID; id != "" && account != "" {
		var known bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_devices WHERE authority='local' AND account_id=? AND installation_id=?)`, account, id).Scan(&known); err != nil {
			return "", err
		}
		if known {
			sum := sha256.Sum256([]byte(id))
			return "device:" + hex.EncodeToString(sum[:16]), nil
		}
	}
	source, _ := credentialSource(ctx)
	return source, nil
}

func (s *Service) unknownSubject(user string) string {
	mac := hmac.New(sha256.New, s.setupHashKey)
	_, _ = mac.Write([]byte("unknown-login:" + strings.ToLower(strings.TrimSpace(user))))
	return hex.EncodeToString(mac.Sum(nil))
}

// Unknown names have the same response progression as known accounts. A keyed
// digest prevents the ledger from becoming a list of guessed usernames.
func (s *Service) unknownCooloff(ctx context.Context, user string) error {
	subject := s.unknownSubject(user)
	var retryAfter int64
	err := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		until, err := unknownLockedUntilTx(ctx, tx, subject)
		if err != nil || until <= time.Now().Unix() {
			return err
		}
		if err = unknownAttemptTx(ctx, tx, subject); err != nil {
			return err
		}
		until, err = unknownLockedUntilTx(ctx, tx, subject)
		if err == nil {
			retryAfter = max(1, until-time.Now().Unix())
		}
		return err
	})
	if err != nil {
		return err
	}
	if retryAfter > 0 {
		return &AccountLockError{RetryAfter: retryAfter}
	}
	return nil
}

func (s *Service) unknownAttempt(ctx context.Context, user string) error {
	subject := s.unknownSubject(user)
	return dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		return unknownAttemptTx(ctx, tx, subject)
	})
}

func unknownLockedUntilTx(ctx context.Context, tx *sql.Tx, subject string) (int64, error) {
	source, _ := credentialSource(ctx)
	var until int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(locked_until),0) FROM identity_unknown_attempts WHERE subject_hash=? AND source_bucket IN (?, '*')`, subject, source).Scan(&until)
	return until, err
}

func unknownAttemptTx(ctx context.Context, tx *sql.Tx, subject string) error {
	policy, err := ReadSignInPolicyTx(ctx, tx)
	if err != nil {
		return err
	}
	source, _ := credentialSource(ctx)
	now := time.Now().Unix()
	for _, bucket := range []string{source, "*"} {
		var attempts int
		var last, until int64
		err := tx.QueryRowContext(ctx, `SELECT failed_attempts,last_failure,locked_until FROM identity_unknown_attempts WHERE subject_hash=? AND source_bucket=?`, subject, bucket).Scan(&attempts, &last, &until)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if last > 0 && now > last {
			attempts >>= min((now-last)/900, 32)
		}
		attempts++
		delay := passwordDelay(policy.Values.PasswordAttempts, attempts)
		_, err = tx.ExecContext(ctx, `INSERT INTO identity_unknown_attempts(subject_hash,source_bucket,failed_attempts,last_failure,locked_until) VALUES(?,?,?,?,?) ON CONFLICT(subject_hash,source_bucket) DO UPDATE SET failed_attempts=excluded.failed_attempts,last_failure=excluded.last_failure,locked_until=excluded.locked_until`, subject, bucket, attempts, now, max(until, now+delay))
		if err != nil {
			return err
		}
	}
	return nil
}

func credentialLockedUntilTx(ctx context.Context, tx *sql.Tx, account string) (int64, error) {
	source, err := credentialBucketTx(ctx, tx, account)
	if err != nil {
		return 0, err
	}
	var until int64
	err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT locked_until FROM identity_credential_attempts WHERE account_id=? AND source_bucket=?),0)`, account, source).Scan(&until)
	return until, err
}

// One source cannot lock an account out for every other source. Failed
// attempts decay by half each quarter hour and backoff is capped; a successful
// *complete* sign-in clears only that source's counter.
func (s *Service) passwordAttempt(ctx context.Context, account string, outcome credentialOutcome) error {
	if account == "" {
		return nil
	}
	return dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		return s.passwordAttemptTx(ctx, tx, account, outcome)
	})
}

type currentPasswordProof struct {
	reset   bool
	account string
	hash    []byte
}

// Prove outside the write transaction: bcrypt is intentionally expensive and
// must never hold SQLite's one writer or either hash slot during a cool-off.
// The mutation rechecks this exact hash in its own transaction before writing.
func (s *Service) proveCurrentPassword(ctx context.Context, bearer, password string) (currentPasswordProof, error) {
	if err := s.RequireSignInKind(ctx, SignInLocal); err != nil {
		return currentPasswordProof{}, err
	}
	var proof currentPasswordProof
	read, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return proof, err
	}
	caller, err := s.directCallerTx(ctx, read.Tx(), bearer, true)
	if err == nil && password == "" {
		required, e := passwordChangeRequired(ctx, read.Tx(), caller.account.ID)
		if e != nil {
			read.Rollback()
			return proof, e
		}
		kind, e := resetSessionTx(ctx, read.Tx(), caller.familyID)
		if e != nil {
			read.Rollback()
			return proof, e
		}
		if required && kind == "password" {
			proof.reset = true
		}
	}

	if err == nil {
		proof.account = caller.account.ID
		err = read.Tx().QueryRowContext(ctx, `SELECT password_hash FROM accounts WHERE id=?`, proof.account).Scan(&proof.hash)
	}
	if closeErr := read.Commit(); err == nil {
		err = closeErr
	}
	if err != nil {
		return currentPasswordProof{}, err
	}
	if proof.reset {
		return proof, nil
	}
	if err = s.credentialCooloff(ctx, proof.account); err != nil {
		return currentPasswordProof{}, err
	}
	valid := false
	if len(password) <= 72 {
		if slotErr := s.takeHashSlot(ctx); slotErr != nil {
			return currentPasswordProof{}, slotErr
		}
		valid = bcrypt.CompareHashAndPassword(proof.hash, []byte(password)) == nil
		<-s.hashSlots
	}
	if !valid {
		if err = s.passwordAttempt(ctx, proof.account, credentialFailure); err != nil {
			return currentPasswordProof{}, err
		}
		return currentPasswordProof{}, ErrCurrentPasswordIncorrect
	}
	return proof, nil
}

func (s *Service) checkCurrentPasswordProofTx(ctx context.Context, tx *sql.Tx, bearer string, proof currentPasswordProof) (directCaller, error) {
	if err := RequireSignInKindTx(ctx, tx, SignInLocal); err != nil {
		return directCaller{}, err
	}
	c, err := s.directCallerTx(ctx, tx, bearer, true)
	if err != nil || c.account.ID != proof.account {
		return directCaller{}, ErrUnauthorized
	}

	if proof.reset {
		required, e := passwordChangeRequired(ctx, tx, c.account.ID)
		if e != nil {
			return directCaller{}, e
		}
		kind, e := resetSessionTx(ctx, tx, c.familyID)
		if e != nil {
			return directCaller{}, e
		}
		if !required || kind != "password" {
			return directCaller{}, ErrUnauthorized
		}
		return c, nil
	}
	var live []byte
	if err = tx.QueryRowContext(ctx, `SELECT password_hash FROM accounts WHERE id=?`, c.account.ID).Scan(&live); err != nil {
		return directCaller{}, err
	}
	if !bytes.Equal(live, proof.hash) {
		return directCaller{}, ErrUnauthorized
	}
	return c, nil
}

func (s *Service) passwordAttemptTx(ctx context.Context, tx *sql.Tx, account string, outcome credentialOutcome) error {
	policy, policyErr := ReadSignInPolicyTx(ctx, tx)
	if policyErr != nil {
		return policyErr
	}
	source, err := credentialBucketTx(ctx, tx, account)
	if err != nil {
		return err
	}
	var attempts int
	var until, last int64
	err = tx.QueryRowContext(ctx, `SELECT failed_attempts,last_failure,locked_until FROM identity_credential_attempts WHERE account_id=? AND source_bucket=?`, account, source).Scan(&attempts, &last, &until)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	now := time.Now().Unix()
	if last > 0 && now > last {
		attempts >>= min((now-last)/900, 32)
	}
	if outcome == credentialProbe {
		return nil
	}
	if outcome == credentialSuccess {
		if _, err = tx.ExecContext(ctx, `INSERT INTO identity_credential_attempts(account_id,source_bucket,failed_attempts,last_failure,locked_until) VALUES(?,?,0,0,0) ON CONFLICT(account_id,source_bucket) DO UPDATE SET failed_attempts=0,last_failure=0,locked_until=0`, account, source); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM identity_account_attempt_budget WHERE account_id=?`, account)
		return err
	}
	attempts++
	delay := passwordDelay(policy.Values.PasswordAttempts, attempts)
	_, err = tx.ExecContext(ctx, `INSERT INTO identity_credential_attempts(account_id,source_bucket,failed_attempts,last_failure,locked_until) VALUES(?,?,?,?,?) ON CONFLICT(account_id,source_bucket) DO UPDATE SET failed_attempts=excluded.failed_attempts,last_failure=excluded.last_failure,locked_until=excluded.locked_until`, account, source, attempts, now, max(until, now+delay))
	if err != nil {
		return err
	}
	var globalAttempts int
	var globalLast int64
	err = tx.QueryRowContext(ctx, `SELECT failed_attempts,last_failure FROM identity_account_attempt_budget WHERE account_id=?`, account).Scan(&globalAttempts, &globalLast)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if globalLast > 0 && now > globalLast {
		globalAttempts >>= min((now-globalLast)/900, 32)
	}
	globalAttempts++
	globalDelay := passwordDelay(policy.Values.PasswordAttempts, globalAttempts)
	var priorUntil int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT next_allowed FROM identity_account_attempt_budget WHERE account_id=?),0)`, account).Scan(&priorUntil); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO identity_account_attempt_budget(account_id,failed_attempts,last_failure,next_allowed) VALUES(?,?,?,?) ON CONFLICT(account_id) DO UPDATE SET failed_attempts=excluded.failed_attempts,last_failure=excluded.last_failure,next_allowed=excluded.next_allowed`, account, globalAttempts, now, max(priorUntil, now+globalDelay)); err != nil {
		return err
	}
	// A evaluated wrong guess inherits the account-wide delay on its own
	// source. New sources may prove the correct password, while repeatedly
	// guessing from any one source remains bounded.
	if _, err = tx.ExecContext(ctx, `UPDATE identity_credential_attempts SET locked_until=MAX(locked_until, ?) WHERE account_id=? AND source_bucket=?`, now+globalDelay, account, source); err != nil {
		return err
	}
	if delay > 0 && passwordDelay(policy.Values.PasswordAttempts, attempts-1) == 0 {
		return s.securityNoticeTx(ctx, tx, account, "sign_in_lockout")
	}
	return nil
}

func (s *Service) revokeCredentialsTx(ctx context.Context, tx *sql.Tx, account string) error {
	return s.revokeCredentialsWithReasonTx(ctx, tx, account, RevokedPasswordChange)
}
func (s *Service) revokeCredentialsWithReasonTx(ctx context.Context, tx *sql.Tx, account string, reason RevocationReason) error {
	// Feed grants are independent of session logout, but not of a password
	// change, recovery or removal of account access. Delete them in the same
	// transaction so re-enabling an account cannot revive its old grants.
	if reason == RevokedPasswordChange || reason == RevokedMembershipRemoved {
		if _, err := tx.ExecContext(ctx, `DELETE FROM topshelf_tokens WHERE account_id=?`, account); err != nil {
			return err
		}
	}
	// API keys and personal access tokens are not ended here: they have a
	// lifetime of their own (api_credentials.go), so a password change or an
	// owner's edit does not silently break what a member has connected. A
	// member who is disabled loses them through the schema's own rule.
	return s.revokeDirectWithReasonTx(ctx, tx, account, "", "", reason)
}

func (s *Service) securityNoticeTx(ctx context.Context, tx *sql.Tx, account, kind string) error {
	var profile string
	if err := tx.QueryRowContext(ctx, `SELECT profile_id FROM accounts WHERE id=?`, account).Scan(&profile); err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	_, err := notify.Raise(tx, now, notify.Draft{
		Audience: notify.AudienceProfile, Scope: notify.ProfileScope("local", account, profile), Severity: notify.SeverityWarning, Source: notify.SourceSecurity,
		Category: kind, DedupeKey: kind + ":" + Token(), Title: "Account security changed", Body: "Review the security settings for your account.", Arguments: map[string]string{"code": kind},
	})
	return err
}
