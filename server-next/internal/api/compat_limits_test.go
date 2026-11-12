package api

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
)

// A companion tool signs in through the compatible routes with a username and an app
// password. That sign-in, and the token it answers, are held to the account's home network
// and viewing hours as a person's own sign-in is: away from home, or outside the hours, the
// tool is refused in plain words, and at home inside the hours it works. An account held to
// nothing signs a tool in from anywhere.
func TestTheCompatibleSignInIsHeldToHomeAndToViewingHours(t *testing.T) {
	s, owner, max, _ := twoMembers(t)
	now := time.Now().UTC()
	s.setClock(func() time.Time { return now })
	on := true
	if got := status(t, s, "PUT", "/v1/admin/settings/integrations", owner, IntegrationSettingsEdit{CompatibleRoutes: &on}); got != 200 {
		t.Fatalf("turning the compatible routes on answered %d", got)
	}
	passwords := map[string]string{}
	for n, name := range []string{"max", "eve"} {
		var made KeyCreated
		if w := call(t, s, "POST", "/v1/me/app-passwords", signInAs(t, s, name, testPassword, 6+n).AccessToken, KeyCreate{Name: "Seerr"}, &made); w.Code != 200 {
			t.Fatalf("making %s an app password answered %d: %s", name, w.Code, w.Body)
		}
		passwords[name] = made.Secret
	}
	tool := `MediaBrowser Client="Seerr", Device="Seerr", DeviceId="setup", Version="2.0.0"`
	ask := func(peer, method, path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.RemoteAddr = peer + ":4000"
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		header := tool
		if token != "" {
			header += `, Token="` + token + `"`
		}
		r.Header.Set("X-Emby-Authorization", header)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	signIn := func(peer, name string) *httptest.ResponseRecorder {
		return ask(peer, "POST", "/Users/AuthenticateByName", `{"Username":"`+name+`","Pw":"`+passwords[name]+`"}`, "")
	}
	token := func(w *httptest.ResponseRecorder) string {
		t.Helper()
		var login struct{ AccessToken string }
		if err := jsonInto(w, &login); err != nil || w.Code != 200 || login.AccessToken == "" {
			t.Fatalf("a tool's sign-in answered %d: %s", w.Code, w.Body)
		}
		return login.AccessToken
	}
	held := token(signIn(atHome, "max"))

	limits := noLimits()
	limits.HomeOnly = true
	hold(t, s, owner, max.ID, limits)
	if w := signIn(away, "max"); w.Code != 403 || !strings.Contains(w.Body.String(), "away_from_home") {
		t.Errorf("a tool signing in away for a home-only account answered %d %s", w.Code, w.Body)
	}
	if w := ask(away, "GET", "/System/Info", "", held); w.Code != 403 || !strings.Contains(w.Body.String(), "away_from_home") {
		t.Errorf("a home-only account's tool token away answered %d %s", w.Code, w.Body)
	}
	if w := ask(atHome, "GET", "/System/Info", "", held); w.Code != 200 {
		t.Errorf("a home-only account's tool token at home answered %d %s", w.Code, w.Body)
	}
	token(signIn(atHome, "max"))
	token(signIn(away, "eve"))

	// Viewing hours: tomorrow only.
	limits = noLimits()
	limits.ViewingHours = &auth.ViewingHours{Timezone: "UTC", Windows: []auth.ViewingWindow{{Days: []int{int(now.Weekday()+1) % 7}, StartMinute: 0, EndMinute: 1440}}}
	hold(t, s, owner, max.ID, limits)
	if w := signIn(atHome, "max"); w.Code != 403 || !strings.Contains(w.Body.String(), "outside_viewing_hours") {
		t.Errorf("a tool signing in outside viewing hours answered %d %s", w.Code, w.Body)
	}
	if w := ask(atHome, "GET", "/System/Info", "", held); w.Code != 403 {
		t.Errorf("a tool's token outside viewing hours answered %d", w.Code)
	}
	token(signIn(atHome, "eve"))
	now = now.Add(24 * time.Hour)
	token(signIn(away, "max"))
	if w := ask(away, "GET", "/System/Info", "", held); w.Code != 200 {
		t.Errorf("a tool's token inside viewing hours answered %d %s", w.Code, w.Body)
	}
}
