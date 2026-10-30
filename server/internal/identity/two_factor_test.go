package identity

import (
	"context"
	"encoding/base32"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func currentTOTP(t *testing.T, secret string) string {
	t.Helper()
	key, e := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if e != nil {
		t.Fatal(e)
	}
	return totpCode(key, uint64(time.Now().Unix()/totpPeriod))
}

// wrongTOTP excludes every code the verifier could accept before this request
// expires, including both drift steps. A six-digit zero is a valid TOTP too.
func wrongTOTP(t *testing.T, ctx context.Context, secret string) string {
	t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("wrong-code fixture requires a bounded request")
	}
	return wrongTOTPInWindow(t, secret, time.Now(), deadline)
}

func wrongTOTPInWindow(t *testing.T, secret string, first, last time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil || last.Before(first) {
		t.Fatal("invalid wrong-code fixture", err)
	}
	accepted := make(map[string]bool)
	for step := first.Unix()/totpPeriod - totpDrift; step <= last.Unix()/totpPeriod+totpDrift; step++ {
		if step >= 0 {
			accepted[totpCode(key, uint64(step))] = true
		}
	}
	for n := 0; n < 1_000_000; n++ {
		code := fmt.Sprintf("%06d", n)
		if !accepted[code] {
			return code
		}
	}
	t.Fatal("fixture window contains every six-digit code")
	return ""
}

func TestWrongTOTPFixtureExcludesTheFullWindowIncludingZero(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	// This fixed reference-key step produces the valid code 000000.
	first := time.Unix(349495*totpPeriod, 0)
	last := first.Add(time.Minute)
	if _, ok := totpStep(secret, "000000", first, 0); !ok {
		t.Fatal("all-zero reference TOTP must remain valid")
	}
	wrong := wrongTOTPInWindow(t, secret, first, last)
	if len(wrong) != totpDigits || wrong == "000000" {
		t.Fatal("wrong-code fixture selected a valid zero code", wrong)
	}
	for at := first; !at.After(last); at = at.Add(time.Second) {
		if _, ok := totpStep(secret, wrong, at, 0); ok {
			t.Fatal("wrong code became valid inside its request window", wrong, at)
		}
	}
}

func TestAccountChangePasswordHashRunsOutsideWriterAndCooloffSkipsSlots(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	s, db, login := directFixture(t)
	slow, err := bcrypt.GenerateFromPassword([]byte("Testing1!"), 14)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE accounts SET password_hash=? WHERE id='account'`, slow); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.proveCurrentPassword(ctx, login.AccountToken, "Testing1!")
		done <- err
	}()
	deadline := time.After(2 * time.Second)
	for len(s.hashSlots) == 0 {
		select {
		case err := <-done:
			t.Fatalf("password check ended before slot observation: %v", err)
		case <-deadline:
			t.Fatal("password check never occupied a hash slot")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if dbwork.WriteGate().ActiveOrWaiting() {
		t.Fatal("bcrypt held the writer gate")
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO identity_account_attempt_budget(account_id,failed_attempts,last_failure,next_allowed) VALUES('account',8,?,?)`, time.Now().Unix(), time.Now().Add(30*time.Second).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO identity_credential_attempts(account_id,source_bucket,failed_attempts,last_failure,locked_until) VALUES('account','unknown',8,?,?) ON CONFLICT(account_id,source_bucket) DO UPDATE SET failed_attempts=excluded.failed_attempts,last_failure=excluded.last_failure,locked_until=excluded.locked_until`, time.Now().Unix(), time.Now().Add(30*time.Second).Unix()); err != nil {
		t.Fatal(err)
	}
	s.hashSlots <- struct{}{}
	s.hashSlots <- struct{}{}
	_, err = s.EnrolTwoFactor(ctx, login.AccountToken, "wrong")
	<-s.hashSlots
	<-s.hashSlots
	if !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("cool-off took a hash slot: %v", err)
	}
	var attempts int
	// C66: a refusal evaluates nothing, so it is not counted and does not
	// extend the source's cool-off.
	if err = db.QueryRow(`SELECT failed_attempts FROM identity_credential_attempts WHERE account_id='account' AND source_bucket='unknown'`).Scan(&attempts); err != nil || attempts != 8 {
		t.Fatalf("a refused account change was counted: %d %v", attempts, err)
	}
	if err = db.QueryRow(`SELECT failed_attempts FROM identity_account_attempt_budget WHERE account_id='account'`).Scan(&attempts); err != nil || attempts != 8 {
		t.Fatalf("refused account change renewed global budget: %d %v", attempts, err)
	}
}

func TestTOTPMatchesTheReferenceVectorAndDoesNotReplay(t *testing.T) {
	// RFC 6238 appendix B, SHA-1, truncated to the six digits this server uses.
	key := []byte("12345678901234567890")
	for step, want := range map[uint64]string{
		1:         "287082",
		37037036:  "081804",
		37037037:  "050471",
		41152263:  "005924",
		66666666:  "279037",
		666666666: "353130",
	} {
		if got := totpCode(key, step); got != want {
			t.Errorf("step %d: got %s want %s", step, got, want)
		}
	}
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(key)
	// Not the Unix epoch: step 0 would collide with the "no code accepted yet"
	// sentinel, which is only reachable in 1970.
	now := time.Unix(1111111109, 0).UTC()
	step := now.Unix() / totpPeriod
	code := totpCode(key, uint64(step))
	if matched, ok := totpStep(secret, code, now, 0); !ok || matched != step {
		t.Fatalf("current code did not verify: %d %v", matched, ok)
	}
	// A code a bystander read off the screen must not work again inside its own
	// thirty-second window.
	if _, ok := totpStep(secret, code, now, step); ok {
		t.Fatal("a code was accepted at or below the last accepted step")
	}
	// One step of drift either way, but no more.
	if _, ok := totpStep(secret, totpCode(key, uint64(step-1)), now, 0); !ok {
		t.Fatal("one step of backward drift was refused")
	}
	if _, ok := totpStep(secret, totpCode(key, uint64(step+2)), now, 0); ok {
		t.Fatal("two steps of forward drift were accepted")
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef", "000000 "} {
		if _, ok := totpStep(secret, bad, now, 0); ok {
			t.Errorf("%q was accepted as a code", bad)
		}
	}
}

func TestTwoFactorEnrolmentIsNotInForceUntilVerified(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s, _, login := directFixture(t)
	state, e := s.TwoFactor(ctx, login.AccountToken)
	if e != nil || state.Enabled || state.PendingEnrolment {
		t.Fatalf("initial state %+v %v", state, e)
	}
	// The account password is required, so a borrowed signed-in device cannot
	// quietly attach a factor the account holder does not hold.
	if _, e = s.EnrolTwoFactor(ctx, login.AccountToken, "Wrong1!aa"); !errors.Is(e, ErrCurrentPasswordIncorrect) {
		t.Fatalf("enrolment without the password: %v", e)
	}
	enrolment, e := s.EnrolTwoFactor(ctx, login.AccountToken, "Testing1!")
	if e != nil {
		t.Fatal(e)
	}
	if len(enrolment.RecoveryCodes) != 10 || enrolment.Secret == "" || enrolment.URI == "" {
		t.Fatalf("enrolment %+v", enrolment)
	}
	state, e = s.TwoFactor(ctx, login.AccountToken)
	if e != nil || state.Enabled || !state.PendingEnrolment || state.RecoveryCodesRemaining != 10 {
		t.Fatalf("pending state %+v %v", state, e)
	}
	// A pending factor must not gate sign-in: a mistyped secret would otherwise
	// lock the account holder out of their own server.
	second, e := s.DirectLogin(ctx, "owner", "Testing1!")
	if e != nil || second.Challenge != nil {
		t.Fatalf("a pending enrolment gated sign-in: %v %+v", e, second.Challenge)
	}
	if _, e = s.VerifyTwoFactor(ctx, login.AccountToken, "Testing1!", wrongTOTP(t, ctx, enrolment.Secret)); !errors.Is(e, ErrFactorInvalid) {
		t.Fatalf("a wrong code completed enrolment: %v", e)
	}
	if _, e = s.VerifyTwoFactor(ctx, login.AccountToken, "Testing1!", currentTOTP(t, enrolment.Secret)); e != nil {
		t.Fatal(e)
	}
	state, e = s.TwoFactor(ctx, login.AccountToken)
	if e != nil || !state.Enabled || state.PendingEnrolment {
		t.Fatalf("verified state %+v %v", state, e)
	}
	if _, e = s.EnrolTwoFactor(ctx, login.AccountToken, "Testing1!"); !errors.Is(e, ErrFactorEnrolled) {
		t.Fatalf("re-enrolment over a live factor: %v", e)
	}
}

func TestSignInChallengeReplacesTheAccountSessionUntilTheFactorIsProven(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s, _, login := directFixture(t)
	enrolment, e := s.EnrolTwoFactor(ctx, login.AccountToken, "Testing1!")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.VerifyTwoFactor(ctx, login.AccountToken, "Testing1!", currentTOTP(t, enrolment.Secret)); e != nil {
		t.Fatal(e)
	}
	// The legacy single-shot sign-in must say what is wrong rather than claiming a
	// profile needs choosing. It runs first because issuing a challenge retires
	// the account's previous one.
	if _, e = s.Login("owner", "Testing1!"); !errors.Is(e, ErrFactorRequired) {
		t.Fatalf("legacy sign-in with a factor: %v", e)
	}
	challenged, e := s.DirectLogin(ctx, "owner", "Testing1!")
	if e != nil {
		t.Fatal(e)
	}
	// The password has been proven; nothing the client may keep has been issued.
	if challenged.Challenge == nil || challenged.AccountToken != "" || challenged.Session != nil {
		t.Fatalf("a confirmed factor did not replace the account session: %+v", challenged)
	}
	if _, e = s.DirectMe(ctx, challenged.Challenge.Token); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("a challenge token was accepted as an account session")
	}
	if _, e = s.CompleteSignInChallenge(ctx, challenged.Challenge.Token, wrongTOTP(t, ctx, enrolment.Secret), true); !errors.Is(e, ErrFactorInvalid) {
		t.Fatalf("a wrong code completed a challenge: %v", e)
	}
	// Enrolment consumed the current step, and the replay fence refuses a code at
	// or below it. A real sign-in happens minutes later; rewinding the stored step
	// is how that gap is expressed without sleeping for thirty seconds.
	if _, e = s.db.Exec(`UPDATE identity_account_factors SET last_step=0`); e != nil {
		t.Fatal(e)
	}
	completed, e := s.CompleteSignInChallenge(ctx, challenged.Challenge.Token, currentTOTP(t, enrolment.Secret), true)
	if e != nil {
		t.Fatal(e)
	}
	if completed.AccountToken == "" {
		t.Fatal("completing the challenge produced no account session")
	}
	if _, e = s.DirectMe(ctx, completed.AccountToken); e != nil {
		t.Fatal(e)
	}
	// The challenge is spent.
	if _, e = s.db.Exec(`UPDATE identity_account_factors SET last_step=0`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CompleteSignInChallenge(ctx, challenged.Challenge.Token, currentTOTP(t, enrolment.Secret), true); !errors.Is(e, ErrUnauthorized) {
		t.Fatalf("a spent challenge was reusable: %v", e)
	}
}

func TestSignInChallengeAttemptsAreBounded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s, _, login := directFixture(t)
	enrolment, e := s.EnrolTwoFactor(ctx, login.AccountToken, "Testing1!")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.VerifyTwoFactor(ctx, login.AccountToken, "Testing1!", currentTOTP(t, enrolment.Secret)); e != nil {
		t.Fatal(e)
	}
	challenged, e := s.DirectLogin(ctx, "owner", "Testing1!")
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < signInChallengeAttempts; i++ {
		if _, e = s.CompleteSignInChallenge(ctx, challenged.Challenge.Token, wrongTOTP(t, ctx, enrolment.Secret), true); !errors.Is(e, ErrFactorInvalid) {
			t.Fatalf("attempt %d: %v", i, e)
		}
	}
	var failed int
	if e = s.db.QueryRow(`SELECT failed_attempts FROM identity_credential_attempts WHERE account_id='account' AND source_bucket='unknown'`).Scan(&failed); e != nil || failed != signInChallengeAttempts {
		t.Fatalf("factor guesses were not charged to credential backoff: %d %v", failed, e)
	}
	// After the budget, even the right code is refused: the challenge is retired,
	// so a guessing loop cannot outlast it by eventually landing on a real code.
	if _, e = s.CompleteSignInChallenge(ctx, challenged.Challenge.Token, currentTOTP(t, enrolment.Secret), true); !errors.Is(e, ErrUnauthorized) {
		t.Fatalf("a retired challenge still accepted a code: %v", e)
	}
}

func TestDisablingTheFactorNeedsThePasswordAndClearsRecoveryCodes(t *testing.T) {
	ctx := context.Background()
	s, _, login := directFixture(t)
	if e := s.DisableTwoFactor(ctx, login.AccountToken, "Testing1!", ""); !errors.Is(e, ErrFactorMissing) {
		t.Fatalf("disabling an absent factor: %v", e)
	}
	enrolment, e := s.EnrolTwoFactor(ctx, login.AccountToken, "Testing1!")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.VerifyTwoFactor(ctx, login.AccountToken, "Testing1!", currentTOTP(t, enrolment.Secret)); e != nil {
		t.Fatal(e)
	}
	if e = s.DisableTwoFactor(ctx, login.AccountToken, "Wrong1!aa", ""); !errors.Is(e, ErrCurrentPasswordIncorrect) {
		t.Fatalf("disabling without the password: %v", e)
	}
	if e = s.DisableTwoFactor(ctx, login.AccountToken, "Testing1!", enrolment.RecoveryCodes[0]); e != nil {
		t.Fatal(e)
	}
	if _, e = s.TwoFactor(ctx, login.AccountToken); !errors.Is(e, ErrUnauthorized) {
		t.Fatalf("factor change kept its predecessor session: %v", e)
	}
	// Sign-in must no longer be challenged, and an old recovery code must not
	// survive as a step-up credential.
	fresh, e := s.DirectLogin(ctx, "owner", "Testing1!")
	if e != nil || fresh.Challenge != nil {
		t.Fatalf("sign-in still challenged after disabling: %v", e)
	}
	state, e := s.TwoFactor(ctx, fresh.AccountToken)
	if e != nil || state.Enabled || state.RecoveryCodesRemaining != 0 {
		t.Fatalf("state after disabling: %+v %v", state, e)
	}
}

func TestPasswordChangeRequiresCurrentPasswordAndFactorInSameRequest(t *testing.T) {
	s, db, login := directFixture(t)
	ctx := context.Background()
	enrolment, err := s.EnrolTwoFactor(ctx, login.AccountToken, "Testing1!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyTwoFactor(ctx, login.AccountToken, "Wrong1!aa", currentTOTP(t, enrolment.Secret)); !errors.Is(err, ErrCurrentPasswordIncorrect) {
		t.Fatal("factor activation accepted no current password", err)
	}
	if _, err = s.VerifyTwoFactor(ctx, login.AccountToken, "Testing1!", currentTOTP(t, enrolment.Secret)); err != nil {
		t.Fatal(err)
	}
	if err = s.ChangeDirectPassword(ctx, login.AccountToken, "Wrong1!aa", "Changed-password1!", enrolment.RecoveryCodes[0]); !errors.Is(err, ErrCurrentPasswordIncorrect) {
		t.Fatal("password change accepted wrong current password", err)
	}
	var attempts int
	if err = db.QueryRow(`SELECT failed_attempts FROM identity_credential_attempts WHERE account_id='account' AND source_bucket='unknown'`).Scan(&attempts); err != nil || attempts != 2 {
		t.Fatalf("wrong current passwords did not count toward cool-off: %d %v", attempts, err)
	}
	if err = s.ChangeDirectPassword(ctx, login.AccountToken, "Testing1!", "Changed-password1!", ""); !errors.Is(err, ErrFactorRequired) {
		t.Fatal("password change accepted no factor", err)
	}
	if err = s.ChangeDirectPassword(ctx, login.AccountToken, "Testing1!", "Changed-password1!", enrolment.RecoveryCodes[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DirectMe(ctx, login.AccountToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("password change kept prior session", err)
	}
}
