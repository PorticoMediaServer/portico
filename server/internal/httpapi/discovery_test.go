package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"testing"
	"time"
)

func TestLibraryDiscoveryAndCollectionPermissions(t *testing.T) {
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ident, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := ident.BrowserSetupToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	setup, err := ident.SetupWithOptions(context.Background(), identity.SetupOptions{SetupToken: secret, AuthMode: "local", Username: "owner", Password: "Local-password1!", Name: "Fixture"}, true)
	if err != nil {
		t.Fatal(err)
	}
	owner := setup.Envelope
	c := catalogtest.New(t, db)
	a := c.Library("a", "A", "movie", "/a")
	b := c.Library("b", "B", "movie", "/b")
	makeMovie := func(library int64, root, title, path, added string) catalogtest.Item {
		t.Helper()
		item := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey(root, path, 0), Title: title, Added: added}, map[string]any{"overview": "Fixture overview", "backdrop_url": "https://image.tmdb.org/t/p/w1280/fixture.png"})
		item.Asset, item.Token = c.File(item.ID, path, 100)
		return item
	}
	itemA := makeMovie(a, "/a", "A", "/a/movie.mkv", "2026-01-01T00:00:00.000Z")
	itemB := makeMovie(b, "/b", "B", "/b/movie.mkv", "2026-01-02T00:00:00.000Z")
	c.Drain()
	control, err := hosted.New(db, ident, "http://127.0.0.1:19410", tl6HostedRootPin(), tl6HostedRootID())
	if err != nil {
		t.Fatal(err)
	}
	policy := hosted.Policy{ServerID: ident.ID(), Revision: 1, IssuedAt: time.Now().UTC().Format(time.RFC3339), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), Members: []hosted.Member{{AccountID: "member", ProfileID: "reader", Role: "member", AllowedLibraries: []string{"a"}}}}
	raw, _ := json.Marshal(policy)
	if err = storeHTTPFixturePolicy(db, raw); err != nil {
		t.Fatal(err)
	}
	reader, err := tl6IssueHostedFixture(t, db, ident, "member", "reader", "member")
	if err != nil {
		t.Fatal(err)
	}
	cat := catalog.New(db)
	collection, err := cat.CreateCollection("a", "List")
	if err != nil {
		t.Fatal(err)
	}
	handler := New(Dependencies{DB: db, Identity: ident, Catalog: cat, Hosted: control})
	request := func(method, path, token string, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/v1/libraries/a/content", "/v1/libraries/a/content?view=browse", "/v1/libraries/a/browse", "/v1/libraries/a/discover", "/v1/libraries/a/categories", "/v1/libraries/a/collections", "/v1/collections/" + collection.ID + "/items"} {
		if w := request("GET", path, reader.AccessToken, ""); w.Code != 200 {
			t.Fatalf("authorized read %s %d %s", path, w.Code, w.Body.String())
		}
		if w := request("GET", path, "", ""); w.Code != 401 {
			t.Fatal("anonymous catalog read admitted", path, w.Code)
		}
	}
	for _, path := range []string{"/v1/libraries/b/content", "/v1/libraries/b/browse", "/v1/libraries/b/discover", "/v1/libraries/b/categories", "/v1/libraries/b/collections"} {
		// CD-51: a library the member is not given is hidden (404); the session
		// stays valid.
		if w := request("GET", path, reader.AccessToken, ""); w.Code != 404 {
			t.Fatal("library restriction bypass", path, w.Code)
		}
	}
	if w := request("GET", "/v1/libraries/b/content?view=collection&entityId="+collection.ID, owner.AccessToken, ""); w.Code != 404 {
		t.Fatal("cross library entity admitted", w.Code, w.Body.String())
	}
	response := request("GET", "/v1/libraries/a/content?view=browse", reader.AccessToken, "")
	var projection catalog.ContentEnvelope
	if err = json.Unmarshal(response.Body.Bytes(), &projection); err != nil || projection.Heading.Fallback != "A" || projection.Scope.ViewerFence == "" || projection.Scope.ServerID != ident.ID() {
		t.Fatal("wire projection", projection, err)
	}
	if w := request("POST", "/v1/libraries/a/collections", reader.AccessToken, `{"name":"No"}`); w.Code != 403 {
		t.Fatal("member mutation admitted", w.Code)
	}
	if w := request("PUT", "/v1/collections/"+collection.ID+"/items/"+itemB.Public, owner.AccessToken, ""); w.Code != 404 {
		t.Fatal("cross-library link admitted", w.Code, w.Body.String())
	}
	if w := request("PUT", "/v1/collections/"+collection.ID+"/items/"+itemA.Public, owner.AccessToken, ""); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	memberDetail := request("GET", "/v1/items/"+itemA.Public+"/detail", reader.AccessToken, "")
	var detail catalog.Detail
	if err = json.Unmarshal(memberDetail.Body.Bytes(), &detail); err != nil || memberDetail.Code != 200 || detail.Scope.ServerID != ident.ID() || detail.Personal.Revision != 0 {
		t.Fatal("detail read", memberDetail.Code, detail, err)
	}
	for _, action := range detail.Actions {
		if action.ID == "delete" || action.ID == "metadata_refresh" {
			t.Fatal("member admin action", action)
		}
	}
	for _, path := range []string{"/v1/items/" + itemB.Public + "/detail", "/v1/items/" + itemB.Public + "/personal-state"} {
		method := "GET"
		body := ""
		if strings.Contains(path, "/personal-state") {
			method = "PUT"
			body = `{"operationId":"bad","expectedRevision":0,"favorite":true}`
		}
		if w := request(method, path, reader.AccessToken, body); w.Code != 404 {
			t.Fatal("cross library detail action", w.Code)
		}
	}
	mutation := `{"operationId":"member-favorite","expectedRevision":0,"favorite":true}`
	applied := request("PUT", "/v1/items/"+itemA.Public+"/personal-state", reader.AccessToken, mutation)
	var receipt catalog.PersonalReceipt
	if err = json.Unmarshal(applied.Body.Bytes(), &receipt); err != nil || applied.Code != 200 || !receipt.Personal.Favorite || receipt.Personal.Revision != 1 || receipt.ServerID != ident.ID() || receipt.ViewerFence != detail.Scope.ViewerFence {
		t.Fatal("personal mutation", applied.Code, applied.Body.String(), err)
	}
	if retried := request("PUT", "/v1/items/"+itemA.Public+"/personal-state", reader.AccessToken, mutation); retried.Code != 200 || retried.Body.String() != applied.Body.String() {
		t.Fatal("HTTP duplicate changed receipt", retried.Code, retried.Body.String())
	}
	if conflict := request("PUT", "/v1/items/"+itemA.Public+"/personal-state", reader.AccessToken, `{"operationId":"stale","expectedRevision":0,"watchlisted":true}`); conflict.Code != 409 {
		t.Fatal("HTTP CAS failed", conflict.Code)
	}
	ownerDetail := request("GET", "/v1/items/"+itemA.Public+"/detail", owner.AccessToken, "")
	if err = json.Unmarshal(ownerDetail.Body.Bytes(), &detail); err != nil || detail.Personal.Favorite {
		t.Fatal("profile preference leaked", detail, err)
	}
	// Home is read as every client reads it: the manifest from its one route (the older content
	// view of it is gone), then each deferred shelf from its own endpoint.
	composeHome := func(token string, manifest []byte) catalog.HomeDocument {
		t.Helper()
		var home catalog.HomeDocument
		if err := json.Unmarshal(manifest, &home); err != nil {
			t.Fatal("Home manifest", string(manifest), err)
		}
		for i, row := range home.Rows {
			if !row.Deferred {
				continue
			}
			w := request("GET", row.Endpoint+"?limit=12", token, "")
			var shelf catalog.HomeRow
			if err := json.Unmarshal(w.Body.Bytes(), &shelf); err != nil || w.Code != 200 {
				t.Fatal("Home shelf", row.ID, w.Code, w.Body.String(), err)
			}
			// A family's shelf answers with its member rows; their entries are what it shows.
			for _, child := range shelf.Children {
				shelf.Entries = append(shelf.Entries, child.Entries...)
			}
			home.Rows[i] = shelf
		}
		return home
	}
	emptyResponse := request("GET", "/v1/home?limit=12", reader.AccessToken, "")
	if emptyResponse.Code != 200 {
		t.Fatal("Home without personalized activity", emptyResponse.Code, emptyResponse.Body.String())
	}
	emptyHome := composeHome(reader.AccessToken, emptyResponse.Body.Bytes())
	// With nothing watched, the only rows with anything in them are what was
	// added lately, and all they hold is the one film this member may see.
	filled := 0
	for _, row := range emptyHome.Rows {
		if len(row.Entries) == 0 {
			continue
		}
		filled++
		if row.ID != "new_on_server" || len(row.Entries) != 1 || row.Entries[0].ID != itemA.Public {
			t.Fatal("Home without personalized activity", row.ID, emptyResponse.Body.String())
		}
	}
	if filled == 0 {
		t.Fatal("Home without personalized activity shows nothing", emptyResponse.Body.String())
	}
	if _, err = db.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES(?,?,25000,0,0,'fixture'),(?,?,60000,0,0,'fixture')`, identity.PersonalKey(reader.Viewer), itemA.ID, identity.PersonalKey(owner.Viewer), itemB.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO progress_activity VALUES(?,?,?,?,?),(?,?,?,?,?)`, identity.PersonalKey(reader.Viewer), "a", itemA.ID, "2026-01-01", "paused", identity.PersonalKey(owner.Viewer), "b", itemB.ID, "2026-01-02", "paused"); err != nil {
		t.Fatal(err)
	}
	settleCompactCatalogue(t, db)
	playbackDetail := request("GET", "/v1/items/"+itemA.Public+"/detail", reader.AccessToken, "")
	if err = json.Unmarshal(playbackDetail.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	resumeOK, startOK := false, false
	for _, action := range detail.Actions {
		if action.ID == "resume" && action.Playback != nil && *action.Playback.StartSeconds == 25 {
			resumeOK = true
		}
		if action.ID == "start_over" && action.Playback != nil && *action.Playback.StartSeconds == 0 {
			startOK = true
		}
	}
	if !resumeOK || !startOK {
		t.Fatal("server playback intents absent", detail.Actions)
	}
	// What a Home says, without the moment it was generated at.
	readHome := func(token string) (catalog.HomeDocument, string) {
		t.Helper()
		w := request("GET", "/v1/home?limit=12", token, "")
		var result catalog.HomeDocument
		if w.Code != 200 {
			t.Fatal("home status", w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		result = composeHome(token, w.Body.Bytes())
		said := result
		said.GeneratedAt = ""
		wire, err := json.Marshal(said)
		if err != nil {
			t.Fatal(err)
		}
		return result, string(wire)
	}
	memberHome, wire := readHome(reader.AccessToken)
	if memberHome.ServerID != ident.ID() || memberHome.ViewerFence == "" || len(memberHome.Rows) == 0 {
		t.Fatal("member home scope", memberHome)
	}
	shown := 0
	for _, row := range memberHome.Rows {
		for _, entry := range row.Entries {
			shown++
			if entry.LibraryID != "a" {
				t.Fatal("Home permission leak", entry)
			}
		}
	}
	if shown == 0 {
		t.Fatal("member Home shows nothing, so the permission check above checked nothing")
	}
	_, again := readHome(reader.AccessToken)
	if wire != again {
		t.Fatal("equal requests changed semantics")
	}
	c.Fields(c.ID(itemB.Public), map[string]any{"title": "Hidden change"})
	c.Drain()
	_, afterHidden := readHome(reader.AccessToken)
	if wire != afterHidden {
		t.Fatal("unauthorized catalog affected Home revision or rows")
	}
	ownerHome, _ := readHome(owner.AccessToken)
	if len(ownerHome.Rows) == 0 || len(ownerHome.Rows[0].Entries) == 0 || ownerHome.Rows[0].Entries[0].ID != itemB.Public {
		t.Fatal("owner Home", ownerHome)
	}
	if w := request("GET", "/v1/home", "", ""); w.Code != 401 {
		t.Fatal("anonymous Home allowed", w.Code)
	}
	if w := request("GET", "/v1/home?sort=title", reader.AccessToken, ""); w.Code != 400 {
		t.Fatal("client Home sort admitted", w.Code)
	}
	if w := request("GET", "/v1/content?view=home&limit=12", reader.AccessToken, ""); w.Code != 400 {
		t.Fatal("the retired content view of Home still answers", w.Code)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, itemA.Asset, false)
	})
	c.Drain()
	unavailable, _ := readHome(reader.AccessToken)
	for _, row := range unavailable.Rows {
		for _, entry := range row.Entries {
			if entry.ID == itemA.Public && (entry.Playback != nil || entry.Available == nil || *entry.Available) {
				t.Fatal("unavailable source offered Home playback", row.ID, entry)
			}
		}
	}
	if _, err = db.Exec(`UPDATE direct_memberships SET disabled=1 WHERE account_id='member'`); err != nil {
		t.Fatal(err)
	}
	if w := request("PUT", "/v1/items/"+itemA.Public+"/personal-state", reader.AccessToken, mutation); w.Code != 401 {
		t.Fatal("receipt replay bypassed disabled membership", w.Code)
	}
	if w := request("GET", "/v1/home", reader.AccessToken, ""); w.Code != 401 {
		t.Fatal("disabled membership received Home", w.Code, w.Body.String())
	}

}
