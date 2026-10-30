package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strings"
	"testing"
	"time"
)

func moveWebContext(t *testing.T) context.Context {
	t.Helper()
	c, e := WithIssuingDevice(context.Background(), DeviceRegistration{InstallationID: Token(), Name: "Owner browser", Platform: "web", App: "portico"}, "192.0.2.1")
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func movePorticoFixture(t *testing.T) (*Service, DirectSignIn, DirectAccount, DirectSignIn) {
	t.Helper()
	s, owner, member, login := memberWithSession(t)
	if _, e := s.CreateDirectProfile(context.Background(), login.AccountToken, "Personal child", "mint"); e != nil {
		t.Fatal(e)
	}
	insertPlay(t, s, "member-play", member.ID, member.PrimaryProfileID, "member", "Member", "Phone")
	insertPlay(t, s, "owner-play", owner.Account.ID, owner.Account.PrimaryProfileID, "owner", "Owner", "Browser")
	web, e := s.DirectLogin(moveWebContext(t), "owner", "Testing1!")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`UPDATE accounts SET password_hash=X'';UPDATE identity_state SET sign_in_kind='portico';INSERT INTO account_portico_links(account_id,hosted_account_id) SELECT id,'acc_'||id FROM accounts`); e != nil {
		t.Fatal(e)
	}
	return s, web, member, login
}
func moveProfiles(t *testing.T, s *Service) string {
	t.Helper()
	var v string
	if e := s.db.QueryRow(`SELECT json_group_array(json_array(id,account_id,name,is_primary,position,art,revision,pin_revision)) FROM (SELECT * FROM direct_profiles ORDER BY id)`).Scan(&v); e != nil {
		t.Fatal(e)
	}
	return v
}
func TestAccountMoveLocalKeepsProfilesAndHistoryAndShowsCodesOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, owner, member, login := movePorticoFixture(t)
	ctx := moveWebContext(t)
	before := moveProfiles(t, s)
	if _, e := s.db.Exec(`INSERT INTO access_invitations(id,code_hash,role,created_ms,expires_ms,created_by) VALUES('unused-a','hash-a','member',0,9999999999999,?),('unused-b','hash-b','member',0,9999999999999,?)`, owner.Account.ID, owner.Account.ID); e != nil {
		t.Fatal(e)
	}
	v, e := s.PreviewAccountMove(ctx, owner.AccountToken, MovePreviewRequest{To: SignInLocal})
	if e != nil || v.People != 1 || v.Devices == nil || v.Invitations != 2 {
		t.Fatalf("preview %+v %v", v, e)
	}
	if m, e := s.PublicAccountMove(ctx); e != nil || m != nil {
		t.Fatalf("unconfirmed preview leaked %v %v", m, e)
	}
	out, e := s.ConfirmLocalMove(ctx, owner.AccountToken, MoveConfirmRequest{MoveID: v.ID, ExpectedRevision: v.Revision, Username: "owner", Password: "Fresh-owner1!"})
	if e != nil {
		t.Fatal(e)
	}
	var unused int
	if e = s.db.QueryRow(`SELECT count(*) FROM access_invitations WHERE state='pending'`).Scan(&unused); e != nil || unused != 0 {
		t.Fatalf("unused invitations survived %d %v", unused, e)
	}
	if before != moveProfiles(t, s) {
		t.Fatal("Local move changed profiles")
	}
	if m, e := s.PublicAccountMove(ctx); e != nil || m == nil || *m != "to_local" {
		t.Fatalf("public state %v %v", m, e)
	}
	if _, e = s.Authenticate(login.Session.AccessToken); !errors.Is(e, ErrUnauthorized) {
		t.Fatalf("old member session survived %v", e)
	}
	var plays, links int
	if e = s.db.QueryRow(`SELECT count(*) FROM play_history`).Scan(&plays); e != nil || plays != 2 {
		t.Fatalf("plays=%d %v", plays, e)
	}
	if e = s.db.QueryRow(`SELECT count(*) FROM account_portico_links`).Scan(&links); e != nil || links != 0 {
		t.Fatalf("links=%d %v", links, e)
	}
	first, e := s.MakeMoveCodes(ctx, out.AccountToken, MoveCodeRequest{AccountIDs: []string{member.ID}})
	if e != nil || len(first.Items) != 1 {
		t.Fatalf("code %+v %v", first, e)
	}
	code := first.Items[0].Code
	var stored string
	if e = s.db.QueryRow(`SELECT code_hash FROM identity_move_codes WHERE account_id=?`, member.ID).Scan(&stored); e != nil || stored == code || stored != Digest(code) {
		t.Fatal("code stored in clear", e)
	}
	preview, e := s.PreviewMoveCode(ctx, code)
	if e != nil || preview.Username != "member" || preview.SignIn != SignInLocal {
		t.Fatalf("code preview %+v %v", preview, e)
	}
	next, e := s.MakeMoveCodes(ctx, out.AccountToken, MoveCodeRequest{AccountIDs: []string{member.ID}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.PreviewMoveCode(ctx, code); !errors.Is(e, ErrMoveCode) {
		t.Fatalf("replaced code survived %v", e)
	}
	q := LocalMoveRedemption{Code: next.Items[0].Code, Username: "member", Password: "New-member1!"}
	resumed, e := s.RedeemLocalMove(ctx, q)
	if e != nil || resumed.Account.ID != member.ID {
		t.Fatalf("redeem %+v %v", resumed, e)
	}
	if _, e = s.RedeemLocalMove(ctx, q); !errors.Is(e, ErrMoveCode) {
		t.Fatalf("code replay %v", e)
	}
	if moveProfiles(t, s) != before {
		t.Fatal("redemption changed profiles")
	}
	if m, e := s.PublicAccountMove(ctx); e != nil || m != nil {
		t.Fatalf("completed move still active %v %v", m, e)
	}
}
func TestAccountMovePreviewCancelStartOverAndMainWebAuthority(t *testing.T) {
	s, owner, member, _ := movePorticoFixture(t)
	ctx := moveWebContext(t)
	phone, e := s.IssueWithDevice(onDevice(t, ownerPhone, "Phone"), owner.Account.ID, owner.Account.PrimaryProfileID, "local", TierOwner, 1)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.PreviewAccountMove(ctx, phone.AccessToken, MovePreviewRequest{To: SignInLocal}); !errors.Is(e, ErrForbidden) {
		t.Fatalf("phone mutated move %v", e)
	}
	v, e := s.PreviewAccountMove(ctx, owner.AccountToken, MovePreviewRequest{To: SignInLocal, StartOver: true})
	if e != nil || v.Devices != nil {
		t.Fatalf("start over preview %+v %v", v, e)
	}
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), `"devices"`) {
		t.Fatal("start over invented device split")
	}
	if e = s.CancelAccountMove(ctx, owner.AccountToken, v.ID, v.Revision); e != nil {
		t.Fatal(e)
	}
	if kind, _ := s.SignInKind(ctx); kind != SignInPortico {
		t.Fatal("cancel flipped kind")
	}
	v, e = s.PreviewAccountMove(ctx, owner.AccountToken, MovePreviewRequest{To: SignInLocal, StartOver: true})
	if e != nil {
		t.Fatal(e)
	}
	q := MoveConfirmRequest{MoveID: v.ID, ExpectedRevision: v.Revision, Username: "owner", Password: "Fresh-owner1!", Confirmation: "wrong"}
	if _, e = s.ConfirmLocalMove(ctx, owner.AccountToken, q); !errors.Is(e, ErrMoveConfirmation) {
		t.Fatalf("unconfirmed erase %v", e)
	}
	q.Confirmation = v.ServerName
	if _, e = s.ConfirmLocalMove(ctx, owner.AccountToken, q); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = s.db.QueryRow(`SELECT count(*) FROM accounts WHERE id=?`, member.ID).Scan(&n); e != nil || n != 0 {
		t.Fatalf("member retained %d %v", n, e)
	}
	if e = s.db.QueryRow(`SELECT count(*) FROM play_history WHERE account_id=?`, member.ID).Scan(&n); e != nil || n != 0 {
		t.Fatalf("member statistics retained %d %v", n, e)
	}
	if e = s.db.QueryRow(`SELECT count(*) FROM play_history WHERE account_id=?`, owner.Account.ID).Scan(&n); e != nil || n != 1 {
		t.Fatalf("owner statistics lost %d %v", n, e)
	}
}
func TestMoveOldPasswordOnlyIssuesBoundSingleUseLinkTicket(t *testing.T) {
	s, owner, member, _ := memberWithSession(t)
	ctx := moveWebContext(t)
	// Build the committed Waiting state through the same relational preparation
	// used by the authenticated claim-return writer; no Hosted transport is mocked.
	e := dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		c, e := s.moveOwnerTx(ctx, tx, owner.AccountToken)
		if e != nil {
			return e
		}
		_, e = tx.Exec(`INSERT INTO identity_moves(id,owner_id,owner_revision,from_kind,to_kind,start_over,stage,revision,people,devices,profiles,server_name,created_ms) VALUES('move-ticket',?,1,'local','portico',0,'moving_to_portico',1,1,2,2,'Fixture',?)`, owner.Account.ID, time.Now().UnixMilli())
		if e != nil {
			return e
		}
		if e = s.prepareMoveMembersTx(ctx, tx, c, AccountMove{ID: "move-ticket", To: SignInPortico}); e != nil {
			return e
		}
		if _, e = tx.Exec(`UPDATE identity_state SET sign_in_kind='portico';INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES(?,'acc_owner')`, owner.Account.ID); e != nil {
			return e
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if m, ends, e := s.PublicAccountMoveWindow(ctx); e != nil || m == nil || *m != "to_portico" || !ends.After(time.Now().Add(29*24*time.Hour)) || ends.After(time.Now().Add(31*24*time.Hour)) {
		t.Fatalf("a waiting member must see the move and when it ends %v %v %v", m, ends, e)
	}
	ticket, e := s.ProveMovePassword(ctx, MovePasswordRequest{Username: "member", Password: "Testing2!"})
	if e != nil || len(ticket.Ticket) != 43 {
		t.Fatalf("ticket %+v %v", ticket, e)
	}
	if _, e = s.Authenticate(ticket.Ticket); !errors.Is(e, ErrUnauthorized) {
		t.Fatalf("link ticket became session %v", e)
	}
	who := PorticoIdentity{AccountID: "acc_member", Username: "member", DisplayName: "Fresh identity", Email: "member@example.test", Consent: "signed"}
	if _, e = s.RedeemPorticoMove(moveWebContext(t), PorticoMoveProof{Ticket: ticket.Ticket, Who: who}); !errors.Is(e, ErrMoveCode) {
		t.Fatalf("other installation took ticket %v", e)
	}
	out, e := s.RedeemPorticoMove(ctx, PorticoMoveProof{Ticket: ticket.Ticket, Who: who})
	if e != nil || out.Account.ID != member.ID {
		t.Fatalf("link %+v %v", out, e)
	}
	if _, e = s.RedeemPorticoMove(ctx, PorticoMoveProof{Ticket: ticket.Ticket, Who: who}); !errors.Is(e, ErrMoveCode) {
		t.Fatalf("ticket replay %v", e)
	}
	// P2-66: the last waiting member reconnected, so no app offers the old sign-in again.
	if m, e := s.PublicAccountMove(ctx); e != nil || m != nil {
		t.Fatalf("move still announced after the last member reconnected %v %v", m, e)
	}
	if _, e = s.ProveMovePassword(ctx, MovePasswordRequest{Username: "member", Password: "Testing2!"}); e == nil {
		t.Fatal("the old password still works after reconnecting")
	}
	var name string
	if e = s.db.QueryRow(`SELECT name FROM direct_profiles WHERE id=?`, member.PrimaryProfileID).Scan(&name); e != nil || name != "Member" {
		t.Fatalf("identity renamed profile %q %v", name, e)
	}
}
func TestMemberDeviceSignOutRetainsBindingsAndOtherDevices(t *testing.T) {
	s, owner, member, first := memberWithSession(t)
	ctx := context.Background()
	second, e := s.DirectLogin(onDevice(t, memberPhone, "Phone"), "member", "Testing2!")
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.Authenticate(owner.Session.AccessToken)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SignOutMemberDevices(ctx, p, member.ID, first.DeviceID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(first.Session.AccessToken); !errors.Is(e, ErrUnauthorized) {
		t.Fatalf("selected device survived %v", e)
	}
	if _, e = s.Authenticate(second.Session.AccessToken); e != nil {
		t.Fatal("other device ended", e)
	}
	if e = s.SignOutMemberDevices(ctx, p, member.ID, ""); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(second.Session.AccessToken); !errors.Is(e, ErrUnauthorized) {
		t.Fatalf("all signout failed %v", e)
	}
	third, e := s.DirectLogin(onDevice(t, memberPhone, "Phone"), "member", "Testing2!")
	if e != nil || third.DeviceID != second.DeviceID {
		t.Fatalf("ordinary signin after signout %+v %v", third, e)
	}
	raw, _ := json.Marshal(third)
	if strings.Contains(string(raw), "deviceApprovalPending") {
		t.Fatal("retired approval DTO")
	}
}

// P2-66: a move that left nobody waiting (an owner alone, or everyone already back) is not
// announced, so a fresh or fully reconnected Portico server never offers the old sign-in.
func TestMoveWithNobodyWaitingIsNotAnnounced(t *testing.T) {
	s, owner, _, _ := memberWithSession(t)
	ctx := moveWebContext(t)
	if m, e := s.PublicAccountMove(ctx); e != nil || m != nil {
		t.Fatalf("fresh server announced a move %v %v", m, e)
	}
	if _, e := s.db.Exec(`INSERT INTO identity_moves(id,owner_id,owner_revision,from_kind,to_kind,start_over,stage,revision,people,devices,profiles,server_name,created_ms) VALUES('move-empty',?,1,'local','portico',0,'moving_to_portico',1,0,1,1,'Fixture',?)`, owner.Account.ID, time.Now().UnixMilli()); e != nil {
		t.Fatal(e)
	}
	if m, e := s.PublicAccountMove(ctx); e != nil || m != nil {
		t.Fatalf("a move with nobody waiting was announced %v %v", m, e)
	}
}
