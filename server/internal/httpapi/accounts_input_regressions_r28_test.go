package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/access"
)

// Keep the global hostile-input battery intact, and exercise this newly added
// command with both real Management authority and a valid narrower session.
func TestPeopleBulkHostileInputIsClientFailureWithoutMutation(t *testing.T) {
	f := administrationFixtureNew(t)
	for _, in := range hostileBodies(t) {
		t.Run(in.name, func(t *testing.T) {
			for _, token := range []string{f.owner, f.member, ""} {
				w := f.call("POST", "/v1/admin/access/members/bulk", token, in.body)
				if w.Code < 400 || w.Code >= 500 {
					t.Fatalf("hostile bulk: %d %s", w.Code, w.Body)
				}
			}
		})
	}
	var changed int
	if e := f.deps.DB.QueryRow(`SELECT count(*) FROM direct_memberships WHERE disabled<>0 OR revision<>1`).Scan(&changed); e != nil || changed != 0 {
		t.Fatalf("hostile request changed memberships: %d %v", changed, e)
	}
	var receipts int
	if e := f.deps.DB.QueryRow(`SELECT count(*) FROM access_receipts`).Scan(&receipts); e != nil || receipts != 0 {
		t.Fatalf("hostile request wrote receipts: %d %v", receipts, e)
	}
}

func TestInvitationWrongKindIsRefusalAndNeverSpendsCode(t *testing.T) {
	f := administrationFixtureNew(t)
	w := f.call("POST", "/v1/admin/access/invitations", f.owner, `{"operationId":"invite-kind-0001","role":"member","allowedLibraries":[],"expiresInHours":24}`)
	if w.Code != 201 {
		t.Fatalf("invite: %d %s", w.Code, w.Body)
	}
	var invite access.Invitation
	if e := json.Unmarshal(w.Body.Bytes(), &invite); e != nil || invite.Code == "" {
		t.Fatalf("invite response: %+v %v", invite, e)
	}
	// Preserve the actual kind invariant rather than weakening the trigger for a
	// fixture. A Portico server has no local password hashes.
	if _, e := f.deps.DB.Exec(`UPDATE accounts SET password_hash=x''; UPDATE identity_state SET sign_in_kind='portico' WHERE singleton=1`); e != nil {
		t.Fatal(e)
	}
	w = f.call("POST", "/v1/access/invitations/accept", "", `{"code":"`+invite.Code+`","username":"guest","password":"Correct-Horse-9"}`)
	if w.Code != http.StatusForbidden || errorCode(t, w) != "sign_in_kind_portico" {
		t.Fatalf("wrong-kind accept: %d %s", w.Code, w.Body)
	}
	var state string
	if e := f.deps.DB.QueryRow(`SELECT state FROM access_invitations WHERE id=?`, invite.ID).Scan(&state); e != nil || state != "pending" {
		t.Fatalf("invitation spent: %s %v", state, e)
	}
	var created bool
	if e := f.deps.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM accounts WHERE username='guest')`).Scan(&created); e != nil || created {
		t.Fatalf("wrong-kind account created: %v %v", created, e)
	}
}

func TestUnknownMemberDeviceSignoutIsNotFoundWithoutRetiringAnyFamily(t *testing.T) {
	f := administrationFixtureNew(t)
	var before, after int
	if err := f.deps.DB.QueryRow(`SELECT count(*) FROM authorization_session_families WHERE revoked=1`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"-1", "99999999999999999999", "null", "missing-member"} {
		for _, suffix := range []string{"/devices/sign-out", "/devices/missing-device/sign-out"} {
			path := "/v1/direct/members/" + id + suffix
			for _, token := range []string{f.owner, f.admin} {
				if w := f.call("POST", path, token, `{}`); w.Code != http.StatusNotFound {
					t.Fatalf("unknown authorized target: %d %s", w.Code, w.Body)
				}
			}
			if w := f.call("POST", path, f.member, `{}`); w.Code != http.StatusForbidden {
				t.Fatalf("member reached target lookup: %d %s", w.Code, w.Body)
			}
		}
	}
	if err := f.deps.DB.QueryRow(`SELECT count(*) FROM authorization_session_families WHERE revoked=1`).Scan(&after); err != nil || after != before {
		t.Fatal("unknown target retired a real family", before, after, err)
	}
}
