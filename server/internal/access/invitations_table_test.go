package access

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func invitationTableActor(t *testing.T, db *sql.DB, issuer *identity.Service, id, profile, role string) (identity.Principal, Authorize) {
	t.Helper()
	env, err := issuer.Issue(id, profile, "local", role, 1)
	if err != nil {
		t.Fatal(err)
	}
	p, err := issuer.Authenticate(env.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	return p, func(ctx context.Context, tx *sql.Tx) error {
		return issuer.RequireMainProfileTx(ctx, tx, p)
	}
}

func TestInvitationTablePagesSearchStatusCountsAndPrivateScope(t *testing.T) {
	s, db, _ := localFixture(t)
	ctx := context.Background()
	issuer, err := identity.New(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner, authorize := invitationTableActor(t, db, issuer, "owner", "owner-profile", identity.TierOwner)
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('admin','admin',X'00','admin-profile',1); UPDATE direct_memberships SET role='admin' WHERE account_id='admin'`); err != nil {
		t.Fatal(err)
	}
	admin, authorizeAdmin := invitationTableActor(t, db, issuer, "admin", "admin-profile", identity.TierAdmin)
	base := time.Now().UTC().Truncate(time.Second)
	states := map[string]int{"pending": 0, "accepted": 0, "revoked": 0, "expired": 0}
	wantCreated := map[string]int64{}
	var hidden Invitation
	var pendingBeyondFirst string
	for i := 0; i < 137; i++ {
		// Several records share exact milliseconds; others share only the wire
		// timestamp's second. Both must page without a duplicate or omission.
		created := base.Add(time.Duration(i/3) * time.Millisecond)
		s.Now = func() time.Time { return created }
		notes := fmt.Sprintf("Recipient %03d", i)
		role := identity.TierMember
		if i%5 == 0 {
			role = identity.TierAdmin
		}
		if i == 5 {
			notes = "private-peer-needle"
		}
		hours := 168
		if i < 10 {
			hours = 1
		}
		v, e := s.Invite(ctx, authorize, owner, InvitationRequest{OperationID: fmt.Sprintf("table-invite-%03d", i), Notes: &notes, Role: role, ExpiresInHours: hours})
		if e != nil {
			t.Fatal(e)
		}
		if i == 5 {
			hidden = v
		}
		if i == 21 {
			pendingBeyondFirst = v.ID
		}
		wantCreated[v.ID] = created.UnixMilli()
		state := "pending"
		if i%4 == 0 {
			if _, e = s.RevokeInvitation(ctx, authorize, v.ID, InvitationRevoke{ExpectedRevision: v.Revision, OperationID: fmt.Sprintf("table-revoke-%03d", i)}); e != nil {
				t.Fatal(e)
			}
			state = "revoked"
		} else if i == 11 {
			hash, e := HashPassword("Correct-Horse-9")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.Accept(ctx, Acceptance{Code: v.Code, Username: "accepted-person", Password: "Correct-Horse-9"}, hash); e != nil {
				t.Fatal(e)
			}
			state = "accepted"
		} else if i < 10 {
			state = "expired"
		}
		states[state]++
	}
	s.Now = func() time.Time { return base.Add(2 * time.Hour) }
	for _, sort := range []string{"sent", "status"} {
		for _, direction := range []string{"asc", "desc"} {
			t.Run(sort+"/"+direction, func(t *testing.T) {
				seen := map[string]bool{}
				cursor, previous := "", ""
				for {
					page, total, matched, e := s.InvitationTable(ctx, authorize, owner, InvitationQuery{Limit: "17", Cursor: cursor}, "", sort, direction)
					if e != nil || total != 137 || matched != 137 {
						t.Fatalf("counts %d/%d: %v", total, matched, e)
					}
					for _, row := range page.Items {
						if seen[row.ID] {
							t.Fatal("duplicate invitation", row.ID)
						}
						seen[row.ID] = true
						key := fmt.Sprintf("%020d/%s", wantCreated[row.ID], row.ID)
						if sort == "status" {
							key = row.State + "/" + key
						}
						if previous != "" && ((direction == "asc" && previous >= key) || (direction == "desc" && previous <= key)) {
							t.Fatal("out-of-order invitations", previous, key)
						}
						previous = key
					}
					cursor = page.NextCursor
					if cursor == "" {
						break
					}
					if _, _, _, e = s.InvitationTable(ctx, authorize, owner, InvitationQuery{Cursor: cursor}, "other-query", sort, direction); !errors.Is(e, ErrInvalid) {
						t.Fatal("cursor crossed query", e)
					}
				}
				if len(seen) != 137 {
					t.Fatalf("read %d invitations", len(seen))
				}
			})
		}
	}
	// A pending record outside the unfiltered first page remains discoverable
	// when filtering happens before pagination, for both authorised roles.
	for _, reader := range []struct {
		principal identity.Principal
		authorize Authorize
	}{{owner, authorize}, {admin, authorizeAdmin}} {
		unfiltered, _, _, e := s.InvitationTable(ctx, reader.authorize, reader.principal, InvitationQuery{Limit: "50"}, "", "sent", "desc")
		if e != nil {
			t.Fatal(e)
		}
		for _, row := range unfiltered.Items {
			if row.ID == pendingBeyondFirst {
				t.Fatal("pending record unexpectedly in first unfiltered page")
			}
		}
		cursor, found := "", false
		for {
			page, _, _, e := s.InvitationTable(ctx, reader.authorize, reader.principal, InvitationQuery{State: "pending", Limit: "50", Cursor: cursor}, "", "sent", "desc")
			if e != nil {
				t.Fatal(e)
			}
			for _, row := range page.Items {
				if row.State != "pending" {
					t.Fatal("non-pending row", row.ID)
				}
				if row.ID == pendingBeyondFirst {
					found = true
				}
			}
			cursor = page.NextCursor
			if cursor == "" {
				break
			}
		}
		if !found {
			t.Fatal("pending record beyond unfiltered first page was lost")
		}
	}
	for state, want := range states {
		page, total, matched, e := s.InvitationTable(ctx, authorize, owner, InvitationQuery{State: state, Limit: "100"}, "", "sent", "desc")
		if e != nil || total != 137 || matched != int64(want) || len(page.Items) != want {
			t.Fatalf("%s count %d/%d rows %d want %d: %v", state, total, matched, len(page.Items), want, e)
		}
	}
	page, total, matched, err := s.InvitationTable(ctx, authorize, owner, InvitationQuery{}, "private-peer-needle", "", "")
	if err != nil || total != 137 || matched != 1 || len(page.Items) != 1 || page.Items[0].ID != hidden.ID {
		t.Fatalf("owner's visible notes: %d/%d %+v %v", total, matched, page, err)
	}
	_, total, matched, err = s.InvitationTable(ctx, authorizeAdmin, admin, InvitationQuery{}, "private-peer-needle", "", "")
	if err != nil || total != 137 || matched != 0 {
		t.Fatalf("administrator inferred a peer note: %d/%d %v", total, matched, err)
	}
	page, _, matched, err = s.InvitationTable(ctx, authorizeAdmin, admin, InvitationQuery{}, "recipient 006", "", "")
	if err != nil || matched != 1 || len(page.Items) != 1 || page.Items[0].Notes == nil || *page.Items[0].Notes != "Recipient 006" || page.Items[0].PorticoUsername != "" {
		t.Fatalf("administrator's visible member notes: %+v %v", page, err)
	}
	if _, err = db.Exec(`UPDATE direct_memberships SET role='member',revision=revision+1 WHERE account_id='admin'`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.InvitationTable(ctx, authorizeAdmin, admin, InvitationQuery{}, "", "", ""); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatal("old administrator authority read invitations", err)
	}
	// A revoked captured family cannot read even otherwise public table facts.
	if err = issuer.Logout(owner); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.InvitationTable(ctx, authorize, owner, InvitationQuery{}, "", "", ""); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatal("retired authority read invitations", err)
	}
}

func TestInvitationTableRejectsMalformedAndScopeSubstitutedCursors(t *testing.T) {
	s, db, _ := localFixture(t)
	issuer, err := identity.New(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, auth := invitationTableActor(t, db, issuer, "owner", "owner-profile", identity.TierOwner)
	for i := 0; i < 3; i++ {
		if _, err = s.Invite(t.Context(), auth, p, InvitationRequest{OperationID: fmt.Sprintf("cursor-invite-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	page, _, _, err := s.InvitationTable(t.Context(), auth, p, InvitationQuery{Limit: "1"}, "", "", "")
	if err != nil || page.NextCursor == "" {
		t.Fatal("first page", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(page.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	for _, cursor := range []string{"garbage", page.NextCursor + "=", page.NextCursor[:8] + "\r\n" + page.NextCursor[8:], base64.RawURLEncoding.EncodeToString(append(raw, ' ')), base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw), `"id":`, `"unknown":1,"id":`, 1))), base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw), `"id":`, `"id":"duplicate","id":`, 1)))} {
		if _, _, _, err = s.InvitationTable(t.Context(), auth, p, InvitationQuery{Cursor: cursor}, "", "", ""); !errors.Is(err, ErrInvalid) {
			t.Fatalf("malformed cursor accepted: %v", err)
		}
	}
	for _, q := range []InvitationQuery{{Limit: "9999"}, {State: "unknown"}} {
		if _, _, _, err = s.InvitationTable(t.Context(), auth, p, q, "", "", ""); !errors.Is(err, ErrInvalid) {
			t.Fatal("malformed query accepted", err)
		}
	}
	for _, c := range [][3]string{{strings.Repeat("x", 161), "", ""}, {"x\n", "", ""}, {"", "notes", ""}, {"", "sent", "sideways"}} {
		if _, _, _, err = s.InvitationTable(t.Context(), auth, p, InvitationQuery{}, c[0], c[1], c[2]); !errors.Is(err, ErrInvalid) {
			t.Fatal("malformed controls accepted", err)
		}
	}
	other := p
	other.AccountID = "member"
	if _, _, _, err = s.InvitationTable(t.Context(), auth, other, InvitationQuery{Cursor: page.NextCursor}, "", "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatal("cursor crossed actor", err)
	}
}

func TestInvitationTableExpiryIsNotRevocationAndReadSnapshotIsCommon(t *testing.T) {
	s, db, _ := localFixture(t)
	issuer, err := identity.New(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, auth := invitationTableActor(t, db, issuer, "owner", "owner-profile", identity.TierOwner)
	base := time.Now().UTC()
	s.Now = func() time.Time { return base }
	old, err := s.Invite(t.Context(), auth, p, InvitationRequest{OperationID: "expiry-offer", ExpiresInHours: 1})
	if err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return base.Add(2 * time.Hour) }
	if _, err = s.Invite(t.Context(), auth, p, InvitationRequest{OperationID: "next-live-offer"}); err != nil {
		t.Fatal(err)
	}
	var state string
	var revision int64
	if err = db.QueryRow(`SELECT state,revision FROM access_invitations WHERE id=?`, old.ID).Scan(&state, &revision); err != nil || state != "pending" || revision != old.Revision {
		t.Fatalf("expiry rewrote administrator history: %s/%d %v", state, revision, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	injected := false
	page, total, matched, err := s.InvitationTable(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if e := auth(ctx, tx); e != nil {
			return e
		}
		injected = true
		// The actor read established this SQLite snapshot. A real concurrent
		// invitation commits before count/page, and belongs only to a refresh.
		_, e := s.Invite(ctx, auth, p, InvitationRequest{OperationID: "late-after-authority-read"})
		return e
	}, p, InvitationQuery{}, "", "", "")
	if err != nil || !injected || total != 2 || matched != 2 || len(page.Items) != 2 {
		t.Fatalf("read mixed snapshots: %d/%d rows%d injected%v: %v", total, matched, len(page.Items), injected, err)
	}
	page, total, matched, err = s.InvitationTable(ctx, auth, p, InvitationQuery{State: "expired"}, "", "", "")
	if err != nil || total != 3 || matched != 1 || len(page.Items) != 1 || page.Items[0].ID != old.ID || page.Items[0].State != "expired" {
		t.Fatalf("refresh/expiry facts: %d/%d %+v %v", total, matched, page, err)
	}
}

func TestInvitationTableSearchesActualPorticoRecipientWithoutInventingEmail(t *testing.T) {
	s, db, _ := fixture(t)
	if _, err := db.Exec(`UPDATE accounts SET password_hash=X''; INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'portico'); INSERT INTO account_portico_links(account_id,hosted_account_id) VALUES('owner','acc_owner')`); err != nil {
		t.Fatal(err)
	}
	issuer, err := identity.New(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The identity service receives verified account facts at this boundary;
	// this is not a Hosted signature/transport acceptance test.
	signIn, err := issuer.PorticoSignIn(t.Context(), identity.PorticoIdentity{AccountID: "acc_owner", Username: "owner", DisplayName: "Owner", SignerKeyID: "invitation-table-fixture"})
	if err != nil || signIn.Session == nil {
		t.Fatal("Portico admission", err)
	}
	selection, err := issuer.SelectDirectProfile(t.Context(), signIn.Session.AccessToken, signIn.Account.PrimaryProfileID, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := issuer.Authenticate(selection.Session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	auth := func(ctx context.Context, tx *sql.Tx) error { return issuer.RequireMainProfileTx(ctx, tx, p) }
	v, err := s.Invite(t.Context(), auth, p, InvitationRequest{OperationID: "real-bound-recipient", PorticoUsername: "Portico_Recipient"})
	if err != nil {
		t.Fatal(err)
	}
	page, total, matched, err := s.InvitationTable(t.Context(), auth, p, InvitationQuery{}, "PORTICO_RECIPIENT", "", "")
	if err != nil || total != 1 || matched != 1 || len(page.Items) != 1 || page.Items[0].ID != v.ID || page.Items[0].PorticoUsername != "portico_recipient" || page.Items[0].Notes != nil {
		t.Fatalf("genuine bound recipient: %d/%d %+v %v", total, matched, page, err)
	}
}

// The join card takes library categories from the same genuine rows as names;
// misleading names and subsequent renames cannot invent a film/TV/book icon.
func TestInvitationPreviewCarriesOnlyActualLibraryKinds(t *testing.T) {
	s, db, _ := localFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE libraries SET name='TV shows',kind='movie' WHERE id='lib';UPDATE libraries SET name='Films',kind='show' WHERE id='other';INSERT INTO libraries(id,name,kind,root) VALUES('music-preview','Books','music','/music-preview'),('books-preview','Music','book','/books-preview')`); err != nil {
		t.Fatal(err)
	}
	issuer, err := identity.New(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	owner, authorize := invitationTableActor(t, db, issuer, "owner", "owner-profile", identity.TierOwner)
	invite, err := s.Invite(ctx, authorize, owner, InvitationRequest{OperationID: "preview-actual-kinds", Role: identity.TierMember, AllowedLibraries: []string{"lib", "other", "music-preview", "books-preview"}})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.Preview(ctx, invite.Code)
	if err != nil || len(preview.Libraries) != 4 {
		t.Fatalf("preview %+v: %v", preview, err)
	}
	kinds := map[string]string{"TV shows": "movie", "Films": "show", "Books": "music", "Music": "book"}
	for _, library := range preview.Libraries {
		if kinds[library.Name] != library.Kind {
			t.Fatalf("category inferred from display name: %+v", library)
		}
	}
	if _, err = db.Exec(`UPDATE libraries SET name='Not a category label' WHERE id='lib'`); err != nil {
		t.Fatal(err)
	}
	preview, err = s.Preview(ctx, invite.Code)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, library := range preview.Libraries {
		if library.Name == "Not a category label" {
			found = true
			if library.Kind != "movie" {
				t.Fatal("rename changed genuine kind")
			}
		}
	}
	if !found {
		t.Fatal("preview did not read current genuine library row")
	}
	if _, err = db.Exec(`UPDATE access_invitations SET allowed_libraries='["unresolvable-library"]' WHERE id=?`, invite.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Preview(ctx, invite.Code); !errors.Is(err, ErrInvitationNotFound) {
		t.Fatalf("missing library was fabricated: %v", err)
	}
}
