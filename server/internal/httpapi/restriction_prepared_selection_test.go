package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// A valid captured handle is needed to test this route. Inventing a handle only
// exercises the cache miss. Both matrices prove the real restricted result and
// current authority, including the no-effect guarantee of foreign cancellation.
func browseSelectionQueryRestrictionCheck(method string) func(*testing.T) {
	return func(t *testing.T) {
		f := newSelectionHTTPFixture(t)
		ctx := context.Background()
		hidden := f.c.Movie(f.c.Handle("movies"), "/hidden-query.mkv", "Film hidden by profile", 2020)
		f.c.Attributes(hidden.ID, "label", "selection-private")
		f.c.Drain()
		body := map[string]any{"pivot": "movies", "limit": 20, "range": map[string]any{"start": 0}, "query": map[string]any{"variant": "predicate", "field": "search", "operator": "contains", "value": "f"}}
		var page catalog.BrowseResult
		w := f.request("POST", "/v1/libraries/movies/browse", nil, body)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Entries) != 2 {
			t.Fatalf("unrestricted control %d %s", w.Code, w.Body.String())
		}
		ownerToken := f.token
		owner, err := f.d.Identity.DirectLogin(ctx, "owner", "long-test-password")
		if err != nil {
			t.Fatal(err)
		}
		profiles, err := f.d.Identity.CreateDirectProfile(ctx, owner.AccountToken, "Restricted selection", "blue")
		if err != nil {
			t.Fatal(err)
		}
		var child identity.DirectProfile
		for _, p := range profiles.Profiles {
			if p.Name == "Restricted selection" && !p.Primary {
				child = p
			}
		}
		if child.ID == "" {
			t.Fatal("normal secondary profile missing")
		}
		if _, err = f.d.Identity.SetProfileRestrictions(ctx, owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 1, AllowUnrated: true, BlockedLabels: []string{"selection-private"}}); err != nil {
			t.Fatal(err)
		}
		selected, err := f.d.Identity.SelectDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectSelection{})
		if err != nil {
			t.Fatal(err)
		}
		f.token = selected.Session.AccessToken
		f.d.PrepareBrowseSelections = true
		f.h = New(f.d)
		prepare := func() string {
			t.Helper()
			w := f.request("POST", "/v1/libraries/movies/browse", nil, body)
			var p catalog.BrowseResult
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &p) != nil || len(p.Entries) != 1 || p.Entries[0].ID != f.item.Public || p.WholeSelectionPreparing == nil || strings.Contains(w.Body.String(), hidden.Public) || strings.Contains(w.Body.String(), "Film hidden") {
				t.Fatalf("restricted page %d %s", w.Code, w.Body.String())
			}
			return browseSelectionQueryPath("movies", p.WholeSelectionPreparing.QueryID)
		}
		// Wait on the store between real HTTP controls. Repeated foreground
		// polling at1ms would correctly starve the lowest-priority cold build
		// under host pressure; clients honor its1s Retry-After instead.
		readyPage := func(path string) (catalog.BrowseSelectionDescriptor, *httptest.ResponseRecorder) {
			t.Helper()
			r := httptest.NewRequest("GET", path, nil)
			r.SetPathValue("id", "movies")
			r.Header.Set("Authorization", "Bearer "+f.token)
			principal, viewer, e := f.d.browseSelectionQueryScope(r)
			if e != nil {
				t.Fatal(e)
			}
			access, e := f.d.browseSelectionAccess(principal, "movies", viewer)
			if e != nil {
				t.Fatal(e)
			}
			handle := readprep.Handle(path[strings.LastIndex(path, "/")+1:])
			deadline := time.Now().Add(5 * time.Second)
			for {
				value, e := f.store.Poll(ctx, access.Caller, handle)
				state := value.State
				value.Release()
				if e != nil {
					t.Fatal(e)
				}
				if state == readprep.Ready {
					return selectionHTTPReady(t, f, path)
				}
				if time.Now().After(deadline) {
					t.Fatal("cold restricted preparation did not finish", f.store.Stats())
				}
				time.Sleep(time.Millisecond)
			}
		}
		path := prepare()
		ready, _ := readyPage(path)
		if ready.Total != 1 || len(ready.Types) != 1 || ready.Types[0].Count != 1 {
			t.Fatalf("hidden title entered exact counts: %+v", ready)
		}
		request := func(verb, path, token string) *httptest.ResponseRecorder {
			t.Helper()
			r := httptest.NewRequest(verb, path, nil)
			if token != "" {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			f.h.ServeHTTP(w, r)
			return w
		}
		for _, verb := range []string{"GET", "DELETE"} {
			if w := request(verb, path, ""); w.Code != 401 {
				t.Fatalf("anonymous %s %d %s", verb, w.Code, w.Body.String())
			}
		}
		// Neither another profile nor another session with the same effective
		// restrictions may borrow or cancel this private interest.
		principal, err := f.d.Identity.Authenticate(f.token)
		if err != nil {
			t.Fatal(err)
		}
		other, err := f.d.Identity.Issue(principal.AccountID, principal.ProfileID, principal.Authority, principal.Role, principal.Epoch)
		if err != nil {
			t.Fatal(err)
		}
		for _, foreign := range []string{ownerToken, other.AccessToken} {
			if w = request("GET", path, foreign); w.Code != 409 || strings.Contains(w.Body.String(), `"selector"`) {
				t.Fatalf("foreign poll %d %s", w.Code, w.Body.String())
			}
			if w = request("DELETE", path, foreign); w.Code != 204 {
				t.Fatalf("foreign cancel %d %s", w.Code, w.Body.String())
			}
			ready, w = readyPage(path)
			if ready.Total != 1 || strings.Contains(w.Body.String(), hidden.Public) || strings.Contains(w.Body.String(), "Film hidden") {
				t.Fatal("foreign cancellation changed own restricted result", w.Body.String())
			}
		}

		if w = request(method, path, f.token); method == "GET" && w.Code != 200 || method == "DELETE" && w.Code != 204 {
			t.Fatalf("owned %s %d %s", method, w.Code, w.Body.String())
		}
		if method == "GET" {
			if w = request("DELETE", path, f.token); w.Code != 204 {
				t.Fatal(w.Code, w.Body.String())
			}
		}
		if w = request("GET", path, f.token); w.Code != 409 {
			t.Fatal("owned cancel did not release demand", w.Code, w.Body.String())
		}
		path = prepare()
		_, _ = readyPage(path)
		// Keep the exact token and handle while making its previously visible title
		// disallowed. Current policy must win over a completed cache payload.
		if _, err = f.d.Identity.SetProfileRestrictions(ctx, owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 2, AllowUnrated: false, BlockedLabels: []string{"selection-private"}}); err != nil {
			t.Fatal(err)
		}
		w = request("GET", path, f.token)
		if w.Code != 409 || strings.Contains(w.Body.String(), `"selector"`) || strings.Contains(w.Body.String(), f.item.Public) || strings.Contains(w.Body.String(), hidden.Public) {
			t.Fatalf("policy drift disclosed cached selection %d %s", w.Code, w.Body.String())
		}
		if _, err = f.d.Identity.EditDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectProfileEdit{ExpectedRevision: child.Revision, Policy: &identity.ProfilePolicy{AllowedLibraries: []string{}}}); err != nil {
			t.Fatal(err)
		}
		for _, verb := range []string{"GET", "DELETE"} {
			held := request(verb, path, f.token)
			absent := request(verb, strings.Replace(path, "/movies/", "/missing-library/", 1), f.token)
			if held.Code != 404 || absent.Code != held.Code || bodyErrorCode(held.Body.String()) != bodyErrorCode(absent.Body.String()) || strings.Contains(held.Body.String(), f.item.Public) || strings.Contains(held.Body.String(), hidden.Public) {
				t.Fatalf("denied library differs from absent %s: %d %s / %d %s", verb, held.Code, held.Body.String(), absent.Code, absent.Body.String())
			}
		}
		if f.store.Stats().Deliveries != 0 {
			t.Fatal("restriction response leaked delivery", f.store.Stats())
		}
	}
}

func TestBrowseSelectionPreparedRestrictedLifecycle(t *testing.T) {
	for _, method := range []string{"GET", "DELETE"} {
		t.Run(method, browseSelectionQueryRestrictionCheck(method))
	}
}
