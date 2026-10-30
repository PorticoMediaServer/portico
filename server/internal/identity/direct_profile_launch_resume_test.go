package identity

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// P2-59 follow-up: a launch is not a switch. A client remembers the profile it
// last used on this installation. Launching again signs the account in without a
// profile choice, and that first choice must re-mint the remembered profile from
// its remembered proof without asking for its PIN, whichever profile it is. An
// actual switch away from a profile being viewed keeps needing the destination's
// own PIN, even when a remembered proof for that destination exists.

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

// rememberProfile is the real remember path: choose the profile with its PIN and
// ask the server for a remembered proof bound to this installation's id.
func rememberProfile(t *testing.T, s *Service, accountToken string, target DirectProfile, pin string) DirectTrust {
	t.Helper()
	selected, err := s.SelectDirectProfile(context.Background(), accountToken, target.ID, DirectSelection{PIN: pin, Trust: true, InstallationID: launchResumeInstallation})
	if err != nil || selected.TrustedSelection == nil {
		t.Fatalf("remembering %s: %+v %v", target.Name, selected.TrustedSelection, err)
	}
	if selected.TrustedSelection.ProfileID != target.ID || selected.TrustedSelection.InstallationID != launchResumeInstallation {
		t.Fatalf("remembered proof is not bound to this profile and installation: %+v", selected.TrustedSelection)
	}
	return *selected.TrustedSelection
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

func TestRememberedProfileResumesFromFreshAccountScopedSignIn(t *testing.T) {
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
			var stored int
			if err := db.QueryRow(`SELECT count(*) FROM direct_profile_trust WHERE token_hash=? AND account_id=? AND profile_id=? AND installation_id=?`, Digest(remembered.Token), login.Account.ID, target.ID, launchResumeInstallation).Scan(&stored); err != nil || stored != 1 {
				t.Fatalf("the remembered proof was not stored once: %d %v", stored, err)
			}
			fresh := freshAccountSignIn(t, s, profiles[switchMain].ID)
			before := liveFamilies(t, db)
			result, err := s.SelectDirectProfile(ctx, fresh.AccountToken, target.ID, DirectSelection{TrustToken: remembered.Token, InstallationID: launchResumeInstallation})
			if err != nil {
				t.Fatalf("the remembered %s profile asked for its PIN again on launch: %v", kind, err)
			}
			want := launchResumeRole(kind)
			if result.Session.Viewer.ProfileID != target.ID || result.Session.Viewer.Role != want {
				t.Fatalf("%s launch landed on %+v, want %s/%s", kind, result.Session.Viewer, target.ID, want)
			}
			if result.Session.SessionFamilyID == "" || result.Session.SessionFamilyID == fresh.Session.SessionFamilyID || result.Session.AccessToken == "" {
				t.Fatalf("%s launch reused the account family or issued no viewing session", kind)
			}
			if after := liveFamilies(t, db); after != before+1 {
				t.Fatalf("%s launch minted %d->%d families", kind, before, after)
			}
			if attempts := launchResumeAttempts(t, db, target.ID); attempts != 0 {
				t.Fatalf("%s launch charged a PIN attempt: %d", kind, attempts)
			}
			if viewer, err := s.Authenticate(result.Session.AccessToken); err != nil || viewer.ProfileID != target.ID || viewer.Role != want {
				t.Fatalf("%s launch did not authenticate: %+v %v", kind, viewer, err)
			}
		})
	}
}

func TestSameProfileTrustReMintAndRefreshStayPINless(t *testing.T) {
	const pin = "4821"
	ctx := context.Background()
	s, db, login, profiles := switchPINFixture(t, map[switchProfileKind]string{switchGuest: pin})
	target := profiles[switchGuest]
	first, err := s.SelectDirectProfile(ctx, login.AccountToken, target.ID, DirectSelection{PIN: pin, Trust: true, InstallationID: launchResumeInstallation})
	if err != nil || first.TrustedSelection == nil {
		t.Fatalf("viewing %s with its PIN: %+v %v", target.Name, first.TrustedSelection, err)
	}
	before := liveFamilies(t, db)
	again, err := s.SelectDirectProfile(ctx, first.Session.AccessToken, target.ID, DirectSelection{TrustToken: first.TrustedSelection.Token, InstallationID: launchResumeInstallation})
	if err != nil {
		t.Fatalf("same-profile trust re-mint asked for the PIN: %v", err)
	}
	if again.Session.Viewer.ProfileID != target.ID || again.Session.Viewer.Role != TierMember {
		t.Fatalf("re-mint landed on %+v", again.Session.Viewer)
	}
	if again.Session.SessionFamilyID == first.Session.SessionFamilyID {
		t.Fatal("re-mint reused the viewing family")
	}
	if after := liveFamilies(t, db); after != before+1 {
		t.Fatalf("re-mint minted %d->%d families", before, after)
	}
	if attempts := launchResumeAttempts(t, db, target.ID); attempts != 0 {
		t.Fatalf("re-mint charged a PIN attempt: %d", attempts)
	}
	refreshed, err := s.RefreshSession(ctx, first.Session.RefreshToken, first.Session.InstallationID, Token())
	if err != nil {
		t.Fatalf("refreshing the viewing session: %v", err)
	}
	if refreshed.Viewer.ProfileID != target.ID || refreshed.Viewer.Role != TierMember {
		t.Fatalf("refresh left the profile it kept: %+v", refreshed.Viewer)
	}
	renewed, err := s.RefreshSession(ctx, again.Session.RefreshToken, again.Session.InstallationID, Token())
	if err != nil || renewed.Viewer.ProfileID != target.ID {
		t.Fatalf("refreshing the re-minted session: %+v %v", renewed.Viewer, err)
	}
}

func TestChangingProfilePINRetiresRememberedProofAndFamilies(t *testing.T) {
	oldPIN, newPIN := "4821", "7153"
	ctx := context.Background()
	s, db, login, profiles := switchPINFixture(t, map[switchProfileKind]string{switchKids: oldPIN})
	target := profiles[switchKids]
	viewing, err := s.SelectDirectProfile(ctx, login.AccountToken, target.ID, DirectSelection{PIN: oldPIN, Trust: true, InstallationID: launchResumeInstallation})
	if err != nil || viewing.TrustedSelection == nil {
		t.Fatalf("remembering %s: %+v %v", target.Name, viewing.TrustedSelection, err)
	}
	oldTrust := *viewing.TrustedSelection
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
	if _, err = s.Authenticate(viewing.Session.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("the old viewing family survived the PIN change: %v", err)
	}
	if _, err = s.RefreshSession(ctx, rotated.RefreshToken, rotated.InstallationID, Token()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("the old family's refresh credential survived the PIN change: %v", err)
	}
	fresh := freshAccountSignIn(t, s, profiles[switchMain].ID)
	before := liveFamilies(t, db)
	if result, err := s.SelectDirectProfile(ctx, fresh.AccountToken, target.ID, DirectSelection{TrustToken: oldTrust.Token, InstallationID: launchResumeInstallation}); !errors.Is(err, ErrProfilePIN) {
		t.Fatalf("the old remembered proof resumed %s after its PIN changed: %+v %v", target.Name, result.Session, err)
	} else {
		var wait *ProfilePINError
		if !errors.As(err, &wait) || wait.Locked || wait.RetryAfterSeconds < 1 {
			t.Fatalf("the retired proof did not ask for the PIN: %+v", wait)
		}
	}
	if after := liveFamilies(t, db); after != before {
		t.Fatalf("a refused resume minted %d->%d families", before, after)
	}
	if attempts := launchResumeAttempts(t, db, target.ID); attempts != 1 {
		t.Fatalf("the refused resume was not charged once: %d", attempts)
	}
	// The retry delay is real; let the same launch try again after it elapses.
	if _, err = db.Exec(`UPDATE direct_profile_pin_attempts SET locked_until_ms=0 WHERE profile_id=?`, target.ID); err != nil {
		t.Fatal(err)
	}
	selected, err := s.SelectDirectProfile(ctx, fresh.AccountToken, target.ID, DirectSelection{PIN: newPIN, Trust: true, InstallationID: launchResumeInstallation})
	if err != nil || selected.TrustedSelection == nil {
		t.Fatalf("the new %s PIN did not open it: %+v %v", target.Name, selected.TrustedSelection, err)
	}
	if selected.Session.Viewer.ProfileID != target.ID || selected.Session.Viewer.Role != TierMember {
		t.Fatalf("the new PIN landed on %+v", selected.Session.Viewer)
	}
	if after := liveFamilies(t, db); after != before+1 {
		t.Fatalf("the new PIN minted %d->%d families", before, after)
	}
	relaunch := freshAccountSignIn(t, s, profiles[switchMain].ID)
	resumed, err := s.SelectDirectProfile(ctx, relaunch.AccountToken, target.ID, DirectSelection{TrustToken: selected.TrustedSelection.Token, InstallationID: launchResumeInstallation})
	if err != nil {
		t.Fatalf("the proof remembered with the new PIN did not resume on launch: %v", err)
	}
	if resumed.Session.Viewer.ProfileID != target.ID || resumed.Session.Viewer.Role != TierMember {
		t.Fatalf("the new remembered proof resumed as %+v", resumed.Session.Viewer)
	}
}

func TestActualSwitchToProtectedProfileNeedsItsPINEvenWithRememberedProof(t *testing.T) {
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
			remembered := rememberProfile(t, s, login.AccountToken, target, pin)
			source, family := switchSourceSession(t, s, login, pair.from, profiles, nil)
			if viewer, err := s.Authenticate(source); err != nil || viewer.ProfileID != profiles[pair.from].ID {
				t.Fatalf("the %s source is not really viewing it: %+v %v", pair.from, viewer, err)
			}
			before := liveFamilies(t, db)
			result, err := s.SelectDirectProfile(ctx, source, target.ID, DirectSelection{TrustToken: remembered.Token, InstallationID: launchResumeInstallation})
			var wait *ProfilePINError
			if !errors.Is(err, ErrProfilePIN) || !errors.As(err, &wait) || wait.Locked || wait.RetryAfterSeconds < 1 {
				t.Fatalf("the %s switch used a remembered proof instead of its own PIN: %+v %v", pair.to, wait, err)
			}
			if result.Session.AccessToken != "" || result.Session.SessionFamilyID != "" {
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
			switched, err := s.SelectDirectProfile(ctx, source, target.ID, DirectSelection{PIN: pin, Trust: true, InstallationID: launchResumeInstallation})
			if err != nil || switched.TrustedSelection == nil {
				t.Fatalf("the %s switch refused its own PIN: %+v %v", pair.to, switched.TrustedSelection, err)
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
			relaunch := freshAccountSignIn(t, s, profiles[switchMain].ID)
			resumed, err := s.SelectDirectProfile(ctx, relaunch.AccountToken, target.ID, DirectSelection{TrustToken: switched.TrustedSelection.Token, InstallationID: launchResumeInstallation})
			if err != nil {
				t.Fatalf("the proof remembered by the switch did not resume on launch: %v", err)
			}
			if resumed.Session.Viewer.ProfileID != target.ID || resumed.Session.Viewer.Role != want {
				t.Fatalf("the switch's remembered proof resumed as %+v", resumed.Session.Viewer)
			}
		})
	}
}
