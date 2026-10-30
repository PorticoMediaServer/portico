package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Sign-up set to "invitation only", wired as the server wires it: an account
// is created only against an unspent member invitation, gets that
// invitation's libraries, and the invitation is spent exactly once.
func TestInviteOnlySignUpCreatesAnAccountAgainstAnInvitation(t *testing.T) {
	f := administrationFixtureNew(t)
	c := catalogtest.New(t, f.deps.DB)
	c.Library("invite-movies", "Invited movies", "movie", t.TempDir())
	c.Drain()
	f.deps.Identity.RedeemInvitation = f.deps.Access.Access.RegistrationRedeemer()
	if err := f.deps.Identity.SetSelfRegistration(identity.SelfRegistrationInvite); err != nil {
		t.Fatal(err)
	}
	issued := f.call("POST", "/v1/admin/access/invitations", f.owner, `{"operationId":"sign-up-invite-1","role":"member","allowedLibraries":["invite-movies"]}`)
	var invite access.Invitation
	if err := json.Unmarshal(issued.Body.Bytes(), &invite); err != nil || issued.Code != 201 || invite.Code == "" {
		t.Fatal("actual owner invitation", issued.Code, err)
	}
	register := func(username, code string) (int, string, identity.DirectSignIn) {
		body, err := json.Marshal(map[string]string{"username": username, "password": "Testing1!", "invitationCode": code})
		if err != nil {
			t.Fatal(err)
		}
		w := f.call("POST", "/v1/auth/register", "", string(body))
		var out identity.DirectSignIn
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, v1Code(w), out
	}
	if code, name, _ := register("nocode", ""); code != 403 || name != "registration_invitation_required" {
		t.Fatalf("sign-up with no invitation: %d %s", code, name)
	}
	if code, name, _ := register("badcode", "this-is-not-an-invitation-code-at-all-0000"); code != 403 || name != "registration_invitation_required" {
		t.Fatalf("sign-up with a made-up invitation: %d %s", code, name)
	}
	// A taken username does not cost the invitee their invitation.
	if code, name, _ := register("owner", invite.Code); code != 409 || name != "username_taken" {
		t.Fatalf("sign-up with a taken username: %d %s", code, name)
	}
	code, _, out := register("invited", invite.Code)
	if code != 201 || out.Session == nil || out.Account.Role != identity.TierMember || len(out.Account.AllowedLibraries) != 1 || out.Account.AllowedLibraries[0] != "invite-movies" {
		t.Fatalf("sign-up with the invitation: %d %+v", code, out.Account)
	}
	if w := f.call("GET", "/v1/me", out.Session.AccessToken, ""); w.Code != 200 {
		t.Fatal("registered session", w.Code, w.Body)
	}
	if code, name, _ := register("second", invite.Code); code != 403 || name != "registration_invitation_required" {
		t.Fatalf("the same invitation used twice: %d %s", code, name)
	}
	var accounts int
	if err := f.deps.DB.QueryRow(`SELECT count(*) FROM accounts WHERE username IN('nocode','badcode','second')`).Scan(&accounts); err != nil || accounts != 0 {
		t.Fatalf("a refused sign-up left %d accounts: %v", accounts, err)
	}
}
