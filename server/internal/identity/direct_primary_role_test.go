package identity

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// E-web-24: returning from a Guest or Kids profile session to the account's
// main profile must issue the account's current membership role. The caller
// session's narrowed role is not the account's role, and it used to leak
// "member" into the main profile's session until the next sign-in.

func liveFamilies(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM authorization_session_families WHERE revoked=0`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func primaryProfile(t *testing.T, profiles []DirectProfile) DirectProfile {
	t.Helper()
	for _, p := range profiles {
		if p.Primary {
			return p
		}
	}
	t.Fatal("primary profile missing")
	return DirectProfile{}
}

// viewingProfileFixture signs the owner in and adds one viewing profile
// through the real profile API, then selects it. "Guest" and "Kids" are the
// product's labels for ordinary non-primary profiles.
func viewingProfileFixture(t *testing.T, name string) (*Service, *sql.DB, DirectSignIn, DirectProfile, DirectSelectionResult) {
	t.Helper()
	ctx := context.Background()
	s, db, owner := directFixture(t)
	snapshot, err := s.CreateDirectProfile(ctx, owner.AccountToken, name, "coral")
	if err != nil {
		t.Fatal(err)
	}
	profile := childProfile(t, snapshot)
	selected, err := s.SelectDirectProfile(ctx, owner.AccountToken, profile.ID, DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Session.Viewer.Role != TierMember || selected.Session.Viewer.ProfileID != profile.ID {
		t.Fatalf("%s selection: %+v", name, selected.Session.Viewer)
	}
	return s, db, owner, profile, selected
}

const mainInstallation = "main-installation"

// protectedMainFixture sets a PIN on the main profile through the real edit
// path, which retires the owner's existing family as a new credential, signs in
// again, and remembers the main profile on one real installation. It then
// selects the viewing profile, so the caller is a member-scoped Guest or Kids
// session while a valid remembered main token exists for the same device.
func protectedMainFixture(t *testing.T, name, pin string) (*Service, *sql.DB, DirectSignIn, string, DirectTrust, DirectSelectionResult) {
	t.Helper()
	ctx := context.Background()
	s, db, owner := directFixture(t)
	snapshot, err := s.CreateDirectProfile(ctx, owner.AccountToken, name, "coral")
	if err != nil {
		t.Fatal(err)
	}
	profile := childProfile(t, snapshot)
	primary := primaryProfile(t, snapshot.Profiles)
	if _, err = s.EditDirectProfile(ctx, owner.AccountToken, primary.ID, DirectProfileEdit{ExpectedRevision: primary.Revision, PIN: &pin}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(owner.AccountToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("the owner's old session survived the new main PIN: %v", err)
	}
	login, err := s.DirectLogin(ctx, "owner", "Testing1!")
	if err != nil || login.Session == nil || login.Session.Viewer.Role != "account" {
		t.Fatalf("protected main sign-in: %+v %v", login.Session, err)
	}
	main, err := s.SelectDirectProfile(ctx, login.AccountToken, primary.ID, DirectSelection{PIN: pin, Trust: true, InstallationID: mainInstallation})
	if err != nil || main.TrustedSelection == nil || main.Session.Viewer.Role != TierOwner {
		t.Fatalf("main selection with its PIN: %+v %v", main, err)
	}
	selected, err := s.SelectDirectProfile(ctx, login.AccountToken, profile.ID, DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Session.Viewer.Role != TierMember {
		t.Fatalf("%s selection: %+v", name, selected.Session.Viewer)
	}
	return s, db, login, primary.ID, *main.TrustedSelection, selected
}

func TestSelectingNonPrimaryProfileIssuesMemberRole(t *testing.T) {
	for _, name := range []string{"Guest", "Kids"} {
		t.Run(name, func(t *testing.T) {
			s, _, _, profile, selected := viewingProfileFixture(t, name)
			ctx := context.Background()
			viewer, err := s.Authenticate(selected.Session.AccessToken)
			if err != nil || viewer.Role != TierMember || viewer.ProfileID != profile.ID {
				t.Fatalf("%s viewer: %+v %v", name, viewer, err)
			}
			me, err := s.DirectMe(ctx, selected.Session.AccessToken)
			if err != nil || me.CanManage || me.Capabilities != (ManagementCapabilities{}) {
				t.Fatalf("%s management projection: %+v %v", name, me, err)
			}
			if _, err = s.CreateDirectProfile(ctx, selected.Session.AccessToken, "Forbidden", "sky"); !errors.Is(err, ErrForbidden) {
				t.Fatalf("%s managed profiles: %v", name, err)
			}
		})
	}
}

// Both non-primary labels take the same rule: a main profile with no PIN opens
// without one, and the session it mints takes the membership's role rather than
// the narrowed caller's.
func TestGuestToPrimaryWithoutPINIssuesOwner(t *testing.T) {
	for _, name := range []string{"Guest", "Kids"} {
		t.Run(name, func(t *testing.T) {
			s, db, owner, _, onViewer := viewingProfileFixture(t, name)
			ctx := context.Background()
			primary := owner.Account.PrimaryProfileID
			before := liveFamilies(t, db)
			back, err := s.SelectDirectProfile(ctx, onViewer.Session.AccessToken, primary, DirectSelection{})
			if err != nil {
				t.Fatal(err)
			}
			if back.Session.Viewer.Role != TierOwner || back.Session.Viewer.ProfileID != primary {
				t.Fatalf("main profile from %s: %+v", name, back.Session.Viewer)
			}
			if back.Session.SessionFamilyID == onViewer.Session.SessionFamilyID || back.Session.AccessToken == onViewer.Session.AccessToken {
				t.Fatalf("%s selection reused the caller's family or bearer", name)
			}
			if after := liveFamilies(t, db); after != before+1 {
				t.Fatalf("%s selection minted %d->%d families", name, before, after)
			}
			viewer, err := s.Authenticate(back.Session.AccessToken)
			if err != nil || viewer.Role != TierOwner {
				t.Fatalf("%s main profile did not authenticate as owner: %+v %v", name, viewer, err)
			}
			if viewerCaller, err := s.Authenticate(onViewer.Session.AccessToken); err != nil || viewerCaller.Role != TierMember {
				t.Fatalf("the %s session changed: %+v %v", name, viewerCaller, err)
			}
		})
	}
}

func TestGuestToPrimaryCorrectPINIssuesOwner(t *testing.T) {
	const pin = "4821"
	s, db, _, primary, _, onGuest := protectedMainFixture(t, "Guest", pin)
	ctx := context.Background()
	before := liveFamilies(t, db)
	back, err := s.SelectDirectProfile(ctx, onGuest.Session.AccessToken, primary, DirectSelection{PIN: pin})
	if err != nil {
		t.Fatal(err)
	}
	if back.Session.Viewer.Role != TierOwner || back.Session.Viewer.ProfileID != primary {
		t.Fatalf("main profile after its PIN: %+v", back.Session.Viewer)
	}
	if back.Session.SessionFamilyID == onGuest.Session.SessionFamilyID {
		t.Fatal("selection reused the caller's family")
	}
	if after := liveFamilies(t, db); after != before+1 {
		t.Fatalf("family minted %d->%d", before, after)
	}
	if viewer, err := s.Authenticate(back.Session.AccessToken); err != nil || viewer.Role != TierOwner {
		t.Fatalf("main profile did not authenticate as owner: %+v %v", viewer, err)
	}
	if guestViewer, err := s.Authenticate(onGuest.Session.AccessToken); err != nil || guestViewer.Role != TierMember {
		t.Fatalf("the Guest session changed: %+v %v", guestViewer, err)
	}
}

func TestGuestToPrimaryRefusesMissingOrRememberedProofWithoutMintingFamily(t *testing.T) {
	const pin = "4821"
	for _, tc := range []struct {
		name      string
		selection func(main DirectTrust) DirectSelection
	}{
		{"no proof", func(DirectTrust) DirectSelection { return DirectSelection{} }},
		{"remembered main trust", func(main DirectTrust) DirectSelection {
			return DirectSelection{TrustToken: main.Token, InstallationID: mainInstallation}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db, _, primary, main, onGuest := protectedMainFixture(t, "Guest", pin)
			ctx := context.Background()
			before := liveFamilies(t, db)
			result, err := s.SelectDirectProfile(ctx, onGuest.Session.AccessToken, primary, tc.selection(main))
			// No fresh PIN was proved for the main profile. The existing
			// contract charges the attempt and asks for the profile's PIN.
			var wait *ProfilePINError
			if !errors.Is(err, ErrProfilePIN) || !errors.As(err, &wait) || wait.RetryAfterSeconds != 2 {
				t.Fatalf("main profile opened without its PIN: %+v %v", wait, err)
			}
			var attempts int
			if err := db.QueryRow(`SELECT failed_attempts FROM direct_profile_pin_attempts WHERE profile_id=?`, primary).Scan(&attempts); err != nil || attempts != 1 {
				t.Fatalf("the refused attempt was not charged: %d %v", attempts, err)
			}
			if result.Session.AccessToken != "" || result.Session.SessionFamilyID != "" {
				t.Fatal("a refused selection issued a session")
			}
			if after := liveFamilies(t, db); after != before {
				t.Fatalf("family minted %d->%d", before, after)
			}
			if viewer, err := s.Authenticate(onGuest.Session.AccessToken); err != nil || viewer.Role != TierMember {
				t.Fatalf("the Guest session changed: %+v %v", viewer, err)
			}
		})
	}
}

func TestGuestToPrimaryWrongPINIsChargedAndLocksWithoutMintingFamily(t *testing.T) {
	const pin = "4821"
	s, db, _, primary, _, onGuest := protectedMainFixture(t, "Guest", pin)
	ctx := context.Background()
	before := liveFamilies(t, db)
	_, err := s.SelectDirectProfile(ctx, onGuest.Session.AccessToken, primary, DirectSelection{PIN: "0000"})
	var wait *ProfilePINError
	if !errors.Is(err, ErrProfilePIN) || !errors.As(err, &wait) || wait.Locked || wait.RetryAfterSeconds != 2 {
		t.Fatalf("wrong main PIN: %+v %v", wait, err)
	}
	var attempts int
	if err := db.QueryRow(`SELECT failed_attempts FROM direct_profile_pin_attempts WHERE profile_id=?`, primary).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("wrong PIN was not charged: %d %v", attempts, err)
	}
	if after := liveFamilies(t, db); after != before {
		t.Fatalf("wrong PIN minted %d->%d", before, after)
	}
	// The real retry delay now holds, so the correct PIN is refused by it.
	wait = nil
	_, err = s.SelectDirectProfile(ctx, onGuest.Session.AccessToken, primary, DirectSelection{PIN: pin})
	if !errors.As(err, &wait) || !wait.Locked {
		t.Fatalf("the charged delay did not hold: %+v %v", wait, err)
	}
	if after := liveFamilies(t, db); after != before {
		t.Fatalf("a locked attempt minted %d->%d", before, after)
	}
}

func TestKidsToPrimaryRefusesKidsTrustWithoutMintingFamily(t *testing.T) {
	const pin = "4821"
	s, db, _, primary, _, onKids := protectedMainFixture(t, "Kids", pin)
	ctx := context.Background()
	kids := onKids.Session.Viewer.ProfileID
	remembered, err := s.SelectDirectProfile(ctx, onKids.Session.AccessToken, kids, DirectSelection{Trust: true, InstallationID: "kids-installation"})
	if err != nil || remembered.TrustedSelection == nil {
		t.Fatalf("Kids trust: %+v %v", remembered.TrustedSelection, err)
	}
	before := liveFamilies(t, db)
	result, err := s.SelectDirectProfile(ctx, onKids.Session.AccessToken, primary, DirectSelection{TrustToken: remembered.TrustedSelection.Token, InstallationID: "kids-installation"})
	var wait *ProfilePINError
	if !errors.Is(err, ErrProfilePIN) || !errors.As(err, &wait) || wait.RetryAfterSeconds != 2 {
		t.Fatalf("Kids trust opened the protected main profile: %+v %v", wait, err)
	}
	if result.Session.AccessToken != "" || liveFamilies(t, db) != before {
		t.Fatal("a refused selection minted a family")
	}
	if kidsViewer, err := s.Authenticate(onKids.Session.AccessToken); err != nil || kidsViewer.Role != TierMember {
		t.Fatalf("the Kids session changed: %+v %v", kidsViewer, err)
	}
}

func TestKidsToPrimaryCorrectPINIssuesOwner(t *testing.T) {
	const pin = "4821"
	s, db, _, primary, _, onKids := protectedMainFixture(t, "Kids", pin)
	ctx := context.Background()
	before := liveFamilies(t, db)
	back, err := s.SelectDirectProfile(ctx, onKids.Session.AccessToken, primary, DirectSelection{PIN: pin})
	if err != nil {
		t.Fatal(err)
	}
	if back.Session.Viewer.Role != TierOwner {
		t.Fatalf("main profile from Kids: %+v", back.Session.Viewer)
	}
	if after := liveFamilies(t, db); after != before+1 {
		t.Fatalf("family minted %d->%d", before, after)
	}
	if viewer, err := s.Authenticate(back.Session.AccessToken); err != nil || viewer.Role != TierOwner {
		t.Fatalf("main profile did not authenticate as owner: %+v %v", viewer, err)
	}
	if kidsViewer, err := s.Authenticate(onKids.Session.AccessToken); err != nil || kidsViewer.Role != TierMember {
		t.Fatalf("the Kids session changed: %+v %v", kidsViewer, err)
	}
}

// The initial profile choice of an account-scoped sign-in is not a switch from
// another profile, so a remembered main selection there keeps working.
func TestAccountScopedInitialChoiceKeepsRememberedMainTrust(t *testing.T) {
	const pin = "4821"
	s, _, login, primary, main, _ := protectedMainFixture(t, "Guest", pin)
	again, err := s.SelectDirectProfile(context.Background(), login.AccountToken, primary, DirectSelection{TrustToken: main.Token, InstallationID: mainInstallation})
	if err != nil {
		t.Fatal(err)
	}
	if again.Session.Viewer.Role != TierOwner {
		t.Fatalf("remembered initial choice took %q", again.Session.Viewer.Role)
	}
}

// memberViewingFixture gives a member account (never the server owner) a
// viewing profile and a session on it, through the real account and sign-in
// paths.
func memberViewingFixture(t *testing.T) (*Service, *sql.DB, DirectSignIn, DirectAccount, DirectProfile, DirectSelectionResult) {
	t.Helper()
	ctx := context.Background()
	s, db, owner := directFixture(t)
	member, err := s.CreateDirectMember(ctx, owner.AccountToken, DirectMemberCreate{Username: "member", Password: "Testing2!", Name: "Member", AllowedLibraries: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := s.DirectLogin(ctx, "member", "Testing2!")
	if err != nil || login.Session == nil || login.Session.Viewer.Role != TierMember {
		t.Fatalf("member sign-in: %+v %v", login.Session, err)
	}
	snapshot, err := s.CreateDirectProfile(ctx, login.AccountToken, "Kids", "sky")
	if err != nil {
		t.Fatal(err)
	}
	profile := childProfile(t, snapshot)
	selected, err := s.SelectDirectProfile(ctx, login.AccountToken, profile.ID, DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	return s, db, owner, member, profile, selected
}

func TestMemberAccountPrimarySelectionStaysMember(t *testing.T) {
	s, _, _, member, _, onKids := memberViewingFixture(t)
	ctx := context.Background()
	back, err := s.SelectDirectProfile(ctx, onKids.Session.AccessToken, member.PrimaryProfileID, DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	if back.Session.Viewer.Role != TierMember {
		t.Fatalf("a member account's main profile took %q", back.Session.Viewer.Role)
	}
	if viewer, err := s.Authenticate(back.Session.AccessToken); err != nil || viewer.Role != TierMember {
		t.Fatalf("member main profile authenticated as %+v %v", viewer, err)
	}
	if _, err = s.DirectMembers(ctx, back.Session.AccessToken); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a member account's main profile administered the server: %v", err)
	}
}

// The real transfer retiers the account's families in the same transaction and
// bumps neither the account epoch nor the caller's family, so the same Guest
// token stays usable and must re-select the primary as a member.
func TestPrimarySelectionUsesCurrentMembershipRoleNotTheCallerSession(t *testing.T) {
	ctx := context.Background()
	s, _, owner, _, onGuest := viewingProfileFixture(t, "Guest")
	primary := owner.Account.PrimaryProfileID
	first, err := s.SelectDirectProfile(ctx, onGuest.Session.AccessToken, primary, DirectSelection{})
	if err != nil || first.Session.Viewer.Role != TierOwner {
		t.Fatalf("main profile before the transfer: %+v %v", first.Session.Viewer, err)
	}
	member, err := s.CreateDirectMember(ctx, owner.AccountToken, DirectMemberCreate{Username: "member", Password: "Testing2!", Name: "Member", AllowedLibraries: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.TransferDirectOwnership(ctx, owner.AccountToken, member.ID, "Testing1!", owner.Account.Revision); err != nil {
		t.Fatal(err)
	}
	second, err := s.SelectDirectProfile(ctx, onGuest.Session.AccessToken, primary, DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	if second.Session.Viewer.Role != TierMember {
		t.Fatalf("the former owner's main profile still took %q", second.Session.Viewer.Role)
	}
	if _, err = s.DirectMembers(ctx, second.Session.AccessToken); !errors.Is(err, ErrForbidden) {
		t.Fatalf("the former owner's main profile still administered: %v", err)
	}
}

func TestPrimarySelectionRefusesDisabledRevokedOrReincarnatedAuthorityWithoutMintingFamily(t *testing.T) {
	for _, tc := range []struct {
		name           string
		breakAuthority func(t *testing.T, s *Service, db *sql.DB, owner DirectSignIn, member DirectAccount, onKids DirectSelectionResult)
	}{
		{"disabled membership", func(t *testing.T, s *Service, _ *sql.DB, owner DirectSignIn, member DirectAccount, _ DirectSelectionResult) {
			if err := s.UpdateDirectMember(context.Background(), owner.AccountToken, member.ID, DirectMemberInput{ExpectedRevision: member.Revision, Disabled: true, AllowedLibraries: []string{}}); err != nil {
				t.Fatal(err)
			}
		}},
		{"revoked session family", func(t *testing.T, s *Service, _ *sql.DB, _ DirectSignIn, _ DirectAccount, onKids DirectSelectionResult) {
			if err := s.LogoutToken(context.Background(), onKids.Session.AccessToken); err != nil {
				t.Fatal(err)
			}
		}},
		{"reincarnated account", func(t *testing.T, _ *Service, db *sql.DB, _ DirectSignIn, member DirectAccount, _ DirectSelectionResult) {
			if _, err := db.Exec(`UPDATE accounts SET epoch=epoch+1 WHERE id=?`, member.ID); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s, db, owner, member, _, onKids := memberViewingFixture(t)
			tc.breakAuthority(t, s, db, owner, member, onKids)
			before := liveFamilies(t, db)
			result, err := s.SelectDirectProfile(ctx, onKids.Session.AccessToken, member.PrimaryProfileID, DirectSelection{})
			if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if result.Session.AccessToken != "" || result.Session.SessionFamilyID != "" {
				t.Fatalf("%s: a refused selection issued a session", tc.name)
			}
			if after := liveFamilies(t, db); after != before {
				t.Fatalf("%s: family minted %d->%d", tc.name, before, after)
			}
		})
	}
}
