package identity

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// Remembering a profile binds to the sign-in (family) that chose it, and a PIN
// change or "Forget remembered devices" makes the profile's other sign-ins ask
// for the PIN again (pin_gate) instead of signing them out. A remembered main
// profile with a PIN also re-enters that PIN for the account's management
// powers once manage_until has passed.

const rememberInstallation = "remember-installation"

type familyFlags struct {
	gated, revoked bool
	remember       string
	manage         string
}

func familyFlagsOf(t *testing.T, db *sql.DB, family string) familyFlags {
	t.Helper()
	var f familyFlags
	if err := db.QueryRow(`SELECT pin_gate=1,revoked=1,remember_until,manage_until FROM authorization_session_families WHERE id=?`, family).Scan(&f.gated, &f.revoked, &f.remember, &f.manage); err != nil {
		t.Fatalf("reading family %q: %v", family, err)
	}
	return f
}

// requireGated: the family is alive, remembers nothing and asks for the PIN.
func requireGated(t *testing.T, db *sql.DB, label, family string) {
	t.Helper()
	f := familyFlagsOf(t, db, family)
	if !f.gated || f.revoked || f.remember != "" {
		t.Fatalf("%s: want a live gated family that remembers nothing, got %+v", label, f)
	}
}

// requireUngated: the family is alive and not gated.
func requireUngated(t *testing.T, db *sql.DB, label, family string) {
	t.Helper()
	f := familyFlagsOf(t, db, family)
	if f.gated || f.revoked {
		t.Fatalf("%s: want a live ungated family, got %+v", label, f)
	}
}

func chooseProfile(t *testing.T, s *Service, token, profile string, q DirectSelection) DirectSelectionResult {
	t.Helper()
	if q.InstallationID == "" {
		q.InstallationID = rememberInstallation
	}
	out, err := s.SelectDirectProfile(context.Background(), token, profile, q)
	if err != nil {
		t.Fatalf("choosing profile %s: %v", profile, err)
	}
	return out
}

func liftPINDelay(t *testing.T, db *sql.DB, profile string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE direct_profile_pin_attempts SET locked_until_ms=0 WHERE profile_id=?`, profile); err != nil {
		t.Fatal(err)
	}
}

func liveProfile(t *testing.T, s *Service, token, id string) DirectProfile {
	t.Helper()
	snapshot, err := s.DirectMe(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	return launchResumeProfile(t, snapshot, id)
}

func setProfilePIN(t *testing.T, s *Service, manager, profile, pin string) {
	t.Helper()
	current := liveProfile(t, s, manager, profile)
	if _, err := s.EditDirectProfile(context.Background(), manager, profile, DirectProfileEdit{ExpectedRevision: current.Revision, PIN: &pin}); err != nil {
		t.Fatalf("setting a PIN: %v", err)
	}
}

func setRememberDays(t *testing.T, s *Service, token string, days PINRememberDays) {
	t.Helper()
	ctx := context.Background()
	policy, err := s.ReadSignInPolicy(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	policy.Values.PINRememberDays = days
	if _, err = s.ChangeSignInPolicy(ctx, token, SignInPolicyPatch{policy.Revision, policy.Values}); err != nil {
		t.Fatal(err)
	}
}

// ownerSession is the main profile chosen while it has no PIN: the server
// policy is changed from the main profile, never from an account-scoped
// sign-in.
func ownerSession(t *testing.T, s *Service, login DirectSignIn, profiles map[switchProfileKind]DirectProfile) string {
	t.Helper()
	return chooseProfile(t, s, login.AccountToken, profiles[switchMain].ID, DirectSelection{}).Session.AccessToken
}

func TestRememberIsIssuedForThePolicyDaysAndIgnoredWhenOff(t *testing.T) {
	const pin = "4821"
	s, db, login, profiles := switchPINFixture(t, map[switchProfileKind]string{switchKids: pin})
	kids := profiles[switchKids]
	owner := ownerSession(t, s, login, profiles)
	for _, days := range []PINRememberDays{30, 7, 90} {
		setRememberDays(t, s, owner, days)
		before := time.Now().UTC()
		out := chooseProfile(t, s, login.AccountToken, kids.ID, DirectSelection{PIN: pin, Remember: true})
		if out.Remembered == nil {
			t.Fatalf("%d days: remember was not honoured", days)
		}
		until, err := time.Parse(time.RFC3339, out.Remembered.ExpiresAt)
		want := before.Add(time.Duration(days) * 24 * time.Hour)
		if err != nil || until.Before(want.Add(-time.Second)) || until.After(want.Add(time.Minute)) {
			t.Fatalf("%d days: expires %q, want about %s: %v", days, out.Remembered.ExpiresAt, want.Format(time.RFC3339), err)
		}
		f := familyFlagsOf(t, db, out.Session.SessionFamilyID)
		if f.remember != out.Remembered.ExpiresAt || f.gated || f.revoked {
			t.Fatalf("%d days: stored %+v for %+v", days, f, out.Remembered)
		}
		manage, err := time.Parse(time.RFC3339, f.manage)
		if err != nil || manage.Before(before.Add(59*time.Minute)) || manage.After(time.Now().UTC().Add(61*time.Minute)) {
			t.Fatalf("%d days: a fresh PIN should open management for an hour, got %q: %v", days, f.manage, err)
		}
		if me, err := s.DirectMe(context.Background(), out.Session.AccessToken); err != nil || !me.RememberProfiles {
			t.Fatalf("%d days: snapshot RememberProfiles=%v %v", days, me.RememberProfiles, err)
		}
	}
	// A choice that does not ask to remember remembers nothing.
	plain := chooseProfile(t, s, login.AccountToken, kids.ID, DirectSelection{PIN: pin})
	if f := familyFlagsOf(t, db, plain.Session.SessionFamilyID); plain.Remembered != nil || f.remember != "" {
		t.Fatalf("a choice without remember remembered: %+v %+v", plain.Remembered, f)
	}
	// With the policy Off, asking to remember is ignored.
	setRememberDays(t, s, owner, 0)
	off := chooseProfile(t, s, login.AccountToken, kids.ID, DirectSelection{PIN: pin, Remember: true})
	if f := familyFlagsOf(t, db, off.Session.SessionFamilyID); off.Remembered != nil || f.remember != "" || f.gated || f.revoked {
		t.Fatalf("remember was honoured while Off: %+v %+v", off.Remembered, f)
	}
	if me, err := s.DirectMe(context.Background(), off.Session.AccessToken); err != nil || me.RememberProfiles {
		t.Fatalf("RememberProfiles while Off: %v %v", me.RememberProfiles, err)
	}
	if viewer, err := s.Authenticate(off.Session.AccessToken); err != nil || viewer.ProfileID != kids.ID {
		t.Fatalf("a choice with remember ignored does not work: %+v %v", viewer, err)
	}
}

func TestSettingAndChangingAPINGatesTheProfilesOtherSignIns(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(t *testing.T, s *Service, manager, profile, pin string)
	}{
		{"edit", setProfilePIN},
		{"reset", func(t *testing.T, s *Service, manager, profile, pin string) {
			t.Helper()
			if _, err := s.PINReset(context.Background(), manager, profile, pin); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const first, second = "4821", "7153"
			s, db, login, profiles := switchPINFixture(t, nil)
			kids, guest := profiles[switchKids], profiles[switchGuest]
			// Two sign-ins on Kids (one remembered), one on Guest, which no PIN
			// change on Kids may touch.
			k1 := chooseProfile(t, s, login.AccountToken, kids.ID, DirectSelection{Remember: true})
			k2 := chooseProfile(t, s, login.AccountToken, kids.ID, DirectSelection{})
			g := chooseProfile(t, s, login.AccountToken, guest.ID, DirectSelection{Remember: true})
			if k1.Remembered == nil {
				t.Fatal("Kids was not remembered")
			}

			// A PIN set on a profile that had none.
			tc.set(t, s, login.AccountToken, kids.ID, first)
			requireGated(t, db, "k1 after the PIN was set", k1.Session.SessionFamilyID)
			requireGated(t, db, "k2 after the PIN was set", k2.Session.SessionFamilyID)
			requireUngated(t, db, "the other profile", g.Session.SessionFamilyID)
			if f := familyFlagsOf(t, db, g.Session.SessionFamilyID); f.remember == "" {
				t.Fatalf("a PIN set on Kids cleared Guest's remembering: %+v", f)
			}
			requireUngated(t, db, "the account sign-in that made the change", login.Session.SessionFamilyID)

			// Entering the PIN again leaves a working, ungated, remembered sign-in.
			again := chooseProfile(t, s, k1.Session.AccessToken, kids.ID, DirectSelection{PIN: first, Remember: true})
			requireUngated(t, db, "the successor", again.Session.SessionFamilyID)
			// A later change gates it as well, and the PIN that was set before.
			liftPINDelay(t, db, kids.ID)
			tc.set(t, s, login.AccountToken, kids.ID, second)
			requireGated(t, db, "the successor after the PIN changed", again.Session.SessionFamilyID)
			requireUngated(t, db, "the other profile again", g.Session.SessionFamilyID)
		})
	}
}

func TestChangingTheMainProfilePINGatesItsOtherSignInsButNotTheChangers(t *testing.T) {
	const pin = "4821"
	s, db, login, profiles := switchPINFixture(t, nil)
	main := profiles[switchMain]
	changer := chooseProfile(t, s, login.AccountToken, main.ID, DirectSelection{})
	other := chooseProfile(t, s, login.AccountToken, main.ID, DirectSelection{Remember: true})
	setProfilePIN(t, s, changer.Session.AccessToken, main.ID, pin)
	requireGated(t, db, "the main profile's other sign-in", other.Session.SessionFamilyID)
	requireUngated(t, db, "the sign-in that changed the PIN", changer.Session.SessionFamilyID)
	if viewer, err := s.Authenticate(changer.Session.AccessToken); err != nil || viewer.ProfileID != main.ID || viewer.Role != TierOwner {
		t.Fatalf("the changing sign-in stopped working: %+v %v", viewer, err)
	}
	// The same through a reset, from the sign-in that is not gated.
	liftPINDelay(t, db, main.ID)
	back := chooseProfile(t, s, changer.Session.AccessToken, main.ID, DirectSelection{PIN: pin})
	if _, err := s.PINReset(context.Background(), back.Session.AccessToken, main.ID, "7153"); err != nil {
		t.Fatal(err)
	}
	requireGated(t, db, "the changing sign-in's sibling after a reset", changer.Session.SessionFamilyID)
	requireUngated(t, db, "the sign-in that reset the PIN", back.Session.SessionFamilyID)
}

func TestRemovingAPINGatesNothing(t *testing.T) {
	const pin = "4821"
	s, db, login, profiles := switchPINFixture(t, map[switchProfileKind]string{switchKids: pin})
	kids := profiles[switchKids]
	k1 := chooseProfile(t, s, login.AccountToken, kids.ID, DirectSelection{PIN: pin, Remember: true})
	k2 := chooseProfile(t, s, login.AccountToken, kids.ID, DirectSelection{PIN: pin})
	setProfilePIN(t, s, login.AccountToken, kids.ID, "")
	for label, family := range map[string]string{"remembered": k1.Session.SessionFamilyID, "plain": k2.Session.SessionFamilyID} {
		if f := familyFlagsOf(t, db, family); f.gated {
			t.Fatalf("removing the PIN gated the %s sign-in: %+v", label, f)
		}
	}
	// And through a reset to no PIN.
	setProfilePIN(t, s, login.AccountToken, kids.ID, pin)
	liftPINDelay(t, db, kids.ID)
	k3 := chooseProfile(t, s, login.AccountToken, kids.ID, DirectSelection{PIN: pin, Remember: true})
	if _, err := s.PINReset(context.Background(), login.AccountToken, kids.ID, ""); err != nil {
		t.Fatal(err)
	}
	if f := familyFlagsOf(t, db, k3.Session.SessionFamilyID); f.gated {
		t.Fatalf("a PIN reset to none gated a sign-in: %+v", f)
	}
}

func TestForgettingRememberedDevicesGatesOnlyThatProfilesRememberedSignInsExceptTheCallers(t *testing.T) {
	const pin = "4821"
	s, db, login, profiles := switchPINFixture(t, map[switchProfileKind]string{switchKids: pin})
	main, kids, guest := profiles[switchMain], profiles[switchKids], profiles[switchGuest]
	caller := chooseProfile(t, s, login.AccountToken, main.ID, DirectSelection{Remember: true})
	mainRemembered := chooseProfile(t, s, login.AccountToken, main.ID, DirectSelection{Remember: true})
	mainPlain := chooseProfile(t, s, login.AccountToken, main.ID, DirectSelection{})
	kidsRemembered := chooseProfile(t, s, login.AccountToken, kids.ID, DirectSelection{PIN: pin, Remember: true})
	kidsPlain := chooseProfile(t, s, login.AccountToken, kids.ID, DirectSelection{PIN: pin})
	guestRemembered := chooseProfile(t, s, login.AccountToken, guest.ID, DirectSelection{Remember: true})

	ctx := context.Background()
	if err := s.ForgetRememberedDevices(ctx, caller.Session.AccessToken, main.ID, ""); err != nil {
		t.Fatal(err)
	}
	requireGated(t, db, "the main profile's remembered sign-in", mainRemembered.Session.SessionFamilyID)
	if f := familyFlagsOf(t, db, caller.Session.SessionFamilyID); f.gated || f.revoked || f.remember == "" {
		t.Fatalf("the caller's own sign-in was touched: %+v", f)
	}
	if f := familyFlagsOf(t, db, mainPlain.Session.SessionFamilyID); f.gated || f.revoked {
		t.Fatalf("a sign-in that never remembered was gated: %+v", f)
	}
	for label, family := range map[string]string{"Kids remembered": kidsRemembered.Session.SessionFamilyID, "Kids plain": kidsPlain.Session.SessionFamilyID, "Guest remembered": guestRemembered.Session.SessionFamilyID} {
		if f := familyFlagsOf(t, db, family); f.gated || f.revoked {
			t.Fatalf("forgetting the main profile's devices gated %s: %+v", label, f)
		}
	}
	if f := familyFlagsOf(t, db, kidsRemembered.Session.SessionFamilyID); f.remember == "" {
		t.Fatalf("another profile's remembering was cleared: %+v", f)
	}

	// Forgetting Kids' devices: the remembered Kids sign-in, not the plain one.
	if err := s.ForgetRememberedDevices(ctx, caller.Session.AccessToken, kids.ID, ""); err != nil {
		t.Fatal(err)
	}
	requireGated(t, db, "Kids' remembered sign-in", kidsRemembered.Session.SessionFamilyID)
	if f := familyFlagsOf(t, db, kidsPlain.Session.SessionFamilyID); f.gated || f.revoked {
		t.Fatalf("Kids' unremembered sign-in was gated: %+v", f)
	}
	if f := familyFlagsOf(t, db, guestRemembered.Session.SessionFamilyID); f.gated || f.remember == "" {
		t.Fatalf("Guest was touched by forgetting Kids: %+v", f)
	}
}

func TestAGatedSignInMayOnlyEnterThePINAgain(t *testing.T) {
	const pin, wrong = "4821", "0000"
	ctx := context.Background()
	s, db, login, profiles := switchPINFixture(t, nil)
	main, kids := profiles[switchMain], profiles[switchKids]
	// A gated main-profile sign-in, so management routes can be tried on it.
	changer := chooseProfile(t, s, login.AccountToken, main.ID, DirectSelection{})
	gated := chooseProfile(t, s, login.AccountToken, main.ID, DirectSelection{Remember: true})
	setProfilePIN(t, s, changer.Session.AccessToken, main.ID, pin)
	requireGated(t, db, "the sign-in under test", gated.Session.SessionFamilyID)
	token := gated.Session.AccessToken

	// Ordinary viewing is refused as profile_pin_reentry, not as a sign-out.
	if _, err := s.Authenticate(token); !errors.Is(err, ErrProfilePINReentry) {
		t.Fatalf("an ordinary request on a gated sign-in: %v", err)
	}
	if _, err := s.AuthenticateContext(WithPINReentryRequest(ctx), token); err != nil {
		t.Fatalf("a re-entry request on a gated sign-in: %v", err)
	}
	// A playback that already started finishes; starting one waits for the PIN.
	if viewer, err := s.AuthenticateContext(WithRunningPlaybackRequest(ctx), token); err != nil || viewer.ProfileID != main.ID {
		t.Fatalf("a running playback's request on a gated sign-in: %+v %v", viewer, err)
	}
	if _, err := s.AuthenticateContext(WithPlaybackRequest(ctx), token); !errors.Is(err, ErrProfilePINReentry) {
		t.Fatalf("a new playback on a gated sign-in: %v", err)
	}
	// It may read its account (GET /v1/direct), but manages nothing.
	if me, err := s.DirectMe(ctx, token); err != nil || len(me.Profiles) < 3 {
		t.Fatalf("a gated sign-in could not read its account: %+v %v", me, err)
	}
	if _, err := s.CreateDirectProfile(ctx, token, "Nope", "sky"); !errors.Is(err, ErrProfilePINReentry) {
		t.Fatalf("a gated sign-in managed profiles: %v", err)
	}
	// The ungated changer is unaffected.
	if _, err := s.Authenticate(changer.Session.AccessToken); err != nil {
		t.Fatalf("the changer was refused: %v", err)
	}

	// A wrong PIN is refused, charged, and leaves the gate in place.
	_, err := s.SelectDirectProfile(ctx, token, main.ID, DirectSelection{PIN: wrong, InstallationID: rememberInstallation})
	var wait *ProfilePINError
	if !errors.Is(err, ErrProfilePIN) || !errors.As(err, &wait) || wait.Locked {
		t.Fatalf("a wrong PIN from a gated sign-in: %+v %v", wait, err)
	}
	if attempts := launchResumeAttempts(t, db, main.ID); attempts != 1 {
		t.Fatalf("the wrong PIN was charged %d times", attempts)
	}
	requireGated(t, db, "the sign-in after a wrong PIN", gated.Session.SessionFamilyID)
	// No PIN at all is refused the same way: nothing skips it.
	liftPINDelay(t, db, main.ID)
	if _, err = s.SelectDirectProfile(ctx, token, main.ID, DirectSelection{InstallationID: rememberInstallation}); !errors.Is(err, ErrProfilePIN) {
		t.Fatalf("a gated sign-in chose its profile without a PIN: %v", err)
	}

	// Choosing the same profile with the PIN mints a working, ungated successor.
	liftPINDelay(t, db, main.ID)
	before := liveFamilies(t, db)
	successor, err := s.SelectDirectProfile(ctx, token, main.ID, DirectSelection{PIN: pin, Remember: true, InstallationID: rememberInstallation})
	if err != nil {
		t.Fatalf("entering the PIN again: %v", err)
	}
	if successor.Session.SessionFamilyID == gated.Session.SessionFamilyID || successor.Session.Viewer.ProfileID != main.ID || successor.Session.Viewer.Role != TierOwner {
		t.Fatalf("the successor: %+v", successor.Session.Viewer)
	}
	if successor.Remembered == nil {
		t.Fatal("the successor did not remember the profile")
	}
	requireUngated(t, db, "the successor", successor.Session.SessionFamilyID)
	if after := liveFamilies(t, db); after != before+1 {
		t.Fatalf("entering the PIN minted %d->%d families", before, after)
	}
	if viewer, err := s.Authenticate(successor.Session.AccessToken); err != nil || viewer.ProfileID != main.ID {
		t.Fatalf("the successor does not work: %+v %v", viewer, err)
	}
	if _, err = s.CreateDirectProfile(ctx, successor.Session.AccessToken, "Allowed", "sky"); err != nil {
		t.Fatalf("the successor could not manage after entering the PIN: %v", err)
	}
	// A gated sign-in may also choose a different profile, with that profile's
	// own PIN rules (Kids has none here).
	other := chooseProfile(t, s, token, kids.ID, DirectSelection{})
	requireUngated(t, db, "another profile chosen from a gated sign-in", other.Session.SessionFamilyID)
}

func TestRefreshRotatesAGatedSignInAndItStaysGated(t *testing.T) {
	const pin = "4821"
	ctx := context.Background()
	s, db, login, profiles := switchPINFixture(t, nil)
	kids := profiles[switchKids]
	k := chooseProfile(t, s, login.AccountToken, kids.ID, DirectSelection{Remember: true})
	setProfilePIN(t, s, login.AccountToken, kids.ID, pin)
	requireGated(t, db, "the Kids sign-in", k.Session.SessionFamilyID)

	rotated, err := s.RefreshSession(ctx, k.Session.RefreshToken, k.Session.InstallationID, Token())
	if err != nil {
		t.Fatalf("a gated sign-in could not refresh: %v", err)
	}
	if rotated.SessionFamilyID != k.Session.SessionFamilyID || rotated.AccessToken == k.Session.AccessToken || rotated.RefreshToken == k.Session.RefreshToken {
		t.Fatalf("refresh did not rotate the same family: %+v", rotated)
	}
	if _, err = s.Authenticate(rotated.AccessToken); !errors.Is(err, ErrProfilePINReentry) {
		t.Fatalf("the refreshed gated sign-in is not gated: %v", err)
	}
	requireGated(t, db, "the refreshed family", rotated.SessionFamilyID)
	// The old credential is spent: rotation did not leave the old bearer alive.
	if _, err = s.AuthenticateContext(WithPINReentryRequest(ctx), k.Session.AccessToken); err == nil {
		t.Log("the previous access token still authenticates within its generation grace")
	}
	// It re-enters the PIN from the refreshed credential and is free again.
	out := chooseProfile(t, s, rotated.AccessToken, kids.ID, DirectSelection{PIN: pin})
	if _, err = s.Authenticate(out.Session.AccessToken); err != nil {
		t.Fatalf("the successor of a refreshed gated sign-in: %v", err)
	}
}

func TestRenamingAProfileAndChangingItsPictureOrLibrariesGateNothing(t *testing.T) {
	const pin = "4821"
	s, db, login, profiles := switchPINFixture(t, map[switchProfileKind]string{switchKids: pin})
	kids := profiles[switchKids]
	k1 := chooseProfile(t, s, login.AccountToken, kids.ID, DirectSelection{PIN: pin, Remember: true})
	k2 := chooseProfile(t, s, login.AccountToken, kids.ID, DirectSelection{PIN: pin})
	name, art := "Renamed", "violet"
	edit := func(e DirectProfileEdit) {
		t.Helper()
		e.ExpectedRevision = liveProfile(t, s, login.AccountToken, kids.ID).Revision
		if _, err := s.EditDirectProfile(context.Background(), login.AccountToken, kids.ID, e); err != nil {
			t.Fatal(err)
		}
	}
	edit(DirectProfileEdit{Name: &name})
	edit(DirectProfileEdit{Art: &art})
	edit(DirectProfileEdit{Policy: &ProfilePolicy{AllowedLibraries: []string{}}})
	for label, family := range map[string]string{"remembered": k1.Session.SessionFamilyID, "plain": k2.Session.SessionFamilyID} {
		requireUngated(t, db, label, family)
	}
	if f := familyFlagsOf(t, db, k1.Session.SessionFamilyID); f.remember == "" {
		t.Fatalf("an edit that is not a PIN change forgot a remembered sign-in: %+v", f)
	}
	if _, err := s.Authenticate(k1.Session.AccessToken); err != nil {
		t.Fatalf("the remembered sign-in stopped working after a rename, picture and library edit: %v", err)
	}
}

func TestARememberedMainProfileWithAPINEntersItAgainToManage(t *testing.T) {
	const pin = "4821"
	ctx := context.Background()
	s, db, login, profiles := switchPINFixture(t, map[switchProfileKind]string{switchMain: pin})
	main := profiles[switchMain]
	expire := func(family string) {
		t.Helper()
		past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
		if _, err := db.Exec(`UPDATE authorization_session_families SET manage_until=? WHERE id=?`, past, family); err != nil {
			t.Fatal(err)
		}
	}
	manages := func(token string) error {
		_, err := s.CreateDirectProfile(ctx, token, "Managed", "sky")
		return err
	}

	// Just chosen with the PIN, remembered: management is open for the hour.
	r := chooseProfile(t, s, login.AccountToken, main.ID, DirectSelection{PIN: pin, Remember: true})
	if me, err := s.DirectMe(ctx, r.Session.AccessToken); err != nil || !me.CanManage || me.ManagePINRequired {
		t.Fatalf("a freshly entered PIN: canManage=%v managePinRequired=%v %v", me.CanManage, me.ManagePINRequired, err)
	}
	if err := manages(r.Session.AccessToken); err != nil {
		t.Fatalf("management right after entering the PIN: %v", err)
	}
	// An hour on, it asks again: on the profile routes and the main-profile check.
	expire(r.Session.SessionFamilyID)
	if err := manages(r.Session.AccessToken); !errors.Is(err, ErrProfilePINManage) {
		t.Fatalf("a remembered main profile managed after its hour: %v", err)
	}
	current := liveProfile(t, s, r.Session.AccessToken, main.ID)
	name := "Nope"
	if _, err := s.EditDirectProfile(ctx, r.Session.AccessToken, main.ID, DirectProfileEdit{ExpectedRevision: current.Revision, Name: &name}); !errors.Is(err, ErrProfilePINManage) {
		t.Fatalf("an edit after the hour: %v", err)
	}
	principal, err := s.Authenticate(r.Session.AccessToken)
	if err != nil {
		t.Fatalf("viewing must keep working while management is locked: %v", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = s.RequireMainProfileTx(ctx, tx, principal)
	tx.Rollback()
	if !errors.Is(err, ErrProfilePINManage) {
		t.Fatalf("the main-profile check after the hour: %v", err)
	}
	me, err := s.DirectMe(ctx, r.Session.AccessToken)
	if err != nil || !me.CanManage || !me.ManagePINRequired {
		t.Fatalf("the locked snapshot: canManage=%v managePinRequired=%v %v", me.CanManage, me.ManagePINRequired, err)
	}

	// A fresh choice with the PIN and remember unlocks it again.
	liftPINDelay(t, db, main.ID)
	renewed := chooseProfile(t, s, r.Session.AccessToken, main.ID, DirectSelection{PIN: pin, Remember: true})
	if err = manages(renewed.Session.AccessToken); err != nil {
		t.Fatalf("management after entering the PIN again: %v", err)
	}
	if me, err = s.DirectMe(ctx, renewed.Session.AccessToken); err != nil || me.ManagePINRequired {
		t.Fatalf("the unlocked snapshot: %v %v", me.ManagePINRequired, err)
	}
	tx, err = s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	principal, err = s.Authenticate(renewed.Session.AccessToken)
	if err == nil {
		err = s.RequireMainProfileTx(ctx, tx, principal)
	}
	tx.Rollback()
	if err != nil {
		t.Fatalf("the main-profile check after entering the PIN again: %v", err)
	}

	// A sign-in that did not remember proved the PIN when it was chosen, so it
	// is not locked, however old manage_until is.
	liftPINDelay(t, db, main.ID)
	plain := chooseProfile(t, s, login.AccountToken, main.ID, DirectSelection{PIN: pin})
	expire(plain.Session.SessionFamilyID)
	if err = manages(plain.Session.AccessToken); err != nil {
		t.Fatalf("a sign-in that did not remember was locked: %v", err)
	}
	if me, err = s.DirectMe(ctx, plain.Session.AccessToken); err != nil || me.ManagePINRequired {
		t.Fatalf("snapshot of a sign-in that did not remember: %v %v", me.ManagePINRequired, err)
	}
	// The account sign-in is not a remembered main profile either.
	if me, err = s.DirectMe(ctx, login.AccountToken); err != nil || me.ManagePINRequired {
		t.Fatalf("snapshot of the account sign-in: %v %v", me.ManagePINRequired, err)
	}
	// A remembered sign-in whose remembering has ended is not locked.
	liftPINDelay(t, db, main.ID)
	lapsed := chooseProfile(t, s, login.AccountToken, main.ID, DirectSelection{PIN: pin, Remember: true})
	expire(lapsed.Session.SessionFamilyID)
	if _, err = db.Exec(`UPDATE authorization_session_families SET remember_until=? WHERE id=?`, time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), lapsed.Session.SessionFamilyID); err != nil {
		t.Fatal(err)
	}
	if me, err = s.DirectMe(ctx, lapsed.Session.AccessToken); err != nil || me.ManagePINRequired {
		t.Fatalf("snapshot of a sign-in whose remembering ended: %v %v", me.ManagePINRequired, err)
	}
}

func TestARememberedMainProfileWithoutAPINIsNeverLockedFromManaging(t *testing.T) {
	ctx := context.Background()
	s, db, login, profiles := switchPINFixture(t, nil)
	r := chooseProfile(t, s, login.AccountToken, profiles[switchMain].ID, DirectSelection{Remember: true})
	if f := familyFlagsOf(t, db, r.Session.SessionFamilyID); f.remember == "" {
		t.Fatalf("not remembered: %+v", f)
	}
	if me, err := s.DirectMe(ctx, r.Session.AccessToken); err != nil || me.ManagePINRequired {
		t.Fatalf("a profile with no PIN asked for one: %v %v", me.ManagePINRequired, err)
	}
	if _, err := s.CreateDirectProfile(ctx, r.Session.AccessToken, "Managed", "sky"); err != nil {
		t.Fatalf("a remembered main profile with no PIN could not manage: %v", err)
	}
}

func TestTurningRememberPINForOffClearsEveryRemembering(t *testing.T) {
	const pin = "4821"
	s, db, login, profiles := switchPINFixture(t, map[switchProfileKind]string{switchKids: pin, switchGuest: pin})
	k := chooseProfile(t, s, login.AccountToken, profiles[switchKids].ID, DirectSelection{PIN: pin, Remember: true})
	g := chooseProfile(t, s, login.AccountToken, profiles[switchGuest].ID, DirectSelection{PIN: pin, Remember: true})
	m := chooseProfile(t, s, login.AccountToken, profiles[switchMain].ID, DirectSelection{Remember: true})
	owner := m.Session.AccessToken
	for _, out := range []DirectSelectionResult{k, g, m} {
		if f := familyFlagsOf(t, db, out.Session.SessionFamilyID); f.remember == "" {
			t.Fatalf("setup: %+v", f)
		}
	}
	// A shorter period is not "Off": it keeps what is remembered.
	setRememberDays(t, s, owner, 7)
	if f := familyFlagsOf(t, db, k.Session.SessionFamilyID); f.remember == "" {
		t.Fatal("a shorter period cleared remembering")
	}
	setRememberDays(t, s, owner, 0)
	for label, out := range map[string]DirectSelectionResult{"Kids": k, "Guest": g, "main": m} {
		f := familyFlagsOf(t, db, out.Session.SessionFamilyID)
		if f.remember != "" || f.gated || f.revoked {
			t.Fatalf("%s after turning Remember PIN Off: %+v", label, f)
		}
		if _, err := s.Authenticate(out.Session.AccessToken); err != nil {
			t.Fatalf("%s sign-in stopped working when Remember PIN went Off: %v", label, err)
		}
	}
	if me, err := s.DirectMe(context.Background(), m.Session.AccessToken); err != nil || me.ManagePINRequired {
		t.Fatalf("snapshot after Off: %v %v", me.ManagePINRequired, err)
	}
}
