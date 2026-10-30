package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
)

func TestTVDBOwnerSelectionHTTPAndCAS(t *testing.T) {
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
	_, e = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','owner-p',1),('member','member',x'00','member-p',1)`)
	if e != nil {
		t.Fatal(e)
	}
	c := catalogtest.New(t, db)
	library := c.Library("lib", "TV", "tv", "/media")
	show := c.Show(library, "Show", 2020)
	if _, e = db.Exec(`INSERT INTO tvdb_series_candidates VALUES(?,42,'Show',2020,'Known overview','{}','2026-09-04T00:00:00Z');UPDATE tvdb_jobs SET status='needs_order',provider_id=42 WHERE show_id=?`, show.ID, show.ID); e != nil {
		t.Fatal(e)
	}
	c.Drain()
	owner, e := ident.Issue("owner", "owner-p", "local", "owner", 1)
	if e != nil {
		t.Fatal(e)
	}
	member, e := ident.Issue("member", "member-p", "local", "member", 1)
	if e != nil {
		t.Fatal(e)
	}
	control, e := hosted.New(db, ident, "", "", "")
	if e != nil {
		t.Fatal(e)
	}
	handler := New(Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db), Metadata: metadata.New(db, ""), Hosted: control})
	request := func(method, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/shows/"+show.Public+"/metadata/tvdb", bytes.NewBufferString(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, token := range []string{"", member.AccessToken} {
		if w := request("GET", token, ""); w.Code != tl6RefusedStatus(token) {
			t.Fatal("nonowner metadata read", w.Code)
		}
		if w := request("PUT", token, `{"expectedRevision":1,"providerId":42,"order":"official"}`); w.Code != tl6RefusedStatus(token) {
			t.Fatal("nonowner match selection", w.Code)
		}
	}
	w := request("GET", owner.AccessToken, "")
	var state metadata.TVDBState
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &state) != nil || state.ServerID != ident.ID() || state.LibraryID != "lib" || state.ViewerFence == "" || len(state.Candidates) != 1 || state.Order != "" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("PUT", owner.AccessToken, `{"expectedRevision":1,"providerId":42,"order":""}`); w.Code != 400 {
		t.Fatal("implicit ordering accepted", w.Code, w.Body.String())
	}
	body := `{"expectedRevision":1,"providerId":42,"order":"official"}`
	if w = request("PUT", owner.AccessToken, body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("PUT", owner.AccessToken, body); w.Code != 409 || !bytes.Contains(w.Body.Bytes(), []byte("metadata_conflict")) {
		t.Fatal("stale selection admitted", w.Code, w.Body.String())
	}
	if _, e = db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE account_id='owner'`); e != nil {
		t.Fatal(e)
	}
	if w = request("GET", owner.AccessToken, ""); w.Code != 401 {
		t.Fatal("revoked owner read", w.Code)
	}
}
