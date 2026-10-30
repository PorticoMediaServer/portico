package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// The administration pages are owner-only with one exception, the channel logo
// read, and every write is fenced on the document's revision. This exercises the
// boundary and one full read/write round trip over HTTP.
func TestAdministrationOwnerBoundaryAndLibrarySettingsRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ident, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','profile',1);INSERT INTO libraries(id,name,kind,root) VALUES('lib','Films','movie','/media/films')`); err != nil {
		t.Fatal(err)
	}
	owner, err := ident.Issue("owner", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	member, _ := ident.Issue("member", "member-profile", "hosted", "member", 1)
	hostedOwner, _ := ident.Issue("hosted-owner", "hosted-profile", "hosted", "owner", 1)
	cat := catalog.New(db)
	control, err := hosted.New(db, ident, "http://127.0.0.1:19410", base64.RawURLEncoding.EncodeToString(make([]byte, 32)), zeroHostedRootID)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(Dependencies{DB: db, Identity: ident, Catalog: cat, Ingestion: ingestion.New(db, cat, assets.Probe{}), Hosted: control})
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		// A request with a body says what the body is, as every client does.
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	ownerOnly := []struct{ method, path string }{
		{"GET", "/v1/admin/filesystem"},
		{"GET", "/v1/admin/library-settings"},
		{"GET", "/v1/admin/analysis-operations"},
		{"GET", "/v1/admin/libraries/lib/settings"},
		{"GET", "/v1/admin/missing-titles"},
		{"GET", "/v1/admin/dvr/settings"},
		{"GET", "/v1/admin/dvr/recording-permissions"},
		{"GET", "/v1/admin/dvr/recording-owners"},
		{"GET", "/v1/admin/dvr/tuners"},
		{"GET", "/v1/admin/live/settings"},
		{"GET", "/v1/admin/maintenance/settings"},
		{"GET", "/v1/admin/storage-usage"},
		{"GET", "/v1/admin/updates"},
		{"GET", "/v1/admin/library-channels/criteria"},
		{"GET", "/v1/admin/library-channels/block-presets"},
	}
	// A Hosted account, even one carrying the owner role, is not this server's
	// owner: only the locally managed owner administers it.
	for _, route := range ownerOnly {
		for _, token := range []string{"", member.AccessToken, hostedOwner.AccessToken} {
			if w := request(route.method, route.path, token, ""); w.Code != 401 {
				t.Fatalf("%s %s with a non-owner token answered %d", route.method, route.path, w.Code)
			}
		}
		if w := request(route.method, route.path, owner.AccessToken, ""); w.Code != 200 {
			t.Fatalf("%s %s as owner answered %d: %s", route.method, route.path, w.Code, w.Body.String())
		}
	}
	storage := request("GET", "/v1/admin/storage-usage", owner.AccessToken, "")
	assertSpecResponse(t, "GET", "/v1/admin/storage-usage", storage)
	var usage struct {
		Result struct {
			Categories []struct {
				ID        string `json:"id"`
				Cleanable bool   `json:"cleanable"`
			} `json:"categories"`
		} `json:"result"`
	}
	if err := json.Unmarshal(storage.Body.Bytes(), &usage); err != nil {
		t.Fatal(err)
	}
	for _, category := range usage.Result.Categories {
		if category.ID == "trash" {
			t.Fatal("storage report still lists a trash: deleted files are not kept anywhere")
		}
	}
	var updateRead struct {
		State  string `json:"state"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(request("GET", "/v1/admin/updates", owner.AccessToken, "").Body.Bytes(), &updateRead); err != nil {
		t.Fatal(err)
	}
	if updateRead.State != "unavailable" || updateRead.Status != "unavailable" {
		t.Fatalf("unconfigured update service state: %+v", updateRead)
	}
	grantBody := `{"owner":{"authority":"local","accountId":"owner","profileId":"profile"},"enabled":true,"revision":1}`
	if w := request("PUT", "/v1/admin/dvr/recording-permissions", member.AccessToken, grantBody); w.Code != 401 {
		t.Fatalf("member managed recording grant: %d %s", w.Code, w.Body.String())
	}
	if w := request("PUT", "/v1/admin/dvr/recording-permissions", owner.AccessToken, grantBody); w.Code != 200 {
		t.Fatalf("owner grant: %d %s", w.Code, w.Body.String())
	}
	if w := request("PUT", "/v1/admin/dvr/recording-permissions", owner.AccessToken, grantBody); w.Code != 409 {
		t.Fatalf("grant revision not fenced: %d %s", w.Code, w.Body.String())
	}
	// An unknown query parameter is a refusal, not a silently ignored filter.
	if w := request("GET", "/v1/admin/missing-titles?unknown=1", owner.AccessToken, ""); w.Code != 400 {
		t.Fatalf("unknown query parameter answered %d", w.Code)
	}
	// A library's settings are read and saved as the document itself, with no
	// envelope around it.
	type libraryDocument struct {
		Revision int64 `json:"revision"`
		Settings struct {
			AllowMediaDeletion bool     `json:"allowMediaDeletion"`
			Analysis           []string `json:"analysis"`
		} `json:"settings"`
		Matrix struct {
			Operations []struct {
				ID string `json:"id"`
			} `json:"operations"`
		} `json:"analysisMatrix"`
	}
	var read struct{ Result libraryDocument }
	w := request("GET", "/v1/admin/libraries/lib/settings", owner.AccessToken, "")
	if err = json.Unmarshal(w.Body.Bytes(), &read.Result); err != nil {
		t.Fatal(err)
	}
	if read.Result.Revision != 1 || read.Result.Settings.AllowMediaDeletion || len(read.Result.Matrix.Operations) == 0 {
		t.Fatalf("library document %+v", read.Result)
	}
	body := `{"expectedRevision":1,"operationId":"films-settings-1","settings":{"allowMediaDeletion":true,"providers":[],"analysis":["probe","local_metadata"],"navigation":{"trickplayIntervalMs":10000,"trickplayTileWidth":320,"trickplayMaxTiles":400,"chapterThumbnailMode":"embedded","videoPreviewEnabled":false,"videoPreviewMs":30000}}}`
	if w = request("PUT", "/v1/admin/libraries/lib/settings", owner.AccessToken, body); w.Code != 200 {
		t.Fatalf("write answered %d: %s", w.Code, w.Body.String())
	}
	if err = json.Unmarshal(w.Body.Bytes(), &read.Result); err != nil {
		t.Fatal(err)
	}
	if read.Result.Revision <= 1 || !contains(read.Result.Settings.Analysis, "probe") || !contains(read.Result.Settings.Analysis, "local_metadata") || len(read.Result.Settings.Analysis) != 2 || !read.Result.Settings.AllowMediaDeletion {
		t.Fatalf("document after write %+v", read.Result.Settings)
	}
	savedRevision := read.Result.Revision
	// Replaying the same operation identifier with the same body returns the
	// first result rather than writing twice.
	if w = request("PUT", "/v1/admin/libraries/lib/settings", owner.AccessToken, body); w.Code != 200 {
		t.Fatalf("replay answered %d: %s", w.Code, w.Body.String())
	}
	if err = json.Unmarshal(w.Body.Bytes(), &read.Result); err != nil {
		t.Fatal(err)
	}
	if read.Result.Revision != savedRevision {
		t.Fatalf("a replay must not advance the revision: %d", read.Result.Revision)
	}
	// A fresh operation at the now-stale revision is a conflict.
	stale := `{"expectedRevision":1,"operationId":"films-settings-2","settings":{"allowMediaDeletion":true,"providers":[],"analysis":["probe"],"navigation":{"trickplayIntervalMs":10000,"trickplayTileWidth":320,"trickplayMaxTiles":400,"chapterThumbnailMode":"embedded","videoPreviewEnabled":false,"videoPreviewMs":30000}}}`
	if w = request("PUT", "/v1/admin/libraries/lib/settings", owner.AccessToken, stale); w.Code != 412 || !strings.Contains(w.Body.String(), `"code":"revision_mismatch"`) {
		t.Fatalf("stale write answered %d: %s", w.Code, w.Body.String())
	}
	// A field-level refusal names the field.
	bad := `{"expectedRevision":2,"operationId":"invalid-settings-key","settings":{"allowMediaDeletion":false,"providers":[],"analysis":["probe"],"navigation":{"trickplayIntervalMs":10000,"trickplayTileWidth":320,"trickplayMaxTiles":400,"chapterThumbnailMode":"nonsense","videoPreviewEnabled":false,"videoPreviewMs":30000}}}`
	w = request("PUT", "/v1/admin/libraries/lib/settings", owner.AccessToken, bad)
	if w.Code != 400 {
		t.Fatalf("invalid write answered %d", w.Code)
	}
	var failure struct {
		Error struct {
			Code   string `json:"code"`
			Fields []struct {
				Path string `json:"path"`
			} `json:"fields"`
		} `json:"error"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Error.Code != "invalid_request" || len(failure.Error.Fields) != 1 || !strings.Contains(failure.Error.Fields[0].Path, "chapterThumbnailMode") {
		t.Fatalf("failure %+v: %s", failure.Error, w.Body.String())
	}
	// The delete preview is reachable and reports the library's own policy.
	if w = request("POST", "/v1/items/missing/delete/preview", owner.AccessToken, ""); w.Code != 404 {
		t.Fatalf("preview of a missing item answered %d: %s", w.Code, w.Body.String())
	}
	// The channel logo read is the one route here that any signed-in viewer may
	// call, so it answers 404 for a logo that does not exist rather than 401.
	if w = request("GET", "/v1/library-channels/unknown/logo", owner.AccessToken, ""); w.Code != 404 {
		t.Fatalf("logo read answered %d: %s", w.Code, w.Body.String())
	}
	if w = request("GET", "/v1/library-channels/unknown/logo", "", ""); w.Code != 401 {
		t.Fatalf("unauthenticated logo read answered %d", w.Code)
	}
}
