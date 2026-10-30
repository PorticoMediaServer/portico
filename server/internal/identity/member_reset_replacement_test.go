package identity

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Regression source: exercise the real accepting writer and recipient path.
// No credentials are printed, including on a failed assertion.
type replacementState struct {
	epoch, revision, pendingEpoch, tries, issuerEpoch, notices, revoked, factors int64
	password                                                                     []byte
	mustChange, turnOff                                                          bool
	hash, issuer                                                                 string
	expires, pendingRevision                                                     int64
}

func replacementSnapshot(t *testing.T, db *sql.DB, account string) replacementState {
	t.Helper()
	var v replacementState
	err := db.QueryRow(`SELECT a.epoch,a.password_hash,m.revision,COALESCE(c.must_change,0),COALESCE(r.code_hash,''),COALESCE(r.epoch,0),COALESCE(r.expires_at,0),COALESCE(r.failed_attempts,0),COALESCE(r.membership_revision,0),COALESCE(r.issuer_id,''),COALESCE(r.issuer_epoch,0),
	 COALESCE(r.turn_off_two_step,0),(SELECT count(*) FROM notification_records),
	 (SELECT count(*) FROM authorization_session_families WHERE account_id=a.id AND revoked=1),
	 (SELECT count(*) FROM identity_account_factors WHERE account_id=a.id)
	 FROM accounts a JOIN direct_memberships m ON m.account_id=a.id LEFT JOIN identity_credential_state c ON c.account_id=a.id LEFT JOIN member_password_resets r ON r.account_id=a.id WHERE a.id=?`, account).
		Scan(&v.epoch, &v.password, &v.revision, &v.mustChange, &v.hash, &v.pendingEpoch, &v.expires, &v.tries, &v.pendingRevision, &v.issuer, &v.issuerEpoch, &v.turnOff, &v.notices, &v.revoked, &v.factors)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func replacementFixture(t *testing.T, withMFA ...bool) (*Service, *sql.DB, DirectSignIn, DirectAccount, MemberResetCode) {
	t.Helper()
	s, db, owner := directFixture(t)
	member, err := s.CreateDirectMember(context.Background(), owner.AccountToken, DirectMemberCreate{Username: "member", Name: "Member", Password: "Original-password1!", AllowedLibraries: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(withMFA) > 0 && withMFA[0] {
		login, err := s.DirectLogin(context.Background(), "member", "Original-password1!")
		if err != nil {
			t.Fatal(err)
		}
		enrolment, err := s.EnrolTwoFactor(context.Background(), login.AccountToken, "Original-password1!")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.VerifyTwoFactor(context.Background(), login.AccountToken, "Original-password1!", currentTOTP(t, enrolment.Secret)); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.MakeMemberResetCode(context.Background(), owner.AccountToken, member.ID, MemberResetRequest{ExpectedRevision: member.Revision})
	if err != nil {
		t.Fatal(err)
	}
	return s, db, owner, member, first
}

func TestMemberResetConditionalReplacementOnlyRotatesPendingCode(t *testing.T) {
	s, db, owner, member, first := replacementFixture(t, true)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE member_password_resets SET failed_attempts=2 WHERE account_id=?`, member.ID); err != nil {
		t.Fatal(err)
	}
	before := replacementSnapshot(t, db, member.ID)
	if before.factors != 1 {
		t.Fatal("fixture requires an actual enrolled factor")
	}
	next, err := s.MakeMemberResetCode(ctx, owner.AccountToken, member.ID, MemberResetRequest{ExpectedRevision: member.Revision, OriginalResetCode: &first.Code, TurnOffTwoStep: true})
	if err != nil {
		t.Fatal(err)
	}
	if next.Code == first.Code || next.ExpiresAt != first.ExpiresAt || next.ServerID != first.ServerID {
		t.Fatal("replacement changed its deadline/server or reused the old code")
	}
	after := replacementSnapshot(t, db, member.ID)
	if after.hash == before.hash || after.hash != s.setupHash("member-reset", next.Code) {
		t.Fatal("pending code did not rotate")
	}
	if !after.turnOff {
		t.Fatal("selected choice not stored")
	}
	after.hash = before.hash
	after.turnOff = before.turnOff
	if !reflect.DeepEqual(before, after) {
		t.Fatal("replacement changed credential, epoch, attempts, issuer, revision, notice, session or MFA state")
	}
	var off bool
	if err = db.QueryRow(`SELECT turn_off_two_step FROM member_password_resets WHERE account_id=?`, member.ID).Scan(&off); err != nil || !off {
		t.Fatal("selected choice not stored")
	}
	// Replacement is single-use custody: an old sheet cannot rotate the successor.
	stable := replacementSnapshot(t, db, member.ID)
	if _, err = s.MakeMemberResetCode(ctx, owner.AccountToken, member.ID, MemberResetRequest{ExpectedRevision: member.Revision, OriginalResetCode: &first.Code}); !errors.Is(err, ErrProfileChanged) {
		t.Fatal("old replacement witness admitted")
	}
	if !reflect.DeepEqual(stable, replacementSnapshot(t, db, member.ID)) {
		t.Fatal("refused replacement mutated state")
	}
	if _, err = s.VerifyMemberReset(ctx, MemberResetVerify{Username: "member", Code: next.Code}); err != nil {
		t.Fatal("rotated code was not consumable")
	}
	if replacementSnapshot(t, db, member.ID).factors != 0 {
		t.Fatal("explicit MFA choice did not wait for recipient proof")
	}
}

func TestMemberResetConditionalReplacementRefusesWithoutEffects(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []string{"empty", "malformed", "wrong", "deleted", "expired", "exhausted", "target-epoch", "issuer-epoch", "issuer-identity", "different-current-issuer", "membership", "fresh-replacement", "chosen-password"} {
		t.Run(kind, func(t *testing.T) {
			s, db, owner, member, first := replacementFixture(t)
			ctx := context.Background()
			original, revision, bearer := first.Code, member.Revision, owner.AccountToken
			var err error
			switch kind {
			case "empty":
				original = ""
			case "malformed":
				original = strings.Repeat("!", 43)
			case "wrong":
				if original[0] == 'A' {
					original = "B" + original[1:]
				} else {
					original = "A" + original[1:]
				}
			case "deleted":
				_, err = db.Exec(`DELETE FROM member_password_resets WHERE account_id=?`, member.ID)
			case "expired":
				_, err = db.Exec(`UPDATE member_password_resets SET expires_at=0 WHERE account_id=?`, member.ID)
			case "exhausted":
				_, err = db.Exec(`UPDATE member_password_resets SET failed_attempts=5 WHERE account_id=?`, member.ID)
			case "target-epoch":
				_, err = db.Exec(`UPDATE accounts SET epoch=epoch+1 WHERE id=?`, member.ID)
			case "issuer-epoch":
				_, err = db.Exec(`UPDATE member_password_resets SET issuer_epoch=issuer_epoch-1 WHERE account_id=?`, member.ID)
			case "issuer-identity":
				_, err = db.Exec(`UPDATE member_password_resets SET issuer_id=? WHERE account_id=?`, member.ID, member.ID)
			case "different-current-issuer":
				var admin DirectAccount
				admin, err = s.CreateDirectMember(ctx, owner.AccountToken, DirectMemberCreate{Username: "admin", Name: "Admin", Password: "Admin-password1!", AllowedLibraries: []string{}})
				if err == nil {
					_, err = db.Exec(`UPDATE direct_memberships SET role='admin' WHERE account_id=?`, admin.ID)
				}
				if err == nil {
					var login DirectSignIn
					login, err = s.DirectLogin(ctx, "admin", "Admin-password1!")
					bearer = login.AccountToken
				}
			case "membership":
				_, err = db.Exec(`UPDATE direct_memberships SET revision=revision+1 WHERE account_id=?`, member.ID)
				revision++
			case "fresh-replacement":
				_, err = s.MakeMemberResetCode(ctx, owner.AccountToken, member.ID, MemberResetRequest{ExpectedRevision: revision})
			case "chosen-password":
				var grant DirectSignIn
				grant, err = s.VerifyMemberReset(ctx, MemberResetVerify{Username: "member", Code: first.Code})
				if err == nil {
					err = s.ChangeDirectPassword(ctx, grant.Session.AccessToken, "", "Successor-password1!", "")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			before := replacementSnapshot(t, db, member.ID)
			_, err = s.MakeMemberResetCode(ctx, bearer, member.ID, MemberResetRequest{ExpectedRevision: revision, OriginalResetCode: &original, TurnOffTwoStep: true})
			if err == nil {
				t.Fatal("unavailable original reset admitted")
			}
			if !reflect.DeepEqual(before, replacementSnapshot(t, db, member.ID)) {
				t.Fatal("refusal changed successor or pending state")
			}
		})
	}
}

func TestMemberResetVerifyAndConditionalReplacementSerialize(t *testing.T) {
	s, db, owner, member, first := replacementFixture(t)
	ctx := context.Background()
	start := make(chan struct{})
	var wg sync.WaitGroup
	var verifyErr, replaceErr error
	var next MemberResetCode
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, verifyErr = s.VerifyMemberReset(ctx, MemberResetVerify{Username: "member", Code: first.Code})
	}()
	go func() {
		defer wg.Done()
		<-start
		next, replaceErr = s.MakeMemberResetCode(ctx, owner.AccountToken, member.ID, MemberResetRequest{ExpectedRevision: member.Revision, OriginalResetCode: &first.Code, TurnOffTwoStep: true})
	}()
	close(start)
	wg.Wait()
	if (verifyErr == nil) == (replaceErr == nil) {
		t.Fatal("exactly one original-code operation must win")
	}
	if verifyErr == nil {
		if !errors.Is(replaceErr, ErrProfileChanged) {
			t.Fatal("consumed reset was not refused")
		}
		if replacementSnapshot(t, db, member.ID).hash != "" {
			t.Fatal("replacement recreated consumed reset")
		}
	} else {
		if !errors.Is(verifyErr, ErrUnauthorized) {
			t.Fatal("old code failure changed semantics")
		}
		if _, err := s.VerifyMemberReset(ctx, MemberResetVerify{Username: "member", Code: next.Code}); err != nil {
			t.Fatal("winning replacement was not consumable")
		}
	}
}
