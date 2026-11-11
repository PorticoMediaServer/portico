package api

import (
	"testing"
)

// A companion tool puts a notice to the owner and administrators in their Notifications; a
// member's tool may not (area 13 rule 38).
func TestAToolPutsANoticeToAdministrators(t *testing.T) {
	th := newTheatre(t)
	s, owner := th.s, th.owner.AccessToken
	on := true
	status(t, s, "PUT", "/v1/admin/settings/integrations", owner, IntegrationSettingsEdit{CompatibleRoutes: &on})
	var key KeyCreated
	call(t, s, "POST", "/v1/admin/api-keys", owner, KeyCreate{Name: "Sonarr", Scopes: []string{"admin"}}, &key)
	notice := `{"Name":"Sonarr","Description":"The disk for downloads is nearly full","Url":"http://sonarr.lan","NotificationLevel":"Warning"}`
	if code, _, _ := compatCall(s, "POST", "/notifications/admin?api_key="+key.Secret, notice, nil); code != 204 {
		t.Fatalf("the notice answered %d, want 204", code)
	}
	inbox := get[Notifications](t, s, owner, "/v1/notifications")
	if len(inbox.Items) == 0 || inbox.Items[0].Kind != "tool_message" ||
		inbox.Items[0].Body != "Sonarr\nThe disk for downloads is nearly full\nhttp://sonarr.lan" {
		t.Fatalf("the owner's notifications are %+v", inbox.Items)
	}
	addMember(t, s, owner, "max", "member")
	var appPassword KeyCreated
	call(t, s, "POST", "/v1/me/app-passwords", signInAs(t, s, "max", testPassword, 6).AccessToken, KeyCreate{Name: "Tool"}, &appPassword)
	device := `MediaBrowser Client="Tool", Device="Tool", DeviceId="tool", Version="1.0.0"`
	_, login, _ := compatCall(s, "POST", "/Users/AuthenticateByName", `{"Username":"max","Pw":"`+appPassword.Secret+`"}`,
		map[string]string{"X-Emby-Authorization": device})
	token, _ := login["AccessToken"].(string)
	if token == "" {
		t.Fatalf("the member's tool could not sign in: %v", login)
	}
	header := map[string]string{"X-Emby-Authorization": device + `, Token="` + token + `"`}
	if code, _, _ := compatCall(s, "POST", "/Notifications/Admin", notice, header); code != 403 {
		t.Errorf("a member's tool putting a notice answered %d, want 403", code)
	}
}
