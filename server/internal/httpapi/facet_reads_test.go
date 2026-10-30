package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

type facetReadStub struct {
	FacetReadRuntime
	initial func(context.Context, readprep.Caller, catalog.Viewer, catalog.FacetRequest) (catalog.SimpleFacetResult, error)
	poll    func(context.Context, readprep.Caller, string) (catalog.SimpleFacetResult, error)
	cancel  func(context.Context, readprep.Caller, string) error
}

func (s facetReadStub) Initial(ctx context.Context, caller readprep.Caller, viewer catalog.Viewer, request catalog.FacetRequest) (catalog.SimpleFacetResult, error) {
	return s.initial(ctx, caller, viewer, request)
}
func (s facetReadStub) Poll(ctx context.Context, caller readprep.Caller, id string) (catalog.SimpleFacetResult, error) {
	return s.poll(ctx, caller, id)
}
func (s facetReadStub) Cancel(ctx context.Context, caller readprep.Caller, id string) error {
	return s.cancel(ctx, caller, id)
}

func facetTestRequest(f *tl11AdmissionFixture, path string) *http.Request {
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.SetPathValue("id", "movies")
	return r
}

func TestFacetReadHTTPPreparationAndBranchBoundaries(t *testing.T) {
	f := newTL11AdmissionFixture(t, "movie")
	d := f.d
	var private readprep.Caller
	calls := 0
	pending := catalog.SimpleFacetResult{Preparing: &catalog.SimpleFacetPreparing{Kind: "facet-preparing", State: "preparing", QueryID: "private_query", RetryAfterMs: 1501}}
	d.FacetReads = facetReadStub{
		initial: func(ctx context.Context, caller readprep.Caller, viewer catalog.Viewer, request catalog.FacetRequest) (catalog.SimpleFacetResult, error) {
			calls++
			private = caller
			if request.Library != "movies" || request.Field != "genre" || request.Q != "D" || request.Limit != 7 || !request.LimitPresent || request.OffsetPresent {
				t.Fatalf("descriptor lost: %+v", request)
			}
			if err := catalog.CheckFacetWindowAuthority(ctx, caller, catalog.FacetBaseSnapshot{Scope: catalog.FacetBaseScope{Library: "movies", Policy: catalog.FacetBasePolicy(viewer.EffectiveRestrictions())}}); err != nil {
				t.Fatal(err)
			}
			return pending, nil
		},
		poll: func(_ context.Context, caller readprep.Caller, id string) (catalog.SimpleFacetResult, error) {
			if caller != private || id != "private_query" {
				t.Fatal("poll changed private caller")
			}
			return pending, nil
		},
		cancel: func(_ context.Context, caller readprep.Caller, id string) error {
			if caller != private || id != "private_query" {
				t.Fatal("cancel changed private caller")
			}
			return nil
		},
	}
	w := httptest.NewRecorder()
	d.facetsHandler(w, facetTestRequest(f, "/v1/libraries/movies/facets?field=genre&q=D&limit=7"))
	if w.Code != 202 || w.Header().Get("Location") != "/v1/facet-queries/private_query" || w.Header().Get("Retry-After") != "2" || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("preparation: %d %v %s", w.Code, w.Header(), w.Body)
	}
	if issues := (apispec.Document{}).ValidateJSON((facetReadResponse{}).JSONSchemaForStatus(202), w.Body.Bytes()); len(issues) != 0 {
		t.Fatal(issues)
	}
	if issues := (apispec.Document{}).ValidateJSON((facetReadResponse{}).JSONSchemaForStatus(200), w.Body.Bytes()); len(issues) == 0 {
		t.Fatal("preparing accepted as ready")
	}
	for _, method := range []string{"GET", "DELETE"} {
		r := facetTestRequest(f, "/v1/facet-queries/private_query")
		r.Method = method
		r.SetPathValue("id", "private_query")
		w = httptest.NewRecorder()
		d.facetQueryHandler(w, r)
		want := 202
		if method == "DELETE" {
			want = 204
		}
		if w.Code != want {
			t.Fatalf("%s: %d %s", method, w.Code, w.Body)
		}
	}
	pending = catalog.SimpleFacetResult{Fallback: &catalog.SimpleFacetFallbackPage{
		Kind: "facet-fallback", FacetPage: catalog.FacetPage{Field: "genre", TotalIsLowerBound: true, Values: []catalog.FacetValue{}},
		Preparation: catalog.SimpleFacetPreparing{Kind: "facet-preparing", State: "preparing", QueryID: "private_query", RetryAfterMs: 1501},
	}}
	w = httptest.NewRecorder()
	d.facetsHandler(w, facetTestRequest(f, "/v1/libraries/movies/facets?field=genre&q=D&limit=7"))
	if w.Code != 200 || w.Header().Get("Location") != "/v1/facet-queries/private_query" || w.Header().Get("Retry-After") != "2" {
		t.Fatalf("fallback: %d %v %s", w.Code, w.Header(), w.Body)
	}
	if issues := (apispec.Document{}).ValidateJSON(facetSchema[catalog.SimpleFacetFallbackPage](), w.Body.Bytes()); len(issues) != 0 {
		t.Fatal(issues)
	}
	if issues := (apispec.Document{}).ValidateJSON((facetReadResponse{}).JSONSchemaForStatus(202), w.Body.Bytes()); len(issues) == 0 {
		t.Fatal("fallback accepted as preparing")
	}
	bad := []byte(strings.Replace(w.Body.String(), `"totalIsLowerBound":true`, `"totalIsLowerBound":false`, 1))
	if issues := (apispec.Document{}).ValidateJSON(facetSchema[catalog.SimpleFacetFallbackPage](), bad); len(issues) == 0 {
		t.Fatal("fallback falsely exact in schema")
	}
	for _, suffix := range []string{"&offset=0", "&after=", "&after=x&before=y", "&after=x&pivot=movies"} {
		w = httptest.NewRecorder()
		d.facetsHandler(w, facetTestRequest(f, "/v1/libraries/movies/facets?field=genre"+suffix))
		if w.Code != 400 {
			t.Fatalf("invalid branch accepted %s: %d %s", suffix, w.Code, w.Body)
		}
	}
	if calls != 2 {
		t.Fatalf("invalid request reached runtime %d times", calls)
	}
}

func TestFacetReadAuthorityIsFamilyPrivateAndLive(t *testing.T) {
	f := newTL11AdmissionFixture(t, "movie")
	p, err := f.d.Identity.Authenticate(f.token)
	if err != nil {
		t.Fatal(err)
	}
	r := facetTestRequest(f, "/v1/libraries/movies/facets?field=genre")
	ctx, caller, err := f.d.facetReadContext(r, p)
	if err != nil {
		t.Fatal(err)
	}
	restrictions, _, err := f.d.viewerRestrictions(r, p)
	if err != nil {
		t.Fatal(err)
	}
	scope := catalog.FacetBaseScope{LibraryID: f.c.Handle("movies"), Library: "movies", Field: "genre", Policy: catalog.FacetBasePolicy(restrictions), Source: catalog.UnrestrictedFacetBaseSource}
	authority, err := catalog.CaptureFacetAuthority(context.Background(), f.db, scope)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := catalog.FacetBaseSnapshot{ID: 1, Scope: scope, Authority: authority}
	if err = catalog.CheckFacetWindowAuthority(ctx, caller, snapshot); err != nil {
		t.Fatal(err)
	}
	other, err := f.d.Identity.Issue(p.AccountID, p.ProfileID, p.Authority, p.Role, p.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	op, err := f.d.Identity.Authenticate(other.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	or := r.Clone(context.Background())
	or.Header.Set("Authorization", "Bearer "+other.AccessToken)
	_, otherCaller, err := f.d.facetReadContext(or, op)
	if err != nil {
		t.Fatal(err)
	}
	if caller == otherCaller {
		t.Fatal("same profile shared a private handle across families")
	}
	if err = catalog.CheckFacetWindowAuthority(ctx, otherCaller, snapshot); !errors.Is(err, catalog.ErrStaleContinuation) {
		t.Fatalf("cross-family: %v", err)
	}
	f.c.Genres(f.item.ID, "fixture", "New Genre")
	if err = catalog.CheckFacetWindowAuthority(ctx, caller, snapshot); err != nil {
		t.Fatalf("content churn revoked unrestricted basis: %v", err)
	}
	if err = f.d.Identity.LogoutToken(context.Background(), f.token); err != nil {
		t.Fatal(err)
	}
	if err = catalog.CheckFacetWindowAuthority(ctx, caller, snapshot); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatalf("revoked family retained facts: %v", err)
	}
}

func TestFacetReadRefusalsRemainTyped(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{readprep.ErrStale, 409, "stale_continuation"}, {readprep.ErrCapacity, 429, "search_busy"}, {catalog.ErrFacetBaseCapacity, 503, "metadata_unavailable"}, {catalog.ErrFacetBaseUnavailable, 503, "metadata_unavailable"}} {
		w := httptest.NewRecorder()
		facetReadFailure(w, tc.err)
		if w.Code != tc.status || errorCode(t, w) != tc.code {
			t.Fatalf("%v: %d %s", tc.err, w.Code, w.Body)
		}
		if tc.status != 409 && w.Header().Get("Retry-After") != "1" {
			t.Fatal("capacity retry floor missing")
		}
	}
}
