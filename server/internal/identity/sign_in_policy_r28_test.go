package identity

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestSignInPolicyCASScopesAndActualPasswordResetFactorConsumers(t *testing.T) {
	ctx := context.Background()
	s, db, original := directFixture(t)
	member, e := s.CreateDirectMember(ctx, original.AccountToken, DirectMemberCreate{Username: "guest", Name: "Guest", Password: "Correct-Horse-9", AllowedLibraries: []string{}})
	if e != nil {
		t.Fatal(e)
	}
	before, e := s.ReadSignInPolicy(ctx, original.AccountToken)
	if e != nil || before.Revision != 1 || before.Values != DefaultSignInPolicy() {
		t.Fatalf("default %+v %v", before, e)
	}
	values := SignInPolicyValues{7, 0, "strict", "strict", 12, 15, "administrators", false, 30}
	current, e := s.ChangeSignInPolicy(ctx, original.AccountToken, SignInPolicyPatch{before.Revision, values})
	if e != nil || current.Revision != 2 || current.Values != values {
		t.Fatalf("policy write %+v %v", current, e)
	}
	if _, e = s.ChangeSignInPolicy(ctx, original.AccountToken, SignInPolicyPatch{before.Revision, DefaultSignInPolicy()}); !errors.Is(e, ErrProfileChanged) {
		t.Fatalf("stale policy overwrote: %v", e)
	}
	// Raising the new-password minimum does not make the existing password fail.
	restricted, e := s.DirectLogin(ctx, "owner", "Testing1!")
	if e != nil || !restricted.TwoStepSetupRequired || restricted.Session == nil || restricted.Session.Viewer.Role != "account" || restricted.CanManage || restricted.Capabilities != (ManagementCapabilities{}) {
		t.Fatalf("restricted sign-in %+v %v", restricted, e)
	}
	horizon, e := time.Parse(time.RFC3339, restricted.Session.AuthorizationHorizon)
	if e != nil || time.Until(horizon) < 7*24*time.Hour-time.Minute || time.Until(horizon) > 7*24*time.Hour {
		t.Fatalf("policy session window: %s %v", restricted.Session.AuthorizationHorizon, e)
	}
	if _, e = s.SelectDirectProfile(ctx, restricted.AccountToken, "profile", DirectSelection{}); !errors.Is(e, ErrFactorSetupRequired) {
		t.Fatalf("profile escaped required enrollment: %v", e)
	}
	if e = s.ChangeDirectPassword(ctx, original.AccountToken, "Testing1!", "Testing2!", ""); !errors.Is(e, ErrDirectInput) {
		t.Fatalf("new short password accepted: %v", e)
	}
	code, e := s.MakeMemberResetCode(ctx, original.AccountToken, member.ID, MemberResetRequest{ExpectedRevision: member.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if code.ServerID != s.ID() {
		t.Fatal("Local member reset link lost canonical identity")
	}
	until, e := time.Parse(time.RFC3339, code.ExpiresAt)
	if e != nil || time.Until(until) < 15*time.Minute-time.Minute || time.Until(until) > 15*time.Minute {
		t.Fatalf("reset lifetime %+v %v", code, e)
	}
	// Enrollment has its own restricted authority and fresh password proof; the
	// account grant acquires profile authority only after a genuine TOTP proof.
	enrol, e := s.EnrolTwoFactor(ctx, restricted.AccountToken, "Testing1!")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.VerifyTwoFactor(ctx, restricted.AccountToken, "Testing1!", currentTOTP(t, enrol.Secret)); e != nil {
		t.Fatal(e)
	}
	selected, e := s.SelectDirectProfile(ctx, restricted.AccountToken, "profile", DirectSelection{})
	if e != nil || selected.Session.Viewer.Role != TierOwner {
		t.Fatalf("verified enrollment could not continue: %+v %v", selected, e)
	}
	if e = s.DisableTwoFactor(ctx, selected.Session.AccessToken, "Testing1!", ""); !errors.Is(e, ErrForbidden) {
		t.Fatalf("required factor disabled: %v", e)
	}
	// The policy helper is a transaction seam, never a JSON settings mirror.
	if e = dbwork.WithWriteTx(ctx, db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		p, e := ReadSignInPolicyTx(ctx, tx)
		if e != nil {
			return e
		}
		p.Values.PasswordMinLength = 10
		_, e = ReplaceSignInPolicyTx(ctx, tx, SignInPolicyPatch{p.Revision, p.Values})
		return e
	}); e != nil {
		t.Fatal(e)
	}
}

func TestAttemptPolicyChoicesKeepBothPasswordAndPINBudgetsBounded(t *testing.T) {
	for _, tc := range []struct {
		policy          AttemptPolicy
		at              int
		before, atDelay int64
	}{{"relaxed", 10, 0, 1}, {"standard", 5, 0, 1}, {"strict", 3, 0, 10}} {
		if got := passwordDelay(tc.policy, tc.at-1); got != tc.before {
			t.Fatalf("password %s before threshold=%d", tc.policy, got)
		}
		if got := passwordDelay(tc.policy, tc.at); got != tc.atDelay {
			t.Fatalf("password %s threshold=%d", tc.policy, got)
		}
		if got := passwordDelay(tc.policy, 10000); got <= 0 || got > 900 {
			t.Fatalf("unbounded password delay %d", got)
		}
	}
	for _, tc := range []struct {
		policy AttemptPolicy
		at     int
		want   int64
	}{{"relaxed", 4, 0}, {"relaxed", 5, 30}, {"standard", 2, 2}, {"standard", 3, 30}, {"strict", 2, 2}, {"strict", 3, 300}} {
		if got := pinDelay(tc.policy, tc.at); got != tc.want {
			t.Fatalf("PIN %s attempts%d=%d, want%d", tc.policy, tc.at, got, tc.want)
		}
	}
}

func TestSignInPolicyRetiresPINProofsRememberedRowsAndEndedHistory(t *testing.T) {
	ctx := context.Background()
	s, db, login := directFixture(t)
	profiles, e := s.CreateDirectProfile(ctx, login.AccountToken, "Protected", "blue")
	if e != nil {
		t.Fatal(e)
	}
	child := childProfile(t, profiles)
	pin := "0123"
	if _, e = s.EditDirectProfile(ctx, login.AccountToken, child.ID, DirectProfileEdit{ExpectedRevision: child.Revision, PIN: &pin}); e != nil {
		t.Fatal(e)
	}
	selected, e := s.SelectDirectProfile(ctx, login.AccountToken, child.ID, DirectSelection{PIN: pin, Trust: true, InstallationID: "installation"})
	if e != nil || selected.TrustedSelection == nil {
		t.Fatalf("remember PIN: %+v %v", selected, e)
	}
	installation := Token()
	if _, e = s.RegisterDevice(ctx, login.AccountToken, registration(installation), ""); e != nil {
		t.Fatal(e)
	}
	if list, e := s.RememberBrowserAccount(ctx, login.AccountToken, installation, true); e != nil || len(list) != 1 {
		t.Fatalf("remember account: %+v %v", list, e)
	}
	ended, e := s.Issue("account", "profile", "local", "owner", 1)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.LogoutToken(ctx, ended.AccessToken); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE authorization_session_families SET revoked_at=? WHERE id=?`, time.Now().Add(-45*24*time.Hour).UTC().Format(time.RFC3339), ended.SessionFamilyID); e != nil {
		t.Fatal(e)
	}
	policy, e := s.ReadSignInPolicy(ctx, login.AccountToken)
	if e != nil {
		t.Fatal(e)
	}
	policy.Values.PINRememberDays = 0
	policy.Values.RememberAccounts = false
	policy.Values.EndedSessionsDays = 30
	if _, e = s.ChangeSignInPolicy(ctx, login.AccountToken, SignInPolicyPatch{policy.Revision, policy.Values}); e != nil {
		t.Fatal(e)
	}
	var trust, remembered int
	if e = db.QueryRow(`SELECT count(*) FROM direct_profile_trust`).Scan(&trust); e != nil || trust != 0 {
		t.Fatalf("PIN proofs survived: %d %v", trust, e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM identity_browser_accounts`).Scan(&remembered); e != nil || remembered != 0 {
		t.Fatalf("remembered descriptors survived: %d %v", remembered, e)
	}
	if list, e := s.BrowserAccounts(ctx, installation); e != nil || len(list) != 0 {
		t.Fatalf("public remembered read while off: %+v %v", list, e)
	}
	if _, e = s.RememberBrowserAccount(ctx, login.AccountToken, installation, true); !errors.Is(e, ErrUnauthorized) {
		t.Fatalf("remembered write while off: %v", e)
	}
	withoutProof, e := s.SelectDirectProfile(ctx, login.AccountToken, child.ID, DirectSelection{PIN: pin, Trust: true, InstallationID: "installation"})
	if e != nil || withoutProof.TrustedSelection != nil {
		t.Fatalf("remember-off issued proof: %+v %v", withoutProof, e)
	}
	// A rejected retired token charges the real PIN budget. Keep this negative
	// after the independent valid-PIN check rather than bypassing that budget.
	if _, e = s.SelectDirectProfile(ctx, login.AccountToken, child.ID, DirectSelection{TrustToken: selected.TrustedSelection.Token, InstallationID: "installation"}); !errors.Is(e, ErrProfilePIN) {
		t.Fatalf("retired PIN proof admitted: %v", e)
	}
	if n, e := s.PruneEndedSessions(ctx); e != nil || n != 1 {
		t.Fatalf("policy retention pruned%d: %v", n, e)
	}
	if _, e = s.Authenticate(login.AccountToken); e != nil {
		t.Fatalf("pruning ended live authority: %v", e)
	}
}

func TestSignInPolicyRefreshUsesLocalWindowAndPorticoDescendantsStayBounded(t *testing.T) {
	ctx := context.Background()
	s, db, login := directFixture(t)
	p, e := s.ReadSignInPolicy(ctx, login.AccountToken)
	if e != nil {
		t.Fatal(e)
	}
	p.Values.StaySignedInDays = 7
	if _, e = s.ChangeSignInPolicy(ctx, login.AccountToken, SignInPolicyPatch{p.Revision, p.Values}); e != nil {
		t.Fatal(e)
	}
	fresh, e := s.RefreshSession(ctx, login.Session.RefreshToken, login.InstallationID, "policy-refresh-0001")
	if e != nil {
		t.Fatal(e)
	}
	until, e := time.Parse(time.RFC3339, fresh.AuthorizationHorizon)
	if e != nil || time.Until(until) > 7*24*time.Hour || time.Until(until) < 7*24*time.Hour-time.Minute {
		t.Fatalf("local refresh ignored policy: %s %v", fresh.AuthorizationHorizon, e)
	}
	// This unit fixture seeds only the admission marker and historical horizon
	// to test descendant issuance. It is not a Hosted identity/consent proof.
	parentUntil := time.Now().UTC().Truncate(time.Second).Add(2 * 24 * time.Hour)
	if _, e = db.Exec(`INSERT INTO portico_admitted_families(family_id,signer_key_id) VALUES(?, 'fixture-signer')`, fresh.SessionFamilyID); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE authorization_session_families SET authorization_horizon=? WHERE id=?`, parentUntil.Format(time.RFC3339), fresh.SessionFamilyID); e != nil {
		t.Fatal(e)
	}
	selected, e := s.SelectDirectProfile(ctx, fresh.AccessToken, "profile", DirectSelection{})
	if e != nil {
		t.Fatal(e)
	}
	descendantUntil, e := time.Parse(time.RFC3339, selected.Session.AuthorizationHorizon)
	if e != nil || descendantUntil.After(parentUntil) {
		t.Fatalf("descendant extended proof: %s %v", selected.Session.AuthorizationHorizon, e)
	}
}
