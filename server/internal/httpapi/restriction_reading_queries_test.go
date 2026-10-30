package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// The matrix callbacks use captured, library-scoped handles from the public
// routes. An opaque query ID is not a grant to another profile or session.
func readingBrowseQueryRestrictionCheck(method string) func(*testing.T) {
	return readingQueryRestrictionCheck(method, false)
}

func readingFacetQueryRestrictionCheck(method string) func(*testing.T) {
	return readingQueryRestrictionCheck(method, true)
}

func readingQueryRestrictionCheck(method string, facet bool) func(*testing.T) {
	return func(t *testing.T) {
		if method != http.MethodGet && method != http.MethodDelete {
			t.Fatalf("unsupported Reading query matrix method %q", method)
		}
		r := newReadingFixture(t, "book", "pdf")
		visiblePath := readingTreeFile(t, r, "Visible query publication.pdf")
		readingScan(t, r.f, r.library)
		visible := readingPathItem(t, r, visiblePath)
		const visibleAuthor = "Visible query author"
		const hiddenAuthor = "Hidden query author"
		readingSeriesOwnerField(t, r, visible, "author", visibleAuthor)
		readingSeriesOwnerField(t, r, r.item, "author", hiddenAuthor)
		c := catalogtest.New(t, r.f.db)
		c.Attributes(c.ID(visible), "contentRating", "PG")
		c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('pg',8) ON CONFLICT(value_key) DO UPDATE SET minimum_age=excluded.minimum_age`)
		readingSeriesDrain(t, r)

		ctx := context.Background()
		owner := readingOwnerLogin(t, r)
		profiles, err := r.f.id.CreateDirectProfile(ctx, owner.AccountToken, "Query matrix reader", "blue")
		if err != nil {
			t.Fatal(err)
		}
		var child identity.DirectProfile
		for _, p := range profiles.Profiles {
			if !p.Primary && p.Name == "Query matrix reader" {
				child = p
			}
		}
		if child.ID == "" {
			t.Fatal("missing real non-primary profile")
		}
		restrictionRevision := int64(1)
		setPolicy := func(tight bool) {
			t.Helper()
			edit := identity.ProfileRestrictionEdit{ExpectedRevision: restrictionRevision, AllowUnrated: false}
			if tight {
				edit.RatingSystem, edit.MaximumAgeRating = "mpaa", "G"
			}
			policy, e := r.f.id.SetProfileRestrictions(ctx, owner.AccountToken, child.ID, edit)
			if e != nil {
				t.Fatal(e)
			}
			restrictionRevision = policy.Revision
		}
		setPolicy(false)
		selected, err := r.f.id.SelectDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectSelection{})
		if err != nil || selected.Session.AccessToken == "" {
			t.Fatal("select restricted profile", err)
		}
		token := selected.Session.AccessToken
		// A fresh account login creates an independent session for the same
		// profile, so matching effective policy alone cannot grant the handle.
		otherLogin := readingOwnerLogin(t, r)
		other, err := r.f.id.SelectDirectProfile(ctx, otherLogin.AccountToken, child.ID, identity.DirectSelection{})
		if err != nil || other.Session.AccessToken == "" || other.Session.AccessToken == token {
			t.Fatal("independent same-profile session", err)
		}
		r.d.PrepareReadingBrowse, r.d.PrepareReadingFacets = !facet, facet
		h := New(r.d)
		base := "/v1/libraries/" + r.library
		startMethod, startPath := "POST", base+"/browse"
		var body any = map[string]any{"pivot": "books", "limit": 10}
		kind, queryPrefix := "reading-browse-preparing", base+"/reading-browse-queries/"
		if facet {
			startMethod, startPath, body = "GET", base+"/facets?field=author&limit=10", nil
			kind, queryPrefix = "reading-facet-preparing", base+"/reading-facet-queries/"
		}
		request := func(verb, path, bearer string) *httptest.ResponseRecorder {
			t.Helper()
			return readingHTTP(t, h, verb, path, bearer, "", nil)
		}
		assertNoFacts := func(w *httptest.ResponseRecorder, markers ...string) {
			t.Helper()
			for _, marker := range append([]string{r.item, r.source, r.path, "Original publication", hiddenAuthor}, markers...) {
				if marker != "" && (strings.Contains(w.Body.String(), marker) || strings.Contains(w.Header().Get("Location"), marker)) {
					t.Fatalf("Reading query leaked %q: %d %s", marker, w.Code, w.Body.String())
				}
			}
			for _, field := range []string{`"entries"`, `"values"`, `"total"`, `"pageInfo"`} {
				if strings.Contains(w.Body.String(), field) {
					t.Fatalf("Reading query returned payload before authority/ready: %d %s", w.Code, w.Body.String())
				}
			}
		}
		assertPending := func(w *httptest.ResponseRecorder, path string, retryMs int) {
			t.Helper()
			var pending ReadingFacetPreparing // Both preparing envelopes have the same fields.
			retrySeconds := "1"
			if retryMs == readingInitialRetryMs {
				retrySeconds = "0"
			}
			if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &pending) != nil || pending.Kind != kind || pending.State != "preparing" || pending.QueryID == "" || pending.RetryAfterMs != retryMs || w.Header().Get("Retry-After") != retrySeconds || w.Header().Get("Location") != path || path != queryPrefix+pending.QueryID {
				t.Fatalf("real restricted preparing control: %d %s %v", w.Code, w.Body.String(), w.Header())
			}
			assertNoFacts(w, visible, visibleAuthor)
		}
		start := func() string {
			t.Helper()
			w := readingHTTP(t, h, startMethod, startPath, token, "", body)
			path := w.Header().Get("Location")
			assertPending(w, path, readingInitialRetryMs)
			return path
		}
		assertReady := func(w *httptest.ResponseRecorder, restricted bool) {
			t.Helper()
			want := 2
			if restricted {
				want = 1
				for _, marker := range []string{r.item, r.source, "Original publication", hiddenAuthor} {
					if strings.Contains(w.Body.String(), marker) {
						t.Fatalf("hidden title escaped ready query: %d %s", w.Code, w.Body.String())
					}
				}
			}
			if w.Code != 200 {
				t.Fatalf("ready control: %d %s", w.Code, w.Body.String())
			}
			if facet {
				var page catalog.FacetPage
				if json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Total != want || len(page.Values) != want || page.TotalIsLowerBound || page.Revision == "" {
					t.Fatalf("facet counts include hidden title or omit visible title: %s", w.Body.String())
				}
				seen := map[string]bool{}
				for _, value := range page.Values {
					if value.Count != 1 || value.CountIsLowerBound {
						t.Fatalf("facet count is not exact: %+v", value)
					}
					seen[value.Label] = true
				}
				if !seen[visibleAuthor] || (!restricted && !seen[hiddenAuthor]) {
					t.Fatalf("facet control did not contain real authors: %+v", page)
				}
			} else {
				var page catalog.BrowseResult
				if json.Unmarshal(w.Body.Bytes(), &page) != nil || page.PageInfo.Total != want || len(page.Entries) != want || page.PageInfo.TotalIsLowerBound || page.PageInfo.HasMore || page.PageInfo.NextCursor != "" || !strings.Contains(w.Body.String(), visible) || (!restricted && !strings.Contains(w.Body.String(), r.item)) {
					t.Fatalf("browse rows/count include hidden title or omit visible title: %s", w.Body.String())
				}
			}
		}
		ready := func(path string) *httptest.ResponseRecorder {
			t.Helper()
			deadline := time.Now().Add(5 * time.Second)
			for {
				w := request("GET", path, token)
				if w.Code != 202 || time.Now().After(deadline) {
					assertReady(w, true)
					return w
				}
				assertPending(w, path, readingBrowseRetryMs)
				time.Sleep(time.Millisecond)
			}
		}
		assertStale := func(path string) {
			t.Helper()
			w := request("GET", path, token)
			if w.Code != 409 || bodyErrorCode(w.Body.String()) != "stale_continuation" {
				t.Fatalf("old handle survived policy/cancel: %d %s", w.Code, w.Body.String())
			}
			assertNoFacts(w, visible, visibleAuthor, r.library, strings.TrimPrefix(path, queryPrefix))
		}
		cancel := func(path string) {
			t.Helper()
			for i := 0; i < 2; i++ {
				w := request("DELETE", path, token)
				if w.Code != 204 || w.Body.Len() != 0 || w.Header().Get("Location") != "" {
					t.Fatalf("own idempotent cancel: %d %s", w.Code, w.Body.String())
				}
			}
			assertStale(path)
		}
		// Foreground owner control proves both scanned titles and both facet
		// values exist; restricted totals of one therefore cannot pass vacuously.
		plain := r.d
		plain.PrepareReadingBrowse, plain.PrepareReadingFacets = false, false
		assertReady(readingHTTP(t, New(plain), startMethod, startPath, r.f.owner.AccessToken, "", body), false)

		for _, state := range []string{"preparing", "ready"} {
			store, release := readingFacetTestStore(t, r, true)
			path, cancelled := start(), start()
			if path == cancelled {
				t.Fatal("preparations did not issue private handles")
			}
			if state == "ready" {
				release()
				ready(path)
				ready(cancelled)
			}
			own := func() {
				if state == "ready" {
					assertReady(request("GET", path, token), true)
				} else {
					assertPending(request("GET", path, token), path, readingBrowseRetryMs)
				}
			}
			// Both a different profile and an independent session of the same
			// restricted profile replay the valid captured handle.
			for _, foreign := range []string{r.f.owner.AccessToken, other.Session.AccessToken} {
				w := request(method, path, foreign)
				absent := request(method, queryPrefix+identity.Token(), foreign)
				want := 409
				if method == "DELETE" {
					want = 204
				}
				if w.Code != want || absent.Code != want || bodyErrorCode(w.Body.String()) != bodyErrorCode(absent.Body.String()) || (method == "DELETE" && (w.Body.Len() != 0 || absent.Body.Len() != 0)) {
					t.Fatalf("foreign %s reveals/consumes handle: %d %s / %d %s", method, w.Code, w.Body.String(), absent.Code, absent.Body.String())
				}
				assertNoFacts(w, visible, visibleAuthor, strings.TrimPrefix(path, queryPrefix))
				own() // In particular foreign DELETE must have no effect.
			}
			anonymous := request(method, path, "")
			if anonymous.Code != 401 {
				t.Fatalf("anonymous query: %d %s", anonymous.Code, anonymous.Body.String())
			}
			assertNoFacts(anonymous, visible, visibleAuthor)
			own()
			cancel(cancelled)
			own()           // Cancelling another interest must preserve this handle.
			setPolicy(true) // Now even the PG book is hidden by the real writer.
			w := request(method, path, token)
			want := 409
			if method == "DELETE" {
				want = 204 // A policy-scoped cancel is private and idempotent.
			}
			if w.Code != want {
				t.Fatalf("changed policy %s: %d %s", method, w.Code, w.Body.String())
			}
			assertNoFacts(w, visible, visibleAuthor)
			assertStale(path)
			if stats := store.Stats(); stats.Deliveries != 0 || stats.DeliveryBytes != 0 {
				t.Fatalf("restriction query leaked delivery: %+v", stats)
			}
			setPolicy(false)
			release()
			store.Close()
		}
		// Capture under restored policy, then remove library access through the
		// administrative profile API. Replaying the old handle must stop before
		// either its preparing envelope or its ready content is returned.
		_, release := readingFacetTestStore(t, r, true)
		path := start()
		release()
		ready(path)
		if _, err = r.f.id.EditDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectProfileEdit{ExpectedRevision: child.Revision, Policy: &identity.ProfilePolicy{AllowedLibraries: []string{}}}); err != nil {
			t.Fatal(err)
		}
		w := request(method, path, token)
		absent := request(method, strings.Replace(path, r.library, absentTwin(r.library), 1), token)
		if w.Code != 404 || absent.Code != w.Code || bodyErrorCode(w.Body.String()) != bodyErrorCode(absent.Body.String()) {
			t.Fatalf("library denial differs from absent: %d %s / %d %s", w.Code, w.Body.String(), absent.Code, absent.Body.String())
		}
		assertNoFacts(w, visible, visibleAuthor, r.library, strings.TrimPrefix(path, queryPrefix))
	}
}

func TestReadingBrowseQueryRestrictions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, method := range []string{"GET", "DELETE"} {
		t.Run(method, readingBrowseQueryRestrictionCheck(method))
	}
}

func TestReadingFacetQueryRestrictions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, method := range []string{"GET", "DELETE"} {
		t.Run(method, readingFacetQueryRestrictionCheck(method))
	}
}
