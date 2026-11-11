package api

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
)

// A server that uses Portico Accounts keeps no passwords: every route that could make an
// account with a password, or set one, refuses, and an accepted invitation waits for Portico
// Services instead of taking a password.
func TestPorticoServerKeepsNoPasswords(t *testing.T) {
	s, owner, _ := porticoServer(t)
	s.hosted = hosted.NotConnected{}
	var before Page[Member]
	call(t, s, "GET", "/v1/admin/members", owner, nil, &before)
	var alex string
	for _, m := range before.Items {
		if m.Username == "alex" {
			alex = m.ID
		}
	}
	var invitation Invitation
	if w := call(t, s, "POST", "/v1/admin/invitations", owner, InvitationCreate{}, &invitation); w.Code != 200 && w.Code != 201 {
		t.Fatalf("making an invitation answered %d: %s", w.Code, w.Body)
	}
	for _, c := range []struct {
		method, path, token string
		body                any
		status              int
	}{
		{"POST", "/v1/admin/members", owner, MemberCreate{Username: "kim", Password: testPassword}, 409},
		{"POST", "/v1/auth/invitations/" + invitation.Code + "/accept", "", InvitationAccept{Username: "kim", Password: testPassword, Device: device(8)}, 503},
		{"POST", "/v1/admin/members/" + alex + "/password-reset", owner, PasswordResetCreate{}, 409},
		{"POST", "/v1/auth/password-reset/confirm", "", PasswordResetRequest{Username: "alex", Code: "AAAA-AAAA-AAAA", NewPassword: "a new password"}, 409},
		{"POST", "/v1/auth/password", owner, PasswordRequest{CurrentPassword: testPassword, NewPassword: "a new password"}, 409},
		{"POST", "/v1/setup/owner", "", SetupOwnerRequest{SetupCode: s.setupCode, Username: "kim", Password: testPassword, Device: device(9)}, 409},
	} {
		if w := call(t, s, c.method, c.path, c.token, c.body, nil); w.Code != c.status {
			t.Errorf("%s %s answered %d, want %d: %s", c.method, c.path, w.Code, c.status, w.Body)
		}
	}
	var after Page[Member]
	call(t, s, "GET", "/v1/admin/members", owner, nil, &after)
	if len(after.Items) != len(before.Items) {
		t.Fatalf("an account was made: %d people, then %d", len(before.Items), len(after.Items))
	}
	// The old password of someone still waiting says what to do next; nothing signs in with one.
	if code := status(t, s, "POST", "/v1/auth/sign-in", "", SignInRequest{Username: "alex", Password: testPassword, Device: device(10)}); code != 409 {
		t.Fatalf("a waiting person's old password answered %d, want 409", code)
	}
}
