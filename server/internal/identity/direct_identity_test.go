package identity

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func directFixture(t *testing.T) (*Service, *sql.DB, DirectSignIn) {
	t.Helper()
	s, db, _ := familyFixture(t)
	hash, e := bcrypt.GenerateFromPassword([]byte("Testing1!"), bcrypt.MinCost)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE accounts SET password_hash=? WHERE id='account'`, hash); e != nil {
		t.Fatal(e)
	}
	login, e := s.DirectLogin(context.Background(), "owner", "Testing1!")
	if e != nil {
		t.Fatal(e)
	}
	return s, db, login
}

func TestMemberPasswordCostAndOwnershipProofOutsideWriter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	s, db, login := directFixture(t)
	member, err := s.CreateDirectMember(ctx, login.AccountToken, DirectMemberCreate{Username: "member", Name: "Member", Password: "Testing2!", AllowedLibraries: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	var hash []byte
	if err := db.QueryRow(`SELECT password_hash FROM accounts WHERE id=?`, member.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if cost, err := bcrypt.Cost(hash); err != nil || cost != PasswordCost {
		t.Fatalf("new member password cost %d: %v", cost, err)
	}
	slow, err := bcrypt.GenerateFromPassword([]byte("Testing1!"), 14)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE accounts SET password_hash=? WHERE id='account'`, slow); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.TransferDirectOwnership(ctx, login.AccountToken, member.ID, "wrong", 1) }()
	deadline := time.After(2 * time.Second)
	for len(s.hashSlots) == 0 {
		select {
		case err := <-done:
			t.Fatalf("password check ended before slot observation: %v", err)
		case <-deadline:
			t.Fatal("password check never occupied a hash slot")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if dbwork.WriteGate().ActiveOrWaiting() {
		t.Fatal("ownership proof held the writer gate during bcrypt")
	}
	if err = <-done; !errors.Is(err, ErrCurrentPasswordIncorrect) {
		t.Fatalf("wrong ownership password response: %v", err)
	}
	var attempts int
	if err = db.QueryRow(`SELECT failed_attempts FROM identity_credential_attempts WHERE account_id='account' AND source_bucket='unknown'`).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("ownership proof did not charge wrong password: %d %v", attempts, err)
	}
}

func TestOlderPasswordHashRehashesAfterSuccessfulSignIn(t *testing.T) {
	if cost, err := bcrypt.Cost(unknownAccountPasswordHash); err != nil || cost != PasswordCost {
		t.Fatalf("unknown-name hash cost %d: %v", cost, err)
	}
	s, db, _ := directFixture(t)
	old, err := bcrypt.GenerateFromPassword([]byte("Testing1!"), 12)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE accounts SET password_hash=? WHERE id='account'`, old); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = s.DirectLogin(context.Background(), "owner", "Testing1!"); err != nil {
			t.Fatalf("verified sign-in %d: %v", i, err)
		}
	}
	var upgraded []byte
	if err = db.QueryRow(`SELECT password_hash FROM accounts WHERE id='account'`).Scan(&upgraded); err != nil {
		t.Fatal(err)
	}
	if cost, err := bcrypt.Cost(upgraded); err != nil || cost != PasswordCost {
		t.Fatalf("upgraded cost %d: %v", cost, err)
	}
}
func childProfile(t *testing.T, snapshot DirectSnapshot) DirectProfile {
	t.Helper()
	for _, p := range snapshot.Profiles {
		if !p.Primary {
			return p
		}
	}
	t.Fatal("child profile missing")
	return DirectProfile{}
}
func TestLegacyLoginRetiresUnreturnedProfileSelectionSession(t *testing.T) {
	ctx := context.Background()
	s, db, first := directFixture(t)
	if _, err := s.CreateDirectProfile(ctx, first.Session.AccessToken, "Child", "mint"); err != nil {
		t.Fatal(err)
	}
	var familiesBefore, devicesBefore int
	if err := db.QueryRow(`SELECT count(*) FROM authorization_session_families WHERE revoked=0`).Scan(&familiesBefore); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM identity_devices`).Scan(&devicesBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login("owner", "Testing1!"); !errors.Is(err, ErrProfileSelection) {
		t.Fatalf("legacy login must require profile selection: %v", err)
	}
	var familiesAfter, devicesAfter int
	if err := db.QueryRow(`SELECT count(*) FROM authorization_session_families WHERE revoked=0`).Scan(&familiesAfter); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM identity_devices`).Scan(&devicesAfter); err != nil {
		t.Fatal(err)
	}
	if familiesAfter != familiesBefore || devicesAfter != devicesBefore {
		t.Fatalf("legacy login left an unusable family or device: families %d->%d devices %d->%d", familiesBefore, familiesAfter, devicesBefore, devicesAfter)
	}
}

func TestLegacyLoginReportsRequiredPasswordChange(t *testing.T) {
	s, db, _ := directFixture(t)
	if _, err := db.Exec(`INSERT INTO identity_credential_state(account_id,must_change) VALUES('account',1) ON CONFLICT(account_id) DO UPDATE SET must_change=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login("owner", "Testing1!"); !errors.Is(err, ErrPasswordChangeRequired) {
		t.Fatalf("legacy sign-in reported the wrong next step: %v", err)
	}
}
func TestDirectCredentialProfileAndServerAuthorityAreIndependent(t *testing.T) {
	ctx := context.Background()
	s, _, login := directFixture(t)
	if login.Session == nil || !login.CanManage {
		t.Fatal("single unprotected primary was not selected")
	}
	if login.AccountToken != login.Session.AccessToken {
		t.Fatal("sign-in minted a separate account grant")
	}
	profiles, e := s.CreateDirectProfile(ctx, login.AccountToken, "Child", "mint")
	if e != nil {
		t.Fatal(e)
	}
	child := childProfile(t, profiles)
	second, e := s.DirectLogin(ctx, "owner", "Testing1!")
	if e != nil || second.Session == nil || second.Session.Viewer.Role != "account" {
		t.Fatal("multi-profile sign-in skipped profile choice", e)
	}
	if _, e = s.Authenticate(second.Session.AccessToken); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("account-scoped family viewed media", e)
	}
	selected, e := s.SelectDirectProfile(ctx, second.AccountToken, child.ID, DirectSelection{})
	if e != nil {
		t.Fatal(e)
	}
	if selected.Session.InstallationID == "" || selected.Session.InstallationID != second.InstallationID {
		t.Fatal("profile selection did not return its refresh binding")
	}
	if refreshed, refreshErr := s.RefreshSession(ctx, selected.Session.RefreshToken, selected.Session.InstallationID, Token()); refreshErr != nil || refreshed.InstallationID != second.InstallationID {
		t.Fatal("profile selection could not refresh from its response", refreshErr)
	} else {
		selected.Session = refreshed
	}
	viewer, e := s.Authenticate(selected.Session.AccessToken)
	if e != nil || viewer.Role != "member" || viewer.ProfileID != child.ID {
		t.Fatal("child inherited server owner role", e)
	}
	if _, e = s.CreateDirectProfile(ctx, selected.Session.AccessToken, "Unauthorized", "blue"); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("child administered account profiles", e)
	}
	me, e := s.DirectMe(ctx, selected.Session.AccessToken)
	if e != nil || me.CanManage {
		t.Fatal("child management projection", e)
	}
	if e = s.EndDirectAccountSession(ctx, second.AccountToken); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(selected.Session.AccessToken); e != nil {
		t.Fatal("ending account grant ended independent viewing session", e)
	}
}
func TestDirectPINRevisionEndsOldFamiliesAndTheNewPINNeedsItsOwnSelection(t *testing.T) {
	ctx := context.Background()
	s, db, login := directFixture(t)
	profiles, e := s.CreateDirectProfile(ctx, login.AccountToken, "Protected", "blue")
	if e != nil {
		t.Fatal(e)
	}
	child := childProfile(t, profiles)
	old, e := s.SelectDirectProfile(ctx, login.AccountToken, child.ID, DirectSelection{})
	if e != nil {
		t.Fatal(e)
	}
	pin := "0123"
	profiles, e = s.EditDirectProfile(ctx, login.AccountToken, child.ID, DirectProfileEdit{ExpectedRevision: child.Revision, PIN: &pin})
	if e != nil {
		t.Fatal(e)
	}
	child = childProfile(t, profiles)
	if _, e = s.Authenticate(old.Session.AccessToken); e == nil {
		t.Fatal("old profile family still works after the PIN change")
	}
	if _, e = s.SelectDirectProfile(ctx, login.AccountToken, child.ID, DirectSelection{PIN: "9999"}); !errors.Is(e, ErrProfilePIN) {
		t.Fatal("wrong PIN accepted", e)
	}
	var attempts int
	if e = db.QueryRow(`SELECT failed_attempts FROM direct_profile_pin_attempts WHERE profile_id=? AND lineage_id=(SELECT id FROM identity_pin_lineages WHERE origin_device_id=?)`, child.ID, login.DeviceID).Scan(&attempts); e != nil || attempts != 1 {
		t.Fatal("failed PIN attempt was rolled back", e)
	}
	if _, e = db.Exec(`UPDATE direct_profile_pin_attempts SET locked_until_ms=0 WHERE profile_id=? AND lineage_id=(SELECT id FROM identity_pin_lineages WHERE origin_device_id=?)`, child.ID, login.DeviceID); e != nil {
		t.Fatal(e)
	}
	selected, e := s.SelectDirectProfile(ctx, login.AccountToken, child.ID, DirectSelection{PIN: pin, Remember: true, InstallationID: "installation"})
	if e != nil || selected.Remembered == nil {
		t.Fatal("explicit remember selection", e)
	}
	// Re-selecting the same profile from its own session proves the PIN again:
	// remembering keeps the device's family, it does not spare the PIN.
	if _, e = s.SelectDirectProfile(ctx, selected.Session.AccessToken, child.ID, DirectSelection{InstallationID: "installation", Remember: true}); !errors.Is(e, ErrProfilePIN) {
		t.Fatal("same-profile reselect without the PIN was admitted", e)
	}
	if _, e = db.Exec(`UPDATE direct_profile_pin_attempts SET locked_until_ms=0 WHERE profile_id=?`, child.ID); e != nil {
		t.Fatal(e)
	}
	again, e := s.SelectDirectProfile(ctx, selected.Session.AccessToken, child.ID, DirectSelection{PIN: pin, InstallationID: "installation", Remember: true})
	if e != nil || again.Session.SessionFamilyID == selected.Session.SessionFamilyID {
		t.Fatal("same-profile selection with the PIN did not issue an independent viewing family", e)
	}
	replacement := "4567"
	if _, e = s.EditDirectProfile(ctx, login.AccountToken, child.ID, DirectProfileEdit{ExpectedRevision: child.Revision, PIN: &replacement}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(again.Session.AccessToken); e == nil {
		t.Fatal("viewing family still works after the PIN revision")
	}
	if _, e = db.Exec(`UPDATE direct_profile_pin_attempts SET locked_until_ms=0 WHERE profile_id=?`, child.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SelectDirectProfile(ctx, login.AccountToken, child.ID, DirectSelection{PIN: pin, InstallationID: "installation"}); !errors.Is(e, ErrProfilePIN) {
		t.Fatal("the old PIN survived the PIN change", e)
	}
}
func TestDirectMembershipIntersectionAndPrimaryNotOwner(t *testing.T) {
	ctx := context.Background()
	s, db, owner := directFixture(t)
	member, e := s.CreateDirectMember(ctx, owner.AccountToken, DirectMemberCreate{Username: "member", Password: "Testing2!", Name: "Member", AllowedLibraries: []string{"movies"}})
	if e != nil {
		t.Fatal(e)
	}
	login, e := s.DirectLogin(ctx, "member", "Testing2!")
	if e != nil || login.Session == nil {
		t.Fatal(e)
	}
	if login.Session.Viewer.Role != "member" || !login.CanManage {
		t.Fatal("primary/account management confused with server ownership")
	}
	if _, e = s.DirectMembers(ctx, login.AccountToken); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("member primary administered server members", e)
	}
	profiles, e := s.CreateDirectProfile(ctx, login.AccountToken, "Narrow", "coral")
	if e != nil {
		t.Fatal(e)
	}
	child := childProfile(t, profiles)
	if _, e = s.EditDirectProfile(ctx, login.AccountToken, child.ID, DirectProfileEdit{ExpectedRevision: child.Revision, Policy: &ProfilePolicy{AllowedLibraries: []string{"movies", "secret"}}}); e != nil {
		t.Fatal(e)
	}
	selected, e := s.SelectDirectProfile(ctx, login.AccountToken, child.ID, DirectSelection{})
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.Authenticate(selected.Session.AccessToken)
	if e != nil {
		t.Fatal(e)
	}
	if e = DirectAllowed(s.db, p, "movies"); e != nil {
		t.Fatal(e)
	}
	if e = DirectAllowed(s.db, p, "secret"); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("profile widened account membership", e)
	}
	if e = s.UpdateDirectMember(ctx, owner.AccountToken, member.ID, DirectMemberInput{ExpectedRevision: member.Revision, Disabled: true, AllowedLibraries: []string{}}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(selected.Session.AccessToken); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("disabled account retained family", e)
	}
	var reason string
	if e = db.QueryRow(`SELECT revoked_reason FROM authorization_session_families WHERE id=?`, selected.Session.SessionFamilyID).Scan(&reason); e != nil || reason != string(RevokedMembershipRemoved) {
		t.Fatalf("membership removal reason %q: %v", reason, e)
	}
}
func TestDirectCapacityDeletionAndStaleProfileRevision(t *testing.T) {
	ctx := context.Background()
	s, db, login := directFixture(t)
	var snapshot DirectSnapshot
	for i := 0; i < 7; i++ {
		var e error
		snapshot, e = s.CreateDirectProfile(ctx, login.AccountToken, "Child", "sky")
		if e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.CreateDirectProfile(ctx, login.AccountToken, "Ninth", "blue"); !errors.Is(e, ErrProfileCapacity) {
		t.Fatal("ninth active profile admitted", e)
	}
	if e := retireProfileAndDrainForTest(t, s, ctx, login.AccountToken, login.Account.PrimaryProfileID, 1, true); !errors.Is(e, ErrPrimaryProfile) {
		t.Fatal("primary deleted", e)
	}
	child := childProfile(t, snapshot)
	if e := retireProfileAndDrainForTest(t, s, ctx, login.AccountToken, child.ID, child.Revision+1, true); !errors.Is(e, ErrProfileChanged) {
		t.Fatal("stale deletion accepted", e)
	}
	session, e := s.SelectDirectProfile(ctx, login.AccountToken, child.ID, DirectSelection{})
	if e != nil {
		t.Fatal(e)
	}
	if e = retireProfileAndDrainForTest(t, s, ctx, login.AccountToken, child.ID, child.Revision, true); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(session.Session.AccessToken); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("deleted profile still authenticated", e)
	}
	var deleted bool
	if e = db.QueryRow(`SELECT deleted FROM direct_profiles WHERE id=?`, child.ID).Scan(&deleted); e != nil || !deleted {
		t.Fatal("profile tombstone missing", e)
	}
	if _, e = s.CreateDirectProfile(ctx, login.AccountToken, "Replacement", "blue"); e != nil {
		t.Fatal("deleted profile consumed capacity", e)
	}
}
func TestDirectPasswordPolicyUsesCharactersAndUTF8ByteLimit(t *testing.T) {
	for _, password := range []string{"Abcdef1!", "\u00c9abcde1!", "abcdefgh1", "ABCDEFGH1", "Abcdefgh", "password", "abcdefgh", "ABCDEFGH", "password1"} {
		if !directPassword(password) {
			t.Errorf("valid password rejected: %q", password)
		}
	}
	for _, password := range []string{"Abcd1!", "\u00c9abcd1", string([]byte{0xff, 'A', 'b', 'c', 'd', 'e', '1', '!'}), "abcdefg", strings.Repeat("é", 37)} {
		if directPassword(password) {
			t.Errorf("invalid password accepted: %q", password)
		}
	}
}

func TestDirectErasureDeletesOwnedAndReceivedSavedDataOnlyInExactAuthority(t *testing.T) {
	ctx := context.Background()
	s, db, login := directFixture(t)
	rows, e := s.CreateDirectProfile(ctx, login.AccountToken, "Delete me", "blue")
	if e != nil {
		t.Fatal(e)
	}
	child := childProfile(t, rows)
	// Identical raw identifiers in Hosted and Local are different owners.
	for _, authority := range []string{"local", "hosted"} {
		if _, e = db.Exec(`INSERT INTO catalog_playlists(token,owner_authority,owner_account,owner_profile,name,creation_operation,creation_hash,created_at) VALUES(?,?,'account',?,'Private list','create','hash','2026-01-01T00:00:00Z')`, authority, authority, child.ID); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = db.Exec(`INSERT INTO playlist_shares(playlist_id,authority,account_id,profile_id,role) VALUES('hosted','local','account',?,'editor')`, child.ID); e != nil {
		t.Fatal(e)
	}
	if e = retireProfileAndDrainForTest(t, s, ctx, login.AccountToken, child.ID, child.Revision, true); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM catalog_playlists WHERE token='local'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("owned resource retained", count, e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM catalog_playlists WHERE token='hosted'`).Scan(&count); e != nil || count != 1 {
		t.Fatal("other authority data erased", count, e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM playlist_shares WHERE playlist_id='hosted'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("received share retained", count, e)
	}
}

// A proven sign-in upgrades a hash made at another cost to PasswordCost.
func TestDirectLoginUpgradesPasswordHashCost(t *testing.T) {
	_, db, _ := directFixture(t) // the fixture stores a MinCost hash, then signs in
	var hash []byte
	if e := db.QueryRow(`SELECT password_hash FROM accounts WHERE id='account'`).Scan(&hash); e != nil {
		t.Fatal(e)
	}
	if cost, e := bcrypt.Cost(hash); e != nil || cost != PasswordCost {
		t.Fatalf("hash cost %d after sign-in, want %d (%v)", cost, PasswordCost, e)
	}
	if bcrypt.CompareHashAndPassword(hash, []byte("Testing1!")) != nil {
		t.Fatal("upgraded hash does not verify the password")
	}
}
