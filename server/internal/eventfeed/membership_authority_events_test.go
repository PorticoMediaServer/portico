package eventfeed

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestAuthorityHintsReachOnlyTheirOwnAccountAtEveryTier(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := New(db)
	ctx := context.Background()
	for _, tc := range []struct {
		name, authority, account, profile, role string
		want                                    bool
	}{
		{"owner", "local", "alice", "main", "owner", true},
		{"admin", "local", "alice", "main", "admin", true},
		{"member", "local", "alice", "main", "member", true},
		{"another-profile", "local", "alice", "other", "member", true},
		{"another-account", "local", "bob", "main", "owner", false},
		{"another-authority", "hosted", "alice", "main", "owner", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := identity.Principal{Viewer: identity.Viewer{Authority: tc.authority, AccountID: tc.account, ProfileID: tc.profile, Role: tc.role}}
			admin := tc.role == "owner" || tc.role == "admin"
			start, err := h.Read(ctx, p, admin, "")
			if err != nil {
				t.Fatal(err)
			}
			wake, release := h.SubscribeFor(p, admin)
			defer release()
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err := apievents.Append(tx, "authority:local:alice", "authority.changed", "account", "self", "2", nil); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			page, err := h.Read(ctx, p, admin, start.NextAfter)
			want := 0
			if tc.want {
				want = 1
			}
			if err != nil || len(page.Events) != want {
				t.Fatalf("own-account authority hint: %+v %v", page, err)
			}
			if tc.want && (page.Events[0].Type != "authority.changed" || page.Events[0].Revision != "2" || len(page.Events[0].Data) != 0) {
				t.Fatal("authority hint leaked more than its own revision", page.Events)
			}
			h.wakeAudiences(map[string]bool{"authority:local:alice": true})
			select {
			case <-wake:
				if !tc.want {
					t.Fatal("another account's authority change woke this reader")
				}
			default:
				if tc.want {
					t.Fatal("own authority change did not wake this reader")
				}
			}
		})
	}
}

func TestAnotherAccountsRoleDoesNotRevealAuthorityOrAdvanceMyRevision(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('alice','alice',x'00','alice-main'),('bob','bob',x'00','bob-main')`); err != nil {
		t.Fatal(err)
	}
	h := New(db)
	ctx := context.Background()
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "bob", ProfileID: "bob-main", Role: "member"}}
	start, err := h.Read(ctx, p, false, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"admin", "member", "admin"} {
		if _, err := db.Exec(`UPDATE direct_memberships SET role=?,revision=revision+1 WHERE account_id='alice'`, role); err != nil {
			t.Fatal(err)
		}
	}
	otherChanges, err := h.Read(ctx, p, false, start.NextAfter)
	if err != nil || len(otherChanges.Events) != 0 {
		t.Fatalf("another account's role change emitted an event: %+v %v", otherChanges, err)
	}
	var revision int64
	if err := db.QueryRow(`SELECT revision FROM direct_memberships WHERE account_id='bob'`).Scan(&revision); err != nil || revision != 1 {
		t.Fatalf("another account advanced my membership revision: %d %v", revision, err)
	}
	if _, err := db.Exec(`UPDATE direct_memberships SET allowed_libraries='["one"]',revision=revision+1 WHERE account_id='bob'`); err != nil {
		t.Fatal(err)
	}
	own, err := h.Read(ctx, p, false, otherChanges.NextAfter)
	if err != nil || len(own.Events) != 1 || own.Events[0].Type != "authority.changed" || own.Events[0].Revision != "2" {
		t.Fatalf("authority revision counted another account's activity: %+v %v", own, err)
	}
}

func TestRoleBoundResyncKeepsOnlyTheOwnAuthorityHint(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := New(db)
	ctx := context.Background()
	for _, wasAdmin := range []bool{false, true} {
		p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "alice", ProfileID: "main"}}
		start, err := h.Read(ctx, p, wasAdmin, "")
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for _, row := range []struct{ audience, kind, resource, revision string }{
			{"admin", "private.admin", "secret", ""},
			{"authority:local:bob", "authority.changed", "self", "99"},
			{"authority:local:alice", "authority.changed", "self", "2"},
		} {
			if err := apievents.Append(tx, row.audience, row.kind, "account", row.resource, row.revision, nil); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		page, err := h.Read(ctx, p, !wasAdmin, start.NextAfter)
		if err != nil || len(page.Events) != 2 || page.Events[0].Type != "stream.resync" || page.Events[1].Type != "authority.changed" || page.Events[1].Revision != "2" || page.Events[1].ID != page.NextAfter || page.Events[0].ID == page.Events[1].ID {
			t.Fatalf("role resync lost the own authority hint or replayed old authority: %+v %v", page, err)
		}
		p.AccountID = "another"
		foreign, err := h.Read(ctx, p, !wasAdmin, start.NextAfter)
		if err != nil || len(foreign.Events) != 1 || foreign.Events[0].Type != "stream.resync" {
			t.Fatalf("foreign cursor revealed membership revision: %+v %v", foreign, err)
		}
	}
}
