package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// "Not interested" on the personal batch (items and the shows, albums and
// books recommendations offer), and "Reset recommendations".
func TestRecommendationFeedbackRoutes(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ident, e := identity.New(db, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('a','Allowed','movie','/a'),('b','Private','tv','/b'),('c','Shows','tv','/c');`); e != nil {
		t.Fatal(e)
	}
	film, _ := seedHTTPAPICatalogEntity(t, db, "a", compactcatalog.Movie, compactcatalog.ItemKey("/a", "/a/a1.mkv", 0), "Harbor First", 0, nil)
	show, _ := seedHTTPAPICatalogEntity(t, db, "c", compactcatalog.Show, compactcatalog.ShowKey("harbor:2020"), "Harbor Show", 0, nil)
	hiddenShow, _ := seedHTTPAPICatalogEntity(t, db, "b", compactcatalog.Show, compactcatalog.ShowKey("secret:2020"), "Secret Show", 0, nil)
	// A show is checked against its browse row: derive them.
	catalogtest.New(t, db).Drain()
	control, e := hosted.New(db, ident, "http://127.0.0.1:19410", testHostedRootPin(), testHostedRootID())
	if e != nil {
		t.Fatal(e)
	}
	policy := hosted.Policy{ServerID: ident.ID(), Revision: 1, IssuedAt: time.Now().UTC().Format(time.RFC3339), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), Members: []hosted.Member{{AccountID: "member", ProfileID: "p", Role: "member", AllowedLibraries: []string{"a", "c"}}}}
	raw, _ := json.Marshal(policy)
	if e = storeHTTPFixturePolicy(db, raw); e != nil {
		t.Fatal(e)
	}
	session, e := issuePorticoFixture(t, db, ident, "member", "p", "member")
	if e != nil {
		t.Fatal(e)
	}
	handler := New(Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db), Hosted: control})
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	batch := func(operation string, rows ...map[string]any) catalog.PersonalBatchReceipt {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"operationId": operation, "items": rows})
		w := request("PUT", "/v1/items/personal-state:batch", session.AccessToken, string(body))
		if w.Code != 200 {
			t.Fatal("batch", operation, w.Code, w.Body.String())
		}
		var out catalog.PersonalBatchReceipt
		if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	first := batch("hide-1", map[string]any{"itemId": film, "notInterested": true}, map[string]any{"itemId": show, "notInterested": true}, map[string]any{"itemId": hiddenShow, "notInterested": true})
	if first.Updated != 2 || !first.Results[0].Personal.NotInterested || !first.Results[1].Personal.NotInterested || first.Results[2].OK || first.Results[2].Code != "not_found" {
		t.Fatalf("not interested: %+v", first)
	}
	if replay := batch("hide-1", map[string]any{"itemId": film, "notInterested": true}, map[string]any{"itemId": show, "notInterested": true}, map[string]any{"itemId": hiddenShow, "notInterested": true}); replay.Results[0].Personal.Revision != first.Results[0].Personal.Revision {
		t.Fatalf("a replay applied again: %+v", replay)
	}
	// A show takes nothing but "not interested".
	if w := batch("show-watch", map[string]any{"itemId": show, "watchlisted": true}); w.Failed != 1 || w.Results[0].Code != "not_found" {
		t.Fatalf("watchlisted on a show: %+v", w)
	}
	undo := batch("hide-1-undo", map[string]any{"itemId": film, "notInterested": false})
	if !undo.Results[0].OK || undo.Results[0].Personal.NotInterested {
		t.Fatalf("undo: %+v", undo)
	}

	reset := `{"operationId":"reset-1"}`
	w := request("POST", "/v1/me/recommendations:reset", session.AccessToken, reset)
	if w.Code != 200 {
		t.Fatal("reset", w.Code, w.Body.String())
	}
	var receipt struct {
		ServerID             string `json:"serverId"`
		OperationID          string `json:"operationId"`
		ClearedNotInterested int64  `json:"clearedNotInterested"`
	}
	if e = json.Unmarshal(w.Body.Bytes(), &receipt); e != nil || receipt.ClearedNotInterested != 1 || receipt.OperationID != "reset-1" || receipt.ServerID != ident.ID() {
		t.Fatalf("reset receipt: %s %v", w.Body.String(), e)
	}
	var marked int
	if e = db.QueryRow(`SELECT count(*) FROM personal_items WHERE not_interested=1`).Scan(&marked); e != nil || marked != 0 {
		t.Fatal("a mark survived the reset", marked, e)
	}
	if again := request("POST", "/v1/me/recommendations:reset", session.AccessToken, reset); again.Code != 200 || again.Body.String() != w.Body.String() {
		t.Fatal("replayed reset", again.Code, again.Body.String())
	}
	if other := request("POST", "/v1/me/recommendations:reset", session.AccessToken, `{"operationId":"reset-1","extra":1}`); other.Code != 400 {
		t.Fatal("unknown field accepted", other.Code)
	}
	if anon := request("POST", "/v1/me/recommendations:reset", "", reset); anon.Code != 401 {
		t.Fatal("anonymous reset", anon.Code)
	}
}
