package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestPlaylistHTTPShareNeverGrantsMedia(t *testing.T) {
	root := t.TempDir()
	db, e := persistence.Open(filepath.Join(root, "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ident, e := identity.New(db, root)
	if e != nil {
		t.Fatal(e)
	}
	operations.WireSetupRemote(ident)
	secret, e := ident.BrowserSetupToken(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	setup, e := ident.SetupWithOptions(context.Background(), identity.SetupOptions{SetupToken: secret, AuthMode: "local", Username: "private-login", Password: "Local-password1!", Name: "Fixture"}, true)
	if e != nil {
		t.Fatal(e)
	}
	owner := setup.Envelope
	_, e = db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('a','A','movie','/a'),('b','B','movie','/b')`)
	if e != nil {
		t.Fatal(e)
	}
	aItem, _ := seedHTTPAPICatalogEntity(t, db, "a", compactcatalog.Movie, "a-item", "Allowed", 0, nil)
	bItem, _ := seedHTTPAPICatalogEntity(t, db, "b", compactcatalog.Movie, "b-item", "Forbidden Title", 0, nil)
	control, e := hosted.New(db, ident, "http://127.0.0.1:19410", testHostedRootPin(), testHostedRootID())
	if e != nil {
		t.Fatal(e)
	}
	policy := hosted.Policy{ServerID: ident.ID(), Revision: 1, IssuedAt: time.Now().UTC().Format(time.RFC3339), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), Members: []hosted.Member{{AccountID: "member", ProfileID: "member-p", Role: "member", AllowedLibraries: []string{"a"}}}}
	raw, _ := json.Marshal(policy)
	if e = storeHTTPFixturePolicy(db, raw); e != nil {
		t.Fatal(e)
	}
	member, e := issuePorticoFixture(t, db, ident, "member", "member-p", "member")
	if e != nil {
		t.Fatal(e)
	}
	handler := New(Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db), Hosted: control})
	request := func(method, path, token string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		if body == nil {
			raw = nil
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	create := request("POST", "/v1/playlists", owner.AccessToken, map[string]any{"operationId": "create", "name": "Mixed", "summary": "A list"})
	if create.Code != 200 {
		t.Fatal(create.Code, create.Body.String())
	}
	var receipt catalog.PlaylistReceipt
	json.Unmarshal(create.Body.Bytes(), &receipt)
	id := receipt.PlaylistID
	if receipt.ServerID != ident.ID() || receipt.ViewerFence == "" {
		t.Fatal(receipt)
	}
	for i, item := range []string{aItem, bItem} {
		w := request("POST", "/v1/playlists/"+id+"/entries", owner.AccessToken, map[string]any{"operationId": item, "expectedRevision": i + 1, "itemId": item})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	share := map[string]any{"operationId": "share", "expectedRevision": 3, "authority": "local", "accountId": "member", "profileId": "member-p", "role": "editor"}
	retired := map[string]any{"operationId": "retired-share", "expectedRevision": 3, "authority": "hosted", "accountId": "member", "profileId": "member-p", "role": "editor"}
	if w := request("PUT", "/v1/playlists/"+id+"/shares", owner.AccessToken, retired); w.Code != 400 {
		t.Fatal("retired authority reached persistence", w.Code, w.Body.String())
	}
	if w := request("PUT", "/v1/playlists/"+id+"/shares", owner.AccessToken, share); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	orderResponse := request("GET", "/v1/playlists/"+id+"/order", member.AccessToken, nil)
	if orderResponse.Code != 200 {
		t.Fatal(orderResponse.Code, orderResponse.Body.String())
	}
	var fullOrder catalog.PlaylistOrder
	if e = json.Unmarshal(orderResponse.Body.Bytes(), &fullOrder); e != nil || len(fullOrder.EntryIDs) != 2 || fullOrder.Revision != 4 || fullOrder.ServerID != ident.ID() || fullOrder.ViewerFence == "" {
		t.Fatal(fullOrder, e)
	}
	if bytes.Contains(orderResponse.Body.Bytes(), []byte(bItem)) || bytes.Contains(orderResponse.Body.Bytes(), []byte("Forbidden")) {
		t.Fatal("order exposed media", orderResponse.Body.String())
	}
	firstWindow := request("GET", "/v1/playlists/"+id+"/order?limit=1", member.AccessToken, nil)
	var window catalog.PlaylistOrder
	if e = json.Unmarshal(firstWindow.Body.Bytes(), &window); e != nil || firstWindow.Code != 200 || len(window.EntryIDs) != 1 || window.NextCursor == "" {
		t.Fatal(firstWindow.Code, firstWindow.Body.String(), e)
	}
	secondWindow := request("GET", "/v1/playlists/"+id+"/order?limit=1&cursor="+window.NextCursor, member.AccessToken, nil)
	var next catalog.PlaylistOrder
	if e = json.Unmarshal(secondWindow.Body.Bytes(), &next); e != nil || secondWindow.Code != 200 || len(next.EntryIDs) != 1 || next.EntryIDs[0] == window.EntryIDs[0] || next.NextCursor != "" {
		t.Fatal(secondWindow.Code, secondWindow.Body.String(), e)
	}
	if invalid := request("PUT", "/v1/playlists/"+id+"/order", member.AccessToken, map[string]any{"operationId": "missing-revision", "entryIds": window.EntryIDs}); invalid.Code != 428 {
		t.Fatal("missing revision accepted", invalid.Code)
	}
	if invalid := request("GET", "/v1/playlists/"+id+"/order?limit=101", owner.AccessToken, nil); invalid.Code != 400 {
		t.Fatal("order exceeded page limit", invalid.Code)
	}
	candidates := request("GET", "/v1/playlists/"+id+"/share-candidates", owner.AccessToken, nil)
	if candidates.Code != 200 || bytes.Contains(candidates.Body.Bytes(), []byte("private-login")) {
		t.Fatal(candidates.Code, candidates.Body.String())
	}
	w := request("GET", "/v1/playlists/"+id+"/content?limit=1", member.AccessToken, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var page catalog.ContentEnvelope
	json.Unmarshal(w.Body.Bytes(), &page)
	if page.PlaylistRevision == nil || *page.PlaylistRevision != 4 {
		t.Fatal(page)
	}
	w = request("GET", "/v1/playlists/"+id+"/content?limit=1&cursor="+page.Sections[0].NextCursor, member.AccessToken, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("Forbidden")) || bytes.Contains(w.Body.Bytes(), []byte(bItem)) {
		t.Fatal("shared media leak", w.Body.String())
	}
	if w = request("POST", "/v1/playlists/"+id+"/entries", member.AccessToken, map[string]any{"operationId": "denied", "expectedRevision": 4, "itemId": bItem}); w.Code != 404 {
		t.Fatal("share granted admission", w.Code, w.Body.String())
	}
	if w = request("PATCH", "/v1/playlists/"+id, member.AccessToken, map[string]any{"operationId": "rename", "expectedRevision": 4, "name": "No"}); w.Code != 401 {
		t.Fatal("editor renamed", w.Code)
	}
	if w = request("GET", "/v1/playlists/"+id+"/share-candidates", member.AccessToken, nil); w.Code != 401 {
		t.Fatal("editor candidate access", w.Code)
	}
	w = request("GET", "/v1/content?view=playlists", member.AccessToken, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Saved directory cards may contain canonical entries awaiting browse
	// publication. Missing artwork must not turn the directory into a 409.
	if bytes.Contains(w.Body.Bytes(), []byte("/art/poster")) {
		t.Fatal("invented artwork for unmaterialized entries", w.Body.String())
	}
	if _, e = db.Exec(`UPDATE catalog_item_details SET poster_url='/fixture-poster' WHERE entity_id IN(SELECT id FROM catalog_entities WHERE public_id IN(pid_blob(?),pid_blob(?)))`, aItem, bItem); e != nil {
		t.Fatal(e)
	}
	w = request("GET", "/v1/content?view=playlists", member.AccessToken, nil)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("/v1/items/"+aItem+"/art/poster")) || bytes.Contains(w.Body.Bytes(), []byte(bItem)) {
		t.Fatal("playlist directory art lost current media access", w.Code, w.Body.String())
	}
	if w = request("DELETE", "/v1/playlists/"+id+"/shares", owner.AccessToken, map[string]any{"operationId": "remove-share", "expectedRevision": 4, "authority": "local", "accountId": "member", "profileId": "member-p"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("GET", "/v1/playlists/"+id+"/content?limit=1&cursor="+page.Sections[0].NextCursor, member.AccessToken, nil); w.Code != 404 {
		t.Fatal("revoked share cursor worked", w.Code, w.Body.String())
	}
	if denied := request("GET", "/v1/playlists/"+id+"/order", member.AccessToken, nil); denied.Code != 404 {
		t.Fatal("revoked order access", denied.Code)
	}
	if w = request("GET", "/v1/content?view=watchlist", member.AccessToken, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, e = db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE account_id=?`, owner.Viewer.AccountID); e != nil {
		t.Fatal(e)
	}
	if w = request("POST", "/v1/playlists", owner.AccessToken, map[string]any{"operationId": "create", "name": "Mixed", "summary": "A list"}); w.Code != 401 {
		t.Fatal("revoked session recovered receipt", w.Code)
	}
}
