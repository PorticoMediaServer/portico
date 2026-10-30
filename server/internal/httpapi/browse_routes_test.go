package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"testing"
)

// The browse routes are exercised end to end: the vocabulary a client reads, the
// expression it posts, the facets it counts, and the pins it arranges.
func TestBrowseRoutesPublishAndExecuteTheVocabulary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ident, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	c := catalogtest.New(t, db)
	a := c.Library("a", "A", "movie", "/a")
	c.Library("b", "B", "tv", "/b")
	names := catalogtest.Names{
		"m1": c.Movie(a, "/a/alpha.mkv", "Alpha", 1995),
		"m2": c.Movie(a, "/a/beta.mkv", "Beta", 2005),
	}
	c.Fields(names["m1"].ID, map[string]any{"added_text": "2026-09-01T00:00:00.000Z"})
	c.Fields(names["m2"].ID, map[string]any{"added_text": "2026-09-02T00:00:00.000Z"})
	c.Genres(names["m1"].ID, "tmdb", "Action")
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','owner-profile',1)`); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	owner, err := ident.Issue("owner", "owner-profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	d := Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db)}
	startFacets := composeHTTPFacetFixture(t, &d)
	startFacets()
	handler := New(d)
	rawRequest := func(ctx context.Context, method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body)).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer "+owner.AccessToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		if isHTTPFixtureFacetPath(method, path) {
			return readyHTTPFacetFixture(t, path, rawRequest)
		}
		return rawRequest(context.Background(), method, path, body)
	}
	decodeBody := func(w *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var out map[string]any
		if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			t.Fatal(e, w.Body.String())
		}
		return out
	}

	w := request("GET", "/v1/libraries/a/browse-capabilities?pivot=movies", "")
	if w.Code != 200 {
		t.Fatalf("capabilities %d %s", w.Code, w.Body.String())
	}
	capabilities := decodeBody(w)
	if capabilities["resolvedPivot"] == nil || len(capabilities["fields"].([]any)) == 0 || len(capabilities["quickFilters"].([]any)) != 7 {
		t.Fatalf("capabilities %s", w.Body.String())
	}

	w = request("POST", "/v1/libraries/a/browse", `{"pivot":"movies","query":{"field":"genre","operator":"contains","value":"Action"},"sort":[{"field":"title","direction":"asc"}],"limit":10}`)
	if w.Code != 200 {
		t.Fatalf("browse %d %s", w.Code, w.Body.String())
	}
	page := decodeBody(w)
	entries := page["entries"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["id"] != names["m1"].Public {
		t.Fatalf("entries %s", w.Body.String())
	}
	info := page["pageInfo"].(map[string]any)
	if info["total"].(float64) != 1 || info["revision"] == "" || page["positionIndex"] == nil {
		t.Fatalf("pageInfo %s", w.Body.String())
	}

	w = request("POST", "/v1/libraries/a/browse", `{"query":{"field":"nonsense","operator":"equals","value":"x"}}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"field":"query.field"`) {
		t.Fatalf("validation %d %s", w.Code, w.Body.String())
	}
	w = request("POST", "/v1/libraries/a/browse", `{"pivot":"songs"}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_pivot") {
		t.Fatalf("pivot %d %s", w.Code, w.Body.String())
	}

	w = request("GET", "/v1/libraries/a/facets?field=genre", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"Action"`) {
		t.Fatalf("facets %d %s", w.Code, w.Body.String())
	}
	if w = request("GET", "/v1/libraries/a/facets?field=nonsense", ""); w.Code != 400 {
		t.Fatalf("unknown facet %d", w.Code)
	}

	// CD-02: a client filters with the facet values it was given. The decade
	// facet publishes strings ("1990"); browsing with them must work.
	w = request("GET", "/v1/libraries/a/facets?field=decade", "")
	tl11AssertBrowseSpecResponse(t, "GET", "/v1/libraries/{id}/facets", w)
	var decades struct {
		Values []struct {
			Value any `json:"value"`
		} `json:"values"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &decades); e != nil || len(decades.Values) == 0 {
		t.Fatalf("decade facet %v %s", e, w.Body.String())
	}
	chosen, _ := json.Marshal([]any{decades.Values[0].Value})
	w = request("POST", "/v1/libraries/a/browse", `{"pivot":"movies","query":{"field":"decade","operator":"in","value":`+string(chosen)+`},"limit":10}`)
	if w.Code != 200 || len(decodeBody(w)["entries"].([]any)) != 1 {
		t.Fatalf("browse by a facet decade %s: %d %s", chosen, w.Code, w.Body.String())
	}
	w = request("POST", "/v1/libraries/a/browse", `{"pivot":"movies","query":{"field":"decade","operator":"equals","value":"nineties"},"limit":10}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "must be numeric") {
		t.Fatalf("non-numeric decade %d %s", w.Code, w.Body.String())
	}

	if w = request("GET", "/v1/me/library-navigation", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"pinnedLibraryIds":[]`) {
		t.Fatalf("navigation %d %s", w.Code, w.Body.String())
	}
	if w = request("PUT", "/v1/me/library-navigation", `{"expectedRevision":0,"pinnedLibraryIds":["b"]}`); w.Code != 200 {
		t.Fatalf("pin libraries %d %s", w.Code, w.Body.String())
	}
	if w = request("PUT", "/v1/me/library-navigation", `{"expectedRevision":0,"pinnedLibraryIds":["a"]}`); w.Code != 409 {
		t.Fatalf("stale pin revision %d %s", w.Code, w.Body.String())
	}
	w = request("GET", "/v1/libraries", "")
	if w.Code != 200 {
		t.Fatalf("libraries %d", w.Code)
	}
	listing := decodeBody(w)["items"].([]any)
	first := listing[0].(map[string]any)
	if first["id"] != "b" || first["pinned"] != true || listing[1].(map[string]any)["pinned"] != false {
		t.Fatalf("library order %s", w.Body.String())
	}

	if w = request("GET", "/v1/saved-pins/order", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"order":[]`) {
		t.Fatalf("pin order %d %s", w.Code, w.Body.String())
	}
	if w = request("PUT", "/v1/saved-pins/order", `{"expectedRevision":0,"order":[{"kind":"view","id":"missing"}]}`); w.Code == 200 {
		t.Fatalf("unpinned resource accepted %s", w.Body.String())
	}
}

func tl11AssertBrowseSpecResponse(t *testing.T, method, path string, w *httptest.ResponseRecorder) {
	t.Helper()
	if err := apispec.ValidateErrorEnvelope(w.Code, w.Body.Bytes()); method != "HEAD" && err != nil {
		t.Fatalf("%s %s %d: %v", method, path, w.Code, err)
	}
	doc, schema, err := apispec.Response(method, path, w.Code)
	if err != nil {
		t.Fatal(err)
	}
	if problems := doc.ValidateJSON(schema, w.Body.Bytes()); len(problems) > 0 {
		t.Fatalf("%s %s %d does not match %s: %v\n%s", method, path, w.Code, doc.File, problems, w.Body.String())
	}
}
