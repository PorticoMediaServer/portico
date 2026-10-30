package identity

import (
	"context"
	"errors"
	"testing"
)

func insertPlay(t *testing.T, s *Service, playback, account, profile, user, profileName, device string) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,profile_name,item_id,library_id,kind,title,parent_title,device_id,device_name,platform,started_ms,updated_ms,position_ms,duration_ms,completed,converted)
 VALUES(?,'local',?,?,?,?,7,'library',1,'A Film','','device',?,'ios',1000,2000,3000,4000,1,0)`, playback, account, profile, user, profileName, device); err != nil {
		t.Fatal(err)
	}
}

func TestDeletingAMemberRemovesThePersonAndKeepsTheirPlaysWithoutTheirName(t *testing.T) {
	ctx := context.Background()
	s, owner, member, login := memberWithSession(t)
	profiles, err := s.CreateDirectProfile(ctx, login.AccountToken, "Kid", "coral")
	if err != nil {
		t.Fatal(err)
	}
	child := childProfile(t, profiles)
	insertPlay(t, s, "play-1", member.ID, member.PrimaryProfileID, "member", "Member", "Member's iPhone")
	insertPlay(t, s, "play-2", member.ID, child.ID, "member", "Kid", "Living room")
	insertPlay(t, s, "play-owner", owner.Account.ID, owner.Account.PrimaryProfileID, "owner", "Owner", "Owner's TV")

	preview, err := s.PreviewDirectMemberDeletion(ctx, owner.AccountToken, member.ID)
	if err != nil || preview.Username != "member" || preview.Role != TierMember || preview.Profiles != 2 || preview.Devices < 1 || preview.PlaysKept != 2 || preview.PorticoAccount {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	// A confirmation of an older state of the member is refused.
	if err = s.DeleteDirectMember(ctx, owner.AccountToken, member.ID, MemberDeletion{ExpectedRevision: preview.Revision + 1}); !errors.Is(err, ErrProfileChanged) {
		t.Fatalf("a stale confirmation: %v", err)
	}
	if err = s.DeleteDirectMember(ctx, owner.AccountToken, member.ID, MemberDeletion{}); !errors.Is(err, ErrDirectInput) {
		t.Fatalf("a deletion with no revision: %v", err)
	}
	if err = s.DeleteDirectMember(ctx, owner.AccountToken, member.ID, MemberDeletion{ExpectedRevision: preview.Revision}); err != nil {
		t.Fatal(err)
	}
	// The person is gone: signed out, cannot sign in, not listed.
	if _, err = s.Authenticate(login.Session.AccessToken); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a deleted member is still signed in: %v", err)
	}
	if _, err = s.DirectLogin(ctx, "member", "Testing2!"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("a deleted member signed in: %v", err)
	}
	members, err := s.DirectMembers(ctx, owner.AccountToken)
	if err != nil || len(members) != 1 || members[0].ID != owner.Account.ID {
		t.Fatalf("members after deletion: %+v %v", members, err)
	}
	for table, where := range map[string]string{
		"accounts": "id=?", "direct_memberships": "account_id=?", "direct_profiles": "account_id=?", "identity_devices": "account_id=?",
	} {
		var n int
		if err = s.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE `+where, member.ID).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s still holds %d rows of the deleted member: %v", table, n, err)
		}
	}
	var reason string
	if err = s.db.QueryRow(`SELECT revoked_reason FROM authorization_session_families WHERE id=?`, login.Session.SessionFamilyID).Scan(&reason); err != nil || reason != string(RevokedMembershipRemoved) {
		t.Fatalf("the session's end is recorded as %q: %v", reason, err)
	}
	// The plays are kept; who played them is not.
	rows, err := s.db.Query(`SELECT playback_id,user_name,profile_name,device_name,title,position_ms FROM play_history ORDER BY playback_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var id, user, profile, device, title string
		var position int64
		if err = rows.Scan(&id, &user, &profile, &device, &title, &position); err != nil {
			t.Fatal(err)
		}
		seen++
		if title != "A Film" || position != 3000 {
			t.Fatalf("play %s lost its facts: %q %d", id, title, position)
		}
		if id == "play-owner" {
			if user != "owner" || profile != "Owner" || device != "Owner's TV" {
				t.Fatalf("another person's play was blanked: %q %q %q", user, profile, device)
			}
			continue
		}
		if user != "" || profile != "" || device != "" {
			t.Fatalf("play %s still names the deleted member: %q %q %q", id, user, profile, device)
		}
	}
	if seen != 3 {
		t.Fatalf("%d plays remain, want all 3", seen)
	}
	// The username is free again.
	if _, err = s.CreateDirectMember(ctx, owner.AccountToken, DirectMemberCreate{Username: "member", Password: "Testing4!", Name: "Someone new", AllowedLibraries: []string{}}); err != nil {
		t.Fatalf("the deleted member's username is still taken: %v", err)
	}
}

func TestWhoMayDeleteWhom(t *testing.T) {
	ctx := context.Background()
	s, owner, member, login := memberWithSession(t)
	other, err := s.CreateDirectMember(ctx, owner.AccountToken, DirectMemberCreate{Username: "other", Password: "Testing3!", Name: "Other", AllowedLibraries: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	accounts := func() int {
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM accounts`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := accounts()
	// A member deletes nobody, and is refused as forbidden, not signed out.
	for _, target := range []string{other.ID, owner.Account.ID, member.ID} {
		if err = s.DeleteDirectMember(ctx, login.AccountToken, target, MemberDeletion{ExpectedRevision: 1}); !errors.Is(err, ErrForbidden) {
			t.Fatalf("a member deleting %s: %v", target, err)
		}
		if _, err = s.PreviewDirectMemberDeletion(ctx, login.AccountToken, target); !errors.Is(err, ErrForbidden) {
			t.Fatalf("a member previewing the deletion of %s: %v", target, err)
		}
	}
	// Nobody deletes the owner, the owner included.
	if err = s.DeleteDirectMember(ctx, owner.AccountToken, owner.Account.ID, MemberDeletion{ExpectedRevision: owner.Account.Revision}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("the owner deleting itself: %v", err)
	}
	// An unknown member is "not found", and a malformed id is bad input.
	if err = s.DeleteDirectMember(ctx, owner.AccountToken, "no-such-account", MemberDeletion{ExpectedRevision: 1}); !errors.Is(err, ErrNotVisible) {
		t.Fatalf("an unknown member: %v", err)
	}
	if err = s.DeleteDirectMember(ctx, owner.AccountToken, "not an id", MemberDeletion{ExpectedRevision: 1}); !errors.Is(err, ErrDirectInput) {
		t.Fatalf("a malformed id: %v", err)
	}
	// No session, no deletion.
	if err = s.DeleteDirectMember(ctx, "", other.ID, MemberDeletion{ExpectedRevision: 1}); !errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrForbidden) {
		t.Fatalf("no session: %v", err)
	}
	if got := accounts(); got != before {
		t.Fatalf("a refused deletion removed an account: %d, was %d", got, before)
	}
	// An administrator deletes a member but not another administrator.
	if _, err = s.db.Exec(`UPDATE direct_memberships SET role='admin',revision=revision+1 WHERE account_id=?`, member.ID); err != nil {
		t.Fatal(err)
	}
	if err = RetierFamiliesForTest(ctx, s, member.ID, TierAdmin); err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateDirectMember(ctx, owner.AccountToken, DirectMemberCreate{Username: "second", Password: "Testing5!", Name: "Second", AllowedLibraries: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE direct_memberships SET role='admin' WHERE account_id=?`, second.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteDirectMember(ctx, login.AccountToken, second.ID, MemberDeletion{ExpectedRevision: second.Revision}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("an administrator deleting another administrator: %v", err)
	}
	if err = s.DeleteDirectMember(ctx, login.AccountToken, other.ID, MemberDeletion{ExpectedRevision: other.Revision}); err != nil {
		t.Fatalf("an administrator deleting a member: %v", err)
	}
	// The owner's own erasure entry point refuses the owner as well.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = s.EraseMemberTx(ctx, tx, owner.Account.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("the erasure accepted the owner's account: %v", err)
	}
}

// A removed profile keeps its final history attribution for the owner only.
func TestDeletingAProfilePreservesItsHistoryAttribution(t *testing.T) {
	ctx := context.Background()
	s, _, member, login := memberWithSession(t)
	profiles, err := s.CreateDirectProfile(ctx, login.AccountToken, "Kid", "coral")
	if err != nil {
		t.Fatal(err)
	}
	child := childProfile(t, profiles)
	insertPlay(t, s, "play-kid", member.ID, child.ID, "member", "Kid", "Tablet")
	insertPlay(t, s, "play-main", member.ID, member.PrimaryProfileID, "member", "Member", "Phone")
	if err = retireProfileAndDrainForTest(t, s, ctx, login.AccountToken, child.ID, child.Revision, true); err != nil {
		t.Fatal(err)
	}
	var user, profile string
	if err = s.db.QueryRow(`SELECT user_name,profile_name FROM play_history WHERE playback_id='play-kid'`).Scan(&user, &profile); err != nil || user != "member" || profile != "Kid" {
		t.Fatalf("the deleted profile's play: user=%q profile=%q %v", user, profile, err)
	}
	if err = s.db.QueryRow(`SELECT user_name,profile_name FROM play_history WHERE playback_id='play-main'`).Scan(&user, &profile); err != nil || user != "member" || profile != "Member" {
		t.Fatalf("another profile's play was changed: user=%q profile=%q %v", user, profile, err)
	}
}

// RetierFamiliesForTest moves a member's sessions to a tier the test set by hand.
func RetierFamiliesForTest(ctx context.Context, s *Service, account, role string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = RetierFamiliesTx(ctx, tx, account, role); err != nil {
		return err
	}
	return tx.Commit()
}
