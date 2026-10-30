package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/integrationids"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"strings"
	"testing"
)

func compatibilityCall(f administrationFixture, method, path, secret, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if secret != "" {
		r.Header.Set("Authorization", `MediaBrowser Client="Test", Device="Fixture", DeviceId="fixture", Version="1", Token="`+secret+`"`)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}
func TestCompatibilityDisabledPublicAndDeclaredAliasBoundary(t *testing.T) {
	f := administrationFixtureNew(t)
	for _, path := range []string{"/System/Info/Public", "/system/info/public", "/mediabrowser/System/Info/Public", "/mediabrowser/system/info/public"} {
		w := compatibilityCall(f, "GET", path, "", "")
		if w.Code != 404 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("disabled compatibility %s: %d", path, w.Code)
		}
	}
	if w := compatibilityCall(f, "GET", "/Items", f.member, ""); w.Code != 404 {
		t.Fatalf("disabled authenticated route: %d %s", w.Code, w.Body)
	}
	if _, err := f.deps.DB.Exec(`INSERT INTO server_settings(singleton,"integrations.compatibilityEnabled") VALUES(1,1) ON CONFLICT(singleton) DO UPDATE SET "integrations.compatibilityEnabled"=1`); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/System/Info/Public", "/system/info/public", "/mediabrowser/System/Info/Public", "/mediabrowser/system/info/public"} {
		w := compatibilityCall(f, "GET", path, "", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"ProductName":"Portico"`) {
			t.Fatalf("public compatibility %s: %d %s", path, w.Code, w.Body)
		}
	}
	if w := compatibilityCall(f, "GET", "/Items", f.member, ""); w.Code != 200 {
		t.Fatalf("enabled authenticated route: %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/Videos/anything/stream", "/socket", "/Sessions/Playing/Progress"} {
		w := compatibilityCall(f, "POST", path, "", `{}`)
		if w.Code == 200 {
			t.Fatalf("undeclared player API unexpectedly succeeded: %s", path)
		}
	}
	if _, err := f.deps.DB.Exec(`UPDATE server_settings SET "integrations.compatibilityEnabled"=0 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct{ path, token string }{{"/System/Info/Public", ""}, {"/Items", f.member}} {
		if w := compatibilityCall(f, "GET", request.path, request.token, ""); w.Code != 404 {
			t.Fatalf("disabled again: %s %d %s", request.path, w.Code, w.Body)
		}
	}
}
func TestCompatibilityTokenHeadersUseNativeScopesAndKeyRecovery(t *testing.T) {
	f := integrationFixture(t)
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"compat-key","name":"Tools","scopes":["read"]}`)
	for _, path := range []string{"/System/Info", "/system/info", "/mediabrowser/System/Info", "/System/Configuration"} {
		w := compatibilityCall(f, "GET", path, key.Secret, "")
		if w.Code != 200 {
			t.Fatalf("info alias: %s %d %s", path, w.Code, w.Body)
		}
	}
	r := httptest.NewRequest("GET", "/System/Info?api_key="+key.Secret, nil)
	r.Header.Set("X-Emby-Token", key.Secret)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("legacy proof: %d %s", w.Code, w.Body)
	}
	r = httptest.NewRequest("GET", "/System/Info", nil)
	r.Header.Set("Authorization", `MediaBrowser Token="`+key.Secret+`"`)
	r.Header.Set("X-MediaBrowser-Token", key.Secret)
	w = httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("identical modern+legacy proof refused: %d", w.Code)
	}
	w = compatibilityCall(f, "POST", "/Library/Media/Updated", key.Secret, `{"Updates":[{"Path":"/media/film","UpdateType":"Created"}]}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("read key wrote library: %d %s", w.Code, w.Body)
	}
	w = compatibilityCall(f, "GET", "/Auth/Keys", key.Secret, "")
	if w.Code != 200 {
		t.Fatalf("key recovery: %d %s", w.Code, w.Body)
	}
	var keys struct {
		Items []struct{ AccessToken string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &keys); err != nil || len(keys.Items) != 1 || keys.Items[0].AccessToken != key.Secret {
		t.Fatal("foreign key recovery mismatch")
	}
}

func TestCompatibilityStockAdministratorSetupMintAndReadback(t *testing.T) {
	f := integrationFixture(t)
	w := f.call("POST", "/v1/integrations/app-passwords", f.owner, `{"operationId":"wizard-app","name":"Request manager","accountId":"owner","profileId":"owner-profile","scopes":["admin"]}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var app struct{ Secret string }
	if err := json.Unmarshal(w.Body.Bytes(), &app); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"Username": "owner", "Pw": app.Secret})
	r := httptest.NewRequest("POST", "/Users/AuthenticateByName", strings.NewReader(string(body)))
	r.Header.Set("Authorization", `MediaBrowser Client="Seerr", Device="Seerr", DeviceId="stock-wizard", Version="1"`)
	r.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	f.handler.ServeHTTP(out, r)
	if out.Code != 200 {
		t.Fatalf("wizard login: %d %s", out.Code, out.Body)
	}
	var login struct {
		AccessToken, ServerId string
		User                  struct {
			Id, ServerId string
			Policy       struct{ IsAdministrator bool }
		}
	}
	if err := json.Unmarshal(out.Body.Bytes(), &login); err != nil || login.AccessToken == "" || !login.User.Policy.IsAdministrator || login.User.ServerId != login.ServerId {
		t.Fatalf("wizard identity: %v %s", err, out.Body)
	}
	w = compatibilityCall(f, "POST", "/Auth/Keys?App=Seerr", login.AccessToken, "")
	if w.Code != 200 {
		t.Fatalf("wizard key mint: %d %s", w.Code, w.Body)
	}
	// Stock setup ignores the POST body and locates AccessToken by AppName.
	w = compatibilityCall(f, "GET", "/Auth/Keys", login.AccessToken, "")
	var keys struct {
		Items []struct{ AppName, AccessToken string }
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &keys) != nil || len(keys.Items) != 1 || keys.Items[0].AppName != "Seerr" || !strings.HasPrefix(keys.Items[0].AccessToken, "pk.") {
		t.Fatalf("wizard key readback: %d %s", w.Code, w.Body)
	}
	key := keys.Items[0].AccessToken
	for _, path := range []string{"/System/Info", "/Users", "/Library/MediaFolders", "/Users/Me", "/DisplayPreferences/usersettings?userId=" + login.User.Id + "&client=emby"} {
		if w = compatibilityCall(f, "GET", path, key, ""); w.Code != 200 {
			t.Fatalf("setup request %s: %d %s", path, w.Code, w.Body)
		}
	}
}

func TestCompatibilityMediaSourcesContinueNativeCursorAndPathQueriesUseKnownFolders(t *testing.T) {
	f := integrationFixture(t)
	c := catalogtest.New(t, f.deps.DB)
	lib := c.Library("movies", "Movies", "movie", "/movies")
	item := c.Movie(lib, "/movies/Film/v0.mkv", "Film", 2000)
	for i := 1; i < 19; i++ {
		c.File(item.ID, fmt.Sprintf("/movies/Film/v%d.mkv", i), 100)
	}
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "Series", 2001)
	var source string
	if err := f.deps.DB.QueryRow(`SELECT id FROM library_sources WHERE library_id='tv' LIMIT 1`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO catalog_identities(root,source_key,public_id,kind) SELECT cl.root,?,e.public_id,e.kind FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.id=?`, "show:folder:"+source+":Series", show.ID)
	c.Drain()
	key := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"versions-key","name":"Versions","scopes":["read"]}`)
	guid, err := (integrationids.Store{DB: f.deps.DB}).Encode(context.Background(), "item", item.Public)
	if err != nil {
		t.Fatal(err)
	}
	w := compatibilityCall(f, "GET", "/Items?Ids="+guid+"&Fields=MediaSources&Limit=1", key.Secret, "")
	var page struct {
		Items []struct {
			Path         string
			MediaSources []struct{ Id string }
		}
		TotalRecordCount int
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 1 || len(page.Items[0].MediaSources) != 19 || page.TotalRecordCount != 1 {
		t.Fatalf("source continuation: %d %s", w.Code, w.Body)
	}
	w = compatibilityCall(f, "GET", "/Items?recursive=true&includeItemTypes=Series&fields=Path,ProviderIds&years=2001", key.Secret, "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].Path != "/tv/Series" {
		t.Fatalf("native stored show folder: %d %s", w.Code, w.Body)
	}
}

func TestCompatibilityNativeTransportUsesResourceRevisionAndRejectsLargerControls(t *testing.T) {
	f := newV1Fixture(t, 1)
	if _, err := f.db.Exec(`INSERT INTO server_settings(singleton,"integrations.compatibilityEnabled") VALUES(1,1) ON CONFLICT(singleton) DO UPDATE SET "integrations.compatibilityEnabled"=1`); err != nil {
		t.Fatal(err)
	}
	var session playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "compat-transport"}, startBody(f.items[0], nil), 201, &session)
	// A progress report advances the resource revision without creating a remote
	// command. The adapter must not confuse the command counter with CAS state.
	f.call("PATCH", "/v1/playback/sessions/"+session.ID, map[string]string{"If-Match": session.Revision}, map[string]any{"positionMs": 3000}, 200, &session)
	guid, err := (integrationids.Store{DB: f.db}).Encode(context.Background(), "session", session.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"Pause", "Unpause", "Seek?SeekPositionTicks=90000000"} {
		w := f.raw("POST", "/Sessions/"+guid+"/Playing/"+cmd, f.owner.AccessToken, nil, nil)
		if w.Code != 200 {
			t.Fatalf("native %s failed: %d %s", cmd, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "streamUrl") || strings.Contains(w.Body.String(), "presentation") {
			t.Fatal("control returned player data")
		}
	}
	w := f.raw("POST", "/Sessions/"+guid+"/Command", f.owner.AccessToken, nil, map[string]string{"Name": "VolumeUp"})
	if w.Code != 404 {
		t.Fatalf("unsupported command accepted: %d", w.Code)
	}
	f.call("GET", "/v1/playback/sessions/"+session.ID, nil, nil, 200, &session)
	var controls playbackv1.SessionControls
	f.call("GET", "/v1/admin/sessions/"+session.ID+"/controls", nil, nil, 200, &controls)
	if controls.PositionMs != 9000 {
		t.Fatalf("seek not translated: %+v", session)
	}
	w = f.raw("POST", "/Sessions/"+guid+"/Playing/Stop", f.owner.AccessToken, nil, nil)
	if w.Code != 200 {
		t.Fatalf("stop: %d %s", w.Code, w.Body)
	}
}

func TestCompatibilityNotificationUsesNativeOwnerRefusal(t *testing.T) {
	f := integrationFixture(t)
	f.deps.Console = operations.New(f.deps.DB)
	f.handler = New(f.deps)
	for _, path := range integrationMatrixAliases("/Notifications/Admin") {
		w := compatibilityCall(f, "POST", path, f.member, `{"Name":"Library update","Description":"New files are ready","ImageUrl":"https://unused.invalid/image"}`)
		if w.Code != 403 || registryErrorCode(t, w) != "not_permitted" {
			t.Fatalf("native permission refusal: %d %s", w.Code, w.Body)
		}
	}
	w := compatibilityCall(f, "POST", "/Notifications/Admin", f.owner, `{"Name":"Library update","Description":"New files are ready"}`)
	if w.Code != 200 {
		t.Fatalf("native owner notification: %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"/Items/not-a-guid/PlaybackInfo", "/Shows/not-a-guid/Episodes"} {
		w = compatibilityCall(f, "GET", path, f.member, "")
		if w.Code != 404 {
			t.Fatalf("unknown GUID: %d %s", w.Code, w.Body)
		}
	}
}
