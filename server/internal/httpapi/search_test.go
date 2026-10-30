package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"testing"
	"time"
)

func TestSearchAuthorizationAndRevocation(t *testing.T) {
	db, e := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ident, e := identity.New(db, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	c := catalogtest.New(t, db)
	a := c.Library("a", "Allowed", "movie", "/a")
	b := c.Library("b", "Private", "movie", "/b")
	c.Movie(a, "/a/first.mkv", "Harbor First", 2020)
	c.Movie(a, "/a/second.mkv", "Harbor Second", 2020)
	c.Movie(b, "/b/secret.mkv", "Harbor Secret", 2020)
	c.Drain()
	control, e := hosted.New(db, ident, "http://127.0.0.1:19410", tl6HostedRootPin(), tl6HostedRootID())
	if e != nil {
		t.Fatal(e)
	}
	apply := func(revision int64, libraries []string) {
		p := hosted.Policy{ServerID: ident.ID(), Revision: revision, IssuedAt: time.Now().UTC().Format(time.RFC3339), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), Members: []hosted.Member{{AccountID: "member", ProfileID: "p", Role: "member", AllowedLibraries: libraries}}}
		raw, _ := json.Marshal(p)
		if e = storeHTTPFixturePolicy(db, raw); e != nil {
			t.Fatal(e)
		}
	}
	apply(1, []string{"a"})
	session, e := tl6IssueHostedFixture(t, db, ident, "member", "p", "member")
	if e != nil {
		t.Fatal(e)
	}
	handler := New(Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db), Hosted: control})
	request := func(path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	path := "/v1/search?q=Harbor&group=movies&limit=1"
	w := request(path, session.AccessToken)
	if w.Code != 200 || strings.Contains(w.Body.String(), "Secret") || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body.String())
	}
	var out catalog.SearchEnvelope
	if e = json.Unmarshal(w.Body.Bytes(), &out); e != nil {
		t.Fatal(e)
	}
	if out.Groups[0].TotalCount != 2 || out.Groups[0].Status != "success" || out.Scope.ViewerFence == "" || out.Query.Sort != "relevance" {
		t.Fatal(out)
	}
	published := map[string]bool{}
	for _, capability := range out.Capabilities.Groups {
		published[capability.ID] = capability.Available
	}
	// People are a published group now; Live TV stays unavailable until a
	// lineup is configured, and the client reads that from the server.
	if !published["people"] || published["live-tv"] {
		t.Fatal("capabilities", out.Capabilities)
	}
	for _, query := range []string{"q=Harbor&q=Secret", "q=Harbor&limit=0", "q=%25%2A", "q=Harbor&group=unknown", "q=Harbor&groups=movies,unknown", "q=Harbor&sort=rank", "q=Harbor&direction=sideways"} {
		if w = request("/v1/search?"+query, session.AccessToken); w.Code != 400 {
			t.Fatal(query, w.Code, w.Body.String())
		}
	}
	if w = request(path, ""); w.Code != 401 {
		t.Fatal("anonymous", w.Code)
	}
	cursor := out.Groups[0].NextCursor
	apply(2, []string{"b"})
	if _, e = db.Exec(`UPDATE direct_memberships SET allowed_libraries='["b"]',revision=revision+1 WHERE account_id='member'`); e != nil {
		t.Fatal(e)
	}
	w = request(path+"&cursor="+url.QueryEscape(cursor), session.AccessToken)
	if w.Code != 400 && w.Code != 409 {
		t.Fatal("stale auth cursor", w.Code, w.Body.String())
	}
	w = request(path, session.AccessToken)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "Harbor First") {
		t.Fatal("revocation leak")
	}
	if _, e = db.Exec(`UPDATE direct_memberships SET disabled=1 WHERE account_id='member'`); e != nil {
		t.Fatal(e)
	}
	if w = request(path, session.AccessToken); w.Code != 401 {
		t.Fatal("restriction", w.Code, w.Body.String())
	}
}
