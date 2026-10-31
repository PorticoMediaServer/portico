package identity

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// P2-59: choosing any profile is governed by the destination's own PIN.
// Remembering a profile is the device keeping its sign-in; it never spares the
// PIN of a choice, whether the destination is the main profile or an ordinary
// Guest/Kids one. Every source session here is a real viewing session: an
// account-scoped sign-in is a launch, not a source.

type switchProfileKind int

const (
	switchMain switchProfileKind = iota
	switchGuest
	switchKids
)

func (p switchProfileKind) String() string {
	switch p {
	case switchMain:
		return "Main"
	case switchGuest:
		return "Guest"
	default:
		return "Kids"
	}
}

const switchInstallation = "switch-installation"

func switchNamedProfile(t *testing.T, snapshot DirectSnapshot, name string) DirectProfile {
	t.Helper()
	for _, p := range snapshot.Profiles {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("profile %q missing", name)
	return DirectProfile{}
}

// switchPINFixture builds three real profiles through the profile API, protects
// only the profiles named in pins through the real edit path, and signs in again
// so the account-scoped family sits on the main profile. No fixture asks for a
// session before the PINs are set.
func switchPINFixture(t *testing.T, pins map[switchProfileKind]string) (*Service, *sql.DB, DirectSignIn, map[switchProfileKind]DirectProfile) {
	t.Helper()
	ctx := context.Background()
	s, db, owner := directFixture(t)
	guestSnapshot, err := s.CreateDirectProfile(ctx, owner.AccountToken, "Guest", "coral")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.CreateDirectProfile(ctx, owner.AccountToken, "Kids", "sky")
	if err != nil {
		t.Fatal(err)
	}
	profiles := map[switchProfileKind]DirectProfile{
		switchMain:  primaryProfile(t, snapshot.Profiles),
		switchGuest: switchNamedProfile(t, guestSnapshot, "Guest"),
		switchKids:  switchNamedProfile(t, snapshot, "Kids"),
	}
	for _, kind := range []switchProfileKind{switchGuest, switchKids, switchMain} {
		pin, protected := pins[kind]
		if !protected {
			continue
		}
		if _, err = s.EditDirectProfile(ctx, owner.AccountToken, profiles[kind].ID, DirectProfileEdit{ExpectedRevision: profiles[kind].Revision, PIN: &pin}); err != nil {
			t.Fatalf("protecting %s: %v", kind, err)
		}
	}
	login, err := s.DirectLogin(ctx, "owner", "Testing1!")
	if err != nil {
		t.Fatal(err)
	}
	if login.Session == nil || login.Session.Viewer.Role != "account" || login.Session.Viewer.ProfileID != profiles[switchMain].ID {
		t.Fatalf("account-scoped sign-in: %+v", login.Session)
	}
	return s, db, login, profiles
}

// switchSourceSession returns a session that is really viewing `from`. The
// fixture's account-scoped sign-in is not a viewing session at all: its profile
// id is the primary placeholder, so even the main profile is selected through
// the real selection path first. Only the destination is protected in this
// table, so the source never needs a PIN.
func switchSourceSession(t *testing.T, s *Service, login DirectSignIn, from switchProfileKind, profiles map[switchProfileKind]DirectProfile, pins map[switchProfileKind]string) (string, string) {
	t.Helper()
	q := DirectSelection{}
	if pin, protected := pins[from]; protected {
		q.PIN = pin
	}
	selected, err := s.SelectDirectProfile(context.Background(), login.AccountToken, profiles[from].ID, q)
	if err != nil {
		t.Fatalf("selecting the %s source: %v", from, err)
	}
	if selected.Session.Viewer.ProfileID != profiles[from].ID {
		t.Fatalf("the %s source session is on %q", from, selected.Session.Viewer.ProfileID)
	}
	return selected.Session.AccessToken, selected.Session.SessionFamilyID
}

func TestDirectProfileSwitchRequiresTheDestinationPIN(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

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

			t.Run("unprotectedDestinationNeedsNoPIN", func(t *testing.T) {
				s, db, login, profiles := switchPINFixture(t, nil)
				source, family := switchSourceSession(t, s, login, pair.from, profiles, nil)
				before := liveFamilies(t, db)
				result, err := s.SelectDirectProfile(ctx, source, profiles[pair.to].ID, DirectSelection{})
				if err != nil {
					t.Fatalf("an unprotected %s destination refused the switch: %v", pair.to, err)
				}
				if result.Session.Viewer.ProfileID != profiles[pair.to].ID {
					t.Fatalf("switch landed on %q", result.Session.Viewer.ProfileID)
				}
				want := TierMember
				if pair.to == switchMain {
					want = TierOwner
				}
				if result.Session.Viewer.Role != want {
					t.Fatalf("%s destination took role %q, want %q", pair.to, result.Session.Viewer.Role, want)
				}
				if result.Session.SessionFamilyID == family || result.Session.AccessToken == "" {
					t.Fatal("the switch reused the source family or issued no bearer")
				}
				if after := liveFamilies(t, db); after != before+1 {
					t.Fatalf("family minted %d->%d", before, after)
				}
				if viewer, err := s.Authenticate(result.Session.AccessToken); err != nil || viewer.ProfileID != profiles[pair.to].ID || viewer.Role != want {
					t.Fatalf("%s destination did not authenticate: %+v %v", pair.to, viewer, err)
				}
			})

			const targetPIN = "4821"
			pin := map[switchProfileKind]string{pair.to: targetPIN}
			wrong := "0000"

			for _, variant := range []struct {
				name       string
				remembered bool
				choose     DirectSelection
				refused    bool
			}{
				{"missingPINRefused", false, DirectSelection{}, true},
				{"wrongPINRefused", false, DirectSelection{PIN: wrong}, true},
				{"rememberedDestinationStillNeedsThePIN", true, DirectSelection{InstallationID: switchInstallation, Remember: true}, true},
				{"correctPINIssuesSession", false, DirectSelection{PIN: targetPIN}, false},
			} {
				t.Run("protectedDestination/"+variant.name, func(t *testing.T) {
					s, db, login, profiles := switchPINFixture(t, pin)
					target := profiles[pair.to]
					if variant.remembered {
						// A real, live remembered sign-in on this destination,
						// obtained through the legitimate path: the destination's
						// own PIN with remember.
						seed := DirectSelection{Remember: true, InstallationID: switchInstallation, PIN: targetPIN}
						seeded, err := s.SelectDirectProfile(ctx, login.AccountToken, target.ID, seed)
						if err != nil || seeded.Remembered == nil {
							t.Fatalf("preparing a remembered destination: %+v %v", seeded.Remembered, err)
						}
					}
					source, family := switchSourceSession(t, s, login, pair.from, profiles, nil)
					before := liveFamilies(t, db)
					result, err := s.SelectDirectProfile(ctx, source, target.ID, variant.choose)
					if variant.refused {
						var wait *ProfilePINError
						if !errors.Is(err, ErrProfilePIN) || !errors.As(err, &wait) {
							t.Fatalf("%s destination opened without its own PIN: %+v %v", pair.to, wait, err)
						}
						if result.Session.AccessToken != "" || result.Session.SessionFamilyID != "" {
							t.Fatal("a refused selection issued a session")
						}
						if after := liveFamilies(t, db); after != before {
							t.Fatalf("a refused selection minted %d->%d families", before, after)
						}
						var charged int
						if err := db.QueryRow(`SELECT COALESCE(sum(failed_attempts),0) FROM direct_profile_pin_attempts WHERE profile_id=?`, target.ID).Scan(&charged); err != nil || charged != 1 {
							t.Fatalf("the refused attempt was not charged once: %d %v", charged, err)
						}
						if viewer, err := s.Authenticate(source); err != nil || viewer.ProfileID != profiles[pair.from].ID {
							t.Fatalf("the %s source session changed: %+v %v", pair.from, viewer, err)
						}
						return
					}
					if err != nil {
						t.Fatalf("%s destination refused its correct PIN: %v", pair.to, err)
					}
					if result.Session.Viewer.ProfileID != target.ID {
						t.Fatalf("switch landed on %q", result.Session.Viewer.ProfileID)
					}
					want := TierMember
					if pair.to == switchMain {
						want = TierOwner
					}
					if result.Session.Viewer.Role != want {
						t.Fatalf("%s destination took role %q, want %q", pair.to, result.Session.Viewer.Role, want)
					}
					if result.Session.SessionFamilyID == family || result.Session.AccessToken == "" {
						t.Fatal("the switch reused the source family or issued no bearer")
					}
					if after := liveFamilies(t, db); after != before+1 {
						t.Fatalf("family minted %d->%d", before, after)
					}
					if viewer, err := s.Authenticate(result.Session.AccessToken); err != nil || viewer.ProfileID != target.ID || viewer.Role != want {
						t.Fatalf("%s destination did not authenticate: %+v %v", pair.to, viewer, err)
					}
				})
			}
		})
	}
}
