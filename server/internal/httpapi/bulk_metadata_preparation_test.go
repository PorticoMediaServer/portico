package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

type bulkPreviewHTTPFixture struct {
	d             Dependencies
	h             http.Handler
	owner, member string
	store         *readprep.Store
}

func installBulkPreviewTestStore(t *testing.T, c *catalog.Service) *readprep.Store {
	t.Helper()
	s, err := readprep.New(readprep.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	c.UseReadPreparations(s)
	return s
}

func newBulkPreviewHTTPFixture(t *testing.T) *bulkPreviewHTTPFixture {
	t.Helper()
	d, owner, member := tl6SupportFixture(t)
	tl6FixtureMovie(t, d, "private-library", "Alpha.mkv", "Alpha", 2020)
	tl6FixtureMovie(t, d, "private-library", "Beta.mkv", "Beta", 2021)
	d.Metadata = metadata.New(d.DB, "")
	if err := d.Metadata.SetArtworkDirectory(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	store := installBulkPreviewTestStore(t, d.Catalog)
	return &bulkPreviewHTTPFixture{d: d, h: New(d), owner: owner.AccessToken, member: member.AccessToken, store: store}
}

func bulkPreviewHTTPReady(t *testing.T, h http.Handler, token string, w *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for w.Code == 202 {
		var pending bulkMetadataPreviewPreparing
		if err := json.Unmarshal(w.Body.Bytes(), &pending); err != nil {
			t.Fatal(err)
		}
		if pending.Kind != "metadata-preview-preparing" || pending.State != "preparing" || pending.RetryAfterMs != 1000 || pending.QueryID == "" || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Retry-After") != "1" {
			t.Fatal(w.Code, w.Header(), w.Body.String())
		}
		path := w.Header().Get("Location")
		if !strings.HasSuffix(path, "/"+pending.QueryID) {
			t.Fatal(path)
		}
		if time.Now().After(deadline) {
			t.Fatal("metadata preview failed to finish")
		}
		time.Sleep(time.Millisecond)
		w = editorRequest(t, h, "GET", path, token, "", nil)
	}
	return w
}

func TestBulkMetadataPreparationHTTPManyAndDeliveryHold(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newBulkPreviewHTTPFixture(t)
	releaseWorker := blockBulkPreviewWorker(t, f.store)
	c := catalogtest.New(t, f.d.DB)
	var request catalog.BulkMetadataPreviewRequest
	for i := 0; i < 70; i++ {
		item := c.Movie(c.Handle("private-library"), fmt.Sprintf("/preview/%d.mkv", i), fmt.Sprintf("Preview %d", i), 1900+i)
		request.Targets = append(request.Targets, catalog.BulkMetadataTarget{Kind: "item", ContentKind: "movie", ID: item.Public, LibraryID: "private-library"})
	}
	c.Drain()
	raw, _ := json.Marshal(request)
	w := editorRequest(t, f.h, "POST", "/v1/metadata/bulk/preview", f.owner, "application/json", raw)
	var pending bulkMetadataPreviewPreparing
	if w.Code != 202 {
		t.Fatalf("cold preparation should yield: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}
	path := w.Header().Get("Location")
	releaseWorker()
	if other := editorRequest(t, f.h, "GET", path, f.member, "", nil); other.Code != 403 {
		t.Fatal(other.Code, other.Body.String())
	}
	w = bulkPreviewHTTPReady(t, f.h, f.owner, w)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var preview metadata.BulkMetadataPreview
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	field := preview.Fields["year"]
	if preview.Total != 70 || field.State != "many" || field.AtLeast != 65 || field.DistinctCount != 0 || field.Value != nil || len(field.Values) != 0 || !field.Truncated || len(preview.Targets) != 70 {
		t.Fatal(preview.Total, field, len(preview.Targets))
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("ETag") != "" {
		t.Fatal(w.Header())
	}
	if stats := f.store.Stats(); stats.Deliveries != 0 || stats.DeliveryBytes != 0 {
		t.Fatal(stats)
	}
	// A disconnected response must retain the whole delivery through Write,
	// then release it even when the client rejects the transport bytes.
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Authorization", "Bearer "+f.owner)
	inspected := false
	writer := facetDisconnectedWriter{httptest.NewRecorder(), func() {
		inspected = true
		if stats := f.store.Stats(); stats.Deliveries != 1 || stats.DeliveryBytes <= 0 || stats.Bytes > f.store.MaxBytes() {
			t.Error(stats)
		}
	}}
	f.h.ServeHTTP(writer, r)
	if !inspected || f.store.Stats().Deliveries != 0 {
		t.Fatal(inspected, f.store.Stats())
	}
	if w = editorRequest(t, f.h, "DELETE", path, f.owner, "", nil); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = editorRequest(t, f.h, "GET", path, f.owner, "", nil); w.Code != 409 || errorCode(t, w) != "stale_continuation" {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestBulkMetadataPreparationHTTPReadyCannotSurviveMetadataDrift(t *testing.T) {
	f := newBulkPreviewHTTPFixture(t)
	releaseWorker := blockBulkPreviewWorker(t, f.store)
	id := editorItemPublic(t, f.d.DB, "Alpha")
	request := catalog.BulkMetadataPreviewRequest{Targets: []catalog.BulkMetadataTarget{{Kind: "item", ContentKind: "movie", ID: id, LibraryID: "private-library"}}}
	raw, _ := json.Marshal(request)
	w := editorRequest(t, f.h, "POST", "/v1/metadata/bulk/preview", f.owner, "application/json", raw)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	path := w.Header().Get("Location")
	releaseWorker()
	w = bulkPreviewHTTPReady(t, f.h, f.owner, w)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := dbwork.ExecWrite(context.Background(), f.d.DB, dbwork.ClassInteractive, `UPDATE catalog_entities SET year=1999 WHERE public_id=pid_blob(?)`, id); err != nil {
		t.Fatal(err)
	}
	w = editorRequest(t, f.h, "GET", path, f.owner, "", nil)
	if w.Code != 409 || errorCode(t, w) != "stale_continuation" {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestBulkMetadataPreparationHTTPPreparingAndErrorHoldThroughWrite(t *testing.T) {
	f := newBulkPreviewHTTPFixture(t)
	releaseWorker := blockBulkPreviewWorker(t, f.store)
	defer releaseWorker()
	id := editorItemPublic(t, f.d.DB, "Alpha")
	request := catalog.BulkMetadataPreviewRequest{Targets: []catalog.BulkMetadataTarget{{Kind: "item", ContentKind: "movie", ID: id, LibraryID: "private-library"}}}
	raw, _ := json.Marshal(request)
	w := editorRequest(t, f.h, "POST", "/v1/metadata/bulk/preview", f.owner, "application/json", raw)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	path := w.Header().Get("Location")
	for _, drift := range []bool{false, true} {
		if drift {
			if _, err := dbwork.ExecWrite(context.Background(), f.d.DB, dbwork.ClassInteractive, `UPDATE catalog_entities SET year=1999 WHERE public_id=pid_blob(?)`, id); err != nil {
				t.Fatal(err)
			}
		}
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+f.owner)
		r.Header.Set("If-None-Match", "*")
		inspected := false
		writer := facetDisconnectedWriter{httptest.NewRecorder(), func() {
			inspected = true
			if stats := f.store.Stats(); stats.Deliveries != 1 || stats.DeliveryBytes <= 0 || stats.Bytes > f.store.MaxBytes() {
				t.Error(stats)
			}
		}}
		f.h.ServeHTTP(writer, r)
		want := 202
		if drift {
			want = 409
		}
		if !inspected || writer.Code != want || writer.Header().Get("ETag") != "" || writer.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(inspected, writer.Code, writer.Header())
		}
		if stats := f.store.Stats(); stats.Deliveries != 0 || stats.DeliveryBytes != 0 {
			t.Fatal(stats)
		}
	}
}

func TestBulkMetadataPreparationHTTPRetryHandlesCancelIndependently(t *testing.T) {
	f := newBulkPreviewHTTPFixture(t)
	other, err := f.d.Identity.Issue("owner", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	releaseWorker := blockBulkPreviewWorker(t, f.store)
	defer releaseWorker()
	id := editorItemPublic(t, f.d.DB, "Alpha")
	request := catalog.BulkMetadataPreviewRequest{Targets: []catalog.BulkMetadataTarget{{Kind: "item", ContentKind: "movie", ID: id, LibraryID: "private-library"}}}
	raw, _ := json.Marshal(request)
	paths := []string{}
	for i := 0; i < 2; i++ {
		w := editorRequest(t, f.h, "POST", "/v1/metadata/bulk/preview", f.owner, "application/json", raw)
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		paths = append(paths, w.Header().Get("Location"))
	}
	// One unrelated blocker plus one content entry, with two private interests.
	if paths[0] == paths[1] || f.store.Stats().Entries != 2 {
		t.Fatal(paths, f.store.Stats())
	}
	// Even another administrative session for the same profile cannot read or
	// cancel this session's handle. DELETE remains privately idempotent.
	if w := editorRequest(t, f.h, "GET", paths[1], other.AccessToken, "", nil); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := editorRequest(t, f.h, "DELETE", paths[1], other.AccessToken, "", nil); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := editorRequest(t, f.h, "DELETE", paths[0], f.owner, "", nil); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := editorRequest(t, f.h, "GET", paths[0], f.owner, "", nil); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := editorRequest(t, f.h, "GET", paths[1], f.owner, "", nil)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	releaseWorker()
	w = bulkPreviewHTTPReady(t, f.h, f.owner, w)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if stats := f.store.Stats(); stats.Deliveries != 0 || stats.DeliveryBytes != 0 {
		t.Fatal(stats)
	}
}

func blockBulkPreviewWorker(t *testing.T, store *readprep.Store) func() {
	t.Helper()
	gate, started := make(chan struct{}), make(chan struct{})
	_, err := store.Submit(context.Background(), "test-blocker", readprep.Key{Namespace: "test", Basis: "test", Canonical: "test"}, readprep.Plan{InitialBytes: 64, New: func(context.Context, *readprep.Budget) (readprep.Computation, error) {
		return &readingFacetBlocker{gate: gate, started: started}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("test blocker did not start")
	}
	return release
}
