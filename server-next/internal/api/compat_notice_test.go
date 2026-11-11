package api

import (
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
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

// A tool that sends a notice on every download cannot fill the administrators' inbox: one key
// puts at most toolNoticesPerHour in an hour, the rest are refused with when to try again,
// and the same words again are taken as sent and stored once.
func TestAToolCannotFillTheAdministratorsNotifications(t *testing.T) {
	th := newTheatre(t)
	s, owner := th.s, th.owner.AccessToken
	on := true
	status(t, s, "PUT", "/v1/admin/settings/integrations", owner, IntegrationSettingsEdit{CompatibleRoutes: &on})
	key := func(name string) string {
		var made KeyCreated
		call(t, s, "POST", "/v1/admin/api-keys", owner, KeyCreate{Name: name, Scopes: []string{"admin"}}, &made)
		return made.Secret
	}
	unread := func() int { return get[Notifications](t, s, owner, "/v1/notifications").UnreadCount }
	before, busy := unread(), key("Sonarr")
	accepted, refused := 0, 0
	for n := range 300 {
		body := fmt.Sprintf(`{"Name":"Episode downloaded","Description":"Episode %d"}`, n)
		switch code, _, _ := compatCall(s, "POST", "/Notifications/Admin?api_key="+busy, body, nil); code {
		case 204:
			accepted++
		case 429:
			refused++
		default:
			t.Fatalf("notice %d answered %d", n, code)
		}
	}
	if accepted != toolNoticesPerHour || refused != 300-toolNoticesPerHour || unread()-before != toolNoticesPerHour {
		t.Fatalf("300 notices in a row: %d accepted, %d refused, %d in the inbox", accepted, refused, unread()-before)
	}
	r := httptest.NewRequest("POST", "/Notifications/Admin?api_key="+busy, strings.NewReader(`{"Name":"One more"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if wait, err := strconv.Atoi(w.Header().Get("Retry-After")); w.Code != 429 || err != nil || wait < 1 || wait > 3600 {
		t.Errorf("a notice past the bound answered %d with Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}

	// Another key is counted on its own; the same words from it again are stored once.
	quiet := key("Radarr")
	before = unread()
	for range 3 {
		if code, _, _ := compatCall(s, "POST", "/Notifications/Admin?api_key="+quiet, `{"Name":"Radarr","Description":"Disk nearly full"}`, nil); code != 204 {
			t.Fatalf("a repeated notice answered %d, want 204", code)
		}
	}
	if got := unread() - before; got != 1 {
		t.Errorf("the same notice sent three times is in the inbox %d times, want once", got)
	}
}

// The bound refills as the hour passes, and the same words count as new once their window has.
func TestToolNoticesRefillWithTime(t *testing.T) {
	var n toolNotices
	key, start := sha256.Sum256([]byte("key")), time.Now()
	words := func(i int) [32]byte { return sha256.Sum256([]byte(strconv.Itoa(i))) }
	for i := range toolNoticesPerHour {
		at := start.Add(time.Duration(i) * time.Second)
		if repeat, wait := n.check(key, words(i), at); repeat || wait != 0 {
			t.Fatalf("notice %d: repeat %v, wait %v", i, repeat, wait)
		}
		n.record(key, words(i), at)
	}
	if _, wait := n.check(key, words(-1), start.Add(time.Minute)); wait != time.Hour-time.Minute {
		t.Errorf("past the bound the wait is %v, want %v (until the first notice is an hour old)", wait, time.Hour-time.Minute)
	}
	if repeat, _ := n.check(key, words(0), start.Add(toolNoticeRepeat-time.Second)); !repeat {
		t.Error("the same words within the window were not taken as sent")
	}
	if repeat, wait := n.check(key, words(0), start.Add(time.Hour)); repeat || wait != 0 {
		t.Errorf("an hour later: repeat %v, wait %v; want a new notice allowed", repeat, wait)
	}
}
