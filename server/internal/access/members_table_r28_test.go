package access

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"testing"
)

func TestPeopleTableStablePagingVisibleSearchAndBulkAtomicity(t *testing.T) {
	s, db, _ := fixture(t)
	ctx := context.Background()
	issuer, e := identity.New(db, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		for i := 0; i < 137; i++ {
			id := fmt.Sprintf("person-%03d", i)
			if _, e := tx.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,email) VALUES(?,?,X'00',?,?)`, id, id, id+"-profile", id+"@example.test"); e != nil {
				return e
			}
		}
		_, e := tx.Exec(`UPDATE direct_memberships SET notes='private-never-search' WHERE account_id='person-001'`)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	ids := map[string]bool{}
	cursor := ""
	for {
		page, e := s.Members(ctx, nil, MemberQuery{Limit: "23", Cursor: cursor, Search: "person-", Sort: "person", Direction: "asc"})
		if e != nil {
			t.Fatal(e)
		}
		if page.TotalCount != 139 || page.MatchedCount != 137 {
			t.Fatalf("counts %+v", page)
		}
		for _, row := range page.Items {
			if ids[row.AccountID] {
				t.Fatal("duplicate", row.AccountID)
			}
			ids[row.AccountID] = true
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
		if _, e = s.Members(ctx, nil, MemberQuery{Cursor: cursor, Search: "different"}); !errors.Is(e, ErrInvalid) {
			t.Fatalf("cursor crossed query %v", e)
		}
	}
	if len(ids) != 137 {
		t.Fatal("missing", len(ids))
	}
	page, e := s.Members(ctx, nil, MemberQuery{Search: "private-never-search"})
	if e != nil || page.MatchedCount != 0 {
		t.Fatalf("private notes inferred %+v %v", page, e)
	}
	page, e = s.Members(ctx, nil, MemberQuery{Role: "admin"})
	if e != nil || page.MatchedCount != 1 {
		t.Fatalf("role filter %d %v", page.MatchedCount, e)
	}
	actor := identity.Principal{Viewer: identity.Viewer{AccountID: "owner", ProfileID: "owner-profile", Authority: "local", Role: "owner"}}
	q := MemberBulkRequest{OperationID: "bulk-atomic-0001", Action: "disable", Targets: []MemberBulkTarget{{"person-001", 1, 1}, {"person-002", 999, 1}}}
	if _, e = s.BulkMembers(ctx, nil, issuer, actor, q); !errors.Is(e, ErrConflict) {
		t.Fatalf("stale %v", e)
	}
	var off bool
	if e = db.QueryRow(`SELECT disabled FROM direct_memberships WHERE account_id='person-001'`).Scan(&off); e != nil || off {
		t.Fatalf("partial bulk %v %v", off, e)
	}
	q.Targets[1].ExpectedRevision = 1
	out, e := s.BulkMembers(ctx, nil, issuer, actor, q)
	if e != nil || len(out.AccountIDs) != 2 {
		t.Fatalf("bulk %+v %v", out, e)
	}
	if _, e = s.BulkMembers(ctx, nil, issuer, actor, q); e != nil {
		t.Fatal("idempotent replay", e)
	}
	q.OperationID = "bulk-owner-refuse"
	q.Targets = []MemberBulkTarget{{"person-003", 1, 1}, {"owner", 1, 1}}
	if _, e = s.BulkMembers(ctx, nil, issuer, actor, q); !errors.Is(e, identity.ErrForbidden) {
		t.Fatalf("hierarchy %v", e)
	}
	if e = db.QueryRow(`SELECT disabled FROM direct_memberships WHERE account_id='person-003'`).Scan(&off); e != nil || off {
		t.Fatal("partial hierarchy", off, e)
	}
}
func TestPersonalAccessTokenPermissionRevokesInsideWriter(t *testing.T) {
	s, db, _ := fixture(t)
	ctx := context.Background()
	issuer, e := identity.New(db, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	env, e := issuer.Issue("member", "member-profile", "local", "member", 1)
	if e != nil {
		t.Fatal(e)
	}
	maker, e := issuer.Authenticate(env.AccessToken)
	if e != nil {
		t.Fatal(e)
	}
	issued, e := s.CreateCredential(ctx, nil, issuer, maker, identity.CredentialKindToken, CredentialRequest{OperationID: "token-permission-1", Name: "Personal", Scopes: []string{"library"}})
	if e != nil {
		t.Fatal(e)
	}
	doc, e := s.Limits(ctx, nil, "member")
	if e != nil || !doc.Limits.AccessTokens {
		t.Fatal("default", doc, e)
	}
	doc.Limits.AccessTokens = false
	if _, e = s.SetLimits(ctx, nil, "member", LimitsChange{ExpectedRevision: doc.Revision, OperationID: "disable-tokens-1", Limits: doc.Limits}); e != nil {
		t.Fatal(e)
	}
	var revoked int64
	var familyRevoked bool
	if e = db.QueryRow(`SELECT c.revoked_ms,f.revoked FROM api_credentials c JOIN authorization_session_families f ON f.id=c.family_id WHERE c.id=?`, issued.Credential.ID).Scan(&revoked, &familyRevoked); e != nil || revoked == 0 || !familyRevoked {
		t.Fatalf("token not retired %d %v %v", revoked, familyRevoked, e)
	}
	if _, e = s.CreateCredential(ctx, nil, issuer, maker, identity.CredentialKindToken, CredentialRequest{OperationID: "token-permission-2", Name: "Blocked", Scopes: []string{"library"}}); !errors.Is(e, ErrAccessTokensOff) {
		t.Fatalf("creation survived permission %v", e)
	}
}
