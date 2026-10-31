package identity

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// P2-59 follow-up: remembering a profile is the device keeping its sign-in, not
// a proof that skips the PIN. A remembered family resumes on launch because the
// device still holds it (and refreshes without a PIN). A fresh account-scoped
// sign-in, and any other choice of a profile with a PIN, proves that PIN.

const launchResumeInstallation = "launch-resume-installation"

func launchResumeRole(kind switchProfileKind) string {
	if kind == switchMain {
		return TierOwner
	}
	return TierMember
}

func launchResumeProfile(t *testing.T, snapshot DirectSnapshot, id string) DirectProfile {
	t.Helper()
	for _, p := range snapshot.Profiles {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("profile %q missing", id)
	return DirectProfile{}
}

func launchResumeAttempts(t *testing.T, db *sql.DB, profile string) int {
	t.Helper()
	var attempts int
	if err := db.QueryRow(`SELECT COALESCE(sum(failed_attempts),0) FROM direct_profile_pin_attempts WHERE profile_id=?`, profile).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	return attempts
}

// rememberProfile is the real remember path: choose the profile with its PIN
// and ask the server to remember it for this installation.
func rememberProfile(t *testing.T, s *Service, accountToken string, target DirectProfile, pin string) DirectSelectionResult {
	t.Helper()
	selected, err := s.SelectDirectProfile(context.Background(), accountToken, target.ID, DirectSelection{PIN: pin, Remember: true, InstallationID: launchResumeInstallation})
	if err != nil || selected.Remembered == nil {
		t.Fatalf("remembering %s: %+v %v", target.Name, selected.Remembered, err)
	}
	return selected
}

// freshAccountSignIn is a client launching again: the account is proved on this
// device, and no profile has been chosen yet. The profile id it carries is the
// primary profile's placeholder, not a viewing selection.
func freshAccountSignIn(t *testing.T, s *Service, placeholder string) DirectSignIn {
	t.Helper()
	login, err := s.DirectLogin(context.Background(), "owner", "Testing1!")
	if err != nil {
		t.Fatal(err)
	}
	if login.Session == nil || login.Session.Viewer.Role != "account" || login.Session.Viewer.ProfileID != placeholder || login.AccountToken == "" {
		t.Fatalf("sign-in was not account-scoped on the placeholder profile: %+v", login.Session)
	}
	return login
}

func TestRememberedProfileKeepsItsFamilyButAFreshSignInNeedsThePIN(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	const pin = "4821"
	for _, kind := range []switchProfileKind{switchMain, switchGuest, switchKids} {
		t.Run(kind.String(), func(t *testing.T) {
			ctx := context.Background()
			s, db, login, profiles := switchPINFixture(t, map[switchProfileKind]string{kind: pin})
			target := profiles[kind]
			remembered := rememberProfile(t, s, login.AccountToken, target, pin)
			want := launchResumeRole(kind)
			// The device resumes by holding the family: it authenticates and
			// refreshes with no PIN and no attempt charged.
			if viewer, err := s.Authenticate(remembered.Session.AccessToken); err != nil || viewer.ProfileID != target.ID || viewer.Role != want {
				t.Fatalf("%s remembered family did not authenticate: %+v %v", kind, viewer, err)
			}
			refreshed, err := s.RefreshSession(ctx, remembered.Session.RefreshToken, remembered.Session.InstallationID, Token())
			if err != nil || refreshed.Viewer.ProfileID != target.ID || refreshed.Viewer.Role != want {
				t.Fatalf("%s remembered family did not refresh: %+v %v", kind, refreshed.Viewer, err)
			}
			if attempts := launchResumeAttempts(t, db, target.ID); attempts != 0 {
				t.Fatalf("%s resume charged a PIN attempt: %d", kind, attempts)
			}
			// A fresh account-scoped sign-in has no family to resume: choosing the
			// profile needs its PIN, whatever was remembered before.
			fresh := freshAccountSignIn(t, s, profiles[switchMain].ID)
			before := liveFamilies(t, db)
			result, err := s.SelectDirectProfile(ctx, fresh.AccountToken, target.ID, DirectSelection{InstallationID: launchResumeInstallation})
			var wait *ProfilePINError
			if !errors.Is(err, ErrProfilePIN) || !errors.As(err, &wait) || wait.Locked {
				t.Fatalf("%s launch without its PIN: %+v %v", kind, wait, err)
			}
			if result.Session.AccessToken != "" || liveFamilies(t, db) != before {
				t.Fatalf("%s refused launch issued a session", kind)
			}
			if attempts := launchResumeAttempts(t, db, target.ID); attempts != 1 {
				t.Fatalf("%s refused launch was not charged once: %d", kind, attempts)
			}
			if _, err = db.Exec(`UPDATE direct_profile_pin_attempts SET locked_until_ms=0 WHERE profile_id=?`, target.ID); err != nil {
				t.Fatal(err)
			}
			result, err = s.SelectDirectProfile(ctx, fresh.AccountToken, target.ID, DirectSelection{PIN: pin, InstallationID: launchResumeInstallation})
			if err != nil || result.Session.Viewer.ProfileID != target.ID || result.Session.Viewer.Role != want {
				t.Fatalf("%s launch with its PIN: %+v %v", kind, result.Session.Viewer, err)
			}
			if result.Remembered != nil {
				t.Fatalf("%s launch remembered without being asked", kind)
			}
		})
	}
}

func TestSameProfileReselectNeedsThePINButRefreshStaysPINless(t *testing.T) {
	const pin = "4821"
	ctx := context.Background()
	s, db, login, profiles := switchPINFixture(t, map[switchProfileKind]string{switchGuest: pin})
	target := profiles[switchGuest]
	first := rememberProfile(t, s, login.AccountToken, target, pin)
	before := liveFamilies(t, db)
	result, err := s.SelectDirectProfile(ctx, first.Session.AccessToken, target.ID, DirectSelection{InstallationID: launchResumeInstallation, Remember: true})
	var wait *ProfilePINError
	if !errors.Is(err, ErrProfilePIN) || !errors.As(err, &wait) || wait.Locked {
		t.Fatalf("a same-profile reselect without the PIN: %+v %v", wait, err)
	}
	if result.Session.AccessToken != "" || liveFamilies(t, db) != before {
		t.Fatal("a refused same-profile reselect issued a session")
	}
	if attempts := launchResumeAttempts(t, db, target.ID); attempts != 1 {
		t.Fatalf("the refused reselect was not charged once: %d", attempts)
	}
	if _, err = db.Exec(`UPDATE direct_profile_pin_attempts SET locked_until_ms=0 WHERE profile_id=?`, target.ID); err != nil {
		t.Fatal(err)
	}
	again, err := s.SelectDirectProfile(ctx, first.Session.AccessToken, target.ID, DirectSelection{PIN: pin, InstallationID: launchResumeInstallation, Remember: true})
	if err != nil || again.Remembered == nil || again.Session.SessionFamilyID == first.Session.SessionFamilyID {
		t.Fatalf("the same profile with its PIN: %+v %v", again, err)
	}
	if again.Session.Viewer.ProfileID != target.ID || again.Session.Viewer.Role != TierMember {
		t.Fatalf("reselect landed on %+v", again.Session.Viewer)
	}
	renewed, err := s.RefreshSession(ctx, again.Session.RefreshToken, again.Session.InstallationID, Token())
	if err != nil || renewed.Viewer.ProfileID != target.ID {
		t.Fatalf("refreshing the reselected session: %+v %v", renewed.Viewer, err)
	}
}

func TestChangingProfilePINMakesTheOldSignInsAskForItAndTheNewPINOpensIt(t *testing.T) {
	oldPIN, newPIN := "4821", "7153"
	ctx := context.Background()
	s, db, login, profiles := switchPINFixture(t, map[switchProfileKind]string{switchKids: oldPIN})
	target := profiles[switchKids]
	viewing := rememberProfile(t, s, login.AccountToken, target, oldPIN)
	rotated, err := s.RefreshSession(ctx, viewing.Session.RefreshToken, viewing.Session.InstallationID, Token())
	if err != nil {
		t.Fatalf("the remembered family could not refresh before the PIN change: %v", err)
	}
	snapshot, err := s.DirectMe(ctx, login.AccountToken)
	if err != nil {
		t.Fatal(err)
	}
	current := launchResumeProfile(t, snapshot, target.ID)
	if _, err = s.EditDirectProfile(ctx, login.AccountToken, target.ID, DirectProfileEdit{ExpectedRevision: current.Revision, PIN: &newPIN}); err != nil {
		t.Fatalf("changing the %s PIN: %v", target.Name, err)
	}
	if _, err = s.Authenticate(viewing.Session.AccessToken); err == nil {
		t.Fatal("the old viewing family still works after the PIN change")
	}
	if _, err = s.Authenticate(rotated.AccessToken); err == nil {
		t.Fatal("the refreshed old viewing family still works after the PIN change")
	}
	fresh := freshAccountSignIn(t, s, profiles[switchMain].ID)
	before := liveFamilies(t, db)
	if result, err := s.SelectDirectProfile(ctx, fresh.AccountToken, target.ID, DirectSelection{PIN: oldPIN, InstallationID: launchResumeInstallation}); !errors.Is(err, ErrProfilePIN) {
		t.Fatalf("the old PIN opened %s after it changed: %+v %v", target.Name, result.Session, err)
	} else {
		var wait *ProfilePINError
		if !errors.As(err, &wait) || wait.Locked || wait.RetryAfterSeconds < 1 {
			t.Fatalf("the old PIN was not charged as a miss: %+v", wait)
		}
	}
	if after := liveFamilies(t, db); after != before {
		t.Fatalf("a refused choice minted %d->%d families", before, after)
	}
	if attempts := launchResumeAttempts(t, db, target.ID); attempts != 1 {
		t.Fatalf("the refused choice was not charged once: %d", attempts)
	}
	// The retry delay is real; let the same launch try again after it elapses.
	if _, err = db.Exec(`UPDATE direct_profile_pin_attempts SET locked_until_ms=0 WHERE profile_id=?`, target.ID); err != nil {
		t.Fatal(err)
	}
	selected, err := s.SelectDirectProfile(ctx, fresh.AccountToken, target.ID, DirectSelection{PIN: newPIN, Remember: true, InstallationID: launchResumeInstallation})
	if err != nil || selected.Remembered == nil {
		t.Fatalf("the new %s PIN did not open it: %+v %v", target.Name, selected.Remembered, err)
	}
	if selected.Session.Viewer.ProfileID != target.ID || selected.Session.Viewer.Role != TierMember {
		t.Fatalf("the new PIN landed on %+v", selected.Session.Viewer)
	}
	if after := liveFamilies(t, db); after != before+1 {
		t.Fatalf("the new PIN minted %d->%d families", before, after)
	}
	if viewer, err := s.Authenticate(selected.Session.AccessToken); err != nil || viewer.ProfileID != target.ID {
		t.Fatalf("the family minted by the new PIN does not work: %+v %v", viewer, err)
	}
}

func TestActualSwitchToProtectedProfileNeedsItsPINEvenWithARememberedFamily(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	const pin = "4821"
	for _, pair := range []struct {
		from, to switchProfileKind
	}{
		{switchMain, switchGuest},
		{switchMain, switchKids},
		{switchGuest, switchMain},
		{switchGuest, switchKids},
		{switchKids, switchMain},
		{switchKids, switchGuest},
	} {
		t.Run(pair.from.String()+"To"+pair.to.String(), func(t *testing.T) {
			ctx := context.Background()
			s, db, login, profiles := switchPINFixture(t, map[switchProfileKind]string{pair.to: pin})
			target := profiles[pair.to]
			// The destination is remembered on this installation already.
			rememberProfile(t, s, login.AccountToken, target, pin)
			source, family := switchSourceSession(t, s, login, pair.from, profiles, nil)
			if viewer, err := s.Authenticate(source); err != nil || viewer.ProfileID != profiles[pair.from].ID {
				t.Fatalf("the %s source is not really viewing it: %+v %v", pair.from, viewer, err)
			}
			before := liveFamilies(t, db)
			result, err := s.SelectDirectProfile(ctx, source, target.ID, DirectSelection{InstallationID: launchResumeInstallation, Remember: true})
			var wait *ProfilePINError
			if !errors.Is(err, ErrProfilePIN) || !errors.As(err, &wait) || wait.Locked || wait.RetryAfterSeconds < 1 {
				t.Fatalf("the %s switch skipped its own PIN because it was remembered: %+v %v", pair.to, wait, err)
			}
			if result.Session.AccessToken != "" || result.Session.SessionFamilyID != "" || result.Remembered != nil {
				t.Fatal("a refused switch issued a session")
			}
			if after := liveFamilies(t, db); after != before {
				t.Fatalf("a refused switch minted %d->%d families", before, after)
			}
			if attempts := launchResumeAttempts(t, db, target.ID); attempts != 1 {
				t.Fatalf("the refused switch was not charged once: %d", attempts)
			}
			if viewer, err := s.Authenticate(source); err != nil || viewer.ProfileID != profiles[pair.from].ID {
				t.Fatalf("the %s source session changed: %+v %v", pair.from, viewer, err)
			}
			// The retry delay is real; let the same session try again after it elapses.
			if _, err = db.Exec(`UPDATE direct_profile_pin_attempts SET locked_until_ms=0 WHERE profile_id=?`, target.ID); err != nil {
				t.Fatal(err)
			}
			switched, err := s.SelectDirectProfile(ctx, source, target.ID, DirectSelection{PIN: pin, Remember: true, InstallationID: launchResumeInstallation})
			if err != nil || switched.Remembered == nil {
				t.Fatalf("the %s switch refused its own PIN: %+v %v", pair.to, switched.Remembered, err)
			}
			want := launchResumeRole(pair.to)
			if switched.Session.Viewer.ProfileID != target.ID || switched.Session.Viewer.Role != want {
				t.Fatalf("the %s switch landed on %+v", pair.to, switched.Session.Viewer)
			}
			if switched.Session.SessionFamilyID == family {
				t.Fatal("the switch reused the source family")
			}
			if after := liveFamilies(t, db); after != before+1 {
				t.Fatalf("the switch minted %d->%d families", before, after)
			}
			if viewer, err := s.Authenticate(switched.Session.AccessToken); err != nil || viewer.ProfileID != target.ID || viewer.Role != want {
				t.Fatalf("the family the switch minted does not work: %+v %v", viewer, err)
			}
		})
	}
}
