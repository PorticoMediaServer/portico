package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type heldReadingPreparation struct {
	gate    <-chan struct{}
	entered chan<- struct{}
}

func (*heldReadingPreparation) AccountedBytes() int64 { return 64 }
func (w *heldReadingPreparation) Step(ctx context.Context, _ *readprep.Budget) (readprep.StepResult, error) {
	close(w.entered)
	select {
	case <-w.gate:
	case <-ctx.Done():
	}
	return readprep.StepResult{}, readprep.ErrStale
}

type readingLeaseWriter struct {
	*httptest.ResponseRecorder
	check func()
}

func (w readingLeaseWriter) Write(b []byte) (int, error) {
	if w.check != nil {
		w.check()
	}
	return w.ResponseRecorder.Write(b)
}
func preparedReadingHTTP(t *testing.T, h http.Handler, method, path, token string, body any, check func()) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(readingLeaseWriter{w, check}, r)
	return w
}

func TestReadingBrowsePreparationHTTPLeaseReadyCancelAndDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newReadingFixture(t, "book", "pdf")
	readingSeriesDrain(t, f)
	cfg := readprep.DefaultConfig()
	cfg.Workers = 1
	store, err := readprep.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	f.d.Catalog.UseReadPreparations(store)
	gate := make(chan struct{})
	entered := make(chan struct{})
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(gate) }) })
	var heldBudget *readprep.Budget
	_, err = store.Submit(context.Background(), "other-domain", readprep.Key{Namespace: "facet", Basis: "basis", Canonical: "query"}, readprep.Plan{InitialBytes: 64, New: func(_ context.Context, b *readprep.Budget) (readprep.Computation, error) {
		heldBudget = b
		return &heldReadingPreparation{gate, entered}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	mux := http.NewServeMux()
	base := "/v1/libraries/" + f.library
	mux.HandleFunc("POST /v1/libraries/{id}/browse", f.d.readingBrowsePreparedHandler)
	mux.HandleFunc("GET /v1/libraries/{id}/reading-browse-queries/{queryId}", f.d.readingBrowseQueryHandler)
	mux.HandleFunc("DELETE /v1/libraries/{id}/reading-browse-queries/{queryId}", f.d.readingBrowseQueryHandler)
	checkLease := func() {
		if stats := store.Stats(); stats.Deliveries != 1 || stats.DeliveryBytes < int64(4096+4*len(base+"/reading-browse-queries/")) {
			t.Fatal("transport had no lease", stats)
		}
	}
	w := preparedReadingHTTP(t, mux, "POST", base+"/browse", f.f.owner.AccessToken, map[string]any{"limit": 1}, checkLease)
	if w.Code != 202 || w.Header().Get("Retry-After") != "0" || w.Header().Get("Location") == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	var pending ReadingBrowsePreparing
	if err = json.Unmarshal(w.Body.Bytes(), &pending); err != nil || pending.Kind != "reading-browse-preparing" || pending.State != "preparing" || pending.QueryID == "" || pending.RetryAfterMs != readingInitialRetryMs {
		t.Fatal(pending, err)
	}
	if store.Stats().Deliveries != 0 {
		t.Fatal("Preparing lease leaked")
	}
	path := base + "/reading-browse-queries/" + pending.QueryID
	w = preparedReadingHTTP(t, mux, "GET", path, f.f.owner.AccessToken, nil, checkLease)
	if w.Code != 202 || w.Header().Get("Retry-After") != "1" || !strings.Contains(w.Body.String(), `"retryAfterMs":1000`) || store.Stats().Deliveries != 0 {
		t.Fatal(w.Code, w.Body.String(), store.Stats())
	}
	// A Poll lease alone is insufficient for the pending envelope. Ordinary
	// contention must refuse before constructing Location, then release its lease.
	if err = heldBudget.Resize(64 + store.MaxBytes() - store.Stats().Bytes - 2048); err != nil {
		t.Fatal(err)
	}
	w = preparedReadingHTTP(t, mux, "GET", path, f.f.owner.AccessToken, nil, func() {
		if store.Stats().Deliveries != 1 {
			t.Fatal("pending capacity response lost its lease")
		}
	})
	if w.Code != 429 || !strings.Contains(w.Body.String(), `"code":"search_busy"`) || w.Header().Get("Retry-After") != "1" || w.Header().Get("Location") != "" {
		t.Fatal(w.Code, w.Body.String(), w.Header())
	}
	if store.Stats().Deliveries != 0 || store.Stats().DeliveryBytes != 0 {
		t.Fatal("pending capacity refusal leaked delivery reservation", store.Stats())
	}
	if err = heldBudget.Resize(64); err != nil {
		t.Fatal(err)
	}
	// A second interest has its own handle. Cancelling one must not kill another.
	second := preparedReadingHTTP(t, mux, "POST", base+"/browse", f.f.owner.AccessToken, map[string]any{"limit": 1}, checkLease)
	var other ReadingBrowsePreparing
	if err = json.Unmarshal(second.Body.Bytes(), &other); err != nil || other.QueryID == pending.QueryID {
		t.Fatal(second.Body.String())
	}
	w = preparedReadingHTTP(t, mux, "DELETE", path, f.f.owner.AccessToken, nil, nil)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	release.Do(func() { close(gate) })
	path = base + "/reading-browse-queries/" + other.QueryID
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		w = preparedReadingHTTP(t, mux, "GET", path, f.f.owner.AccessToken, nil, checkLease)
		if w.Code != 202 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var page catalog.BrowseResult
	if err = json.Unmarshal(w.Body.Bytes(), &page); err != nil || page.PageInfo.Total != 1 || len(page.Entries) != 1 {
		t.Fatal(page, err)
	}
	if store.Stats().Deliveries != 0 || store.Stats().DeliveryBytes != 0 {
		t.Fatal("ready delivery leaked", store.Stats())
	}
	// Completed cache hits must still reject source drift without returning rows.
	if _, err = f.d.DB.Exec(`INSERT INTO content_rating_ages VALUES('http-count-drift',0)`); err != nil {
		t.Fatal(err)
	}
	w = preparedReadingHTTP(t, mux, "GET", path, f.f.owner.AccessToken, nil, nil)
	if w.Code != 409 || store.Stats().Deliveries != 0 {
		t.Fatal(w.Code, w.Body.String(), store.Stats())
	}
}

func TestReadingBrowsePreparationRouteContracts(t *testing.T) {
	definitions := policyErrorDefinitions()
	errors, e := apierror.New(definitions...)
	if e != nil {
		t.Fatal(e)
	}
	registry := apikit.New(func(context.Context, *http.Request, apikit.Metadata) error { return nil }, errors)
	if e = registerReadingBrowseQueryRoutes(registry, Dependencies{}); e != nil {
		t.Fatal(e)
	}
	defs := registry.Definitions()
	if len(defs) != 2 || defs[0].ID != "get_library_reading_browse_query" || defs[1].ID != "delete_library_reading_browse_query" {
		t.Fatal(defs)
	}
	if _, e = apikit.Generate(defs); e != nil {
		t.Fatal(e)
	}
	pendingShape := readingBrowseQueryResponse{}.JSONSchemaForStatus(202)
	state := pendingShape["properties"].(map[string]any)["state"].(map[string]any)
	if state["const"] != "preparing" || !strings.Contains(fmt.Sprint(pendingShape["required"]), "state") {
		t.Fatal("preparing state contract missing", pendingShape)
	}
	shape := readingBrowseQueryResponse{}.JSONSchemaForStatus(200)
	want, _ := apikit.Schema(reflect.TypeFor[catalog.BrowseResult]())
	a, _ := json.Marshal(shape)
	b, _ := json.Marshal(want)
	if !bytes.Equal(a, b) {
		t.Fatal("ready contract changed")
	}
}

func TestReadingBrowsePreparationRealClientRangeAndSeek(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newReadingFixture(t, "book", "pdf")
	err := dbwork.WithWriteTx(context.Background(), f.d.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		var library int64
		if e := tx.QueryRow(`SELECT id FROM catalog_libraries WHERE library_id=?`, f.library).Scan(&library); e != nil {
			return e
		}
		item, _, e := compactcatalog.UpsertEntityTx(context.Background(), tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Ebook, Key: "prepared-second", Title: "Second publication"})
		if e != nil {
			return e
		}
		asset, _, e := compactcatalog.UpsertAssetTx(context.Background(), tx, compactcatalog.Asset{Path: "/prepared-second.pdf", Container: "pdf", Size: 100, ModifiedNS: 1})
		if e != nil {
			return e
		}
		if e = compactcatalog.LinkAssetTx(context.Background(), tx, item, asset, compactcatalog.Link{}); e != nil {
			return e
		}
		_, e = tx.Exec(`INSERT INTO reading_publications VALUES(?,?,'pdf')`, item, asset)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	readingSeriesDrain(t, f)
	cfg := readprep.DefaultConfig()
	cfg.Workers = 1
	cfg.OriginalTTL = 2 * time.Minute
	store, err := readprep.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	f.d.Catalog.UseReadPreparations(store)
	mux := http.NewServeMux()
	base := "/v1/libraries/" + f.library
	mux.HandleFunc("POST /v1/libraries/{id}/browse", f.d.readingBrowsePreparedHandler)
	mux.HandleFunc("GET /v1/libraries/{id}/reading-browse-queries/{queryId}", f.d.readingBrowseQueryHandler)
	// Force an observable 202 on each original request, including the exact
	// range body emitted by BrowseSession.fetchPage on its first load.
	prepare := func(body any) catalog.BrowseResult {
		gate := make(chan struct{})
		entered := make(chan struct{})
		var release sync.Once
		t.Cleanup(func() { release.Do(func() { close(gate) }) })
		_, e := store.Submit(context.Background(), readprep.Caller(fmt.Sprint("block-", body)), readprep.Key{Namespace: "block", Basis: fmt.Sprint(body), Canonical: fmt.Sprint(time.Now().UnixNano())}, readprep.Plan{InitialBytes: 64, New: func(context.Context, *readprep.Budget) (readprep.Computation, error) {
			return &heldReadingPreparation{gate, entered}, nil
		}})
		if e != nil {
			t.Fatal(e)
		}
		<-entered
		submitted := time.Now()
		w := preparedReadingHTTP(t, mux, "POST", base+"/browse", f.f.owner.AccessToken, body, nil)
		if w.Code != 202 {
			release.Do(func() { close(gate) })
			t.Fatal("real client body did not prepare", w.Code, w.Body.String())
		}
		var pending ReadingBrowsePreparing
		if e = json.Unmarshal(w.Body.Bytes(), &pending); e != nil {
			t.Fatal(e)
		}
		release.Do(func() { close(gate) })
		until := time.Now().Add(3 * time.Second)
		for time.Now().Before(until) {
			w = preparedReadingHTTP(t, mux, "GET", base+"/reading-browse-queries/"+pending.QueryID, f.f.owner.AccessToken, nil, nil)
			if w.Code != 202 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var page catalog.BrowseResult
		if e = json.Unmarshal(w.Body.Bytes(), &page); e != nil {
			t.Fatal(e)
		}
		if page.PageInfo.NextCursor != "" {
			raw, _, _ := strings.Cut(page.PageInfo.NextCursor, ".")
			b, e := base64.RawURLEncoding.DecodeString(raw)
			if e != nil {
				t.Fatal(e)
			}
			var envelope struct{ Expires int64 }
			if e = json.Unmarshal(b, &envelope); e != nil || envelope.Expires > submitted.Add(cfg.OriginalTTL+time.Second).Unix() {
				t.Fatal("original preparation expiry renewed", envelope, e)
			}
		}
		if store.Stats().Deliveries != 0 {
			t.Fatal("range delivery leaked")
		}
		return page
	}
	first := prepare(map[string]any{"pivot": "books", "limit": 1, "range": map[string]any{"start": 0}})
	if first.PageInfo.Start != 0 || first.PageInfo.Total != 2 || len(first.Entries) != 1 {
		t.Fatal(first)
	}
	next := prepare(map[string]any{"pivot": "books", "limit": 1, "range": map[string]any{"start": 1, "revision": first.PageInfo.Revision}})
	if next.PageInfo.Start != 1 || next.PageInfo.Total != 2 || len(next.Entries) != 1 || next.Entries[0].ID == first.Entries[0].ID {
		t.Fatal(first, next)
	}
	seek := prepare(map[string]any{"pivot": "books", "limit": 1, "seek": map[string]any{"prefix": "S"}})
	if len(seek.Entries) != 1 || seek.Entries[0].Title != "Second publication" {
		t.Fatal(seek)
	}
}

func TestReadingBrowseSharedContentKeepsSessionRevocationPrivate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newReadingFixture(t, "book", "pdf")
	readingSeriesDrain(t, f)
	login := readingOwnerLogin(t, f)
	selected, err := f.f.id.SelectDirectProfile(context.Background(), login.AccountToken, f.p.ProfileID, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	secondToken := selected.Session.AccessToken
	cfg := readprep.DefaultConfig()
	cfg.Workers = 1
	store, err := readprep.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	f.d.Catalog.UseReadPreparations(store)
	gate := make(chan struct{})
	entered := make(chan struct{})
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(gate) }) })
	_, err = store.Submit(context.Background(), "blocking", readprep.Key{Namespace: "block", Basis: "b", Canonical: "c"}, readprep.Plan{InitialBytes: 64, New: func(context.Context, *readprep.Budget) (readprep.Computation, error) {
		return &heldReadingPreparation{gate, entered}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	mux := http.NewServeMux()
	base := "/v1/libraries/" + f.library
	mux.HandleFunc("POST /v1/libraries/{id}/browse", f.d.readingBrowsePreparedHandler)
	mux.HandleFunc("GET /v1/libraries/{id}/reading-browse-queries/{queryId}", f.d.readingBrowseQueryHandler)
	body := map[string]any{"pivot": "books", "limit": 1, "range": map[string]any{"start": 0}}
	submit := func(token string) ReadingBrowsePreparing {
		w := preparedReadingHTTP(t, mux, "POST", base+"/browse", token, body, nil)
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		var out ReadingBrowsePreparing
		if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	one, two := submit(f.f.owner.AccessToken), submit(secondToken)
	if one.QueryID == two.QueryID || store.Stats().Entries != 2 {
		t.Fatal("sessions did not share content with private handles", store.Stats())
	}
	w := preparedReadingHTTP(t, mux, "GET", base+"/reading-browse-queries/"+one.QueryID, secondToken, nil, nil)
	if w.Code != 409 {
		t.Fatal("cross-session handle accepted", w.Code, w.Body.String())
	}
	if err = f.f.id.LogoutToken(context.Background(), f.f.owner.AccessToken); err != nil {
		t.Fatal(err)
	}
	release.Do(func() { close(gate) })
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		w = preparedReadingHTTP(t, mux, "GET", base+"/reading-browse-queries/"+two.QueryID, secondToken, nil, nil)
		if w.Code != 202 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if w.Code != 200 {
		t.Fatal("revoked producer session killed another caller's content", w.Code, w.Body.String())
	}
	w = preparedReadingHTTP(t, mux, "GET", base+"/reading-browse-queries/"+one.QueryID, f.f.owner.AccessToken, nil, nil)
	if w.Code != 401 {
		t.Fatal("revoked caller read shared content", w.Code, w.Body.String())
	}
	if store.Stats().Deliveries != 0 {
		t.Fatal("private revocation leaked a lease", store.Stats())
	}
}

func TestReadingBrowseReadyDeliveryRefusesAggregateAndGiantWithoutLeaseLeak(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newReadingFixture(t, "book", "pdf")
	err := dbwork.WithWriteTx(context.Background(), f.d.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		var library int64
		if e := tx.QueryRow(`SELECT id FROM catalog_libraries WHERE library_id=?`, f.library).Scan(&library); e != nil {
			return e
		}
		item, _, e := compactcatalog.UpsertEntityTx(context.Background(), tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Ebook, Key: "capacity-second", Title: "Second"})
		if e != nil {
			return e
		}
		asset, _, e := compactcatalog.UpsertAssetTx(context.Background(), tx, compactcatalog.Asset{Path: "/capacity-second.pdf", Container: "pdf", Size: 100, ModifiedNS: 1})
		if e != nil {
			return e
		}
		if e = compactcatalog.LinkAssetTx(context.Background(), tx, item, asset, compactcatalog.Link{}); e != nil {
			return e
		}
		_, e = tx.Exec(`INSERT INTO reading_publications VALUES(?,?,'pdf')`, item, asset)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	readingSeriesDrain(t, f)
	for _, giant := range []bool{false, true} {
		size, limit := 150000, 2
		if giant {
			size, limit = 300000, 1
		}
		if _, err = f.d.DB.Exec(`UPDATE catalog_browse_rows SET title=? WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?)`, strings.Repeat("x", size), f.library); err != nil {
			t.Fatal(err)
		}
		cfg := readprep.DefaultConfig()
		cfg.Workers = 1
		cfg.MaxBytes = 8 << 20
		store, e := readprep.New(cfg)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { store.Close() })
		f.d.Catalog.UseReadPreparations(store)
		gate := make(chan struct{})
		entered := make(chan struct{})
		var release sync.Once
		t.Cleanup(func() { release.Do(func() { close(gate) }) })
		_, e = store.Submit(context.Background(), "held", readprep.Key{Namespace: "held", Basis: "b", Canonical: "c"}, readprep.Plan{InitialBytes: 64, New: func(context.Context, *readprep.Budget) (readprep.Computation, error) {
			return &heldReadingPreparation{gate, entered}, nil
		}})
		if e != nil {
			t.Fatal(e)
		}
		<-entered
		mux := http.NewServeMux()
		base := "/v1/libraries/" + f.library
		mux.HandleFunc("POST /v1/libraries/{id}/browse", f.d.readingBrowsePreparedHandler)
		mux.HandleFunc("GET /v1/libraries/{id}/reading-browse-queries/{queryId}", f.d.readingBrowseQueryHandler)
		w := preparedReadingHTTP(t, mux, "POST", base+"/browse", f.f.owner.AccessToken, map[string]any{"pivot": "books", "limit": limit, "range": map[string]any{"start": 0}}, nil)
		if w.Code != 202 {
			release.Do(func() { close(gate) })
			t.Fatal(w.Code, w.Body.String())
		}
		var pending ReadingBrowsePreparing
		if e = json.Unmarshal(w.Body.Bytes(), &pending); e != nil {
			t.Fatal(e)
		}
		release.Do(func() { close(gate) })
		until := time.Now().Add(3 * time.Second)
		for time.Now().Before(until) {
			w = preparedReadingHTTP(t, mux, "GET", base+"/reading-browse-queries/"+pending.QueryID, f.f.owner.AccessToken, nil, func() {
				if store.Stats().Deliveries != 1 {
					t.Fatal("capacity serialization lost its lease")
				}
			})
			if w.Code != 202 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if w.Code != 429 || !strings.Contains(w.Body.String(), `"code":"search_busy"`) || w.Header().Get("Retry-After") != "1" {
			t.Fatal("unreserved page hydration bypassed the cap", giant, w.Code, w.Body.String())
		}
		if store.Stats().Deliveries != 0 || store.Stats().DeliveryBytes != 0 {
			t.Fatal("capacity refusal leaked delivery reservation", store.Stats())
		}
	}
}

func TestReadingBrowsePublicCohortDispatchAndProofRefusal(t *testing.T) {
	f := newReadingFixture(t, "book", "pdf")
	readingSeriesDrain(t, f)
	path := "/v1/libraries/" + f.library + "/browse"
	body := map[string]any{"pivot": "books", "limit": 1, "range": map[string]any{"start": 0}}
	oracle := preparedReadingHTTP(t, New(f.d), "POST", path, f.f.owner.AccessToken, body, nil)
	if oracle.Code != 200 {
		t.Fatal(oracle.Code, oracle.Body.String())
	}
	store, release := readingFacetTestStore(t, f, true)
	f.d.PrepareReadingBrowse = true
	h := New(f.d)
	pending := preparedReadingHTTP(t, h, "POST", path, f.f.owner.AccessToken, body, nil)
	var envelope ReadingBrowsePreparing
	if pending.Code != 202 || json.Unmarshal(pending.Body.Bytes(), &envelope) != nil || envelope.State != "preparing" || envelope.Kind != "reading-browse-preparing" || pending.Header().Get("Retry-After") != "0" || pending.Header().Get("Location") == "" {
		t.Fatal(pending.Code, pending.Header(), pending.Body.String())
	}
	body["selectionBasis"] = strings.Repeat("a", 64)
	refused := preparedReadingHTTP(t, h, "POST", path, f.f.owner.AccessToken, body, nil)
	if refused.Code != 409 {
		t.Fatal("supplied whole-selection proof was ignored", refused.Code, refused.Body.String())
	}
	release()
	ready := readingFacetReady(t, f, h, pending.Header().Get("Location"))
	var want, got catalog.BrowseResult
	if err := json.Unmarshal(oracle.Body.Bytes(), &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(ready.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("prepared public browse changed page: want=%+v got=%+v", want, got)
	}
	if store.Stats().Deliveries != 0 {
		t.Fatal("public page leaked delivery", store.Stats())
	}
}
