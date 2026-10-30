package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// castScreen is one television in these tests. A device registration is only
// accepted under the production installation grammar, so each screen carries
// its own well-formed installation id rather than a short label.
func castScreen(t *testing.T, label, name string) context.Context {
	t.Helper()
	return onDevice(t, "cast-"+label+"-"+strings.Repeat("0", 32), name)
}

// issueCastTarget issues a screen's session exactly as pairing does, then reads
// back the genuine facts a reconnect depends on: its own device record bound to
// the new family, the cast-target grant, a live unrevoked family horizon and no
// ordinary refresh credential. A fixture that never published these fails here
// instead of passing a later assertion for the wrong reason.
func issueCastTarget(t *testing.T, s *Service, ctx context.Context, account, profile, derivedFrom string) Envelope {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	out, err := s.IssueCastTargetTx(ctx, tx, "local", account, profile, 1, time.Time{}, derivedFrom)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var devices, bindings, grants, refreshes, revoked int
	var horizon string
	err = s.db.QueryRow(`SELECT
		(SELECT count(*) FROM identity_devices WHERE id=? AND authority='local' AND account_id=?),
		(SELECT count(*) FROM identity_device_families WHERE family_id=? AND device_id=?),
		(SELECT count(*) FROM authorization_family_grants WHERE family_id=? AND grant_kind=?),
		(SELECT count(*) FROM identity_refresh_credentials WHERE family_id=?),
		COALESCE((SELECT revoked FROM authorization_session_families WHERE id=?),-1),
		COALESCE((SELECT authorization_horizon FROM authorization_session_families WHERE id=?),'')`,
		out.DeviceID, account, out.SessionFamilyID, out.DeviceID,
		out.SessionFamilyID, GrantCastTarget, out.SessionFamilyID, out.SessionFamilyID, out.SessionFamilyID).
		Scan(&devices, &bindings, &grants, &refreshes, &revoked, &horizon)
	if err != nil {
		t.Fatal(err)
	}
	if devices != 1 || bindings != 1 || grants != 1 || refreshes != 0 || revoked != 0 {
		t.Fatalf("cast target published devices=%d bindings=%d grants=%d refreshes=%d revoked=%d", devices, bindings, grants, refreshes, revoked)
	}
	if live, err := time.Parse(time.RFC3339, horizon); err != nil || !live.After(time.Now()) {
		t.Fatalf("cast target horizon %q: %v", horizon, err)
	}
	return out
}

// A Cast target's session, whoever paired it: a member's, playback only, not
// held for the owner's approval, and never widened afterwards.
func TestACastTargetSessionIsAMembersAndOnlyForPlayback(t *testing.T) {
	ctx := context.Background()
	s, _, owner := directFixture(t)
	linkedMember(t, s, "linked", "acc_linked")
	pairing, err := s.PorticoSignIn(ctx, PorticoIdentity{AccountID: "acc_linked", Username: "linked", SignerKeyID: "key-cast"})
	if err != nil || pairing.Session == nil {
		t.Fatalf("pairing: %+v %v", pairing, err)
	}
	// A member's screen is not held by the approval rule: the paired device vouches for it.
	screenCtx := castScreen(t, "member-living-room", "Living room TV")
	member := issueCastTarget(t, s, screenCtx, "linked", "linked-profile", pairing.Session.SessionFamilyID)
	// The owner's screen is a member's too.
	ownerScreen := issueCastTarget(t, s, castScreen(t, "owner-den", "Den TV"), owner.Account.ID, owner.Account.PrimaryProfileID, "")
	if ownerScreen.Viewer.Role != TierMember || member.Viewer.Role != TierMember || ownerScreen.RefreshToken != "" {
		t.Fatalf("issued: owner's screen role=%q refresh=%q; member's screen role=%q", ownerScreen.Viewer.Role, ownerScreen.RefreshToken, member.Viewer.Role)
	}

	// Outside playback it is refused, as a refusal and not as a failed sign-in.
	if _, err = s.AuthenticateContext(ctx, ownerScreen.AccessToken); !errors.Is(err, ErrForbidden) {
		t.Fatalf("the owner's screen on a request that is not playback: %v", err)
	}
	if status, _, _ := Refusal(err); status != 403 {
		t.Fatalf("the refusal answers %d", status)
	}
	p, err := s.AuthenticateContext(WithPlaybackRequest(ctx), ownerScreen.AccessToken)
	if err != nil || p.Role != TierMember || p.Grant != GrantCastTarget || p.AccountID != owner.Account.ID {
		t.Fatalf("the owner's screen on a playback request: %+v %v", p, err)
	}
	// No account route is open to it, playback request or not.
	if _, err = s.DirectMe(WithPlaybackRequest(ctx), ownerScreen.AccessToken); !errors.Is(err, ErrForbidden) {
		t.Fatalf("the screen read the owner's account: %v", err)
	}
	if _, err = s.SelectDirectProfile(WithPlaybackRequest(ctx), ownerScreen.AccessToken, owner.Account.PrimaryProfileID, DirectSelection{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("the screen chose a profile and got an ordinary session: %v", err)
	}
	if _, err = s.DirectMembers(WithPlaybackRequest(ctx), ownerScreen.AccessToken); !errors.Is(err, ErrForbidden) {
		t.Fatalf("the screen listed members: %v", err)
	}
	if _, err = s.Devices(WithPlaybackRequest(ctx), ownerScreen.AccessToken, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("the screen listed devices: %v", err)
	}
	// A change of the account's tier does not reach it.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = RetierFamiliesTx(ctx, tx, owner.Account.ID, TierOwner); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if p, err = s.AuthenticateContext(WithPlaybackRequest(ctx), ownerScreen.AccessToken); err != nil || p.Role != TierMember {
		t.Fatalf("the screen after its account's sessions were re-tiered: %+v %v", p.Role, err)
	}
	// It ends with the signing key behind the session that paired it.
	if n := revokeKey(t, s, "key-cast"); n != 2 {
		t.Fatalf("revoking the pairing session's signing key ended %d sessions, want the phone's and its screen's", n)
	}
	if _, err = s.AuthenticateContext(WithPlaybackRequest(ctx), member.AccessToken); !errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrForbidden) {
		t.Fatalf("the member's screen after the key was revoked: %v", err)
	}
	if _, err = s.AuthenticateContext(WithPlaybackRequest(ctx), ownerScreen.AccessToken); err != nil {
		t.Fatalf("the owner's screen, which no key admitted, was ended: %v", err)
	}
	// An ordinary session is not affected by any of this.
	if _, err = s.AuthenticateContext(ctx, owner.Session.AccessToken); err != nil {
		t.Fatalf("an ordinary session outside a playback request: %v", err)
	}
}

// A device-token-authorized reconnect can validate the exact target family
// after its access token expires, without authenticating that expired bearer.
func TestCastTargetParentAllowsOnlyAccessExpiry(t *testing.T) {
	ctx := context.Background()
	s, db, owner := directFixture(t)
	screen := issueCastTarget(t, s, castScreen(t, "parent-test", "TV"), owner.Account.ID, owner.Account.PrimaryProfileID, owner.Session.SessionFamilyID)
	expired := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	// authorization_access is a read projection of the family token, so the
	// exact access token's own row is what expires; the family, its horizon,
	// device credential and grant stay live for a device-token reconnect.
	if _, err := db.Exec(`UPDATE authorization_family_tokens SET expires_at=? WHERE token_hash=?`, expired, Digest(screen.AccessToken)); err != nil {
		t.Fatal(err)
	}
	expected := Principal{Viewer: screen.Viewer, Epoch: 1, Grant: GrantCastTarget}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	facts, err := s.CastTargetParentTx(ctx, tx, screen.SessionFamilyID, expected)
	if err != nil || facts.ID != screen.SessionFamilyID || facts.DeviceID != screen.DeviceID {
		t.Fatalf("valid parent: %+v %v", facts, err)
	}
	if _, err = s.FamilyAuthorityTx(ctx, tx, screen.SessionFamilyID, expected); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("ordinary family accepted expired token: %v", err)
	}
	if _, err = tx.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=?`, screen.SessionFamilyID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CastTargetParentTx(ctx, tx, screen.SessionFamilyID, expected); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked parent: %v", err)
	}
}

func TestCastTargetParentRefusesChangedFactsAndRetiredToken(t *testing.T) {
	ctx := context.Background()
	s, db, owner := directFixture(t)
	screen := issueCastTarget(t, s, castScreen(t, "parent-fence", "TV"), owner.Account.ID, owner.Account.PrimaryProfileID, owner.Session.SessionFamilyID)
	expected := Principal{Viewer: screen.Viewer, Epoch: 1, Grant: GrantCastTarget}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	changed := expected
	changed.ProfileID = "other-profile"
	if _, err = s.CastTargetParentTx(ctx, tx, screen.SessionFamilyID, changed); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("changed profile: %v", err)
	}
	changed = expected
	changed.Role = TierOwner
	if _, err = s.CastTargetParentTx(ctx, tx, screen.SessionFamilyID, changed); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("changed role: %v", err)
	}
	changed = expected
	changed.Epoch++
	if _, err = s.CastTargetParentTx(ctx, tx, screen.SessionFamilyID, changed); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("changed epoch: %v", err)
	}
	changed = expected
	changed.Grant = ""
	if _, err = s.CastTargetParentTx(ctx, tx, screen.SessionFamilyID, changed); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("changed grant: %v", err)
	}
	if _, err = tx.Exec(`UPDATE authorization_family_tokens SET retired=1 WHERE family_id=?`, screen.SessionFamilyID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CastTargetParentTx(ctx, tx, screen.SessionFamilyID, expected); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("retired token: %v", err)
	}
}

func TestCastTargetLocalParentHorizonDoesNotExtend(t *testing.T) {
	s, db, owner := directFixture(t)
	horizon := time.Now().Add(6 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE authorization_session_families SET authorization_horizon=? WHERE id=?`, horizon, owner.Session.SessionFamilyID); err != nil {
		t.Fatal(err)
	}
	child := issueCastTarget(t, s, castScreen(t, "local-horizon", "TV"), owner.Account.ID, owner.Account.PrimaryProfileID, owner.Session.SessionFamilyID)
	var childHorizon string
	if err := db.QueryRow(`SELECT authorization_horizon FROM authorization_session_families WHERE id=?`, child.SessionFamilyID).Scan(&childHorizon); err != nil {
		t.Fatal(err)
	}
	if childHorizon > horizon {
		t.Fatalf("child horizon %q exceeds original %q", childHorizon, horizon)
	}
}
