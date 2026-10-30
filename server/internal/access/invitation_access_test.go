package access

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/streamlimits"
)

func limitedAccess() *InvitationAccess {
	return &InvitationAccess{MaxStreamsMode: streamlimits.Limit, MaxStreams: 2, AllowRemote: false, AccessTokens: false, MaxContentRating: " pg-13 "}
}

func memberLimits(t *testing.T, db *sql.DB, account string) LimitsDocument {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	out, err := readLimits(tx, account)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertGranted(t *testing.T, got LimitsDocument) {
	t.Helper()
	l := got.Limits
	if l.MaxStreamsMode != streamlimits.Limit || l.MaxStreams != 2 || l.AllowRemote || l.AccessTokens || l.MaxContentRating != "PG-13" || l.MaximumAge != 13 {
		t.Fatalf("the invitation's access was not applied: %+v", l)
	}
	// Everything the invitation does not carry starts at the default.
	d := DefaultLimits()
	if l.MaxProfiles != d.MaxProfiles || l.Conversion != d.Conversion || !l.AllowUnrated || len(l.Schedule.Windows) != 0 {
		t.Fatalf("an invitation changed limits it does not carry: %+v", l)
	}
}

func TestInvitationAccessIsStoredValidatedAndRead(t *testing.T) {
	store, _, _ := fixture(t)
	ctx := context.Background()
	plain, err := store.Invite(ctx, nil, owner(), InvitationRequest{OperationID: "access-default", Role: identity.TierMember})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Access != DefaultInvitationAccess() {
		t.Fatalf("an invitation without access did not read as the defaults: %+v", plain.Access)
	}
	limited, err := store.Invite(ctx, nil, owner(), InvitationRequest{OperationID: "access-limited", Role: identity.TierMember, Access: limitedAccess()})
	if err != nil {
		t.Fatal(err)
	}
	want := InvitationAccess{MaxStreamsMode: streamlimits.Limit, MaxStreams: 2, MaxContentRating: "PG-13"}
	if limited.Access != want {
		t.Fatalf("created: %+v", limited.Access)
	}
	read, err := store.Invitation(ctx, nil, limited.ID)
	if err != nil || read.Access != want {
		t.Fatalf("read: %v %+v", err, read.Access)
	}
	page, _, _, err := store.InvitationTable(ctx, nil, owner(), InvitationQuery{}, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range page.Items {
		found = found || item.ID == limited.ID && item.Access == want
	}
	if !found {
		t.Fatalf("the invitations table lost the access: %+v", page.Items)
	}
	// A replay of the operation returns the same invitation; changing the
	// grant under the same operation is refused.
	replay, err := store.Invite(ctx, nil, owner(), InvitationRequest{OperationID: "access-limited", Role: identity.TierMember, Access: limitedAccess()})
	if err != nil || replay.ID != limited.ID || replay.Code != "" {
		t.Fatalf("replay: %v %+v", err, replay)
	}
	if _, err = store.Invite(ctx, nil, owner(), InvitationRequest{OperationID: "access-limited", Role: identity.TierMember, Access: &InvitationAccess{MaxStreamsMode: streamlimits.Unlimited}}); !errors.Is(err, ErrIdempotencyKeyReused) {
		t.Fatalf("a changed grant reused an operation: %v", err)
	}
	for name, bad := range map[string]InvitationAccess{
		"zero streams is not unlimited": {MaxStreamsMode: streamlimits.Limit, MaxStreams: 0},
		"a count with inherit":          {MaxStreamsMode: streamlimits.Inherit, MaxStreams: 3},
		"an unknown mode":               {MaxStreamsMode: "some"},
		"too many streams":              {MaxStreamsMode: streamlimits.Limit, MaxStreams: 1001},
		"an unknown rating":             {MaxStreamsMode: streamlimits.Inherit, MaxContentRating: "NOT-A-RATING"},
	} {
		bad := bad
		if _, err = store.Invite(ctx, nil, owner(), InvitationRequest{OperationID: "bad-" + name, Role: identity.TierMember, Access: &bad}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s was accepted: %v", name, err)
		}
	}
}

func TestInvitationAccessEditReplacesOrKeepsTheGrant(t *testing.T) {
	store, _, _ := fixture(t)
	ctx := context.Background()
	invite, err := store.Invite(ctx, nil, owner(), InvitationRequest{OperationID: "edit-access", Role: identity.TierMember, Access: limitedAccess()})
	if err != nil {
		t.Fatal(err)
	}
	kept, err := store.EditInvitation(ctx, nil, owner(), invite.ID, InvitationChange{ExpectedRevision: invite.Revision, OperationID: "edit-keep", Role: identity.TierMember, ExpiresAt: invite.ExpiresAt})
	if err != nil || kept.Access != invite.Access {
		t.Fatalf("an edit without access changed the grant: %v %+v", err, kept.Access)
	}
	unlimited := InvitationAccess{MaxStreamsMode: streamlimits.Unlimited, AllowRemote: true, AccessTokens: true}
	changed, err := store.EditInvitation(ctx, nil, owner(), invite.ID, InvitationChange{ExpectedRevision: kept.Revision, OperationID: "edit-change", Role: identity.TierMember, ExpiresAt: invite.ExpiresAt, Access: &unlimited})
	if err != nil || changed.Access != unlimited || changed.Revision != kept.Revision+1 {
		t.Fatalf("edit: %v %+v", err, changed)
	}
	bad := InvitationAccess{MaxStreamsMode: streamlimits.Limit}
	if _, err = store.EditInvitation(ctx, nil, owner(), invite.ID, InvitationChange{ExpectedRevision: changed.Revision, OperationID: "edit-bad", Role: identity.TierMember, ExpiresAt: invite.ExpiresAt, Access: &bad}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an invalid edit was accepted: %v", err)
	}
	// A stale revision is a conflict, not a silent overwrite.
	if _, err = store.EditInvitation(ctx, nil, owner(), invite.ID, InvitationChange{ExpectedRevision: kept.Revision, OperationID: "edit-stale", Role: identity.TierMember, ExpiresAt: invite.ExpiresAt, Access: limitedAccess()}); err == nil {
		t.Fatal("a stale edit replaced the grant")
	}
}

func TestLocalAcceptanceAppliesTheInvitationAccessWithTheMembership(t *testing.T) {
	store, db, _ := localFixture(t)
	ctx := context.Background()
	invite, err := store.Invite(ctx, nil, owner(), InvitationRequest{OperationID: "local-access", Role: identity.TierMember, Access: limitedAccess()})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := HashPassword("Correct-Horse-9")
	if err != nil {
		t.Fatal(err)
	}
	joined, err := store.Accept(ctx, Acceptance{Code: invite.Code, Username: "limited", Password: "Correct-Horse-9"}, hash)
	if err != nil {
		t.Fatal(err)
	}
	assertGranted(t, memberLimits(t, db, joined.AccountID))
}

func TestLocalAcceptanceThatFailsWritesNoAccess(t *testing.T) {
	store, db, _ := localFixture(t)
	ctx := context.Background()
	invite, err := store.Invite(ctx, nil, owner(), InvitationRequest{OperationID: "local-taken", Role: identity.TierMember, Access: limitedAccess()})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := HashPassword("Correct-Horse-9")
	if err != nil {
		t.Fatal(err)
	}
	// The username is taken, so nothing is created, and no limits row appears.
	if _, err = store.Accept(ctx, Acceptance{Code: invite.Code, Username: "member", Password: "Correct-Horse-9"}, hash); !errors.Is(err, ErrConflict) {
		t.Fatalf("taken username: %v", err)
	}
	var rows, streams int
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM access_limits),(SELECT count(*) FROM account_stream_limits)`).Scan(&rows, &streams); err != nil || rows != 0 || streams != 0 {
		t.Fatalf("a failed acceptance wrote access: limits=%d streams=%d %v", rows, streams, err)
	}
}

func TestPorticoAcceptanceAppliesAccessOnlyWhereItGrantsMembership(t *testing.T) {
	store, db, _ := fixture(t)
	ctx := context.Background()
	signedIn := func(ctx context.Context, tx *sql.Tx) (identity.DirectSignIn, error) { return identity.DirectSignIn{}, nil }
	accountOf := func(hosted string) string {
		t.Helper()
		var id string
		if err := db.QueryRow(`SELECT account_id FROM account_portico_links WHERE hosted_account_id=?`, hosted).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	// A new member gets the grant.
	first, err := store.Invite(ctx, nil, owner(), InvitationRequest{OperationID: "portico-new", Role: identity.TierMember, Access: limitedAccess()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AcceptPortico(ctx, PorticoAcceptance{Code: first.Code, AccountID: "acc_new", Username: "fresh", DisplayName: "Fresh"}, signedIn); err != nil {
		t.Fatal(err)
	}
	account := accountOf("acc_new")
	assertGranted(t, memberLimits(t, db, account))
	// An active member's limits are left as the owner set them.
	before := memberLimits(t, db, account)
	open, err := store.Invite(ctx, nil, owner(), InvitationRequest{OperationID: "portico-active", Role: identity.TierMember, Access: &InvitationAccess{MaxStreamsMode: streamlimits.Unlimited, AllowRemote: true, AccessTokens: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AcceptPortico(ctx, PorticoAcceptance{Code: open.Code, AccountID: "acc_new", Username: "fresh", DisplayName: "Fresh"}, signedIn); err != nil {
		t.Fatal(err)
	}
	if after := memberLimits(t, db, account); after.Revision != before.Revision || after.Limits.MaxStreamsMode != streamlimits.Limit || after.Limits.AllowRemote {
		t.Fatalf("an invitation rewrote an active member's limits: %+v", after)
	}
	// A turned-off member comes back with exactly the new invitation's grant.
	if _, err = db.Exec(`UPDATE direct_memberships SET disabled=1,disabled_ms=1,revision=revision+1 WHERE account_id=?`, account); err != nil {
		t.Fatal(err)
	}
	back, err := store.Invite(ctx, nil, owner(), InvitationRequest{OperationID: "portico-back", Role: identity.TierMember, Access: &InvitationAccess{MaxStreamsMode: streamlimits.Unlimited, AllowRemote: true, AccessTokens: true, MaxContentRating: "R"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AcceptPortico(ctx, PorticoAcceptance{Code: back.Code, AccountID: "acc_new", Username: "fresh", DisplayName: "Fresh"}, signedIn); err != nil {
		t.Fatal(err)
	}
	restored := memberLimits(t, db, account)
	if restored.Limits.MaxStreamsMode != streamlimits.Unlimited || !restored.Limits.AllowRemote || !restored.Limits.AccessTokens || restored.Limits.MaxContentRating != "R" || restored.Revision != before.Revision+1 {
		t.Fatalf("a restored member did not get the new grant: %+v", restored)
	}
	// A sign-in that fails rolls the membership and its access back together.
	failing, err := store.Invite(ctx, nil, owner(), InvitationRequest{OperationID: "portico-rollback", Role: identity.TierMember, Access: limitedAccess()})
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("sign-in failed")
	if _, err = store.AcceptPortico(ctx, PorticoAcceptance{Code: failing.Code, AccountID: "acc_rollback", Username: "rollback", DisplayName: "Rollback"}, func(context.Context, *sql.Tx) (identity.DirectSignIn, error) {
		return identity.DirectSignIn{}, boom
	}); !errors.Is(err, boom) {
		t.Fatalf("sign-in failure: %v", err)
	}
	var links int
	if err = db.QueryRow(`SELECT count(*) FROM account_portico_links WHERE hosted_account_id='acc_rollback'`).Scan(&links); err != nil || links != 0 {
		t.Fatalf("a failed acceptance left an account: %d %v", links, err)
	}
}

func TestRegistrationRedeemAppliesTheInvitationAccess(t *testing.T) {
	store, db, _ := fixture(t)
	ctx := context.Background()
	invite, err := store.Invite(ctx, nil, owner(), InvitationRequest{OperationID: "register-access", Role: identity.TierMember, Access: limitedAccess()})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.RegistrationRedeemer()(ctx, tx, invite.Code, "member"); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertGranted(t, memberLimits(t, db, "member"))
}
