package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestMemberResetReplacesPasswordAndRequiresChange(t *testing.T) {
	ctx := context.Background()
	s, db, owner := directFixture(t)
	member, err := s.CreateDirectMember(ctx, owner.AccountToken, DirectMemberCreate{Username: "member", Name: "Member", Password: "Old-password1!", AllowedLibraries: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := s.DirectLogin(ctx, "member", "Old-password1!")
	if err != nil {
		t.Fatal(err)
	}
	resetCode, err := s.MakeMemberResetCode(ctx, owner.AccountToken, member.ID, MemberResetRequest{ExpectedRevision: member.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(login.Session.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old viewer: %v", err)
	}
	if _, err = s.DirectMe(ctx, login.AccountToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old account: %v", err)
	}
	if _, err = s.DirectLogin(ctx, "member", "Old-password1!"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old password: %v", err)
	}
	reset, err := s.VerifyMemberReset(ctx, MemberResetVerify{Username: "member", Code: resetCode.Code})
	if err != nil {
		t.Fatal(err)
	}
	if !reset.PasswordChangeRequired || reset.Session == nil || reset.Session.Viewer.Role != "account" {
		t.Fatal("temporary password obtained viewer authority")
	}
	if _, err = s.Authenticate(reset.Session.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("temporary password account family accessed media", err)
	}
	if _, err = s.DirectMe(ctx, reset.AccountToken); !errors.Is(err, ErrPasswordChangeRequired) {
		t.Fatalf("temporary password escaped gate: %v", err)
	}
	if err = s.ChangeDirectPassword(ctx, reset.Session.AccessToken, "", "Chosen-password1!", ""); err != nil {
		t.Fatal(err)
	}
	var revokedReason string
	if err = db.QueryRow(`SELECT revoked_reason FROM authorization_session_families WHERE id=?`, reset.Session.SessionFamilyID).Scan(&revokedReason); err != nil || revokedReason != string(RevokedPasswordChange) {
		t.Fatalf("password-change reason %q: %v", revokedReason, err)
	}
	var changedHash []byte
	if err = db.QueryRow(`SELECT password_hash FROM accounts WHERE id=?`, member.ID).Scan(&changedHash); err != nil {
		t.Fatal(err)
	}
	if cost, err := bcrypt.Cost(changedHash); err != nil || cost != PasswordCost {
		t.Fatalf("changed password cost %d, want %d: %v", cost, PasswordCost, err)
	}
	fresh, err := s.DirectLogin(ctx, "member", "Chosen-password1!")
	if err != nil || fresh.PasswordChangeRequired || fresh.Session == nil {
		t.Fatalf("chosen password: %+v %v", fresh, err)
	}
}

func TestMFASecretsArePlainAndReadable(t *testing.T) {
	ctx := context.Background()
	s, db, login := directFixture(t)
	enrollment, err := s.EnrolTwoFactor(ctx, login.AccountToken, "Testing1!")
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err = db.QueryRow(`SELECT secret FROM identity_account_factors WHERE account_id='account'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	// Plain storage: the row holds the JSON secret, readable by the server.
	secret, err := s.openFactorSecret("account", stored)
	if err != nil || secret != enrollment.Secret {
		t.Fatalf("stored factor unreadable: %v", err)
	}
	var codeHash string
	if err = db.QueryRow(`SELECT code_hash FROM identity_recovery_codes LIMIT 1`).Scan(&codeHash); err != nil {
		t.Fatal(err)
	}
	for _, code := range enrollment.RecoveryCodes {
		if codeHash == Digest(normalizeRecoveryCode(code)) {
			t.Fatal("unpeppered recovery code")
		}
	}
	if _, err = s.VerifyTwoFactor(ctx, login.AccountToken, "Testing1!", currentTOTP(t, enrollment.Secret)); err != nil {
		t.Fatal(err)
	}
	if err = s.DisableTwoFactor(ctx, login.AccountToken, "Testing1!", ""); !errors.Is(err, ErrFactorRequired) {
		t.Fatalf("password alone disabled MFA: %v", err)
	}
}

func TestAccountBackoffIsIsolated(t *testing.T) {
	ctx := context.Background()
	s, db, login := directFixture(t)
	_, err := s.CreateDirectMember(ctx, login.AccountToken, DirectMemberCreate{Username: "other", Name: "Other", Password: "Other-password1!", AllowedLibraries: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		_, _ = s.DirectLogin(ctx, "owner", "wrong")
	}
	// The fifth guess sets a one-second cool-off ending at the next whole
	// second, so the lock can expire between the guess and the assertion below.
	// This assertion exercises admission while locked, not expiry: verify the
	// fifth guess established the lock, then keep the fixture locked across the
	// boundary or a shared-runner scheduling gap.
	source, _ := credentialSource(ctx)
	var attempts int
	var lastFailure, lockedUntil int64
	if err := db.QueryRow(`SELECT failed_attempts,last_failure,locked_until FROM identity_credential_attempts WHERE account_id='account' AND source_bucket=?`, source).Scan(&attempts, &lastFailure, &lockedUntil); err != nil || attempts != 5 || lockedUntil != lastFailure+1 {
		t.Fatalf("fifth guess must establish a one-second source lock: attempts=%d last=%d until=%d err=%v", attempts, lastFailure, lockedUntil, err)
	}
	if _, err = db.Exec(`UPDATE identity_credential_attempts SET locked_until=? WHERE account_id='account' AND source_bucket=?`, time.Now().Unix()+30, source); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DirectLogin(ctx, "owner", "Testing1!"); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("password was checked during cool-off: %v", err)
	}
	if _, err = db.Exec(`UPDATE identity_credential_attempts SET locked_until=0; UPDATE identity_account_attempt_budget SET next_allowed=0`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DirectLogin(ctx, "owner", "Testing1!"); err != nil {
		t.Fatalf("correct password was refused after cool-off: %v", err)
	}
	if _, err = s.DirectLogin(ctx, "other", "Other-password1!"); err != nil {
		t.Fatalf("unrelated account locked: %v", err)
	}
}

func TestAccountBackoffIsScopedToSourceAndDecays(t *testing.T) {
	s, db, _ := directFixture(t)
	first := WithSignInSource(context.Background(), "198.51.100.4:1234")
	second := WithSignInSource(context.Background(), "203.0.113.4:1234")
	for range 5 {
		_, _ = s.DirectLogin(first, "owner", "wrong")
	}
	if _, err := db.Exec(`UPDATE identity_credential_attempts SET locked_until=4102444800 WHERE source_bucket='198.51.100.0/24'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DirectLogin(first, "owner", "wrong"); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("source was not delayed: %v", err)
	}
	if _, err := s.DirectLogin(second, "owner", "Testing1!"); err != nil {
		t.Fatalf("correct password from another source inherited the attacker's lockout: %v", err)
	}
	if _, err := db.Exec(`UPDATE identity_credential_attempts SET last_failure=last_failure-3600,locked_until=0 WHERE source_bucket='198.51.100.0/24'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DirectLogin(first, "owner", "Testing1!"); err != nil {
		t.Fatalf("old failures did not decay: %v", err)
	}
}

func TestUnknownUsernameGetsTheSameBoundedCooloff(t *testing.T) {
	s, db, _ := directFixture(t)
	first := WithSignInSource(context.Background(), "[2001:db8:ab::1]:443")
	for range 5 {
		if _, err := s.DirectLogin(first, "unlisted", "wrong"); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("unknown name changed the initial answer: %v", err)
		}
	}
	subject := s.unknownSubject("Unlisted")
	if strings.Contains(subject, "unlisted") {
		t.Fatal("raw guessed name became a ledger key")
	}
	var attempts int
	if err := db.QueryRow(`SELECT failed_attempts FROM identity_unknown_attempts WHERE subject_hash=? AND source_bucket='*'`, subject).Scan(&attempts); err != nil || attempts != 5 {
		t.Fatalf("unknown name evaded the global attempt budget: %d %v", attempts, err)
	}
	var lastFailure, lockedUntil int64
	if err := db.QueryRow(`SELECT last_failure,locked_until FROM identity_unknown_attempts WHERE subject_hash=? AND source_bucket='*'`, subject).Scan(&lastFailure, &lockedUntil); err != nil || lockedUntil != lastFailure+1 {
		t.Fatalf("fifth unknown guess must establish a one-second lock: last=%d until=%d err=%v", lastFailure, lockedUntil, err)
	}
	// This assertion exercises admission while locked, not expiry. Keep the
	// fixture locked across a wall-clock second or a shared-runner scheduling gap.
	if _, err := db.Exec(`UPDATE identity_unknown_attempts SET locked_until=? WHERE subject_hash=?`, time.Now().Unix()+30, subject); err != nil {
		t.Fatal(err)
	}
	s.hashSlots <- struct{}{}
	s.hashSlots <- struct{}{}
	started := time.Now()
	for range 3 {
		_, err := s.DirectLogin(WithSignInSource(context.Background(), "[2001:db8:ac::1]:443"), "UNLISTED", "wrong")
		var locked *AccountLockError
		if !errors.As(err, &locked) || locked.RetryAfter < 1 || locked.RetryAfter > 60 {
			t.Fatalf("unknown name escaped the cool-off: %v", err)
		}
	}
	if time.Since(started) > time.Second {
		t.Fatal("early unknown guesses waited for password hash slots")
	}
	<-s.hashSlots
	<-s.hashSlots
	if err := db.QueryRow(`SELECT failed_attempts FROM identity_unknown_attempts WHERE subject_hash=? AND source_bucket='*'`, subject).Scan(&attempts); err != nil || attempts != 8 {
		t.Fatalf("early unknown guesses were not counted: %d %v", attempts, err)
	}
	if _, err := db.Exec(`UPDATE identity_unknown_attempts SET last_failure=last_failure-3600,locked_until=0 WHERE subject_hash=?`, subject); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DirectLogin(first, "unlisted", "wrong"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unknown-name cool-off did not decay: %v", err)
	}
}

func TestAccountBudgetCannotKeepCorrectPasswordLockedOut(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db, _ := directFixture(t)
	known := WithSignInSource(context.Background(), "[2001:db8:1::1]:443")
	if _, err := s.DirectLogin(known, "owner", "Testing1!"); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 12; n++ {
		peer := fmt.Sprintf("[2001:db8:%x::1]:443", n+1)
		_, _ = s.DirectLogin(WithSignInSource(context.Background(), peer), "owner", "wrong")
	}
	var attempts int
	var last, until int64
	if err := db.QueryRow(`SELECT failed_attempts,last_failure,next_allowed FROM identity_account_attempt_budget WHERE account_id='account'`).Scan(&attempts, &last, &until); err != nil || attempts != 12 || until-last != 60 {
		t.Fatalf("rotating networks escaped the account budget: attempts=%d delay=%d err=%v", attempts, until-last, err)
	}
	// A prior-success source and a previously unseen source can both prove the
	// correct password even while the account ledger reports a cool-off.
	if _, err := s.DirectLogin(known, "owner", "Testing1!"); err != nil {
		t.Fatalf("prior-success source was locked out: %v", err)
	}
	for n := 1; n <= 12; n++ {
		peer := fmt.Sprintf("[2001:db8:%x::1]:443", n+21)
		_, _ = s.DirectLogin(WithSignInSource(context.Background(), peer), "owner", "wrong")
	}
	fresh := WithSignInSource(context.Background(), "[2001:db8:99::1]:443")
	if _, err := s.DirectLogin(fresh, "owner", "Testing1!"); err != nil {
		t.Fatalf("new source's correct password was locked out: %v", err)
	}
	// These five guesses are evaluated and establish the source cool-off; a
	// refused request must not renew the account-wide ledger. The refused
	// source is delayed immediately, without taking a password-hash slot.
	for n := 1; n <= 5; n++ {
		_, _ = s.DirectLogin(known, "owner", "wrong")
	}
	var sourceAttempts int
	var lastFailure, lockedUntil int64
	if err := db.QueryRow(`SELECT failed_attempts,last_failure,locked_until FROM identity_credential_attempts WHERE account_id='account' AND source_bucket='2001:db8:1::/64'`).Scan(&sourceAttempts, &lastFailure, &lockedUntil); err != nil || sourceAttempts != 5 || lockedUntil != lastFailure+1 {
		t.Fatalf("fifth evaluated guess must establish a one-second source lock: attempts=%d last=%d until=%d err=%v", sourceAttempts, lastFailure, lockedUntil, err)
	}
	// This assertion exercises admission while locked, not expiry. Keep the
	// fixture locked across a wall-clock second or a shared-runner scheduling gap.
	if _, err := db.Exec(`UPDATE identity_credential_attempts SET locked_until=? WHERE account_id='account' AND source_bucket='2001:db8:1::/64'`, time.Now().Unix()+30); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := db.QueryRow(`SELECT failed_attempts FROM identity_account_attempt_budget WHERE account_id='account'`).Scan(&before); err != nil || before != 5 {
		t.Fatalf("five evaluated guesses must be counted account-wide: before=%d err=%v", before, err)
	}
	s.hashSlots <- struct{}{}
	s.hashSlots <- struct{}{}
	started := time.Now()
	for range 3 {
		if _, err := s.DirectLogin(known, "owner", "wrong"); !errors.Is(err, ErrAccountLocked) {
			t.Fatalf("early guess was checked or admitted: %v", err)
		}
	}
	if time.Since(started) > time.Second {
		t.Fatal("early guesses waited or consumed a hash slot")
	}
	var after int
	if err := db.QueryRow(`SELECT failed_attempts FROM identity_account_attempt_budget WHERE account_id='account'`).Scan(&after); err != nil || after != before {
		t.Fatalf("refused requests renewed account-wide budget: before=%d after=%d err=%v", before, after, err)
	}
	<-s.hashSlots
	<-s.hashSlots
	if _, err := db.Exec(`UPDATE identity_credential_attempts SET locked_until=0 WHERE account_id='account' AND source_bucket='2001:db8:1::/64'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DirectLogin(known, "owner", "Testing1!"); err != nil {
		t.Fatalf("correct password was refused after source cool-off: %v", err)
	}
	var remaining int
	if err := db.QueryRow(`SELECT count(*) FROM identity_account_attempt_budget WHERE account_id='account'`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("complete sign-in left stale account budget: %d %v", remaining, err)
	}
}

func TestLocalOwnerRecoveryRevokesSessionsAndRequiresNewPassword(t *testing.T) {
	ctx := context.Background()
	s, _, login := directFixture(t)
	enrollment, err := s.EnrolTwoFactor(ctx, login.AccountToken, "Testing1!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyTwoFactor(ctx, login.AccountToken, "Testing1!", currentTOTP(t, enrollment.Secret)); err != nil {
		t.Fatal(err)
	}
	code := requestResetFileCode(t, s)
	recovered, err := s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: code, TurnOffTwoStep: true})
	if err != nil {
		t.Fatal(err)
	}
	// C58: the account whose two-factor sign-in recovery removed is told in the
	// app, with a prompt to set it up again.
	var notices int
	if err = s.db.QueryRow(`SELECT count(*) FROM notification_records WHERE category='two_factor_reset' AND actions LIKE '%settings-security%'`).Scan(&notices); err != nil || notices != 1 {
		t.Fatalf("two-factor reset notices: %d %v", notices, err)
	}
	if _, err = s.Authenticate(login.Session.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("recovery retained old session", err)
	}
	if recovered.Challenge != nil || !recovered.PasswordChangeRequired || recovered.Session == nil || recovered.Session.Viewer.Role != "account" {
		t.Fatalf("recovery did not require a new password: %v", err)
	}
	if err = s.ChangeDirectPassword(ctx, recovered.Session.AccessToken, "", "Recovered-password1!", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DirectLogin(ctx, "owner", code); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("recovery code remained usable", err)
	}
}

func TestArtworkCapabilityCannotBecomeAFeedCredential(t *testing.T) {
	s, _, _ := directFixture(t)
	token := Token()
	cap, err := s.TopShelfArtworkCapability(token, "item")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cap, token) {
		t.Fatal("feed token disclosed")
	}
	if _, err = s.ResolveTopShelfArtwork(context.Background(), cap, "other"); !errors.Is(err, ErrTopShelfToken) {
		t.Fatal("capability widened to another item")
	}
	if _, err = s.TopShelfGrant(context.Background(), cap); !errors.Is(err, ErrTopShelfToken) {
		t.Fatal("art capability accepted as feed token")
	}
}

func TestSharedNetworkGuessesCannotHoldTheOwnerOut(t *testing.T) {
	s, db, _ := directFixture(t)
	const peer = "198.51.100.20:443"
	phone := Token()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`INSERT INTO identity_devices(id,authority,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('device-phone','local','account',?,'Phone','ios','portico','1.0',?,?)`, phone, now, now); err != nil {
		t.Fatal(err)
	}
	network := WithSignInSource(context.Background(), peer)
	for range 6 {
		_, _ = s.DirectLogin(network, "owner", "wrong")
	}
	// Where a patient guesser on this network ends up: at the 60 s cap.
	if _, err := db.Exec(`UPDATE identity_credential_attempts SET failed_attempts=12,locked_until=? WHERE account_id='account' AND source_bucket='198.51.100.0/24'`, time.Now().Unix()+60); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DirectLogin(network, "owner", "Testing1!"); !errors.Is(err, ErrAccountLocked) {
		t.Fatalf("the guessing network is not cooling off: %v", err)
	}
	device, err := WithIssuingDevice(network, registration(phone), peer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DirectLoginFrom(device, "owner", "Testing1!", true); err != nil {
		t.Fatalf("the owner's known device on the same network was held out: %v", err)
	}
	// Refusals do not extend the network's cool-off.
	var before int64
	if err = db.QueryRow(`SELECT locked_until FROM identity_credential_attempts WHERE account_id='account' AND source_bucket='198.51.100.0/24'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if _, err = s.DirectLogin(network, "owner", "Testing1!"); !errors.Is(err, ErrAccountLocked) {
			t.Fatalf("refused while cooling off: %v", err)
		}
	}
	var after int64
	if err = db.QueryRow(`SELECT locked_until FROM identity_credential_attempts WHERE account_id='account' AND source_bucket='198.51.100.0/24'`).Scan(&after); err != nil || after != before {
		t.Fatalf("refused requests extended the cool-off: %d -> %d %v", before, after, err)
	}
	// Once the bounded cool-off passes, the correct password gets in.
	if before-time.Now().Unix() > 60 {
		t.Fatalf("cool-off longer than a minute: %ds", before-time.Now().Unix())
	}
	if _, err = db.Exec(`UPDATE identity_credential_attempts SET locked_until=? WHERE account_id='account' AND source_bucket='198.51.100.0/24'`, time.Now().Unix()-1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DirectLogin(network, "owner", "Testing1!"); err != nil {
		t.Fatalf("correct password refused after the cool-off: %v", err)
	}
}

// Reset-file verification can explicitly remove the owner's two-factor setup; the
// owner gets an in-app notice and a re-enrol prompt. Other accounts keep
// theirs: with plaintext storage there is no key to lose.
func TestRecoveryResetMFARemovesOwnerFactorOnly(t *testing.T) {
	ctx := context.Background()
	s, db, owner := directFixture(t)
	if _, err := s.CreateDirectMember(ctx, owner.AccountToken, DirectMemberCreate{Username: "member", Name: "Member", Password: "Member-password1!", AllowedLibraries: []string{}}); err != nil {
		t.Fatal(err)
	}
	member, err := s.DirectLogin(ctx, "member", "Member-password1!")
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := s.EnrolTwoFactor(ctx, member.AccountToken, "Member-password1!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyTwoFactor(ctx, member.AccountToken, "Member-password1!", currentTOTP(t, enrollment.Secret)); err != nil {
		t.Fatal(err)
	}
	ownerEnrollment, err := s.EnrolTwoFactor(ctx, owner.AccountToken, "Testing1!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyTwoFactor(ctx, owner.AccountToken, "Testing1!", currentTOTP(t, ownerEnrollment.Secret)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: requestResetFileCode(t, s), TurnOffTwoStep: true}); err != nil {
		t.Fatal(err)
	}
	var notices, factors, ownerFactors int
	if err = db.QueryRow(`SELECT count(*) FROM notification_records WHERE category='two_factor_reset'`).Scan(&notices); err != nil || notices != 1 {
		t.Fatalf("notices for the owner whose factor was removed: %d %v", notices, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM identity_account_factors`).Scan(&factors); err != nil || factors != 1 {
		t.Fatalf("member factor removed with the owner's: %d %v", factors, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM identity_account_factors WHERE account_id='account'`).Scan(&ownerFactors); err != nil || ownerFactors != 0 {
		t.Fatalf("owner factors left: %d %v", ownerFactors, err)
	}
}
