package api

import "testing"

func TestAdminCannotReadOrChangeAnAdminInvitation(t *testing.T) {
	s := newTestServer(t)
	owner := onMainProfile(t, s).AccessToken
	addMember(t, s, owner, "manager", "admin")
	admin := signInAs(t, s, "manager", testPassword, 8).AccessToken
	var key KeyCreated
	call(t, s, "POST", "/v1/admin/api-keys", owner, KeyCreate{Name: "Tool", Scopes: []string{"admin"}}, &key)
	if key.Secret == "" {
		t.Fatal("no key")
	}
	for name, token := range map[string]string{"administrator": admin, "owner key": key.Secret} {
		t.Run(name, func(t *testing.T) {
			var inv, member Invitation
			call(t, s, "POST", "/v1/admin/invitations", owner, InvitationCreate{Role: "admin"}, &inv)
			call(t, s, "POST", "/v1/admin/invitations", owner, InvitationCreate{Role: "member"}, &member)
			memberSeen := false
			for _, got := range get[Page[Invitation]](t, s, token, "/v1/admin/invitations").Items {
				if got.ID == inv.ID {
					t.Error("unauthorized administrator invitation in list")
				}
				if got.ID == member.ID && got.Code != "" {
					memberSeen = true
				}
			}
			if !memberSeen {
				t.Error("member invitation missing")
			}
			for _, edit := range []InvitationEdit{{Role: new("member")}, {Note: new("changed")}} {
				if code := status(t, s, "PATCH", "/v1/admin/invitations/"+inv.ID, token, edit); code != 403 {
					t.Errorf("changed admin invitation: HTTP %d", code)
				}
			}
			if code := status(t, s, "DELETE", "/v1/admin/invitations/"+inv.ID, token, nil); code != 403 {
				t.Errorf("revoked admin invitation: HTTP %d", code)
			}
			if code := status(t, s, "DELETE", "/v1/admin/invitations/"+member.ID, token, nil); code != 204 {
				t.Errorf("member revoke: HTTP %d", code)
			}
			if code := status(t, s, "DELETE", "/v1/admin/invitations/"+inv.ID, owner, nil); code != 204 {
				t.Errorf("owner revoke: HTTP %d", code)
			}
		})
	}
}
