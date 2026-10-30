package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// One occupied store worker makes pending deterministic without sleeping inside
// production code or replacing the real DB, authorizer, reducer or HTTP reader.
type readingFacetBlocker struct {
	gate    <-chan struct{}
	started chan<- struct{}
}

type facetDisconnectedWriter struct {
	*httptest.ResponseRecorder
	inspect func()
}

func (w facetDisconnectedWriter) Write([]byte) (int, error) {
	w.inspect()
	return 0, errors.New("test client disconnected")
}

func (*readingFacetBlocker) AccountedBytes() int64 { return 64 }
func (b *readingFacetBlocker) Step(ctx context.Context, _ *readprep.Budget) (readprep.StepResult, error) {
	close(b.started)
	select {
	case <-b.gate:
		return readprep.StepResult{Done: true}, nil
	case <-ctx.Done():
		return readprep.StepResult{}, ctx.Err()
	}
}

func readingFacetTestStore(t *testing.T, r *readingFixture, blocked bool, limits ...int64) (*readprep.Store, func()) {
	t.Helper()
	config := readprep.Config{Workers: 1}
	if len(limits) > 0 {
		config.MaxBytes = limits[0]
	}
	store, err := readprep.New(config)
	if err != nil {
		t.Fatal(err)
	}
	r.d.Catalog.UseReadPreparations(store)
	t.Cleanup(func() { store.Close() })
	if !blocked {
		return store, func() {}
	}
	gate, started := make(chan struct{}), make(chan struct{})
	_, err = store.Submit(context.Background(), "test-blocker", readprep.Key{Namespace: "test", Basis: "immutable", Canonical: "blocker"}, readprep.Plan{InitialBytes: 64, New: func(context.Context, *readprep.Budget) (readprep.Computation, error) {
		return &readingFacetBlocker{gate, started}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("store worker did not start")
	}
	var once bool
	release := func() {
		if !once {
			once = true
			close(gate)
		}
	}
	t.Cleanup(release)
	return store, release
}

func TestReadingFacetQueriesHTTPCapacityIsTerminalTypedRefusal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	r := newReadingFixture(t, "book", "pdf")
	readingSeriesOwnerField(t, r, r.item, "author", "Never truncated")
	readingSeriesDrain(t, r)
	store, release := readingFacetTestStore(t, r, true, 64<<10)
	h := readingFacetTestHandler(t, r)
	_, path := readingFacetStart(t, r, h)
	release()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w := readingHTTP(t, h, "GET", path, r.f.owner.AccessToken, "", nil)
		if w.Code == 202 {
			time.Sleep(time.Millisecond)
			continue
		}
		if w.Code != 429 || !strings.Contains(w.Body.String(), `"code":"search_busy"`) || w.Header().Get("Retry-After") != "1" || strings.Contains(w.Body.String(), `"values"`) {
			t.Fatalf("capacity became success/retry loop: %d %s", w.Code, w.Body.String())
		}
		for i := 0; i < 3; i++ {
			again := readingHTTP(t, h, "GET", path, r.f.owner.AccessToken, "", nil)
			if again.Code != 429 {
				t.Fatalf("terminal failure restarted: %d %s", again.Code, again.Body.String())
			}
		}
		if stats := store.Stats(); stats.Bytes > store.MaxBytes() || stats.Deliveries != 0 {
			t.Fatalf("capacity leak: %+v", stats)
		}
		return
	}
	t.Fatal("capacity kept preparing forever")
}

func readingFacetTestHandler(t *testing.T, r *readingFixture) http.Handler {
	t.Helper()
	registry := apikit.NewContextual(r.d.v1Authorize, policyCatalogue())
	if err := registerReadingFacetQueryRoutes(registry, r.d); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET "+readingFacetQueryPath, registry)
	mux.Handle("DELETE "+readingFacetQueryPath, registry)
	// The parent-owned rollout hook is deliberately mounted only by this test.
	mux.HandleFunc("GET /v1/libraries/{id}/facets", func(w http.ResponseWriter, req *http.Request) {
		r.d.prepareReadingFacets(w, req, catalog.FacetRequest{Field: "author", Limit: 1})
	})
	return mux
}

func readingFacetStart(t *testing.T, r *readingFixture, h http.Handler) (ReadingFacetPreparing, string) {
	t.Helper()
	w := readingHTTP(t, h, "GET", "/v1/libraries/"+r.library+"/facets", r.f.owner.AccessToken, "", nil)
	if w.Code != 202 {
		t.Fatalf("initial: %d %s", w.Code, w.Body.String())
	}
	var pending ReadingFacetPreparing
	if err := json.Unmarshal(w.Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}
	location := w.Header().Get("Location")
	if pending.Kind != "reading-facet-preparing" || pending.State != "preparing" || pending.QueryID == "" || pending.RetryAfterMs != readingInitialRetryMs || w.Header().Get("Retry-After") != "0" || location != readingFacetPollLocation(r.library, readprep.Handle(pending.QueryID)) {
		t.Fatalf("pending: %+v headers=%v", pending, w.Header())
	}
	return pending, location
}

func readingFacetReady(t *testing.T, r *readingFixture, h http.Handler, location string) *httptest.ResponseRecorder {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w := readingHTTP(t, h, "GET", location, r.f.owner.AccessToken, "", nil)
		if w.Code == 200 {
			return w
		}
		if w.Code != 202 {
			t.Fatalf("poll: %d %s", w.Code, w.Body.String())
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Reading facet did not finish")
	return nil
}

func TestReadingFacetQueriesHTTPPendingDedupeCancelReady(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	readingSeriesOwnerField(t, r, r.item, "author", "Exact author")
	readingSeriesDrain(t, r)
	store, release := readingFacetTestStore(t, r, true)
	h := readingFacetTestHandler(t, r)
	one, path1 := readingFacetStart(t, r, h)
	two, path2 := readingFacetStart(t, r, h)
	if one.QueryID == two.QueryID || store.Stats().Entries != 2 {
		t.Fatalf("no private handles/dedupe: %v", store.Stats())
	}
	w := readingHTTP(t, h, "GET", path2, r.f.owner.AccessToken, "", nil)
	if w.Code != 202 || strings.Contains(w.Body.String(), `"values"`) {
		t.Fatalf("pending became success: %d %s", w.Code, w.Body.String())
	}
	w = readingHTTP(t, h, "DELETE", path1, r.f.owner.AccessToken, "", nil)
	if w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	w = readingHTTP(t, h, "GET", path1, r.f.owner.AccessToken, "", nil)
	if w.Code != 409 {
		t.Fatalf("cancelled handle: %d %s", w.Code, w.Body.String())
	}
	release()
	w = readingFacetReady(t, r, h, path2)
	var page catalog.FacetPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Values) != 1 || page.Values[0].Label != "Exact author" || page.Values[0].Count != 1 || page.TotalIsLowerBound || page.Revision == "" {
		t.Fatalf("ready: %+v", page)
	}
	// The original foreground oracle remains installed and has identical facts.
	oracle := readingHTTP(t, New(r.d), "GET", "/v1/libraries/"+r.library+"/facets?field=author&limit=1", r.f.owner.AccessToken, "", nil)
	var old catalog.FacetPage
	if oracle.Code != 200 || json.Unmarshal(oracle.Body.Bytes(), &old) != nil {
		t.Fatalf("oracle: %d %s", oracle.Code, oracle.Body.String())
	}
	old.Revision = page.Revision
	if !reflect.DeepEqual(old, page) {
		t.Fatalf("oracle mismatch: %+v != %+v", old, page)
	}
	first := w.Body.String()
	for i := 0; i < 3; i++ {
		ctx, statements := dbwork.TraceQueryPlans(context.Background())
		req := httptest.NewRequest("GET", path2, nil).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer "+r.f.owner.AccessToken)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 200 || w.Body.String() != first {
			t.Fatalf("cached ready: %d %s", w.Code, w.Body.String())
		}
		for _, sql := range statements() {
			upper := strings.ToUpper(sql)
			if strings.Contains(upper, "COUNT(") || strings.Contains(upper, "GROUP BY") || strings.Contains(upper, "FROM READING_ATTRIBUTION_MEMBERS") {
				t.Fatalf("cached poll reduced/recounted source: %s", sql)
			}
		}
	}
	if store.Stats().Deliveries != 0 || store.Stats().DeliveryBytes != 0 {
		t.Fatalf("handler leaked delivery: %+v", store.Stats())
	}
	req := httptest.NewRequest("GET", path2, nil)
	req.Header.Set("Authorization", "Bearer "+r.f.owner.AccessToken)
	inspected := false
	h.ServeHTTP(facetDisconnectedWriter{httptest.NewRecorder(), func() {
		inspected = true
		if stats := store.Stats(); stats.Deliveries != 1 || stats.DeliveryBytes == 0 {
			t.Fatalf("lease released before transport: %+v", stats)
		}
	}}, req)
	if !inspected || store.Stats().Deliveries != 0 || store.Stats().DeliveryBytes != 0 {
		t.Fatal("disconnect leaked or prematurely released delivery")
	}
}

func TestReadingFacetQueriesHTTPHandleDomainLibraryRestartDrift(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	readingSeriesOwnerField(t, r, r.item, "author", "Private author")
	readingSeriesDrain(t, r)
	store, release := readingFacetTestStore(t, r, true)
	h := readingFacetTestHandler(t, r)
	pending, path := readingFacetStart(t, r, h)
	release()
	readingFacetReady(t, r, h, path)
	// A different domain's handle bound to the same authenticated session must
	// not become Reading content. Obtain the exact Reading caller at the seam.
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("Authorization", "Bearer "+r.f.owner.AccessToken)
	req.SetPathValue("id", r.library)
	a, err := r.d.readingFacetAuthority(req)
	if err != nil {
		t.Fatal(err)
	}
	otherDomain := readprep.Caller("simple:" + string(a.expected))
	// Even when the opaque handle actually names another domain's work, the
	// typed Reading HTTP endpoint cannot consume it or release that demand.
	foreignGate, foreignStarted := make(chan struct{}), make(chan struct{})
	defer close(foreignGate)
	foreign, err := store.Submit(context.Background(), otherDomain, readprep.Key{Namespace: "simple-facet", Basis: "immutable", Canonical: "other-domain"}, readprep.Plan{InitialBytes: 64, New: func(context.Context, *readprep.Budget) (readprep.Computation, error) {
		return &readingFacetBlocker{foreignGate, foreignStarted}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	foreignPath := readingFacetPollLocation(r.library, foreign)
	w := readingHTTP(t, h, "GET", foreignPath, r.f.owner.AccessToken, "", nil)
	if w.Code != 409 {
		t.Fatalf("cross-domain: %d %s", w.Code, w.Body.String())
	}
	w = readingHTTP(t, h, "DELETE", foreignPath, r.f.owner.AccessToken, "", nil)
	if w.Code != 204 {
		t.Fatalf("private cancel response: %d %s", w.Code, w.Body.String())
	}
	foreignResult, err := store.Poll(context.Background(), otherDomain, foreign)
	foreignResult.Release()
	if err != nil {
		t.Fatalf("another domain's interest was cancelled: %v", err)
	}
	// Replaying a valid handle under another identity/domain cannot poll/cancel it.
	if _, err = store.Poll(context.Background(), otherDomain, readprep.Handle(pending.QueryID)); err != readprep.ErrStale {
		t.Fatal(err)
	}
	if err = store.Cancel(context.Background(), otherDomain, readprep.Handle(pending.QueryID)); err != nil {
		t.Fatal(err)
	}
	readingFacetReady(t, r, h, path)
	w = readingHTTP(t, h, "GET", strings.Replace(path, r.library, "other-library", 1), r.f.owner.AccessToken, "", nil)
	if w.Code == 200 || w.Code == 202 || strings.Contains(w.Body.String(), "Private author") {
		t.Fatalf("cross-library leak: %d %s", w.Code, w.Body.String())
	}
	// A real synchronous input mutation invalidates a completed/cached result.
	readingSeriesOwnerField(t, r, r.item, "author", "Replacement author")
	readingSeriesDrain(t, r)
	w = readingHTTP(t, h, "GET", path, r.f.owner.AccessToken, "", nil)
	if w.Code != 409 || strings.Contains(w.Body.String(), "Private author") {
		t.Fatalf("drift: %d %s", w.Code, w.Body.String())
	}
	// A fresh process store has no previous query data; restart is typed stale.
	fresh, err := readprep.New(readprep.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fresh.Close() })
	r.d.Catalog.UseReadPreparations(fresh)
	w = readingHTTP(t, h, "GET", path, r.f.owner.AccessToken, "", nil)
	if w.Code != 409 {
		t.Fatalf("restart: %d %s", w.Code, w.Body.String())
	}
}

func TestReadingFacetQueriesHTTPRevokedReadyAndPending(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, ready := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "ready"}[ready], func(t *testing.T) {
			r := newReadingFixture(t, "book", "pdf")
			readingSeriesOwnerField(t, r, r.item, "author", "Revoked author")
			readingSeriesDrain(t, r)
			store, release := readingFacetTestStore(t, r, true)
			h := readingFacetTestHandler(t, r)
			_, path := readingFacetStart(t, r, h)
			if ready {
				release()
				readingFacetReady(t, r, h, path)
			}
			if err := r.f.id.LogoutToken(context.Background(), r.f.owner.AccessToken); err != nil {
				t.Fatal(err)
			}
			for _, method := range []string{"GET", "DELETE"} {
				w := readingHTTP(t, h, method, path, r.f.owner.AccessToken, "", nil)
				if w.Code != 401 || strings.Contains(w.Body.String(), "Revoked author") {
					t.Fatalf("revoked %s: %d %s", method, w.Code, w.Body.String())
				}
			}
			if store.Stats().Deliveries != 0 {
				t.Fatal("revoked delivery leaked")
			}
		})
	}
}

func TestReadingFacetQueriesStatusContracts(t *testing.T) {
	registry := apikit.NewContextual((Dependencies{}).v1Authorize, policyCatalogue())
	if err := registerReadingFacetQueryRoutes(registry, Dependencies{}); err != nil {
		t.Fatal(err)
	}
	generated, err := apikit.GenerateWith(registry.Definitions(), apikit.Options{GrandfatheredUnionTags: GrandfatheredUnionTags})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string `json:"operationId"`
			Parameters  []struct {
				Name string `json:"name"`
			} `json:"parameters"`
			Responses map[string]struct {
				Headers map[string]any `json:"headers"`
				Content map[string]struct {
					Schema map[string]any `json:"schema"`
				} `json:"content"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err = json.Unmarshal(generated.OpenAPI, &doc); err != nil {
		t.Fatal(err)
	}
	get := doc.Paths[readingFacetQueryPath]["get"]
	if get.OperationID != "get_library_reading_facet_query" || doc.Paths[readingFacetQueryPath]["delete"].OperationID != "delete_library_reading_facet_query" {
		t.Fatal("operation IDs changed")
	}
	if len(get.Parameters) != 2 || get.Parameters[0].Name != "id" || get.Parameters[1].Name != "queryId" {
		t.Fatalf("params: %+v", get.Parameters)
	}
	if len(get.Responses["200"].Headers) != 0 || len(get.Responses["202"].Headers) != 2 {
		t.Fatal("status headers changed")
	}
	ready := get.Responses["200"].Content["application/json"].Schema
	pending := get.Responses["202"].Content["application/json"].Schema
	if ready["anyOf"] != nil || ready["properties"].(map[string]any)["values"] == nil || pending["properties"].(map[string]any)["kind"] == nil {
		t.Fatal("ready/pending schema lost")
	}
}

func TestReadingFacetQueriesHTTPPolicyChangeCannotReuseReadyHandle(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	readingSeriesOwnerField(t, r, r.item, "author", "Formerly visible author")
	readingSeriesDrain(t, r)
	owner := readingOwnerLogin(t, r)
	profiles, err := r.f.id.CreateDirectProfile(context.Background(), owner.AccountToken, "Facet reader", "blue")
	if err != nil {
		t.Fatal(err)
	}
	var profile identity.DirectProfile
	for _, p := range profiles.Profiles {
		if p.Name == "Facet reader" {
			profile = p
		}
	}
	if profile.ID == "" {
		t.Fatal("missing real profile")
	}
	selected, err := r.f.id.SelectDirectProfile(context.Background(), owner.AccountToken, profile.ID, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	r.f.owner = selected.Session
	store, release := readingFacetTestStore(t, r, true)
	h := readingFacetTestHandler(t, r)
	_, path := readingFacetStart(t, r, h)
	release()
	readingFacetReady(t, r, h, path)
	_, err = r.f.id.SetProfileRestrictions(context.Background(), owner.AccountToken, profile.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 1, AllowUnrated: false})
	if err != nil {
		t.Fatal(err)
	}
	w := readingHTTP(t, h, "GET", path, r.f.owner.AccessToken, "", nil)
	if w.Code != 409 || strings.Contains(w.Body.String(), "Formerly visible author") {
		t.Fatalf("policy reused old result: %d %s", w.Code, w.Body.String())
	}
	if store.Stats().Deliveries != 0 {
		t.Fatal("policy refusal leaked delivery")
	}
}

func TestReadingFacetQueriesPublicCohortDispatch(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	readingSeriesOwnerField(t, r, r.item, "author", "Resident Author")
	readingSeriesDrain(t, r)
	path := "/v1/libraries/" + r.library + "/facets?field=author&limit=1"
	oracle := readingHTTP(t, New(r.d), "GET", path, r.f.owner.AccessToken, "", nil)
	if oracle.Code != 200 {
		t.Fatal(oracle.Code, oracle.Body.String())
	}
	store, release := readingFacetTestStore(t, r, true)
	r.d.PrepareReadingFacets = true
	h := New(r.d)
	pending := readingHTTP(t, h, "GET", path, r.f.owner.AccessToken, "", nil)
	if pending.Code != 202 || pending.Header().Get("Retry-After") != "0" || pending.Header().Get("Location") == "" {
		t.Fatal(pending.Code, pending.Header(), pending.Body.String())
	}
	release()
	ready := readingFacetReady(t, r, h, pending.Header().Get("Location"))
	var want, got catalog.FacetPage
	if err := json.Unmarshal(oracle.Body.Bytes(), &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(ready.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// The new opaque revision binds the complete preparation witness. Facts
	// and the existing wire shape retain their exact foreground semantics.
	if len(got.Revision) != 64 {
		t.Fatal("missing complete facet basis", got.Revision)
	}
	want.Revision = got.Revision
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("public dispatch changed facet oracle: want=%+v got=%+v", want, got)
	}
	if store.Stats().Deliveries != 0 {
		t.Fatal("public response leaked delivery", store.Stats())
	}
}
