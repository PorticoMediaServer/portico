package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// httpFacetFixture is this fixture's facet composition root. Start drains
// production source maintenance into the single BASE worker. WaitPrepared waits
// on the real preparation behind a pending query ID outside every HTTP handler:
// the fixture calls it after the ordinary handler returns and before its next
// ordinary authenticated poll, so no request deadline, Retry-After interval or
// production pacing changes. It is the fixture's own control, not a global.
type httpFacetFixture struct {
	start func()
	wait  func(context.Context, string) error
}

func (f httpFacetFixture) Start() { f.start() }

func (f httpFacetFixture) WaitPrepared(ctx context.Context, queryID string) error {
	if f.wait == nil {
		return errors.New("facet fixture has no preparation wait")
	}
	return f.wait(ctx, queryID)
}

// composeHTTPFacetFixture mirrors the process composition root. The caller
// drains production source maintenance before starting the single BASE worker.
// Register database cleanup BEFORE this helper, so Run and the shared store
// finish before the database is closed.
func composeHTTPFacetFixture(t *testing.T, d *Dependencies) httpFacetFixture {
	t.Helper()
	store, err := readprep.New(readprep.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var done chan error
	t.Cleanup(func() {
		cancel()
		if done != nil {
			if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("facet fixture worker: %v", err)
			}
		}
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	bases, err := catalog.NewFacetRuntime(d.DB, store, catalog.CheckFacetWindowAuthority, 128<<20)
	if err != nil {
		t.Fatal(err)
	}
	genres, err := catalog.NewGenreRuntime(d.DB, store, catalog.CheckFacetWindowAuthority, bases)
	if err != nil {
		t.Fatal(err)
	}
	d.Catalog.UseReadPreparations(store)
	d.Catalog.UseGenreRuntime(genres)
	d.FacetReads, d.GenreReads = bases, genres
	start := func() {
		t.Helper()
		if done != nil {
			t.Fatal("facet fixture worker started twice")
		}
		done = make(chan error, 1)
		go func() { done <- bases.Run(ctx) }()
	}
	return httpFacetFixture{start: start, wait: bases.WaitPreparedForTest}
}

type httpFacetFixtureCall func(context.Context, string, string, string) *httptest.ResponseRecorder

// httpFacetFixtureWait waits for the real preparation behind one pending query
// ID. The fixture owns it; it is not production behavior and takes no HTTP
// request context.
type httpFacetFixtureWait func(context.Context, string) error

// readyHTTPFacetFixture follows only typed SIMPLE preparation, using the same
// authenticated caller for initial, poll and cancel. These old callers consume
// the response immediately and never retain anchors, so release their handle.
// Contextual/Reading responses and initial errors pass through unchanged.
//
// Each pending response stays exactly as production served it: its 202/fallback
// shape, Location, Retry-After and private headers are asserted, and the next
// ordinary authenticated poll is issued only after wait reports the real worker
// has left that preparation. The wait runs outside the handler under this
// fixture's existing 60s deadline; no handler deadline, Retry-After interval or
// production pacing is changed, and there is no sleep.
func readyHTTPFacetFixture(t *testing.T, path string, call httpFacetFixtureCall, wait httpFacetFixtureWait) *httptest.ResponseRecorder {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	u, err := url.ParseRequestURI(path)
	if err != nil {
		t.Fatal(err)
	}
	queryID := ""
	defer func() {
		if queryID != "" {
			w := call(ctx, http.MethodDelete, "/v1/facet-queries/"+queryID, "")
			if w.Code != http.StatusNoContent {
				t.Errorf("cancel facet fixture handle: %d %s", w.Code, w.Body.String())
			}
		}
	}()
	w := call(ctx, http.MethodGet, path, "")
	polled := false
	for {
		var kind struct {
			Kind string `json:"kind"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &kind)
		if w.Code == http.StatusAccepted && kind.Kind == "reading-facet-preparing" && !polled {
			return w
		}
		fallback := w.Code == http.StatusOK && kind.Kind == "facet-fallback"
		if w.Code != http.StatusAccepted && !fallback {
			break
		}
		var pending catalog.SimpleFacetPreparing
		if fallback {
			if issues := (apispec.Document{}).ValidateJSON(facetSchema[catalog.SimpleFacetFallbackPage](), w.Body.Bytes()); len(issues) != 0 {
				t.Fatalf("facet fixture intermediate schema: %v\n%s", issues, w.Body.String())
			}
			var page catalog.SimpleFacetFallbackPage
			if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			pending = page.Preparation
		} else {
			if issues := (apispec.Document{}).ValidateJSON(facetSchema[catalog.SimpleFacetPreparing](), w.Body.Bytes()); len(issues) != 0 {
				t.Fatalf("facet fixture preparing schema: %v\n%s", issues, w.Body.String())
			}
			if err := json.Unmarshal(w.Body.Bytes(), &pending); err != nil {
				t.Fatal(err)
			}
		}
		if queryID != "" && queryID != pending.QueryID {
			t.Fatal("facet fixture poll changed private handle")
		}
		queryID = pending.QueryID
		location := "/v1/facet-queries/" + queryID
		seconds, err := strconv.Atoi(w.Header().Get("Retry-After"))
		if err != nil || seconds < 1 || int64(seconds)*1000 < int64(pending.RetryAfterMs) || w.Header().Get("Location") != location || w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("facet fixture preparing headers: %v %s", w.Header(), w.Body.String())
		}
		remaining, _ := ctx.Deadline()
		if int64(seconds) > int64(time.Until(remaining)/time.Second) {
			t.Fatalf("facet fixture %s Retry-After exceeds polling deadline: %v", path, w.Header())
		}
		// The handler has already returned. Wait on that preparation's real
		// worker outside the handler, under this fixture's own deadline, then
		// take the next ordinary authenticated poll.
		if err := wait(ctx, queryID); err != nil {
			t.Fatalf("facet fixture %s preparation wait: %v", path, err)
		}
		w = call(ctx, http.MethodGet, location, "")
		polled = true
		if err := ctx.Err(); err != nil {
			t.Fatalf("facet fixture did not become ready: %v", err)
		}
	}
	var envelope struct {
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &envelope)
	if polled && (w.Code != http.StatusOK || envelope.Kind != "simple-keyset") || envelope.Kind == "facet-fallback" {
		t.Fatalf("facet fixture requires exact ready result: %d %s", w.Code, w.Body.String())
	}
	if w.Code == http.StatusOK && envelope.Kind == "simple-keyset" {
		page, err := validateHTTPFixtureSimpleFacet(w.Body.Bytes(), u.Query().Get("field"))
		if err != nil {
			t.Fatal(err)
		}
		if queryID != "" && queryID != page.Window.QueryID {
			t.Fatal("ready facet fixture changed private handle")
		}
		queryID = page.Window.QueryID
		limit := 200
		if u.Query().Has("limit") {
			limit, err = strconv.Atoi(u.Query().Get("limit"))
			if err != nil {
				t.Fatal(err)
			}
			limit = min(limit, 200)
		}
		if page.Window.Limit != limit {
			t.Fatalf("facet fixture changed requested limit: %d, want %d", page.Window.Limit, limit)
		}
		if w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("facet fixture ready disclosure headers: %v", w.Header())
		}
	}
	return w
}

func validateHTTPFixtureSimpleFacet(raw []byte, field string) (catalog.SimpleFacetPage, error) {
	var page catalog.SimpleFacetPage
	if issues := (apispec.Document{}).ValidateJSON(facetSchema[catalog.SimpleFacetPage](), raw); len(issues) != 0 {
		return page, fmt.Errorf("SIMPLE ready schema: %v", issues)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&page); err != nil {
		return page, err
	}
	w := page.Window
	if page.Field != field || page.Revision == "" || w.Basis != page.Revision || w.QueryID == "" || w.Returned != len(page.Values) || w.Returned > w.Limit || page.Total < int64(w.Returned) || page.TotalIsLowerBound {
		return page, fmt.Errorf("SIMPLE ready disclosure/window mismatch: %+v", page)
	}
	if (w.Returned == 0) != (w.Reload == "") || w.Previous == "" && w.Next == "" && page.Total != int64(w.Returned) {
		return page, fmt.Errorf("SIMPLE ready anchors/total mismatch: %+v", page)
	}
	for _, value := range page.Values {
		if value.Count < 1 || value.CountIsLowerBound {
			return page, fmt.Errorf("SIMPLE ready count is not exact: %+v", value)
		}
	}
	return page, nil
}

// projectGoldenSimpleFacet validates the new ready wire before removing only
// its protocol metadata. Keep every legacy semantic facet field for the frozen
// oracle; contextual and Reading payloads never enter this projection.
func projectGoldenSimpleFacet(raw []byte, field string) ([]byte, error) {
	page, err := validateHTTPFixtureSimpleFacet(raw, field)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Field             string               `json:"field"`
		Total             int64                `json:"total"`
		TotalIsLowerBound bool                 `json:"totalIsLowerBound"`
		Values            []catalog.FacetValue `json:"values"`
	}{page.Field, page.Total, page.TotalIsLowerBound, page.Values})
}

func isHTTPFixtureFacetPath(method, path string) bool {
	u, err := url.ParseRequestURI(path)
	return err == nil && method == http.MethodGet && strings.HasPrefix(u.Path, "/v1/libraries/") && strings.HasSuffix(u.Path, "/facets")
}

func TestGoldenSimpleFacetProjection(t *testing.T) {
	raw := []byte(`{"kind":"simple-keyset","field":"genre","revision":"7","total":2,"totalIsLowerBound":false,"values":[{"value":"Drama","label":"Drama","count":3,"countIsLowerBound":false},{"value":"Action","label":"Action","count":1,"countIsLowerBound":false}],"window":{"queryId":"private_handle","basis":"7","limit":200,"returned":2,"reload":"opaque_anchor"}}`)
	projected, err := projectGoldenSimpleFacet(raw, "genre")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"field":"genre","total":2,"totalIsLowerBound":false,"values":[{"value":"Drama","label":"Drama","count":3,"countIsLowerBound":false},{"value":"Action","label":"Action","count":1,"countIsLowerBound":false}]}`
	if canonical(string(projected)) != canonical(want) {
		t.Fatalf("semantic projection changed membership/order/counts: %s", projected)
	}
	for name, mutate := range map[string]func(map[string]any){
		"preparing":         func(doc map[string]any) { doc["kind"] = "facet-preparing" },
		"fallback":          func(doc map[string]any) { doc["kind"] = "facet-fallback" },
		"missing window":    func(doc map[string]any) { delete(doc, "window") },
		"missing revision":  func(doc map[string]any) { delete(doc, "revision") },
		"empty revision":    func(doc map[string]any) { doc["revision"] = "" },
		"wrong basis":       func(doc map[string]any) { doc["window"].(map[string]any)["basis"] = "8" },
		"wrong returned":    func(doc map[string]any) { doc["window"].(map[string]any)["returned"] = 1 },
		"too small limit":   func(doc map[string]any) { doc["window"].(map[string]any)["limit"] = 1 },
		"truncated total":   func(doc map[string]any) { doc["total"] = 1 },
		"no next anchor":    func(doc map[string]any) { doc["total"] = 3 },
		"missing reload":    func(doc map[string]any) { delete(doc["window"].(map[string]any), "reload") },
		"lower bound total": func(doc map[string]any) { doc["totalIsLowerBound"] = true },
		"lower bound count": func(doc map[string]any) { doc["values"].([]any)[0].(map[string]any)["countIsLowerBound"] = true },
		"zero count":        func(doc map[string]any) { doc["values"].([]any)[0].(map[string]any)["count"] = 0 },
		"null values":       func(doc map[string]any) { doc["values"] = nil },
		"wrong field":       func(doc map[string]any) { doc["field"] = "studio" },
		"unknown metadata":  func(doc map[string]any) { doc["unchecked"] = true },
	} {
		t.Run(name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			mutate(doc)
			bad, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := projectGoldenSimpleFacet(bad, "genre"); err == nil {
				t.Fatalf("invalid ready wire was hidden by projection: %s", bad)
			}
		})
	}
	// A legitimate empty result remains exact, rather than being manufactured
	// from one of the pending/error/malformed payloads above.
	empty := []byte(`{"kind":"simple-keyset","field":"genre","revision":"7","total":0,"totalIsLowerBound":false,"values":[],"window":{"queryId":"private_handle","basis":"7","limit":200,"returned":0}}`)
	projected, err = projectGoldenSimpleFacet(empty, "genre")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(projected, &doc); err != nil || !reflect.DeepEqual(doc["values"], []any{}) || doc["total"] != float64(0) {
		t.Fatalf("empty exact response changed: %s (%v)", projected, err)
	}
}

// Exercise eight representative facet cases from the independently recorded
// oracle. Protocol metadata is validated before projection; expected rows,
// ordering, counts and status codes are never regenerated from the response.
func TestFacetReadyRepresentativeValuesMatchRecordedOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	var recorded []goldenCase
	if err = json.Unmarshal(raw, &recorded); err != nil {
		t.Fatal(err)
	}
	expected := map[string]goldenCase{}
	for _, c := range recorded {
		expected[c.Name] = c
	}
	f, call := buildGoldenFixture(t)
	checked := 0
	for _, c := range goldenMatrix(f.library, f.item) {
		if !strings.HasPrefix(c.Name, "facets:") || strings.Contains(c.Name, ":music/") {
			continue
		}
		u, err := url.ParseRequestURI(c.Path)
		if err != nil {
			t.Fatal(err)
		}
		field := u.Query().Get("field")
		if field != "genre" && field != "decade" && field != "collection" {
			continue
		}
		want, known := expected[c.Name]
		if added, ok := goldenNewBrowseFacets(t, f, c); ok {
			want, known = added, true
		}
		if !known {
			t.Fatalf("facet has no independent oracle: %s", c.Name)
		}
		status, body := call(c.Method, c.Path, c.Body, c.Viewer)
		if status == http.StatusOK {
			var envelope struct {
				Kind string `json:"kind"`
			}
			if err := json.Unmarshal([]byte(body), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Kind == "simple-keyset" {
				u, err := url.ParseRequestURI(c.Path)
				if err != nil {
					t.Fatal(err)
				}
				projected, err := projectGoldenSimpleFacet([]byte(body), u.Query().Get("field"))
				if err != nil {
					t.Fatalf("%s: %v", c.Name, err)
				}
				body = string(projected)
			}
		}
		c.Status = status
		want.Response = goldenBrowseExtension(t, f, c, want.Response)
		if status != want.Status || canonical(body) != canonical(want.Response) {
			t.Errorf("%s: status %d want %d\nactual: %s\nrecorded: %s", c.Name, status, want.Status, canonical(body), canonical(want.Response))
		}
		checked++
	}
	if checked != 8 {
		t.Fatal("facet oracle coverage unexpectedly shrank", checked)
	}
}
