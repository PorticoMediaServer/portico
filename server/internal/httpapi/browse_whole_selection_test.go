package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

type wholeSelectionBody struct {
	WholeSelection *struct {
		Selector struct {
			Query struct {
				LibraryID string                   `json:"libraryId"`
				Pivot     string                   `json:"pivot"`
				Filter    json.RawMessage          `json:"filter"`
				Sort      []map[string]any         `json:"sort"`
			} `json:"query"`
		} `json:"selector"`
		CatalogRevision string `json:"catalogRevision"`
		Total           int    `json:"total"`
		Types           []struct {
			Kind             string         `json:"kind"`
			Count            int            `json:"count"`
			CapabilityCounts map[string]int `json:"capabilityCounts"`
			Libraries        []struct {
				LibraryID string `json:"libraryId"`
				Count     int    `json:"count"`
			} `json:"libraries"`
		} `json:"types"`
	} `json:"wholeSelection"`
	PageInfo struct {
		Revision   string `json:"revision"`
		NextCursor string `json:"nextCursor"`
		Total      int    `json:"total"`
	} `json:"pageInfo"`
}

// The drawn "Select all N" rides on the first page of a view whose job replay is
// provably identical, and on no other page.
func TestBrowseWholeSelectionIsPublishedOncePerReplayableView(t *testing.T) {
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
	c := catalogtest.New(t, db)
	library := c.Library("a", "A", "movie", "/a")
	c.Movie(library, "/a/alpha.mkv", "Alpha", 1995)
	c.Movie(library, "/a/beta.mkv", "Beta", 2005)
	c.Drain()
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',x'00','owner-profile',1)`); err != nil {
		t.Fatal(err)
	}
	owner, err := ident.Issue("owner", "owner-profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	handler := New(Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db)})
	read := func(body string) (wholeSelectionBody, *httptest.ResponseRecorder) {
		t.Helper()
		r := httptest.NewRequest("POST", "/v1/libraries/a/browse", bytes.NewBufferString(body))
		r.Header.Set("Authorization", "Bearer "+owner.AccessToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var out wholeSelectionBody
		if w.Code == 200 {
			if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err, w.Body.String())
			}
			assertSpecResponse(t, "POST", "/v1/libraries/{id}/browse", w)
		}
		return out, w
	}
	first, w := read(`{"pivot":"movies","limit":1}`)
	if w.Code != 200 || first.WholeSelection == nil {
		t.Fatalf("first page %d %s", w.Code, w.Body.String())
	}
	whole := first.WholeSelection
	if whole.Total != 2 || whole.CatalogRevision != first.PageInfo.Revision || whole.CatalogRevision == "" {
		t.Fatalf("identity %+v page %+v", whole, first.PageInfo)
	}
	if whole.Selector.Query.LibraryID != "a" || whole.Selector.Query.Pivot != "movies" || len(whole.Selector.Query.Sort) == 0 {
		t.Fatalf("selector %+v", whole.Selector.Query)
	}
	if len(whole.Types) != 1 || whole.Types[0].Kind != "movie" || whole.Types[0].Count != 2 {
		t.Fatalf("types %+v", whole.Types)
	}
	if len(whole.Types[0].Libraries) != 1 || whole.Types[0].Libraries[0].LibraryID != "a" || whole.Types[0].Libraries[0].Count != 2 {
		t.Fatalf("libraries %+v", whole.Types[0].Libraries)
	}
	// The counts come from the same live authority the entries get. This
	// fixture wires only the router's own playback service, so Queue is offered
	// and everything that needs a scheduler, downloads, metadata or the
	// administration setting is not. Nothing is inferred from the kind.
	want := map[string]int{"play": 0, "queue": 2, "playlist": 0, "myList": 0, "watched": 0, "collection": 0,
		"favorite": 0, "download": 0, "edit": 0, "refresh": 0, "delete": 0}
	for key, expected := range want {
		if value, ok := whole.Types[0].CapabilityCounts[key]; !ok || value != expected {
			t.Fatalf("capability %q = %d (present %t), want %d", key, value, ok, expected)
		}
	}
	// A later page carries no aggregate, and the fixture must really page.
	if first.PageInfo.NextCursor == "" {
		t.Fatalf("fixture did not page at limit 1: %s", w.Body.String())
	}
	next, w := read(`{"pivot":"movies","limit":1,"cursor":"` + first.PageInfo.NextCursor + `"}`)
	if w.Code != 200 || next.WholeSelection != nil {
		t.Fatalf("cursor page carried an aggregate: %d %s", w.Code, w.Body.String())
	}
	// A seeded order matches different titles when a job replays it.
	seeded, w := read(`{"pivot":"movies","limit":40,"sort":[{"field":"random","direction":"asc","seed":9}]}`)
	if w.Code != 200 || seeded.WholeSelection != nil {
		t.Fatalf("seeded view carried an aggregate: %d %s", w.Code, w.Body.String())
	}
}
