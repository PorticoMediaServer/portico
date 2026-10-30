package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
	"time"
)

func TestShowWorkspaceHTTPPermissionAndParentResolution(t *testing.T) {
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
	c := catalogtest.New(t, db)
	a := c.Library("a", "TV", "tv", "/a")
	b := c.Library("b", "Hidden", "tv", "/b")
	showA := c.Show(a, "Visible", 0)
	showB := c.Show(b, "Hidden", 0)
	seasonA := c.Season(showA, 1)
	seasonB := c.Season(showB, 1)
	c.Drain()
	control, e := hosted.New(db, ident, "http://127.0.0.1:19410", tl6HostedRootPin(), tl6HostedRootID())
	if e != nil {
		t.Fatal(e)
	}
	policy := hosted.Policy{ServerID: ident.ID(), Revision: 1, IssuedAt: time.Now().UTC().Format(time.RFC3339), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), Members: []hosted.Member{{AccountID: "account", ProfileID: "profile", Role: "member", AllowedLibraries: []string{"a"}}}}
	apply := func() {
		if len(policy.Members) == 0 {
			if _, e = db.Exec(`UPDATE direct_memberships SET disabled=1 WHERE account_id='account'`); e != nil {
				t.Fatal(e)
			}
		} else {
			encoded, _ := json.Marshal(policy.Members[0].AllowedLibraries)
			if _, e = db.Exec(`UPDATE direct_memberships SET allowed_libraries=?,revision=revision+1 WHERE account_id='account'`, string(encoded)); e != nil {
				t.Fatal(e)
			}
		}
		raw, _ := json.Marshal(policy)
		if e = storeHTTPFixturePolicy(db, raw); e != nil {
			t.Fatal(e)
		}
	}
	apply()
	session, e := tl6IssueHostedFixture(t, db, ident, "account", "profile", "member")
	if e != nil {
		t.Fatal(e)
	}
	h := New(Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db), Hosted: control})
	read := func(path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	path := "/v1/libraries/a/show-workspace?seasonId=" + seasonA.Public
	if w := read(path, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := read(path, session.AccessToken)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var out catalog.ShowWorkspace
	if e = json.Unmarshal(w.Body.Bytes(), &out); e != nil || out.Show.ID != showA.Public || out.Scope.LibraryID != "a" || out.Selected.SeasonID == nil || *out.Selected.SeasonID != seasonA.Public || out.Scope.ViewerFence == "" {
		t.Fatal(out, e)
	}
	for _, direction := range []string{"asc", "desc"} {
		ordered := read(path+"&episodeDirection="+direction, session.AccessToken)
		var workspace catalog.ShowWorkspace
		if ordered.Code != 200 || json.Unmarshal(ordered.Body.Bytes(), &workspace) != nil || workspace.Episodes.Query.Direction != direction {
			t.Fatalf("HTTP order not propagated: %d %s", ordered.Code, ordered.Body.String())
		}
	}
	for _, bad := range []string{"/v1/libraries/b/show-workspace?showId=" + showB.Public, "/v1/libraries/a/show-workspace?showId=" + showA.Public + "&seasonId=" + seasonB.Public, path + "&seasonId=" + seasonB.Public, path + "&limit=1000", path + "&unknown=1", path + "&episodeDirection=sideways", path + "&episodeDirection=asc&episodeDirection=desc", path + "&episodeSort=title"} {
		if w = read(bad, session.AccessToken); w.Code == 200 {
			t.Fatal("unauthorized/invalid context accepted", bad)
		}
	}
	policy.Revision++
	policy.Members = nil
	apply()
	if w = read(path, session.AccessToken); w.Code != 401 {
		t.Fatal("revoked member retained show workspace", w.Code)
	}
}
