package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

const preparedMetadataPath = "/v1/metadata/bulk/preview"

// Exercise the real background builder and snapshot-bound poll API. The production
// qualification constant stays false; these cases also run with a Go overlay
// changing ONLY that constant, without changing tracked catalogue sources.
func prepareMetadataHTTP(t *testing.T, f *preparedMetadataHTTPFixture) (catalog.BulkMetadataPreviewRequest, catalog.SelectionPreparationAccess, catalog.BrowseSelectionPreparing, catalog.Viewer) {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/libraries/movies/browse", nil)
	r.SetPathValue("id", "movies")
	r.Header.Set("Authorization", "Bearer "+f.token)
	p, viewer, err := f.d.browseSelectionQueryScope(r)
	if err != nil {
		t.Fatal(err)
	}
	access, err := f.d.browseSelectionAccess(p, "movies", viewer)
	if err != nil {
		t.Fatal(err)
	}
	var pending catalog.BrowseSelectionPreparing
	var revision string
	err = f.d.compose(r, viewer.EffectiveRestrictions(), func(cat *catalog.Service) error {
		request := catalog.BrowseRequest{Library: "movies", Pivot: "movies", Limit: 1, Query: &catalog.BrowseNode{Field: "search", Operator: "contains", Value: "f"}}
		page, e := cat.BrowseEntities(viewer, request)
		if e != nil {
			return e
		}
		defer page.Release()
		revision = page.PageInfo.Revision
		pending, e = cat.SubmitBrowseSelectionPreparation(request, viewer, *page.Bound, access)
		return e
	})
	if errors.Is(err, catalog.ErrSelectionBatchUnsupported) && strings.Contains(err.Error(), "local search consumer qualification pending") {
		t.Skip("local-search qualification is off; run this named test with the isolated constant-only Go overlay")
	}
	if err != nil {
		t.Fatal(err)
	}
	var descriptor catalog.BrowseSelectionDescriptor
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		// Wait on the memory store without manufacturing continuous foreground
		// snapshot demand that would correctly make the cold worker yield.
		probe, e := f.store.Poll(context.Background(), access.Caller, readprep.Handle(pending.QueryID))
		state := probe.State
		probe.Release()
		if e != nil {
			t.Fatal(e)
		}
		if state != readprep.Ready {
			time.Sleep(time.Millisecond)
			continue
		}
		err = f.d.compose(r, viewer.EffectiveRestrictions(), func(cat *catalog.Service) error {
			result, e := cat.PollBrowseSelectionPreparation("movies", viewer, pending.QueryID, access, catalog.HomeActionAuthority{})
			defer result.Release()
			if e == nil && result.Ready != nil {
				descriptor = *result.Ready
			}
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
		break
	}
	if descriptor.Total > catalog.BrowseSelectionConversionLimit && descriptor.UnavailableActions != nil && descriptor.UnavailableActions.MetadataPreview != "" {
		t.Fatalf("qualified large metadata preview is still unavailable: %+v", descriptor.UnavailableActions)
	}
	if descriptor.CatalogRevision != revision || len(descriptor.Selector.Query.SelectionBasis) != 64 {
		t.Fatalf("lost original revision/full basis: %+v", descriptor)
	}
	return catalog.BulkMetadataPreviewRequest{Selector: &catalog.JobSelector{Query: &descriptor.Selector.Query}, Expected: &catalog.JobExpected{CatalogRevision: revision}}, access, pending, viewer
}

// Isolate the production registration because this pin's full registry has
// unrelated audience-less routes. Keep its real authorizer and scope gate.
func preparedMetadataRegistry(d Dependencies) *apikit.Registry {
	r := apikit.NewContextual(d.v1Authorize, policyCatalogue())
	r.GateScopes(registryScopeGate)
	registerBulkMetadataPreview(r, d)
	return r
}

type preparedMetadataHTTPFixture struct {
	d     Dependencies
	c     *catalogtest.Catalog
	token string
	store *readprep.Store
	h     *apikit.Registry
}

func (f *preparedMetadataHTTPFixture) request(method, path string, headers map[string]string, body any) *httptest.ResponseRecorder {
	raw, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

func preparedMetadataFixture(t *testing.T, total int) (*preparedMetadataHTTPFixture, []catalog.BulkMetadataTarget, string) {
	t.Helper()
	d, owner := completedLocalFoundationFixture(t)
	d.Administration = administration.New(d.DB)
	store, err := readprep.New(readprep.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	d.Catalog.UseReadPreparations(store)
	c := catalogtest.New(t, d.DB)
	library := c.Library("movies", "Movies", "movie", "/movies")
	c.Library("other", "Other", "movie", "/other")
	targets := make([]catalog.BulkMetadataTarget, 0, total)
	for i := 0; i < total; i++ {
		item := c.Movie(library, fmt.Sprintf("/movies/prepared-%d.mkv", i), fmt.Sprintf("Film %03d", i), 2000+i%2)
		targets = append(targets, catalog.BulkMetadataTarget{Kind: "item", ContentKind: "movie", ID: item.Public, LibraryID: "movies"})
		c.Genres(item.ID, "tmdb", "Drama")
	}
	c.Drain()
	d.Metadata = metadata.New(d.DB, "")
	root := filepath.Join(t.TempDir(), "preview-cache")
	if err := d.Metadata.SetArtworkDirectory(root); err != nil {
		t.Fatal(err)
	}
	f := &preparedMetadataHTTPFixture{d: d, c: c, token: owner.AccessToken, store: store, h: preparedMetadataRegistry(d)}
	return f, targets, root
}

// A regular file makes even a short-lived spool attempt fail, unlike checking
// an empty directory after a request whose spool may already have been removed.
func blockMetadataSpool(t *testing.T, root string) {
	t.Helper()
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("spool forbidden"), 0600); err != nil {
		t.Fatal(err)
	}
}

func preparedMetadataError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status || errorCode(t, w) != code || strings.Contains(w.Body.String(), `"targets"`) {
		t.Fatalf("response %d %s; want %d %s", w.Code, w.Body.String(), status, code)
	}
	if err := apispec.ValidateErrorEnvelope(w.Code, w.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
}

type preparedMetadataWriter struct {
	*httptest.ResponseRecorder
	t     *testing.T
	store *readprep.Store
	fail  bool
	wrote bool
}

func (w *preparedMetadataWriter) Write(raw []byte) (int, error) {
	w.wrote = true
	stats := w.store.Stats()
	// The base conversion and the reducer's independent total must BOTH still
	// be held after typed serialization and throughout the transport write.
	if stats.Deliveries != 1 || stats.DeliveryBytes <= bulkMetadataConversionBytes+(64<<10) {
		w.t.Fatalf("conversion/reducer delivery released or shrunk before write: %+v", stats)
	}
	if w.fail {
		return 0, io.ErrClosedPipe
	}
	return w.ResponseRecorder.Write(raw)
}

func TestBulkMetadataPreparedHTTPMemory200AndDelivery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, targets, root := preparedMetadataFixture(t, 200)
	in, _, _, browseViewer := prepareMetadataHTTP(t, f)
	// Compare against the real general memory editor before making the
	// artwork-cache parent unusable for every preview path.
	legacy := f.request("POST", preparedMetadataPath, nil, catalog.BulkMetadataPreviewRequest{Targets: targets})
	legacy = bulkPreviewHTTPReady(t, f.h, f.token, legacy)
	var oracle metadata.BulkMetadataPreview
	if legacy.Code != 200 || json.Unmarshal(legacy.Body.Bytes(), &oracle) != nil || oracle.Total != 200 {
		t.Fatalf("legacy oracle %d %s", legacy.Code, legacy.Body.String())
	}
	blockMetadataSpool(t, root)
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, disconnect := range []bool{false, true} {
		t.Run(fmt.Sprintf("disconnect=%v", disconnect), func(t *testing.T) {
			r := httptest.NewRequest("POST", preparedMetadataPath, bytes.NewReader(raw))
			r.Header.Set("Authorization", "Bearer "+f.token)
			r.Header.Set("Content-Type", "application/json")
			w := &preparedMetadataWriter{ResponseRecorder: httptest.NewRecorder(), t: t, store: f.store, fail: disconnect}
			f.h.ServeHTTP(w, r)
			if w.Code != 200 || !w.wrote {
				t.Fatalf("prepared preview %d %s", w.Code, w.Body.String())
			}
			if stats := f.store.Stats(); stats.Deliveries != 0 || stats.DeliveryBytes != 0 || stats.Running != 0 || stats.Queued != 0 {
				t.Fatalf("delivery/work survived response: %+v", stats)
			}
			if !disconnect {
				var got metadata.BulkMetadataPreview
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.Total != 200 || len(got.Targets) != 0 || got.SelectionRevision != in.Expected.CatalogRevision || got.ViewerFence == browseViewer.Fence || got.ViewerFence == "" || got.ServerID != f.d.Identity.ID() {
					t.Fatalf("lost count/revision or route fence: %+v", got)
				}
				if !reflect.DeepEqual(got.Fields, oracle.Fields) || !reflect.DeepEqual(got.Schema, oracle.Schema) {
					t.Fatal("memory preview differs from the legacy editor oracle")
				}
			}
		})
	}
}

func TestBulkMetadataPreparedHTTPCachedRequestNoCensus(t *testing.T) {
	f, _, root := preparedMetadataFixture(t, 1)
	in, _, _, _ := prepareMetadataHTTP(t, f)
	blockMetadataSpool(t, root)
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	ctx, trace := dbwork.TraceQueryPlans(context.Background())
	r := httptest.NewRequest("POST", preparedMetadataPath, bytes.NewReader(raw)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("cached request %d %s", w.Code, w.Body.String())
	}
	// Inspect actual request statements, not source text: conversion must
	// hydrate held IDs without MATCH, document reads or an entity census.
	statements := trace()
	if len(statements) == 0 || len(statements) >= 1024 {
		t.Fatalf("missing/truncated request trace: %d", len(statements))
	}
	for _, q := range statements {
		lower := strings.ToLower(q)
		if strings.Contains(lower, " match ") || strings.Contains(lower, "catalog_search_documents") || strings.Contains(lower, "catalog_search_fts") || strings.Contains(lower, "catalog_entities indexed by catalog_entities_library_id") {
			t.Fatalf("foreground query replay/census: %s", q)
		}
	}
	if f.store.Stats().Deliveries != 0 {
		t.Fatal("cached request leaked delivery")
	}
}

func TestBulkMetadataPreparedHTTPLegacyGroupedMemory(t *testing.T) {
	f, _, root := preparedMetadataFixture(t, 2)
	r := httptest.NewRequest("POST", preparedMetadataPath, nil)
	r.Header.Set("Authorization", "Bearer "+f.token)
	p, err := f.d.Identity.Authenticate(f.token)
	if err != nil {
		t.Fatal(err)
	}
	libraries, fence, err := f.d.homeScopeFor(r, p)
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := f.d.catalogViewer(r, p, libraries, fence)
	if err != nil {
		t.Fatal(err)
	}
	query := &catalog.BrowseNode{All: []catalog.BrowseNode{{Field: "search", Operator: "contains", Value: "f"}, {Field: "genre", Operator: "equals", Value: "Drama"}}}
	if catalog.BrowseNeedsLocalSearch(query) {
		t.Fatal("fixture did not select the legacy grouped prefix branch")
	}
	var page catalog.BrowseResult
	err = f.d.compose(r, viewer.EffectiveRestrictions(), func(cat *catalog.Service) error {
		var e error
		page, e = cat.BrowseEntities(viewer, catalog.BrowseRequest{Library: "movies", Pivot: "movies", Limit: 2, Query: query})
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	defer page.Release()
	filter, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	in := catalog.BulkMetadataPreviewRequest{Selector: &catalog.JobSelector{Query: &catalog.JobQuery{LibraryID: "movies", Pivot: "movies", Filter: filter, Sort: page.Applied.Sort}}, Expected: &catalog.JobExpected{CatalogRevision: page.PageInfo.Revision}}
	w := f.request("POST", preparedMetadataPath, nil, in)
	w = bulkPreviewHTTPReady(t, f.h, f.token, w)
	var got metadata.BulkMetadataPreview
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Total != 2 || got.SelectionRevision != in.Expected.CatalogRevision {
		t.Fatalf("legacy grouped request %d %s", w.Code, w.Body.String())
	}
	blockMetadataSpool(t, root)
	w = f.request("POST", preparedMetadataPath, nil, in)
	w = bulkPreviewHTTPReady(t, f.h, f.token, w)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Total != 2 || len(got.Targets) != 0 {
		t.Fatalf("memory-only grouped request %d %s", w.Code, w.Body.String())
	}
	if stats := f.store.Stats(); stats.Entries < 1 || stats.Deliveries != 0 {
		t.Fatalf("general request did not use bounded preparation: %+v", stats)
	}

}

func TestBulkMetadataPreparedHTTPGeneral201StreamsWithoutForegroundCensus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, _, root := preparedMetadataFixture(t, 201)
	f.c.Exec(`UPDATE catalog_entities SET year=1900+(id%70) WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id='movies')`)
	f.c.Drain()
	in, _, _, _ := prepareMetadataHTTP(t, f)
	blockMetadataSpool(t, root)
	releaseWorker := blockBulkPreviewWorker(t, f.store)
	defer releaseWorker()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	ctx, trace := dbwork.TraceQueryPlans(context.Background())
	r := httptest.NewRequest("POST", preparedMetadataPath, bytes.NewReader(raw)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	path := w.Header().Get("Location")
	for _, statement := range trace() {
		lower := strings.ToLower(statement)
		if strings.Contains(lower, " match ") || strings.Contains(lower, "catalog_search_documents") || strings.Contains(lower, "catalog_entities indexed by catalog_entities_library_id") {
			t.Fatalf("large metadata foreground census: %s", statement)
		}
	}
	var pending bulkMetadataPreviewPreparing
	if err := json.Unmarshal(w.Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}
	if polled := editorRequest(t, f.h, "GET", path, f.token, "", nil); polled.Code != 202 {
		t.Fatal(polled.Code, polled.Body.String())
	}
	_, caller, _, err := f.d.bulkMetadataPreviewScope(r)
	if err != nil {
		t.Fatal(err)
	}
	releaseWorker()
	// Do not manufacture continuous foreground demand by violating the one-
	// second HTTP retry floor. As with the count-preparation helper above, wait
	// on the memory store, then verify the real ready HTTP response separately.
	deadline := time.Now().Add(5 * time.Second)
	for {
		probe, err := f.store.Poll(context.Background(), caller, readprep.Handle(pending.QueryID))
		state := probe.State
		probe.Release()
		if err != nil {
			t.Fatal(err)
		}
		if state == readprep.Ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("metadata preview failed to finish without foreground demand")
		}
		time.Sleep(time.Millisecond)
	}
	w = editorRequest(t, f.h, "GET", path, f.token, "", nil)
	var got metadata.BulkMetadataPreview
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	year := got.Fields["year"]
	if got.Total != 201 || len(got.Targets) != 0 || got.SelectionRevision != in.Expected.CatalogRevision || year.State != "many" || year.AtLeast != 65 || year.DistinctCount != 0 || year.Value != nil || len(year.Values) != 0 || !year.Truncated {
		t.Fatal(got.Total, got.Targets, got.SelectionRevision, year)
	}
	if stats := f.store.Stats(); stats.Deliveries != 0 || stats.Bytes > f.store.MaxBytes() {
		t.Fatal(stats)
	}
	f.c.Exec(`UPDATE catalog_entities SET year=2001 WHERE id=(SELECT min(e.id) FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id WHERE l.library_id='movies')`)
	stale := editorRequest(t, f.h, "GET", path, f.token, "", nil)
	preparedMetadataError(t, stale, 409, "stale_continuation")
	if stats := f.store.Stats(); stats.Deliveries != 0 {
		t.Fatal(stats)
	}
}

func TestBulkMetadataPreparedHTTPUnprovedLocalSearchCannotFallThrough(t *testing.T) {
	f, _, _ := preparedMetadataFixture(t, 1)
	in := catalog.BulkMetadataPreviewRequest{Selector: &catalog.JobSelector{Query: &catalog.JobQuery{
		LibraryID: "movies", Pivot: "movies", Filter: json.RawMessage(`{"field":"search","operator":"contains","value":"f"}`),
	}}, Expected: &catalog.JobExpected{CatalogRevision: "unproved-original-revision"}}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	ctx, trace := dbwork.TraceQueryPlans(context.Background())
	r := httptest.NewRequest("POST", preparedMetadataPath, bytes.NewReader(raw)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	preparedMetadataError(t, w, 400, "invalid_request")
	for _, statement := range trace() {
		lower := strings.ToLower(statement)
		if strings.Contains(lower, " match ") || strings.Contains(lower, "catalog_search_documents") || strings.Contains(lower, "catalog_entities indexed by catalog_entities_library_id") {
			t.Fatalf("unproved local search reached query traversal: %s", statement)
		}
	}
	if stats := f.store.Stats(); stats.Entries != 0 || stats.Deliveries != 0 {
		t.Fatal(stats)
	}
}

func TestBulkMetadataPreparedHTTPBasisRevisionAndAuthority(t *testing.T) {
	f, _, root := preparedMetadataFixture(t, 1)
	in, _, _, _ := prepareMetadataHTTP(t, f)
	blockMetadataSpool(t, root)
	for _, change := range []string{"missing-revision", "query", "basis"} {
		t.Run(change, func(t *testing.T) {
			changed := in
			query := *in.Selector.Query
			changed.Selector = &catalog.JobSelector{Query: &query}
			expected := *in.Expected
			changed.Expected = &expected
			switch change {
			case "missing-revision":
				expected.CatalogRevision = ""
			case "query":
				query.Filter = json.RawMessage(`{"field":"search","operator":"contains","value":"z"}`)
			case "basis":
				query.SelectionBasis = strings.Repeat("0", 64)
			}
			w := f.request("POST", preparedMetadataPath, nil, changed)
			if change == "missing-revision" {
				preparedMetadataError(t, w, 400, "invalid_request")
			} else {
				preparedMetadataError(t, w, 409, "stale_continuation")
			}
		})
	}
	// This changes the full predicate witness without changing the original
	// Browse page revision. Passing only the page revision would accept it.
	f.c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('prepared-http-drift',18)`)
	r := httptest.NewRequest("POST", "/v1/libraries/movies/browse", nil)
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.SetPathValue("id", "movies")
	_, viewer, err := f.d.browseSelectionQueryScope(r)
	if err != nil {
		t.Fatal(err)
	}
	err = f.d.compose(r, viewer.EffectiveRestrictions(), func(cat *catalog.Service) error {
		page, e := cat.BrowseEntities(viewer, catalog.BrowseRequest{Library: "movies", Pivot: "movies", Limit: 1, Query: &catalog.BrowseNode{Field: "search", Operator: "contains", Value: "f"}})
		defer page.Release()
		if e == nil && page.PageInfo.Revision != in.Expected.CatalogRevision {
			t.Fatal("fixture changed ordinary page revision too")
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	w := f.request("POST", preparedMetadataPath, nil, in)
	preparedMetadataError(t, w, 409, "stale_continuation")
	p, err := f.d.Identity.Authenticate(f.token)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.Identity.Logout(p); err != nil {
		t.Fatal(err)
	}
	w = f.request("POST", preparedMetadataPath, nil, in)
	if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Fatalf("revoked authority borrowed proof: %d %s", w.Code, w.Body.String())
	}
	if f.store.Stats().Deliveries != 0 {
		t.Fatal("stale/revoked conversion leaked delivery")
	}
}

func TestBulkMetadataPreparedHTTPCapacity(t *testing.T) {
	f, _, root := preparedMetadataFixture(t, 1)
	in, access, pending, _ := prepareMetadataHTTP(t, f)
	blockMetadataSpool(t, root)
	// Hold an independent delivery so conversion itself can fit, but its
	// subsequent metadata aggregation cannot. No fake service/error injection.
	pressure, err := f.store.Poll(context.Background(), access.Caller, readprep.Handle(pending.QueryID))
	if err != nil {
		t.Fatal(err)
	}
	defer pressure.Release()
	if err = pressure.ResizeDelivery(f.store.MaxBytes() - f.store.Stats().Bytes - bulkMetadataConversionBytes - (96 << 10)); err != nil {
		t.Fatal(err)
	}
	w := f.request("POST", preparedMetadataPath, nil, in)
	preparedMetadataError(t, w, 429, "rate_limited")
	if w.Header().Get("Retry-After") != "1" || !strings.Contains(w.Body.String(), `"retry":"same_request"`) {
		t.Fatalf("missing retry floor/policy: %#v %s", w.Header(), w.Body.String())
	}
	if f.store.Stats().Deliveries != 1 {
		t.Fatal("failed request leaked its lease or released someone else's")
	}
	pressure.Release()
	if f.store.Stats().Deliveries != 0 {
		t.Fatal("pressure cleanup leaked")
	}
}

func TestBulkMetadataPreparedHTTPErrorMappingAndDeclarations(t *testing.T) {
	registry := preparedMetadataRegistry(Dependencies{})
	var declared []string
	for _, route := range registry.Definitions() {
		if route.ID == "post_metadata_bulk_preview" {
			declared = route.Errors
		}
	}
	for _, tc := range []struct {
		cause    error
		prepared bool
		code     string
		status   int
	}{
		{readprep.ErrStale, true, "stale_continuation", 409},
		{catalog.ErrStaleContinuation, true, "stale_continuation", 409},
		{catalog.ErrStaleContinuation, false, "stale_continuation", 409},
		{readprep.ErrCapacity, true, "rate_limited", 429},
		{readprep.ErrClosed, true, "rate_limited", 429},
		{readprep.ErrUncached, true, "rate_limited", 429},
		{catalog.ErrSelectionBatchUnsupported, true, "invalid_request", 400},
	} {
		t.Run(fmt.Sprintf("%s/prepared=%v", tc.cause, tc.prepared), func(t *testing.T) {
			mapped := bulkMetadataPreviewError(fmt.Errorf("wrapped: %w", tc.cause), tc.prepared)
			var typed *apierror.Error
			if !errors.As(mapped, &typed) || typed.Code != tc.code || !errors.Is(mapped, tc.cause) || !slices.Contains(declared, tc.code) {
				t.Fatalf("untyped/undeclared error: %v; declarations %v", mapped, declared)
			}
			w := httptest.NewRecorder()
			registry.Errors().Write(w, "prepared-http-test", mapped)
			preparedMetadataError(t, w, tc.status, tc.code)
			if tc.status == 429 && w.Header().Get("Retry-After") != "1" {
				t.Fatal("capacity response lacks retry floor")
			}
		})
	}
}
