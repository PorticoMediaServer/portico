package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// memberNamed reads one member as the owner sees them.
func memberNamed(t *testing.T, s *Server, owner, username string) Member {
	t.Helper()
	for _, m := range get[Page[Member]](t, s, owner, "/v1/admin/members").Items {
		if m.Username == username {
			return m
		}
	}
	t.Fatalf("no member %s", username)
	return Member{}
}

// An invitation carries the limits a member is held to, and the account it creates is held to
// them from its first sign-in, joining included: a home-only invitation accepted away is
// refused and stays open, and accepted at home gives an account held to every limit it
// carried. An administrator sets on an invitation only what they could set on a member.
func TestAnInvitationCarriesTheAccountsLimits(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	var inv Invitation
	body := json.RawMessage(`{"limits":{"homeOnly":true,"maxRatingAge":13,"maxProfiles":2}}`)
	if w := call(t, s, "POST", "/v1/admin/invitations", owner, body, &inv); w.Code >= 300 || !inv.Limits.HomeOnly || inv.Limits.MaxRatingAge == nil ||
		inv.Limits.MaxProfiles == nil || !inv.Limits.AllowTokens || !inv.Limits.AllowWatchTogether {
		t.Fatalf("an invitation with limits answered %d %+v", w.Code, inv.Limits)
	}
	accept := func(peer string) *httptest.ResponseRecorder {
		return sent(t, s, peer, "POST", "/v1/auth/invitations/"+inv.Code+"/accept", "", InvitationAccept{Username: "kim", Password: testPassword, Device: device(5)}, nil)
	}
	if w := accept(away); w.Code != 403 || !strings.Contains(w.Body.String(), "away_from_home") {
		t.Fatalf("a home-only invitation accepted away answered %d %s", w.Code, w.Body)
	}
	if w := accept(atHome); w.Code != 200 {
		t.Fatalf("the same invitation accepted at home answered %d %s", w.Code, w.Body)
	}
	got := memberNamed(t, s, owner, "kim").Limits
	if !got.HomeOnly || got.MaxRatingAge == nil || *got.MaxRatingAge != 13 || got.MaxProfiles == nil || *got.MaxProfiles != 2 || !got.AllowTokens {
		t.Fatalf("the account made from the invitation is held to %+v", got)
	}

	addMember(t, s, owner, "ada", "admin")
	admin := signInAs(t, s, "ada", testPassword, 1).AccessToken
	if code := status(t, s, "POST", "/v1/admin/invitations", admin, json.RawMessage(`{"limits":{"maxProfiles":1}}`)); code != 403 {
		t.Errorf("an administrator inviting with fewer profiles answered %d, want 403", code)
	}
	var made Invitation
	if w := call(t, s, "POST", "/v1/admin/invitations", admin, json.RawMessage(`{"limits":{"homeOnly":true}}`), &made); w.Code >= 300 || !made.Limits.HomeOnly {
		t.Fatalf("an administrator inviting with home only answered %d %+v", w.Code, made.Limits)
	}
	if code := status(t, s, "PATCH", "/v1/admin/invitations/"+made.ID, admin, json.RawMessage(`{"limits":{"maxProfiles":1}}`)); code != 403 {
		t.Errorf("an administrator editing an invitation to fewer profiles answered %d, want 403", code)
	}
	var edited Invitation
	if w := call(t, s, "PATCH", "/v1/admin/invitations/"+made.ID, owner, json.RawMessage(`{"limits":{"maxProfiles":1}}`), &edited); w.Code != 200 ||
		edited.Limits.MaxProfiles == nil || *edited.Limits.MaxProfiles != 1 || !edited.Limits.HomeOnly {
		t.Errorf("the owner editing an invitation to one profile answered %d %+v", w.Code, edited.Limits)
	}
}

// Joining with a Portico Account holds the account to the invitation's limits too.
func TestAPorticoJoinCarriesTheInvitationsLimits(t *testing.T) {
	p := newPorticoRig(t)
	p.link(accountJustin)
	var inv Invitation
	if w := call(t, p.s, "POST", "/v1/admin/invitations", p.owner.AccessToken, json.RawMessage(`{"limits":{"maxRatingAge":13}}`), &inv); w.Code >= 300 {
		t.Fatalf("the invitation answered %d: %s", w.Code, w.Body)
	}
	if code, refused, _ := p.signIn(accountSam, inv.Code, 2); code != 200 {
		t.Fatalf("joining with the invitation answered %d %s", code, refused)
	}
	if got := memberNamed(t, p.s, p.owner.AccessToken, "sam").Limits; got.MaxRatingAge == nil || *got.MaxRatingAge != 13 {
		t.Fatalf("the account that joined is held to %+v", got)
	}
}
