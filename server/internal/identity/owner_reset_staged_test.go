package identity

import (
	"context"
	"errors"
	"testing"
)

func TestOwnerResetPostProofTwoFactorChoiceCommitsWithPassword(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, mode := range []string{"keep", "disable", "write rollback", "revoked grant"} {
		t.Run(mode, func(t *testing.T) {
			s, db, owner := directFixture(t)
			ctx := context.Background()
			enrol, err := s.EnrolTwoFactor(ctx, owner.AccountToken, "Testing1!")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.VerifyTwoFactor(ctx, owner.AccountToken, "Testing1!", currentTOTP(t, enrol.Secret)); err != nil {
				t.Fatal(err)
			}
			code := requestResetFileCode(t, s)
			reset, err := s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: code})
			if err != nil || reset.Session == nil {
				t.Fatal("file proof", err)
			}
			if reset.Account.Username != "owner" {
				t.Fatal("reset lost actual owner username")
			}
			if _, err = s.VerifyOwnerReset(ctx, OwnerResetVerify{Code: code}); !errors.Is(err, ErrUnauthorized) {
				t.Fatal("file proof reused", err)
			}
			state, err := s.TwoFactor(ctx, reset.Session.AccessToken)
			if err != nil || !state.Enabled {
				t.Fatal("post-proof conditional security read", err)
			}
			if _, err = s.CreateDirectProfile(ctx, reset.Session.AccessToken, "Not permitted", "blue"); err == nil {
				t.Fatal("security read widened reset authority")
			}
			if mode == "revoked grant" {
				if err = s.LogoutToken(ctx, reset.Session.AccessToken); err != nil {
					t.Fatal(err)
				}
				if err = s.ChangeDirectPasswordWithReset(ctx, reset.Session.AccessToken, "", "Chosen-password1!", "", true); err == nil {
					t.Fatal("revoked grant disabled factor")
				}
				var enabled int
				if err = db.QueryRow(`SELECT count(*) FROM identity_account_factors WHERE confirmed=1`).Scan(&enabled); err != nil || enabled != 1 {
					t.Fatal("revocation damaged factor", err)
				}
				return
			}
			if mode == "write rollback" {
				if _, err = db.Exec(`CREATE TRIGGER staged_password_fail BEFORE UPDATE OF password_hash ON accounts BEGIN SELECT RAISE(ABORT,'fixture refusal');END`); err != nil {
					t.Fatal(err)
				}
				if err = s.ChangeDirectPasswordWithReset(ctx, reset.Session.AccessToken, "", "Chosen-password1!", "", true); err == nil {
					t.Fatal("injected password failure ignored")
				}
				if state, err = s.TwoFactor(ctx, reset.Session.AccessToken); err != nil || !state.Enabled {
					t.Fatal("failed writer turned off factor or spent grant", err)
				}
				if _, err = db.Exec(`DROP TRIGGER staged_password_fail`); err != nil {
					t.Fatal(err)
				}
			}
			turnOff := mode != "keep"
			if err = s.ChangeDirectPasswordWithReset(ctx, reset.Session.AccessToken, "", "Chosen-password1!", "", turnOff); err != nil {
				t.Fatal("post-proof password step", err)
			}
			if _, err = s.TwoFactor(ctx, reset.Session.AccessToken); err == nil {
				t.Fatal("spent password grant remains usable")
			}
			in, err := s.DirectLogin(ctx, "owner", "Chosen-password1!")
			if err != nil {
				t.Fatal(err)
			}
			if turnOff {
				if in.Session == nil || in.Challenge != nil {
					t.Fatal("explicit factor removal did not sign in normally")
				}
			} else {
				if in.Challenge == nil || in.Session != nil {
					t.Fatal("preserved factor bypassed on fresh sign-in")
				}
				var unused int
				if err = db.QueryRow(`SELECT count(*) FROM identity_recovery_codes WHERE used=0`).Scan(&unused); err != nil || unused != len(enrol.RecoveryCodes) {
					t.Fatal("password-only recovery spent authenticator code", err)
				}
			}
		})
	}
}

func TestPostProofFactorChoiceCannotBeUsedByOrdinaryOwner(t *testing.T) {
	s, _, owner := directFixture(t)
	ctx := context.Background()
	enrol, err := s.EnrolTwoFactor(ctx, owner.AccountToken, "Testing1!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyTwoFactor(ctx, owner.AccountToken, "Testing1!", currentTOTP(t, enrol.Secret)); err != nil {
		t.Fatal(err)
	}
	if err = s.ChangeDirectPasswordWithReset(ctx, owner.AccountToken, "Testing1!", "Chosen-password1!", enrol.RecoveryCodes[0], true); !errors.Is(err, ErrForbidden) {
		t.Fatal("ordinary password proof used recovery-only choice", err)
	}
	if state, err := s.TwoFactor(ctx, owner.AccountToken); err != nil || !state.Enabled {
		t.Fatal("ordinary owner choice changed factor", err)
	}
}

func TestPostProofFactorChoiceRefusesMemberResetGrant(t *testing.T) {
	s, owner, member, login := memberWithSession(t)
	ctx := context.Background()
	enrol, err := s.EnrolTwoFactor(ctx, login.AccountToken, "Testing2!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.VerifyTwoFactor(ctx, login.AccountToken, "Testing2!", currentTOTP(t, enrol.Secret)); err != nil {
		t.Fatal(err)
	}
	resetCode, err := s.MakeMemberResetCode(ctx, owner.AccountToken, member.ID, MemberResetRequest{ExpectedRevision: member.Revision})
	if err != nil {
		t.Fatal(err)
	}
	reset, err := s.VerifyMemberReset(ctx, MemberResetVerify{Username: member.Username, Code: resetCode.Code})
	if err != nil || reset.Session == nil {
		t.Fatal(err)
	}
	if _, err = s.TwoFactor(ctx, reset.Session.AccessToken); !errors.Is(err, ErrPasswordChangeRequired) {
		t.Fatal("owner file read widened member reset", err)
	}
	if err = s.ChangeDirectPasswordWithReset(ctx, reset.Session.AccessToken, "", "Chosen-password1!", enrol.RecoveryCodes[0], true); !errors.Is(err, ErrForbidden) {
		t.Fatal("member reset grant disabled its own factor", err)
	}
}
