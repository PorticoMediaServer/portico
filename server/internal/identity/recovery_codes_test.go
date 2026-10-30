package identity

import (
	"context"
	"testing"
)

func TestReplacementRecoveryCodesNeedPasswordAndConfirmedFactorAndInvalidateOldSet(t *testing.T) {
	ctx := context.Background()
	s, db, owner := directFixture(t)
	if _, err := s.ReplaceRecoveryCodes(ctx, owner.AccountToken, "Testing1!", "anything"); err == nil {
		t.Fatal("codes without a confirmed factor")
	}
	enrol, err := s.EnrolTwoFactor(ctx, owner.AccountToken, "Testing1!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReplaceRecoveryCodes(ctx, owner.AccountToken, "Testing1!", enrol.RecoveryCodes[0]); err == nil {
		t.Fatal("pending enrolment accepted")
	}
	if _, err = s.VerifyTwoFactor(ctx, owner.AccountToken, "Testing1!", currentTOTP(t, enrol.Secret)); err != nil {
		t.Fatal(err)
	}
	for _, proof := range [][2]string{{"", enrol.RecoveryCodes[0]}, {"wrong-password", enrol.RecoveryCodes[0]}, {"Testing1!", "wrong-code"}} {
		if _, err = s.ReplaceRecoveryCodes(ctx, owner.AccountToken, proof[0], proof[1]); err == nil {
			t.Fatal("bad proof accepted")
		}
	}
	var before int
	if err = db.QueryRow(`SELECT count(*) FROM identity_recovery_codes WHERE account_id=? AND used=0`, owner.Account.ID).Scan(&before); err != nil || before != len(enrol.RecoveryCodes) {
		t.Fatal("refusal changed prior codes", before, err)
	}
	out, err := s.ReplaceRecoveryCodes(ctx, owner.AccountToken, "Testing1!", enrol.RecoveryCodes[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Codes) != len(enrol.RecoveryCodes) {
		t.Fatal("replacement count", len(out.Codes))
	}
	for _, old := range enrol.RecoveryCodes {
		var exists bool
		if err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM identity_recovery_codes WHERE account_id=? AND code_hash=?)`, owner.Account.ID, s.setupHash("mfa-recovery:"+owner.Account.ID, normalizeRecoveryCode(old))).Scan(&exists); err != nil || exists {
			t.Fatal("old code retained", err)
		}
	}
	if _, err = s.ReplaceRecoveryCodes(ctx, owner.AccountToken, "Testing1!", enrol.RecoveryCodes[0]); err == nil {
		t.Fatal("old proof replay accepted")
	}
	if _, err = s.ReplaceRecoveryCodes(ctx, owner.AccountToken, "Testing1!", out.Codes[0]); err != nil {
		t.Fatal("actual new code refused", err)
	}
}
