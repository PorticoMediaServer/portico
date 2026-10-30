package identity

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// What an owner may change about a member without signing them out, and what
// still ends their sessions.

func memberWithSession(t *testing.T) (*Service, DirectSignIn, DirectAccount, DirectSignIn) {
	t.Helper()
	ctx := context.Background()
	s, _, owner := directFixture(t)
	member, err := s.CreateDirectMember(ctx, owner.AccountToken, DirectMemberCreate{Username: "member", Password: "Testing2!", Name: "Member", AllowedLibraries: []string{"movies"}})
	if err != nil {
		t.Fatal(err)
	}
	login, err := s.DirectLogin(ctx, "member", "Testing2!")
	if err != nil || login.Session == nil {
		t.Fatalf("member sign-in: %v", err)
	}
	return s, owner, member, login
}

func TestGivingOrTakingALibraryAppliesWithoutSigningTheMemberOut(t *testing.T) {
	ctx := context.Background()
	s, owner, member, login := memberWithSession(t)
	p, err := s.Authenticate(login.Session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err = DirectAllowed(s.db, p, "shows"); !errors.Is(err, ErrNotVisible) {
		t.Fatalf("a library not yet given was visible: %v", err)
	}
	if err = s.UpdateDirectMember(ctx, owner.AccountToken, member.ID, DirectMemberInput{ExpectedRevision: member.Revision, AllowedLibraries: []string{"movies", "shows"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(login.Session.AccessToken); err != nil {
		t.Fatalf("giving a library signed the member out: %v", err)
	}
	if err = DirectAllowed(s.db, p, "shows"); err != nil {
		t.Fatalf("the new library is not visible on the next request: %v", err)
	}
	// Taking one away is live in the same way: gone on the next request.
	if err = s.UpdateDirectMember(ctx, owner.AccountToken, member.ID, DirectMemberInput{ExpectedRevision: member.Revision + 1, AllowedLibraries: []string{"shows"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(login.Session.AccessToken); err != nil {
		t.Fatalf("taking a library signed the member out: %v", err)
	}
	if err = DirectAllowed(s.db, p, "movies"); !errors.Is(err, ErrNotVisible) {
		t.Fatalf("a library that was taken away is still visible: %v", err)
	}
	// The refresh credential still works: nothing about the sign-in ended.
	var revoked, epoch int
	if err = s.db.QueryRow(`SELECT f.revoked,a.epoch FROM authorization_session_families f JOIN accounts a ON a.id=f.account_id WHERE f.id=?`, login.Session.SessionFamilyID).Scan(&revoked, &epoch); err != nil || revoked != 0 || epoch != 1 {
		t.Fatalf("library edits touched the session: revoked=%d epoch=%d %v", revoked, epoch, err)
	}
}

func TestTurningAMemberOffOrReplacingTheirPasswordStillSignsThemOut(t *testing.T) {
	ctx := context.Background()
	s, owner, member, login := memberWithSession(t)
	if _, err := s.MakeMemberResetCode(ctx, owner.AccountToken, member.ID, MemberResetRequest{ExpectedRevision: member.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(login.Session.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a replaced password left the old session alive: %v", err)
	}
	var reason string
	if err := s.db.QueryRow(`SELECT revoked_reason FROM authorization_session_families WHERE id=?`, login.Session.SessionFamilyID).Scan(&reason); err != nil || reason != string(RevokedPasswordChange) {
		t.Fatalf("reason %q %v", reason, err)
	}

	s, owner, member, login = memberWithSession(t)
	if err := s.UpdateDirectMember(ctx, owner.AccountToken, member.ID, DirectMemberInput{ExpectedRevision: member.Revision, AllowedLibraries: []string{"movies"}, Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(login.Session.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a member who was turned off is still signed in: %v", err)
	}
	if _, err := s.DirectLogin(ctx, "member", "Testing2!"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a member who was turned off signed in again: %v", err)
	}
}

func TestOnlyAnAdministratorMayEditAMemberAndTheRefusalIsNotASignOut(t *testing.T) {
	ctx := context.Background()
	s, owner, member, login := memberWithSession(t)
	other, err := s.CreateDirectMember(ctx, owner.AccountToken, DirectMemberCreate{Username: "other", Password: "Testing3!", Name: "Other", AllowedLibraries: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	// A member editing another member is refused as forbidden, not as
	// unauthenticated: a client ends the sign-in on the second.
	err = s.UpdateDirectMember(ctx, login.AccountToken, other.ID, DirectMemberInput{ExpectedRevision: other.Revision, AllowedLibraries: []string{"movies"}})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("a member edited another member: %v", err)
	}
	if status, _, _ := Refusal(err); status != 403 {
		t.Fatalf("the refusal answers %d", status)
	}
	// Nobody edits the owner this way.
	err = s.UpdateDirectMember(ctx, owner.AccountToken, owner.Account.ID, DirectMemberInput{ExpectedRevision: owner.Account.Revision, AllowedLibraries: []string{}, Disabled: true})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("the owner was edited through member management: %v", err)
	}
	_ = member
}

func TestAProfilesLibraryListChangesLiveAndANewPINStillSignsItOut(t *testing.T) {
	ctx := context.Background()
	s, _, _, login := memberWithSession(t)
	profiles, err := s.CreateDirectProfile(ctx, login.AccountToken, "Child", "coral")
	if err != nil {
		t.Fatal(err)
	}
	child := childProfile(t, profiles)
	selected, err := s.SelectDirectProfile(ctx, login.AccountToken, child.ID, DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Authenticate(selected.Session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err = DirectAllowed(s.db, p, "movies"); err != nil {
		t.Fatal(err)
	}
	edited, err := s.EditDirectProfile(ctx, login.AccountToken, child.ID, DirectProfileEdit{ExpectedRevision: child.Revision, Policy: &ProfilePolicy{AllowedLibraries: []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(selected.Session.AccessToken); err != nil {
		t.Fatalf("narrowing a profile's libraries signed it out: %v", err)
	}
	if err = DirectAllowed(s.db, p, "movies"); !errors.Is(err, ErrNotVisible) {
		t.Fatalf("the narrowed list is not in force on the next request: %v", err)
	}
	pin := "4821"
	if _, err = s.EditDirectProfile(ctx, login.AccountToken, child.ID, DirectProfileEdit{ExpectedRevision: childProfile(t, edited).Revision, PIN: &pin}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(selected.Session.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a new PIN left a device on the profile that never entered it: %v", err)
	}
}

func TestOwnershipTransferMovesBothPeoplesSessionsWithoutSigningThemOut(t *testing.T) {
	ctx := context.Background()
	s, owner, member, login := memberWithSession(t)
	if err := s.TransferDirectOwnership(ctx, owner.AccountToken, member.ID, "Testing1!", owner.Account.Revision+1); !errors.Is(err, ErrProfileChanged) {
		t.Fatalf("a stale revision: %v", err)
	}
	if err := s.TransferDirectOwnership(ctx, owner.AccountToken, owner.Account.ID, "Testing1!", owner.Account.Revision); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a transfer to oneself: %v", err)
	}
	if err := s.TransferDirectOwnership(ctx, owner.AccountToken, member.ID, "not the password", owner.Account.Revision); err == nil {
		t.Fatal("a transfer without the owner's password went through")
	}
	if err := s.TransferDirectOwnership(ctx, owner.AccountToken, member.ID, "Testing1!", owner.Account.Revision); err != nil {
		t.Fatal(err)
	}
	was, err := s.Authenticate(owner.Session.AccessToken)
	if err != nil || was.Role != TierMember {
		t.Fatalf("the former owner's session: role=%q %v", was.Role, err)
	}
	now, err := s.Authenticate(login.Session.AccessToken)
	if err != nil || now.Role != TierOwner {
		t.Fatalf("the new owner's session: role=%q %v", now.Role, err)
	}
	// The former owner's device can no longer do what an owner does.
	if _, err = s.DirectMembers(ctx, owner.Session.AccessToken); !errors.Is(err, ErrForbidden) {
		t.Fatalf("the former owner still lists members: %v", err)
	}
	if _, err = s.DirectMembers(ctx, login.Session.AccessToken); err != nil {
		t.Fatalf("the new owner cannot list members: %v", err)
	}
}

func TestOwnerTransitionHookSharesOnlyTheAcceptingTransaction(t *testing.T) {
	ctx := context.Background()
	s, owner, member, _ := memberWithSession(t)
	if _, err := s.db.Exec(`CREATE TABLE owner_hook_probe(value INTEGER NOT NULL); INSERT INTO owner_hook_probe VALUES(0)`); err != nil {
		t.Fatal(err)
	}
	calls := 0
	refuse := errors.New("fixture retirement refused")
	s.OnOwnerChangedTx = func(ctx context.Context, tx *sql.Tx) error {
		calls++
		var role string
		if err := tx.QueryRowContext(ctx, `SELECT role FROM direct_memberships WHERE account_id=?`, member.ID).Scan(&role); err != nil {
			return err
		}
		if role != TierOwner {
			t.Fatalf("hook before accepted owner: %s", role)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE owner_hook_probe SET value=value+1`); err != nil {
			return err
		}
		return refuse
	}
	if err := s.TransferDirectOwnership(ctx, owner.AccountToken, member.ID, "Testing1!", owner.Account.Revision+1); !errors.Is(err, ErrProfileChanged) || calls != 0 {
		t.Fatal("preview/stale transfer invoked retirement", calls, err)
	}
	if err := s.TransferDirectOwnership(ctx, owner.AccountToken, member.ID, "Testing1!", owner.Account.Revision); !errors.Is(err, refuse) {
		t.Fatal(err)
	}
	var value int
	if err := s.db.QueryRow(`SELECT value FROM owner_hook_probe`).Scan(&value); err != nil || value != 0 {
		t.Fatal("retirement escaped rollback", value, err)
	}
	principal, err := s.Authenticate(owner.Session.AccessToken)
	if err != nil || principal.Role != TierOwner {
		t.Fatal("failed retirement changed owner", principal.Role, err)
	}
	refuse = nil
	if err = s.TransferDirectOwnership(ctx, owner.AccountToken, member.ID, "Testing1!", owner.Account.Revision); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT value FROM owner_hook_probe`).Scan(&value); err != nil || value != 1 || calls != 2 {
		t.Fatal("accepted retirement count", value, calls, err)
	}
}
