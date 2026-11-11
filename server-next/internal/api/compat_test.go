package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/compat"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// compatCall sends a request the way a companion tool does: header names and paths as the
// tool writes them.
func compatCall(s *Server, method, path, body string, header map[string]string) (int, map[string]any, []any) {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	var object map[string]any
	var list []any
	json.Unmarshal(w.Body.Bytes(), &object)
	json.Unmarshal(w.Body.Bytes(), &list)
	return w.Code, object, list
}

// What the companion tools are known to ask, by tool, against a member who may see only the
// shows library: each request is answered, within what that member may see, whatever the
// capitals of its address and wherever the tool puts its key. Nothing in an answer can be
// played. Turning the routes off closes them and ends the sign-ins made through them.
func TestCompatibleRoutesForTools(t *testing.T) {
	th := newTheatre(t)
	s, owner := th.s, th.owner.AccessToken
	if code, _, _ := compatCall(s, "GET", "/System/Info/Public", "", nil); code != 404 {
		t.Fatalf("the compatible routes answered %d before the owner turned them on", code)
	}
	on, off := true, false
	if got := status(t, s, "PUT", "/v1/admin/settings/integrations", owner, IntegrationSettingsEdit{CompatibleRoutes: &on}); got != 200 {
		t.Fatalf("turning the compatible routes on answered %d", got)
	}
	max := addMember(t, s, owner, "max", "member")
	shows := []string{th.libs["shows"].PublicID}
	if got := status(t, s, "PATCH", "/v1/admin/members/"+max.ID, owner, MemberEdit{AllLibraries: &off, Libraries: &shows}); got != 200 {
		t.Fatalf("limiting the member answered %d", got)
	}
	var appPassword KeyCreated
	if w := call(t, s, "POST", "/v1/me/app-passwords", signInAs(t, s, "max", testPassword, 6).AccessToken, KeyCreate{Name: "Seerr"}, &appPassword); w.Code != 200 {
		t.Fatalf("making an app password answered %d: %s", w.Code, w.Body)
	}
	var page ItemPage
	call(t, s, "GET", "/v1/items?library="+th.libs["shows"].PublicID+"&kind=show", owner, nil, &page)
	show, episode := compat.ID(page.Items[0].ID), compat.ID(th.eps[0])

	// Seerr and Home Assistant sign in with the username and the app password, never the
	// account's own password.
	wizard := map[string]string{"X-Emby-Authorization": `MediaBrowser Client="Seerr", Device="Seerr", DeviceId="setup", Version="2.0.0"`}
	if code, _, _ := compatCall(s, "POST", "/Users/AuthenticateByName", `{"Username":"max","Pw":"`+testPassword+`"}`, wizard); code != 401 {
		t.Errorf("signing in with the account's password answered %d, want 401", code)
	}
	code, login, _ := compatCall(s, "POST", "/Users/AuthenticateByName", `{"Username":"Max","Pw":"`+appPassword.Secret+`"}`, wizard)
	token, _ := login["AccessToken"].(string)
	if code != 200 || token == "" {
		t.Fatalf("signing in with the app password answered %d: %v", code, login)
	}
	header := map[string]string{"X-Emby-Authorization": `MediaBrowser Client="Seerr", Device="Seerr", DeviceId="setup", Version="2.0.0", Token="` + token + `"`}
	byQuery := func(path string) string {
		if strings.Contains(path, "?") {
			return path + "&api_key=" + token
		}
		return path + "?api_key=" + token
	}

	type ask struct {
		tool, method, path string
		check              func(code int, object map[string]any, list []any) string
	}
	ok := func(code int, _ map[string]any, _ []any) string {
		if code != 200 && code != 204 {
			return "answered " + http.StatusText(code)
		}
		return ""
	}
	items := func(want int) func(int, map[string]any, []any) string {
		return func(code int, object map[string]any, _ []any) string {
			got, _ := object["Items"].([]any)
			if code != 200 || len(got) != want {
				return "answered " + http.StatusText(code) + " with " + strings.Repeat("an item ", len(got))
			}
			return ""
		}
	}
	for _, a := range []ask{
		{"every tool", "GET", "/System/Info/Public", func(code int, o map[string]any, _ []any) string {
			if code != 200 || o["Version"] != compat.Version || strings.Contains(o["Version"].(string), "test") {
				return "did not answer as the fixed version"
			}
			return ""
		}},
		{"Sonarr and Radarr", "GET", "/mediabrowser/System/Info", ok},
		{"Seerr", "GET", "/Users/Me", ok},
		{"Seerr", "POST", "/Auth/Keys?App=Seerr", ok},
		{"Seerr", "GET", "/Auth/Keys", func(code int, o map[string]any, _ []any) string {
			keys, _ := o["Items"].([]any)
			if code != 200 || len(keys) != 1 || keys[0].(map[string]any)["AppName"] != "Seerr" || keys[0].(map[string]any)["AccessToken"] != token {
				return "did not hand back the same access under the app's name"
			}
			return ""
		}},
		{"Seerr", "GET", "/Library/MediaFolders", items(1)},
		{"Seerr", "GET", "/DisplayPreferences/usersettings?userId=" + compat.ID(max.ID) + "&client=emby", ok},
		{"Seerr and Jellystat", "GET", "/Users", func(code int, _ map[string]any, list []any) string {
			if code != 200 || len(list) != 1 {
				return "listed other people to a member"
			}
			return ""
		}},
		{"Seerr", "GET", "/Items?IncludeItemTypes=Movie&Recursive=true", items(0)},
		{"Seerr", "GET", "/Items?IncludeItemTypes=Series&Recursive=true&Fields=ProviderIds", items(1)},
		{"Seerr", "GET", "/Items?IncludeItemTypes=Episode&Recursive=true&StartIndex=1&Limit=1", func(code int, o map[string]any, _ []any) string {
			got, _ := o["Items"].([]any)
			if code != 200 || len(got) != 1 || o["TotalRecordCount"] != float64(2) {
				return "did not page"
			}
			return ""
		}},
		{"Seerr", "GET", "/Items?IncludeItemTypes=Episode&Recursive=true&StartIndex=5", items(0)},
		{"Bazarr", "GET", "/Shows/" + show + "/Seasons", items(1)},
		{"Bazarr", "GET", "/Shows/" + show + "/Episodes?season=1&fields=Path,MediaSources", items(2)},
		{"Bazarr", "GET", "/Items?Ids=" + episode + "&Fields=MediaSources", items(1)},
		{"Jellystat", "GET", "/Items/" + episode + "/playbackinfo", func(code int, o map[string]any, _ []any) string {
			sources, _ := o["MediaSources"].([]any)
			if code != 200 || len(sources) != 1 || sources[0].(map[string]any)["SupportsDirectPlay"] != false {
				return "did not answer media facts that cannot be played"
			}
			return ""
		}},
		{"Jellystat", "GET", "/items/latest?limit=5", ok},
		{"Home Assistant", "GET", "/Sessions", ok},
		{"Home Assistant", "GET", "/Users/" + compat.ID(max.ID) + "/Views", items(1)},
		{"Jellystat", "GET", "/Plugins", ok},
		{"a member's tool", "GET", "/Items/" + compat.ID(th.film) + "/PlaybackInfo", func(code int, _ map[string]any, _ []any) string {
			if code != 404 {
				return "showed a film the member may not see"
			}
			return ""
		}},
	} {
		for _, h := range []map[string]string{header, nil} {
			path := a.path
			if h == nil {
				path = byQuery(path)
			}
			code, object, list := compatCall(s, a.method, path, "", h)
			if problem := a.check(code, object, list); problem != "" {
				t.Errorf("%s: %s %s %s (%d)", a.tool, a.method, path, problem, code)
			}
		}
	}

	// A server key with only the read scope cannot report changed files.
	var key KeyCreated
	call(t, s, "POST", "/v1/admin/api-keys", owner, KeyCreate{Name: "Sonarr", Scopes: []string{"read"}}, &key)
	if code, _, _ := compatCall(s, "POST", "/Library/Media/Updated?api_key="+key.Secret, `{"Updates":[]}`, nil); code != 403 {
		t.Errorf("a read-only key reporting changed files answered %d, want 403", code)
	}

	// Off closes the routes; on again, the old sign-in no longer works.
	status(t, s, "PUT", "/v1/admin/settings/integrations", owner, IntegrationSettingsEdit{CompatibleRoutes: &off})
	if code, _, _ := compatCall(s, "GET", "/System/Info", "", header); code != 404 {
		t.Errorf("with the routes off a tool was answered %d, want 404", code)
	}
	status(t, s, "PUT", "/v1/admin/settings/integrations", owner, IntegrationSettingsEdit{CompatibleRoutes: &on})
	if code, _, _ := compatCall(s, "GET", "/System/Info", "", header); code != 401 {
		t.Errorf("a sign-in made before the routes were turned off answered %d, want 401", code)
	}
}

// kidsKey makes a read-only token that acts as a profile limited to the shows library, while
// the account's main profile plays a film. Keys are made only from the main profile today,
// so the token is put in directly.
func kidsKey(t *testing.T, th *theatre) string {
	t.Helper()
	var kid Profile
	if w := call(t, th.s, "POST", "/v1/profiles", th.owner.AccessToken, ProfileCreate{Name: "Kids"}, &kid); w.Code >= 300 {
		t.Fatalf("making a profile answered %d: %s", w.Code, w.Body)
	}
	limits := Restrictions{Libraries: []string{th.libs["shows"].PublicID}, BlockedLabels: []string{}}
	if got := status(t, th.s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", th.owner.AccessToken, limits); got != 200 {
		t.Fatalf("limiting the profile answered %d", got)
	}
	ctx := context.Background()
	p, err := th.s.db.Profile(ctx, 1, kid.ID)
	if err != nil {
		t.Fatal(err)
	}
	secret := keyPrefix + "kids-tool"
	if _, _, err := th.s.db.CreateAPIKey(ctx, db.NewAPIKey{UserID: 1, ProfileID: p.ID, Kind: "personal_token", Name: "Kids tool",
		KeyHash: keyHash(secret), Scopes: []string{"read"}, Limit: 25, Now: th.clock}); err != nil {
		t.Fatal(err)
	}
	th.play(th.owner.AccessToken, th.film)
	return secret
}

// A tool whose key acts as a limited profile sees that the account has a stream, but not the
// title the profile may not see.
func TestCompatSessionsHideTitlesTheKeyMayNotSee(t *testing.T) {
	th := newTheatre(t)
	on := true
	status(t, th.s, "PUT", "/v1/admin/settings/integrations", th.owner.AccessToken, IntegrationSettingsEdit{CompatibleRoutes: &on})
	key := kidsKey(t, th)
	code, _, list := compatCall(th.s, "GET", "/Sessions?api_key="+key, "", nil)
	raw, _ := json.Marshal(list)
	if code != 200 || len(list) != 1 || strings.Contains(string(raw), "Heat") || strings.Contains(string(raw), compat.ID(th.film)) {
		t.Errorf("a limited profile's tool was shown %d %s", code, raw)
	}
}
