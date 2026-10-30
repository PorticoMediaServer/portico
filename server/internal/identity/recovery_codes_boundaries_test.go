package identity

import (
	"context"
	"errors"
	"testing"
)

// ACC-LEAF-08: the accepting boundaries of ReplaceRecoveryCodes. A failure at the
// notice write must roll back both the old-code consumption and the replacement;
// a stale, secondary-profile or other-account session must not replace anyone's
// codes; and an absent confirmed factor refuses. The only injected failure is a
// test-only trigger on the notification table, the same pattern the catalog and
// administration suites already use; nothing here bypasses the guard under test.

func confirmedFactor(t *testing.T, s *Service, token, password string) TwoFactorEnrolment {
	t.Helper()
	enrol, err := s.EnrolTwoFactor(context.Background(), token, password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyTwoFactor(context.Background(), token, password, currentTOTP(t, enrol.Secret)); err != nil {
		t.Fatal(err)
	}
	return enrol
}

func unusedOwnerCodes(t *testing.T, s *Service, account string) int {
	t.Helper()
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM identity_recovery_codes WHERE account_id=? AND used=0`, account).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestReplaceRecoveryCodesRollsBackOnNoticeFailure(t *testing.T) {
	ctx := context.Background()
	s, db, owner := directFixture(t)
	enrol := confirmedFactor(t, s, owner.AccountToken, "Testing1!")
	before := unusedOwnerCodes(t, s, owner.Account.ID)
	if before != len(enrol.RecoveryCodes) {
		t.Fatal("fixture did not store the enrolled set", before, len(enrol.RecoveryCodes))
	}
	if _, err := db.Exec(`CREATE TRIGGER refuse_recovery_notice BEFORE INSERT ON notification_records BEGIN SELECT RAISE(ABORT,'notice refused'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := s.ReplaceRecoveryCodes(ctx, owner.AccountToken, "Testing1!", enrol.RecoveryCodes[0])
	if err == nil {
		t.Fatal("a refused notice write accepted the replacement")
	}
	if _, err = db.Exec(`DROP TRIGGER refuse_recovery_notice`); err != nil {
		t.Fatal(err)
	}
	if got := unusedOwnerCodes(t, s, owner.Account.ID); got != before {
		t.Fatal("failed replacement changed the usable set", before, got)
	}
	for _, old := range enrol.RecoveryCodes {
		var exists bool
		if err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM identity_recovery_codes WHERE account_id=? AND code_hash=?)`,
			owner.Account.ID, s.setupHash("mfa-recovery:"+owner.Account.ID, normalizeRecoveryCode(old))).Scan(&exists); err != nil || !exists {
			t.Fatal("failed replacement consumed an old code", err)
		}
	}
	// The same proof code still works, which proves the factor replay accounting
	// rolled back with the replacement rather than spending the code.
	out, err := s.ReplaceRecoveryCodes(ctx, owner.AccountToken, "Testing1!", enrol.RecoveryCodes[0])
	if err != nil {
		t.Fatal("rolled-back proof was spent", err)
	}
	if len(out.Codes) != len(enrol.RecoveryCodes) {
		t.Fatal("replacement count", len(out.Codes))
	}
}

func TestReplaceRecoveryCodesRefusesOtherAccountsAndStaleSessions(t *testing.T) {
	ctx := context.Background()
	for _, scenario := range []string{"other-account", "stale-session", "secondary-profile"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, owner := directFixture(t)
			enrol := confirmedFactor(t, s, owner.AccountToken, "Testing1!")
			// A proof code that stays unused through this scenario's own setup, so the
			// revoked/secondary gate is the only possible cause of the refusal.
			code := enrol.RecoveryCodes[1]
			bearer, password := owner.AccountToken, "Testing1!"
			switch scenario {
			case "other-account":
				// A member of the same server is still a different account: its own
				// call has no confirmed factor and cannot touch the owner's set.
				if _, err := s.CreateDirectMember(ctx, owner.AccountToken, DirectMemberCreate{
					Username: "member", Name: "Member", Password: "Member-password1!", AllowedLibraries: []string{},
				}); err != nil {
					t.Fatal(err)
				}
				login, err := s.DirectLogin(ctx, "member", "Member-password1!")
				if err != nil {
					t.Fatal(err)
				}
				bearer, password = login.AccountToken, "Member-password1!"
			case "stale-session":
				// With a confirmed factor the password alone only yields a challenge,
				// so finish the real challenge first, then revoke that very session.
				pending, err := s.DirectLogin(ctx, "owner", "Testing1!")
				if err != nil {
					t.Fatal(err)
				}
				if pending.Challenge == nil || pending.AccountToken != "" {
					t.Fatal("fixture issued a session without the second factor")
				}
				completed, err := s.CompleteSignInChallenge(ctx, pending.Challenge.Token, enrol.RecoveryCodes[0], true)
				if err != nil || completed.Session == nil || completed.Session.AccessToken == "" {
					t.Fatal("challenge was not completed", err)
				}
				bearer = completed.Session.AccessToken
				if err = s.LogoutToken(ctx, bearer); err != nil {
					t.Fatal(err)
				}
			case "secondary-profile":
				snapshot, err := s.CreateDirectProfile(ctx, owner.AccountToken, "Second", "blue")
				if err != nil {
					t.Fatal(err)
				}
				second := ""
				for _, profile := range snapshot.Profiles {
					if profile.ID != owner.Account.PrimaryProfileID {
						second = profile.ID
					}
				}
				selection, err := s.SelectDirectProfile(ctx, owner.AccountToken, second, DirectSelection{})
				if err != nil || selection.Session.AccessToken == "" {
					t.Fatal("secondary-profile session was not issued", err)
				}
				bearer = selection.Session.AccessToken
			}
			// Baseline after setup: the stale-session vector spends a different code
			// completing the real challenge, so its count has already changed.
			before := unusedOwnerCodes(t, s, owner.Account.ID)
			_, err := s.ReplaceRecoveryCodes(ctx, bearer, password, code)
			if scenario == "other-account" {
				if !errors.Is(err, ErrFactorMissing) {
					t.Fatalf("another account's call was not refused by the confirmed-factor gate: %v", err)
				}
			} else if err == nil {
				t.Fatal("session replaced recovery codes")
			}
			if got := unusedOwnerCodes(t, s, owner.Account.ID); got != before {
				t.Fatal("refusal changed the owner's usable set", before, got)
			}
		})
	}
}

func TestReplaceRecoveryCodesNeedsConfirmedFactor(t *testing.T) {
	ctx := context.Background()
	s, _, owner := directFixture(t)
	enrol, err := s.EnrolTwoFactor(ctx, owner.AccountToken, "Testing1!")
	if err != nil {
		t.Fatal(err)
	}
	before := unusedOwnerCodes(t, s, owner.Account.ID)
	if _, err = s.ReplaceRecoveryCodes(ctx, owner.AccountToken, "Testing1!", enrol.RecoveryCodes[0]); err == nil {
		t.Fatal("pending enrolment replaced recovery codes")
	}
	if got := unusedOwnerCodes(t, s, owner.Account.ID); got != before {
		t.Fatal("refusal changed the pending set", before, got)
	}
}
