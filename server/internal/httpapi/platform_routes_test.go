package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestTitleQueryPagesByIndexAndAppliesTheCallersRestrictions(t *testing.T) {
	f := administrationFixtureNew(t)
	c := catalogtest.New(t, f.deps.DB)
	library := c.Library("movies", "Movies", "movie", "/movies")
	other := c.Library("private", "Private", "movie", "/private")
	titles := []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo"}
	items := map[string]catalogtest.Item{}
	for i, title := range titles {
		items[title] = c.Movie(library, "/movies/"+title+".mkv", title, 2000+i)
	}
	c.Movie(other, "/private/hidden.mkv", "Hidden", 1999)
	c.Attributes(items["Alpha"].ID, "contentRating", "G")
	c.Attributes(items["Bravo"].ID, "contentRating", "R")
	c.Attributes(items["Charlie"].ID, "contentRating", "G")
	c.Attributes(items["Delta"].ID, "contentRating", "R")
	c.Attributes(items["Echo"].ID, "contentRating", "G")
	if _, err := f.deps.DB.Exec(`INSERT INTO catalog_external_ids(entity_id,provider,provider_kind,provider_id) VALUES(?,?,?,?)`, items["Charlie"].ID, "tmdb", "movie", "603"); err != nil {
		t.Fatal(err)
	}
	c.Drain()

	read := func(token, query string) (int, catalog.TitlePage, string) {
		t.Helper()
		w := f.call("GET", "/v1/titles?"+query, token, "")
		var page catalog.TitlePage
		_ = json.Unmarshal(w.Body.Bytes(), &page)
		return w.Code, page, w.Body.String()
	}
	names := func(page catalog.TitlePage) string {
		out := []string{}
		for _, item := range page.Items {
			out = append(out, item.Title)
		}
		return strings.Join(out, ",")
	}
	// The owner walks the library two at a time, in title order, to the end.
	seen, cursor := []string{}, ""
	for range 5 {
		code, page, body := read(f.owner, "library=movies&kind=movie&limit=2&cursor="+url.QueryEscape(cursor))
		if code != 200 || page.Items == nil || page.Limit != 2 {
			t.Fatalf("page: %d %s", code, body)
		}
		for _, item := range page.Items {
			seen = append(seen, item.Title)
			if item.LibraryID != "movies" || item.Kind != "movie" || item.ProviderIDs == nil || !item.Available {
				t.Fatalf("row: %+v", item)
			}
		}
		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}
	if strings.Join(seen, ",") != "Alpha,Bravo,Charlie,Delta,Echo" {
		t.Fatalf("walk: %v", seen)
	}
	// Sorting by year descending with a range on the same key.
	if code, page, body := read(f.owner, "library=movies&kind=movie&sort=year&order=desc&yearFrom=2001&yearTo=2003"); code != 200 || names(page) != "Delta,Charlie,Bravo" {
		t.Fatalf("year range: %d %s", code, body)
	}
	// A provider id finds the title without naming a library.
	code, page, body := read(f.owner, "provider=tmdb&providerId=603")
	if code != 200 || names(page) != "Charlie" || page.Items[0].ProviderIDs["tmdb"] != "603" {
		t.Fatalf("provider lookup: %d %s", code, body)
	}
	if code, page, _ = read(f.owner, "provider=tmdb&providerId=604"); code != 200 || len(page.Items) != 0 {
		t.Fatalf("unknown provider id: %d %+v", code, page)
	}
	// Refusals: everything the indexes cannot answer, and anything unbounded.
	for _, bad := range []string{
		"kind=movie",                               // no library
		"library=movies",                           // no kind
		"library=movies&kind=nonsense",             // unknown kind
		"library=movies&kind=movie&sort=rating",    // no index for that sort
		"library=movies&kind=movie&order=sideways", //
		"library=movies&kind=movie&yearFrom=2000",  // range on a key that is not the sort
		"library=movies&kind=movie&sort=title&addedAfter=2020-01-01T00:00:00Z",
		"library=movies&kind=movie&sort=added&addedAfter=yesterday",
		"library=movies&kind=movie&limit=101",
		"library=movies&kind=movie&limit=0",
		"library=movies&kind=movie&limit=-1",
		"library=movies&kind=movie&cursor=forged",
		"library=movies&kind=movie&filter=anything", // unknown parameter
		"library=movies&kind=movie&kind=show",       // repeated parameter
		"provider=plex&providerId=1",
		"provider=tmdb",
		"provider=tmdb&providerId=603&sort=title",
	} {
		if code, _, body := read(f.owner, bad); code != 400 {
			t.Fatalf("%q was accepted: %d %s", bad, code, body)
		}
	}
	// A cursor belongs to the query and the caller that made it.
	_, first, _ := read(f.owner, "library=movies&kind=movie&limit=2")
	if code, _, body := read(f.owner, "library=movies&kind=movie&limit=2&sort=year&cursor="+url.QueryEscape(first.NextCursor)); code != 400 {
		t.Fatalf("a cursor was reused for another sort: %d %s", code, body)
	}
	if code, _, body := read(f.admin, "library=movies&kind=movie&limit=2&cursor="+url.QueryEscape(first.NextCursor)); code == 200 {
		t.Fatalf("a cursor was reused by another member: %d %s", code, body)
	}
	if code, _, _ := read("", "library=movies&kind=movie"); code != 401 {
		t.Fatalf("anonymous query: %d", code)
	}

	// A restricted member: the ceiling is applied in the query, so pages are
	// full pages of what they may see and the walk never reveals the rest. A
	// limit is judged by classified ages; a rating not yet classified is withheld.
	if _, err := f.deps.Catalog.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	limits := access.DefaultLimits()
	limits.MaxContentRating = "PG"
	limits.AllowUnrated = false
	current, err := f.deps.Access.Access.Limits(context.Background(), nil, "member")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.deps.Access.Access.SetLimits(context.Background(), nil, "member", access.LimitsChange{ExpectedRevision: current.Revision, OperationID: "title-query-limit", Limits: limits}); err != nil {
		t.Fatal(err)
	}
	p := identity.Principal{Viewer: identity.Viewer{AccountID: "member", ProfileID: "member-profile", Authority: "local", Role: "member"}}
	viewer, err := f.deps.catalogViewer(httptest.NewRequest("GET", "/v1/titles", nil), p, []string{"movies"}, "member-fence")
	if err != nil {
		t.Fatal(err)
	}
	restricted, err := f.deps.Catalog.QueryTitles(viewer, catalog.TitleQuery{Library: "movies", Kind: "movie", Limit: 2})
	if err != nil || names(restricted) != "Alpha,Charlie" || restricted.NextCursor == "" {
		t.Fatalf("restricted first page: %v %s", err, names(restricted))
	}
	restricted, err = f.deps.Catalog.QueryTitles(viewer, catalog.TitleQuery{Library: "movies", Kind: "movie", Limit: 2, Cursor: restricted.NextCursor})
	if err != nil || names(restricted) != "Echo" || restricted.NextCursor != "" {
		t.Fatalf("restricted second page: %v %s", err, names(restricted))
	}
	// A library outside the caller's own reads as absent, by list and by lookup.
	if _, err = f.deps.Catalog.QueryTitles(viewer, catalog.TitleQuery{Library: "private", Kind: "movie"}); !errors.Is(err, catalog.ErrTitleLibrary) {
		t.Fatalf("a library the member may not see answered: %v", err)
	}
	if _, err = f.deps.DB.Exec(`INSERT INTO catalog_external_ids(entity_id,provider,provider_kind,provider_id) VALUES(?,?,?,?)`, items["Bravo"].ID, "tmdb", "movie", "700"); err != nil {
		t.Fatal(err)
	}
	if found, err := f.deps.Catalog.QueryTitles(viewer, catalog.TitleQuery{Provider: "tmdb", ProviderID: "700"}); err != nil || len(found.Items) != 0 {
		t.Fatalf("a restricted title was found by provider id: %v %+v", err, found)
	}
	if found, err := f.deps.Catalog.QueryTitles(catalog.Viewer{Profile: "nobody"}, catalog.TitleQuery{Provider: "tmdb", ProviderID: "603"}); err != nil || len(found.Items) != 0 {
		t.Fatalf("a viewer with no libraries found a title: %v %+v", err, found)
	}
}

func TestPathsChangedNeedsTheLibraryScopeAndAnOwner(t *testing.T) {
	f := administrationFixtureNew(t)
	body := `{"paths":[{"path":"/media/films/New Film (2024)","change":"added"}]}`
	// Without a scan service the route says so rather than pretending.
	if w := f.call("POST", "/v1/libraries/paths-changed", f.owner, body); w.Code != 503 && registryErrorCode(t, w) != "administration_unavailable" {
		t.Fatalf("no scan service: %d %s", w.Code, w.Body)
	}
	if w := f.call("POST", "/v1/libraries/paths-changed", f.member, body); w.Code != 403 {
		t.Fatalf("a member reported paths: %d %s", w.Code, w.Body)
	}
	if w := f.call("POST", "/v1/libraries/paths-changed", "", body); w.Code != 401 {
		t.Fatalf("an anonymous caller reported paths: %d %s", w.Code, w.Body)
	}
	read := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"paths-key-read","name":"Reader","scopes":["read","playback"]}`)
	if w := f.call("POST", "/v1/libraries/paths-changed", read.Secret, body); w.Code != 403 || registryErrorCode(t, w) != "insufficient_scope" {
		t.Fatalf("a key without the library scope reported paths: %d %s", w.Code, w.Body)
	}
	manage := issueCredential(t, f, "/v1/admin/api-keys", f.owner, `{"operationId":"paths-key-library","name":"Download manager","scopes":["library"]}`)
	if w := f.call("POST", "/v1/libraries/paths-changed", manage.Secret, body); w.Code == 401 || w.Code == 403 {
		t.Fatalf("a library key was refused: %d %s", w.Code, w.Body)
	}
	// A library key is not a read key.
	if w := f.call("GET", "/v1/titles?library=movies&kind=movie", manage.Secret, ""); w.Code != 403 || registryErrorCode(t, w) != "insufficient_scope" {
		t.Fatalf("a library-only key read titles: %d %s", w.Code, w.Body)
	}
	for _, bad := range []string{`{}`, `{"paths":[]}`, `{"paths":[{"path":"/x","change":"renamed"}]}`, `{"paths":[{"path":"/x"}],"extra":1}`, `{"paths":[` + strings.TrimSuffix(strings.Repeat(`{"path":"/x"},`, 201), ",") + `]}`} {
		if w := f.call("POST", "/v1/libraries/paths-changed", f.owner, bad); w.Code != 400 && registryErrorCode(t, w) != "administration_unavailable" {
			t.Fatalf("%s accepted: %d %s", fmt.Sprintf("%.60s", bad), w.Code, w.Body)
		}
	}
}
