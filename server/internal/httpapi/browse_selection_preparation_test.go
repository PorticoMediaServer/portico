package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

type selectionHTTPFixture struct {
	*tl11AdmissionFixture
	store *readprep.Store
}

func newSelectionHTTPFixture(t *testing.T, configs ...readprep.Config) *selectionHTTPFixture {
	f := newTL11AdmissionFixture(t, "movie")
	config := readprep.Config{Workers: 1}
	if len(configs) > 0 {
		config = configs[0]
	}
	store, err := readprep.New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	f.d.Catalog.UseReadPreparations(store)
	f.d.Administration = administration.New(f.db)
	f.c.Library("other", "Other", "movie", "/other")
	f.c.Drain()
	f.h = New(f.d)
	return &selectionHTTPFixture{tl11AdmissionFixture: f, store: store}
}

func (f *selectionHTTPFixture) prepare(t *testing.T, hold <-chan struct{}) (catalog.BrowseSelectionPreparing, string) {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/libraries/movies/browse", nil)
	r.SetPathValue("id", "movies")
	r.Header.Set("Authorization", "Bearer "+f.token)
	p, v, err := f.d.browseSelectionQueryScope(r)
	if err != nil {
		t.Fatal(err)
	}
	var out catalog.BrowseResult
	err = f.d.compose(r, v.EffectiveRestrictions(), func(cat *catalog.Service) error {
		request := catalog.BrowseRequest{Library: "movies", Pivot: "movies", Limit: 1}
		var err error
		out, err = cat.BrowseEntities(v, request)
		if err != nil {
			return err
		}
		if hold == nil {
			return f.d.attachWholeSelectionPreparation(r, p, v, cat, request, browseRequestBody{}, &out)
		}
		access, err := f.d.browseSelectionAccess(p, "movies", v)
		if err != nil {
			return err
		}
		check := access.Check
		access.Check = func(read *catalog.Service) error {
			if err := check(read); err != nil {
				return err
			}
			if dbwork.ClassFrom(read.Context(), dbwork.ClassInteractive) == dbwork.ClassBackgroundMedia {
				select {
				case <-hold:
				case <-read.Context().Done():
					return read.Context().Err()
				}
			}
			return nil
		}
		pending, err := cat.SubmitBrowseSelectionPreparation(request, v, *out.Bound, access)
		out.WholeSelectionPreparing = &pending
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 1 || out.WholeSelection != nil || out.WholeSelectionPreparing == nil {
		t.Fatalf("ordinary page was blocked or replaced: %+v", out)
	}
	return *out.WholeSelectionPreparing, out.PageInfo.Revision
}

func selectionHTTPReady(t *testing.T, f *selectionHTTPFixture, path string) (catalog.BrowseSelectionDescriptor, *httptest.ResponseRecorder) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w := f.request("GET", path, nil, nil)
		if w.Code == 200 {
			var value catalog.BrowseSelectionDescriptor
			if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
				t.Fatal(err)
			}
			return value, w
		}
		if w.Code != 202 {
			t.Fatalf("poll %d: %s", w.Code, w.Body.String())
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("selection preparation did not finish")
	return catalog.BrowseSelectionDescriptor{}, nil
}

func TestBrowseSelectionQueryHTTPPreparingReadyCancelAndScope(t *testing.T) {
	f := newSelectionHTTPFixture(t)
	hold := make(chan struct{})
	pending, revision := f.prepare(t, hold)
	path := browseSelectionQueryPath("movies", pending.QueryID)
	w := f.request("GET", path, nil, nil)
	if w.Code != 202 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Location") != path || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("pending %d %#v %s", w.Code, w.Header(), w.Body.String())
	}
	var value catalog.BrowseSelectionPreparing
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if value.Kind != "browse-selection-preparing" || value.State != "preparing" || value.QueryID != pending.QueryID || value.RetryAfterMs != 1000 {
		t.Fatal(value)
	}
	if f.store.Stats().Deliveries != 0 {
		t.Fatal("pending HTTP delivery leaked")
	}
	wrong := f.request("GET", browseSelectionQueryPath("other", pending.QueryID), nil, nil)
	if wrong.Code != 409 {
		t.Fatalf("cross-library handle %d %s", wrong.Code, wrong.Body.String())
	}
	// A foreign caller's idempotent cancellation releases no demand here.
	foreignCancel := f.request("DELETE", browseSelectionQueryPath("other", pending.QueryID), nil, nil)
	if foreignCancel.Code != 204 {
		t.Fatal(foreignCancel.Code, foreignCancel.Body.String())
	}
	close(hold)
	ready, w := selectionHTTPReady(t, f, path)
	if ready.Total != 1 || ready.CatalogRevision != revision || len(ready.Types) != 1 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("ready %+v", ready)
	}
	if f.store.Stats().Deliveries != 0 {
		t.Fatal("ready HTTP delivery leaked")
	}
	for range 2 {
		if cancel := f.request("DELETE", path, nil, nil); cancel.Code != 204 {
			t.Fatal(cancel.Code, cancel.Body.String())
		}
	}
	if after := f.request("GET", path, nil, nil); after.Code != 409 {
		t.Fatal("cancelled handle survived", after.Code, after.Body.String())
	}
	if f.store.Stats().Deliveries != 0 {
		t.Fatal("error HTTP delivery leaked")
	}
}

func TestBrowseSelectionQueryHTTPLiveDeletePolicyAndForegroundGate(t *testing.T) {
	f := newSelectionHTTPFixture(t)
	// Installing the shared store does not enable the new foreground behavior.
	ordinary := f.request("POST", "/v1/libraries/movies/browse", nil, map[string]any{"pivot": "movies", "limit": 1})
	if ordinary.Code != 200 || !strings.Contains(ordinary.Body.String(), `"wholeSelection":`) || strings.Contains(ordinary.Body.String(), `"wholeSelectionPreparing":`) {
		t.Fatalf("foreground switched without cohort: %d %s", ordinary.Code, ordinary.Body.String())
	}
	pending, _ := f.prepare(t, nil)
	path := browseSelectionQueryPath("movies", pending.QueryID)
	before, _ := selectionHTTPReady(t, f, path)
	if before.Types[0].CapabilityCounts.Delete != 0 {
		t.Fatal("initial library deletion was offered")
	}
	p, err := f.d.Identity.Authenticate(f.token)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := f.d.Administration.LibrarySettingsFor(context.Background(), f.d.administrationAuthority(p), "movies")
	if err != nil {
		t.Fatal(err)
	}
	settings.Settings.AllowMediaDeletion = true
	if _, err = f.d.Administration.SaveLibrarySettings(context.Background(), f.d.administrationAuthority(p), "movies", administration.Change[administration.LibrarySettings]{ExpectedRevision: settings.Revision, OperationID: "selection-live-delete", Settings: settings.Settings}); err != nil {
		t.Fatal(err)
	}
	after, w := selectionHTTPReady(t, f, path)
	if after.Types[0].CapabilityCounts.Delete != 1 {
		t.Fatalf("final poll cached delete policy: %+v %s", after, w.Body.String())
	}
	if f.store.Stats().Deliveries != 0 {
		t.Fatal("live projection delivery leaked")
	}
}

func TestBrowseSelectionQueryHTTPBasisDriftAndPrivateSession(t *testing.T) {
	f := newSelectionHTTPFixture(t)
	pending, _ := f.prepare(t, nil)
	path := browseSelectionQueryPath("movies", pending.QueryID)
	_, _ = selectionHTTPReady(t, f, path)
	p, err := f.d.Identity.Authenticate(f.token)
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.d.Identity.Issue(p.AccountID, p.ProfileID, p.Authority, p.Role, p.Epoch)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Authorization", "Bearer "+other.AccessToken)
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != 409 {
		t.Fatalf("another session consumed handle: %d %s", w.Code, w.Body.String())
	}
	f.c.Exec(`UPDATE library_revisions SET revision=revision+1 WHERE library_id='movies'`)
	w = f.request("GET", path, nil, nil)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "stale_continuation") {
		t.Fatalf("basis drift %d %s", w.Code, w.Body.String())
	}
	if f.store.Stats().Deliveries != 0 {
		t.Fatal("drift delivery leaked")
	}
}

func TestBrowseSelectionQueryStatusContracts(t *testing.T) {
	raw := []byte(`{"kind":"browse-selection-preparing","state":"preparing","queryId":"id","retryAfterMs":1000}`)
	shape := (browseSelectionQueryResponse{}).JSONSchemaForStatus(202)
	encoded, err := json.Marshal(shape)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(encoded, &shape); err != nil {
		t.Fatal(err)
	}
	doc := apispec.Document{}
	if problems := doc.ValidateJSON(shape, raw); len(problems) != 0 {
		t.Fatal(problems)
	}
	for _, bad := range []string{
		strings.Replace(string(raw), "browse-selection-preparing", "reading-facet-preparing", 1),
		strings.Replace(string(raw), "preparing\",\"queryId", "ready\",\"queryId", 1),
		strings.Replace(string(raw), "1000", "0", 1),
		strings.TrimSuffix(string(raw), "}") + `,"total":0}`,
	} {
		if problems := doc.ValidateJSON(shape, []byte(bad)); len(problems) == 0 {
			t.Fatalf("invalid 202 accepted: %s", bad)
		}
	}
	registry, err := FoundationRegistry(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, definition := range registry.Definitions() {
		if definition.ID != "get_library_browse_selection_query" && definition.ID != "delete_library_browse_selection_query" {
			continue
		}
		seen[definition.ID] = true
		if definition.Path != "/v1/libraries/{id}/browse-selection-queries/{queryId}" {
			t.Fatal(definition.Path)
		}
		if definition.ID == "get_library_browse_selection_query" {
			retry := false
			for _, header := range definition.ResponseHeaders {
				if header.Status == 202 && header.Name == "Retry-After" && header.Required {
					retry = true
				}
			}
			if !retry {
				t.Fatal("pending retry floor is not in registry metadata")
			}
		}
	}
	if len(seen) != 2 {
		t.Fatal(seen)
	}
}

func TestBrowseSelectionQueryHTTPCapacityAndRevocation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	t.Run("capacity", func(t *testing.T) {
		f := newSelectionHTTPFixture(t, readprep.Config{Workers: 1, MaxBytes: 1 << 20})
		pending, _ := f.prepare(t, nil)
		path := browseSelectionQueryPath("movies", pending.QueryID)
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			w := f.request("GET", path, nil, nil)
			if w.Code == 429 {
				if w.Header().Get("Retry-After") != "1" || !strings.Contains(w.Body.String(), `"retry":"same_request"`) {
					t.Fatal(w.Header(), w.Body.String())
				}
				if f.store.Stats().Deliveries != 0 {
					t.Fatal("capacity delivery leaked")
				}
				return
			}
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("factory capacity did not produce a typed retryable response")
	})
	t.Run("revoked session", func(t *testing.T) {
		f := newSelectionHTTPFixture(t)
		pending, _ := f.prepare(t, nil)
		path := browseSelectionQueryPath("movies", pending.QueryID)
		_, _ = selectionHTTPReady(t, f, path)
		p, err := f.d.Identity.Authenticate(f.token)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.d.Identity.Logout(p); err != nil {
			t.Fatal(err)
		}
		w := f.request("GET", path, nil, nil)
		if w.Code != 401 && w.Code != 403 {
			t.Fatal("revoked session obtained selection", w.Code, w.Body.String())
		}
		if f.store.Stats().Deliveries != 0 {
			t.Fatal("revoked delivery leaked")
		}
	})
}

type selectionHTTPGate struct{ release <-chan struct{} }

func (*selectionHTTPGate) AccountedBytes() int64 { return 256 }
func (g *selectionHTTPGate) Step(ctx context.Context, b *readprep.Budget) (readprep.StepResult, error) {
	if err := b.Resize(512); err != nil {
		return readprep.StepResult{}, err
	}
	select {
	case <-g.release:
		return readprep.StepResult{Done: true, Payload: []byte(`null`)}, nil
	case <-ctx.Done():
		return readprep.StepResult{}, ctx.Err()
	}
}

func TestBrowseSelectionPublicRangeZeroPageStartsPreparation(t *testing.T) {
	f := newSelectionHTTPFixture(t)
	f.c.Movie(f.c.Handle("movies"), "/movies/second.mkv", "Second", 2001)
	f.c.Drain()
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	// Occupy the real shared worker. The ordinary public POST must still
	// return its page and a real query handle before aggregate work can start.
	if _, err := f.store.Submit(context.Background(), "test-gate", readprep.Key{Namespace: "test-gate", Basis: "test-gate", Canonical: "test-gate"}, readprep.Plan{InitialBytes: 256, New: func(ctx context.Context, b *readprep.Budget) (readprep.Computation, error) {
		if err := b.Resize(512); err != nil {
			return nil, err
		}
		return &selectionHTTPGate{release: release}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	f.d.PrepareBrowseSelections = true
	f.h = New(f.d)
	w := f.request("POST", "/v1/libraries/movies/browse", nil, map[string]any{"pivot": "movies", "limit": 1, "range": map[string]any{"start": 0}})
	if w.Code != 200 {
		t.Fatalf("range0 %d %s", w.Code, w.Body.String())
	}
	var page catalog.BrowseResult
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.PageInfo.Start != 0 || page.WholeSelection != nil || page.WholeSelectionPreparing == nil {
		t.Fatalf("range0 did not start: %s", w.Body.String())
	}
	query := page.WholeSelectionPreparing
	if query.Kind != "browse-selection-preparing" || query.QueryID == "" || query.RetryAfterMs != 1000 {
		t.Fatal(query)
	}
	poll := f.request("GET", browseSelectionQueryPath("movies", query.QueryID), nil, nil)
	if poll.Code != 202 {
		t.Fatalf("aggregate unexpectedly blocked/ran: %d %s", poll.Code, poll.Body.String())
	}
	close(release)
	released = true
	ready, _ := selectionHTTPReady(t, f, browseSelectionQueryPath("movies", query.QueryID))
	if ready.Total != 2 || ready.CatalogRevision != page.PageInfo.Revision {
		t.Fatalf("range0 prepared another page: %+v", ready)
	}
	bare := f.request("POST", "/v1/libraries/movies/browse", nil, map[string]any{"pivot": "movies", "limit": 1})
	var cursorPage catalog.BrowseResult
	if bare.Code != 200 {
		t.Fatal(bare.Code, bare.Body.String())
	}
	if err := json.Unmarshal(bare.Body.Bytes(), &cursorPage); err != nil {
		t.Fatal(err)
	}
	if cursorPage.PageInfo.NextCursor == "" {
		t.Fatal("fixture did not publish a real cursor")
	}
	for _, body := range []map[string]any{
		{"pivot": "movies", "limit": 1, "range": map[string]any{"start": 1, "revision": page.PageInfo.Revision}},
		{"pivot": "movies", "limit": 1, "cursor": cursorPage.PageInfo.NextCursor},
		{"pivot": "movies", "limit": 1, "seek": map[string]any{"prefix": "F"}},
	} {
		response := f.request("POST", "/v1/libraries/movies/browse", nil, body)
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), `"wholeSelectionPreparing"`) {
			t.Fatalf("noninitial window started preparation: %s", response.Body.String())
		}
	}
}

func TestBrowseSelectionHeldProofBindsPublicRangeZeroConversion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newSelectionHTTPFixture(t)
	// The read proof supports the existing explicit-ID command bound (200),
	// including views larger than the ordinary100-row page maximum.
	for i := 0; i < 100; i++ {
		f.c.Movie(f.c.Handle("movies"), fmt.Sprintf("/held/%d.mkv", i), fmt.Sprintf("Held %03d", i), 2020)
	}
	f.c.Drain()
	pending, revision := f.prepare(t, nil)
	descriptor, _ := selectionHTTPReady(t, f, browseSelectionQueryPath("movies", pending.QueryID))
	basis := descriptor.Selector.Query.SelectionBasis
	if len(basis) != 64 || descriptor.Total != 101 {
		t.Fatalf("prepared query omitted held basis: %+v", descriptor)
	}
	f.d.PrepareBrowseSelections = true
	f.h = New(f.d)
	body := map[string]any{"pivot": "movies", "limit": 200, "range": map[string]any{"start": 0, "revision": revision}, "selectionBasis": basis}
	w := f.request("POST", "/v1/libraries/movies/browse", nil, body)
	var page catalog.BrowseResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.PageInfo.Revision != revision || page.PageInfo.HasMore || len(page.Entries) != descriptor.Total || page.WholeSelectionPreparing != nil {
		t.Fatalf("proof conversion %d %s", w.Code, w.Body.String())
	}
	// Current exact predicate changes while P2-13's published page revision stays.
	f.c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('public-held-proof-drift',18)`)
	ordinary := f.request("POST", "/v1/libraries/movies/browse", nil, map[string]any{"pivot": "movies", "limit": 200, "range": map[string]any{"start": 0, "revision": revision}})
	if ordinary.Code != 200 || json.Unmarshal(ordinary.Body.Bytes(), &page) != nil || page.PageInfo.Revision != revision {
		t.Fatalf("ordinary cursor regression %d %s", ordinary.Code, ordinary.Body.String())
	}
	w = f.request("POST", "/v1/libraries/movies/browse", nil, body)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "stale_continuation") || strings.Contains(w.Body.String(), `"entries"`) {
		t.Fatalf("stale proof returned new IDs: %d %s", w.Code, w.Body.String())
	}
	for _, invalid := range []any{nil, "", strings.Repeat("A", 64), "abcd"} {
		body["selectionBasis"] = invalid
		w = f.request("POST", "/v1/libraries/movies/browse", nil, body)
		if w.Code != 400 {
			t.Fatalf("invalid proof %v: %d %s", invalid, w.Code, w.Body.String())
		}
	}
}

func TestBrowseSelectionLocalSearchPreparesExactSelection(t *testing.T) {
	f := newSelectionHTTPFixture(t)
	f.d.PrepareBrowseSelections = true
	f.h = New(f.d)
	for _, sort := range []string{"title", "forYou"} {
		body := map[string]any{"pivot": "movies", "limit": 1, "range": map[string]any{"start": 0}, "query": map[string]any{"field": "search", "operator": "contains", "value": "f"}, "sort": []map[string]string{{"field": sort, "direction": "desc"}}}
		w := f.request("POST", "/v1/libraries/movies/browse", nil, body)
		var page catalog.BrowseResult
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Entries) != 1 || page.WholeSelectionPreparing == nil || page.WholeSelection != nil || page.WholeSelectionUnavailable != nil {
			t.Fatalf("local search page %d %s", w.Code, w.Body.String())
		}
		ready, _ := selectionHTTPReady(t, f, browseSelectionQueryPath("movies", page.WholeSelectionPreparing.QueryID))
		if ready.Total != 1 || ready.CatalogRevision != page.PageInfo.Revision || len(ready.Selector.Query.SelectionBasis) != 64 {
			t.Fatalf("exact proof %+v", ready)
		}
		if sort == "forYou" {
			// cfb658c23 made bounded ForYou display conversion available to
			// Delete. It remains unsupported as a general Play/Queue iterator.
			excluded := ready.UnavailableActions
			if excluded == nil || excluded.Play != "unsupported-sort" || excluded.Queue != "unsupported-sort" || excluded.Delete != "" || excluded.Playlist != "" || excluded.MetadataPreview != "" {
				t.Fatalf("consumer exclusions %+v", excluded)
			}
		} else if ready.UnavailableActions != nil {
			t.Fatalf("ordinary sort excluded %+v", ready.UnavailableActions)
		}
	}
	if f.store.Stats().Deliveries != 0 {
		t.Fatal("delivery leak", f.store.Stats())
	}
}
