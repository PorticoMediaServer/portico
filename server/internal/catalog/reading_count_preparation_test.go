package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
	"strings"
	"sync"
	"testing"
	"time"
)

func countStoreForTest(t *testing.T, cfg readprep.Config) *readprep.Store {
	t.Helper()
	s, e := readprep.New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func countPollForTest(t *testing.T, s *readprep.Store, caller readprep.Caller, h readprep.Handle) (readprep.Result, error) {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		r, e := s.Poll(context.Background(), caller, h)
		if e != nil {
			return r, e
		}
		if r.State == readprep.Ready {
			return r, nil
		}
		r.Release()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("preparation did not finish")
	return readprep.Result{}, nil
}

func TestReadingCountPreparationDetachesRequestSnapshotAndMapsDrift(t *testing.T) {
	for _, drift := range []bool{false, true} {
		c, s, v, _ := readingBrowseFixture(t, "book")
		c.Drain()
		store := countStoreForTest(t, readprep.DefaultConfig())
		s.UseReadPreparations(store)
		gate := make(chan struct{})
		entered := make(chan struct{})
		var release sync.Once
		t.Cleanup(func() { release.Do(func() { close(gate) }) })
		cfg := readprep.DefaultConfig()
		cfg.Workers = 1
		// Hold the sole worker until the submitting snapshot is closed.
		store.Close()
		store = countStoreForTest(t, cfg)
		s.UseReadPreparations(store)
		_, e := store.Submit(context.Background(), "blocker", readprep.Key{Namespace: "blocker", Basis: "b", Canonical: "c"}, readprep.Plan{InitialBytes: 64, New: func(context.Context, *readprep.Budget) (readprep.Computation, error) {
			return &countObservedWork{bytes: 64, step: func(ctx context.Context, _ *readprep.Budget) (readprep.StepResult, error) {
				close(entered)
				select {
				case <-gate:
				case <-ctx.Done():
				}
				return readprep.StepResult{}, readprep.ErrStale
			}}, nil
		}})
		if e != nil {
			t.Fatal(e)
		}
		<-entered
		requestCtx, cancel := context.WithCancel(context.Background())
		var handle readprep.Handle
		err := dbwork.WithReadSnapshot(requestCtx, c.DB, func(ctx context.Context) error {
			bound := s.WithContext(ctx)
			bound.readTx = dbwork.Snapshot(ctx)
			var e error
			handle, e = bound.SubmitReadingBrowseCount("reading/library/session", v, BrowseRequest{Library: "reading", Limit: 1}, time.Time{})
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
		<-entered
		cancel()
		if drift {
			c.Exec(`INSERT INTO content_rating_ages VALUES('adapter-drift',0)`)
		}
		release.Do(func() { close(gate) })
		result, err := countPollForTest(t, store, "reading/library/session", handle)
		defer result.Release()
		if drift {
			if !errors.Is(err, readprep.ErrStale) {
				t.Fatal("drift collapsed into ErrFailed", err)
			}
			continue
		}
		if err != nil {
			t.Fatal("request state retained", err)
		}
		var payload ReadingBrowseCountPayload
		if err = json.Unmarshal(result.Payload, &payload); err != nil || payload.Count.RawTotal != 4 || payload.Request.Library != "reading" {
			t.Fatal(payload, err)
		}
	}
	for _, e := range []error{sql.ErrNoRows, ErrReadingProjectionChanged, ErrStaleContinuation, ErrVisibilityBuilding} {
		if !errors.Is(readingPreparationError(e), readprep.ErrStale) {
			t.Fatal(e)
		}
	}
	if !errors.Is(readingPreparationError(ErrReadingCountCapacity), readprep.ErrCapacity) {
		t.Fatal("capacity became failure")
	}
}

type countObservedWork struct {
	work  readprep.Computation
	after func(error)
	step  func(context.Context, *readprep.Budget) (readprep.StepResult, error)
	bytes int64
}

func (w *countObservedWork) AccountedBytes() int64 {
	if w.work != nil {
		return w.work.AccountedBytes()
	}
	return w.bytes
}
func (w *countObservedWork) Step(ctx context.Context, b *readprep.Budget) (readprep.StepResult, error) {
	if w.work == nil {
		return w.step(ctx, b)
	}
	_, err := w.work.Step(ctx, b)
	w.after(err)
	if err == nil {
		err = readprep.ErrStale
	}
	return readprep.StepResult{}, err
}

func TestReadingCountAdapterAbsoluteScalarPeakVersusContention(t *testing.T) {
	c, s, v, items := readingBrowseFixture(t, "book")
	c.Drain()
	for _, absolute := range []bool{true, false} {
		size := 512 << 10
		if !absolute {
			size = 128 << 10
		}
		c.Exec(`UPDATE catalog_browse_rows SET title=? WHERE entity_id=?`, strings.Repeat("x", size), items[0].ID)
		r, err := s.NewReadingCountReduction(v, BrowseRequest{Library: "reading", Query: &BrowseNode{Field: "title", Operator: "contains", Value: "x"}})
		if err != nil {
			t.Fatal(err)
		}
		r.entity = items[0].ID
		r.afterEntity = r.entity
		r.phase = countQuery
		r.pendingBytes = int64(size)
		cfg := readprep.DefaultConfig()
		cfg.MaxBytes = 8 << 20
		cfg.Workers = 2
		store := countStoreForTest(t, cfg)
		gate := make(chan struct{})
		var release sync.Once
		t.Cleanup(func() { release.Do(func() { close(gate) }) })
		if !absolute {
			entered := make(chan struct{})
			_, err = store.Submit(context.Background(), "occupied", readprep.Key{Namespace: "occupied", Basis: "b", Canonical: "c"}, readprep.Plan{InitialBytes: 2500 << 10, New: func(context.Context, *readprep.Budget) (readprep.Computation, error) {
				return &countObservedWork{bytes: 2500 << 10, step: func(ctx context.Context, _ *readprep.Budget) (readprep.StepResult, error) {
					close(entered)
					select {
					case <-gate:
					case <-ctx.Done():
					}
					return readprep.StepResult{}, readprep.ErrStale
				}}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			<-entered
		}
		w := &readingCountComputation{service: s, seed: ReadingBrowseCountRequest{Library: "reading"}, reduction: r, viewer: v, factoryBytes: 1024, storeMaximum: store.MaxBytes()}
		observed := make(chan error, 1)
		_, err = store.Submit(context.Background(), "observed", readprep.Key{Namespace: "count-test", Basis: r.Witness(), Canonical: "request"}, readprep.Plan{InitialBytes: w.AccountedBytes(), New: func(context.Context, *readprep.Budget) (readprep.Computation, error) {
			return &countObservedWork{work: w, after: func(err error) { observed <- err }}, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case err = <-observed:
		case <-time.After(3 * time.Second):
			t.Fatal("worker did not return")
		}
		if absolute {
			if err != nil || r.phase != countPublication || r.count != 0 {
				t.Fatal("exception ran a second raw unit or failed", err, r.phase, r.count)
			}
		} else if !errors.Is(err, readprep.ErrCapacity) || r.phase != countQuery {
			t.Fatal("ordinary contention bypassed reservation", err, r.phase)
		}
		release.Do(func() { close(gate) })
	}
}

func TestReadingPreparedDeliveryReservesBeforeHydration(t *testing.T) {
	c, s, v, _ := readingBrowseFixture(t, "book")
	c.Drain()
	request := BrowseRequest{Library: "reading", Limit: 1}
	r, err := s.NewReadingCountReduction(v, request)
	if err != nil {
		t.Fatal(err)
	}
	finishReadingCount(t, s, v, r, defaultReadingCountBudget)
	count, _ := r.Result()
	refused := errors.New("delivery refused")
	calls := 0
	_, err = s.BrowseReadingWithPreparedCount(v, request, count, func(peak int64, oversized bool) error {
		calls++
		if peak <= 0 {
			t.Fatal("unmeasured hydration")
		}
		return refused
	})
	if !errors.Is(err, refused) || calls != 1 {
		t.Fatal("hydration ignored admission", err, calls)
	}
}

func TestReadingCountPolicyUsesRetainedCapacityNotRequestLimit(t *testing.T) {
	c, s, v, _ := readingBrowseFixture(t, "book")
	c.Drain()
	v.Restrictions.BlockedLabels = []string{strings.Repeat("x", 70000)}
	if _, err := s.NewReadingCountReduction(v, BrowseRequest{Library: "reading"}); err != nil {
		t.Fatal("server policy inherited a request metadata cap", err)
	}
	v.Restrictions.BlockedLabels = []string{strings.Repeat("x", ReadingCountMaximumBytes)}
	if _, err := s.NewReadingCountReduction(v, BrowseRequest{Library: "reading"}); !errors.Is(err, ErrReadingCountCapacity) {
		t.Fatal("large retained state was not capacity", err)
	}
}

func TestReadingCountContentSharesAcrossCallersAndCurrentFences(t *testing.T) {
	c, s, v, _ := readingBrowseFixture(t, "book")
	c.Drain()
	store := countStoreForTest(t, readprep.DefaultConfig())
	s.UseReadPreparations(store)
	one, two := v, v
	one.Fence = "private-grant-one"
	two.Fence = "private-grant-two"
	first, err := s.BrowseEntities(one, BrowseRequest{Library: "reading", Limit: 1, Range: &BrowseRange{Start: 0}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.BrowseEntities(two, BrowseRequest{Library: "reading", Limit: 1, Range: &BrowseRange{Start: 0}})
	if err != nil {
		t.Fatal(err)
	}
	if first.PageInfo.Revision == second.PageInfo.Revision {
		t.Fatal("test did not vary current authority")
	}
	requestOne := BrowseRequest{Library: "reading", Limit: 1, Range: &BrowseRange{Start: 1, Revision: first.PageInfo.Revision}}
	requestTwo := BrowseRequest{Library: "reading", Limit: 1, Range: &BrowseRange{Start: 1, Revision: second.PageInfo.Revision}}
	h1, err := s.SubmitReadingBrowseCount("caller-one", one, requestOne, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	h2, err := s.SubmitReadingBrowseCount("caller-two", two, requestTwo, time.Now().Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 || store.Stats().Entries != 1 {
		t.Fatal("equivalent content did not deduplicate", store.Stats())
	}
	r1, err := countPollForTest(t, store, "caller-one", h1)
	if err != nil {
		t.Fatal(err)
	}
	defer r1.Release()
	r2, err := countPollForTest(t, store, "caller-two", h2)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Release()
	if string(r1.Payload) != string(r2.Payload) || !r1.OriginalExpiry.Before(r2.OriginalExpiry) {
		t.Fatal("shared facts or caller expiries mixed")
	}
	for _, private := range []string{one.Fence, two.Fence, "caller-one", "caller-two", first.PageInfo.Revision, second.PageInfo.Revision} {
		if strings.Contains(string(r1.Payload), private) {
			t.Fatal("private authority retained in shared facts")
		}
	}
	var payload ReadingBrowseCountPayload
	if err = json.Unmarshal(r2.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	page, err := s.BrowsePreparedReadingCountPayload(two, payload, r2.OriginalExpiry, "server", nil)
	if err != nil || page.PageInfo.Revision != second.PageInfo.Revision || page.PageInfo.Start != 1 || page.PageInfo.Total != 4 {
		t.Fatal(page.PageInfo, err)
	}
	if err = store.Cancel(context.Background(), "caller-one", h1); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Poll(context.Background(), "caller-one", h1); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("caller cancellation ignored")
	}
	still, err := store.Poll(context.Background(), "caller-two", h2)
	defer still.Release()
	if err != nil || still.State != readprep.Ready {
		t.Fatal("one caller revoked another", err)
	}
}

func TestReadingCountContentRebindsValidatedContinuationForCurrentViewer(t *testing.T) {
	c, s, v, _ := readingBrowseFixture(t, "book")
	c.Drain()
	store := countStoreForTest(t, readprep.DefaultConfig())
	s.UseReadPreparations(store)
	first, second := v, v
	first.Fence = "cursor-owner-one"
	second.Fence = "cursor-owner-two"
	makeRequest := func(viewer Viewer) BrowseRequest {
		page, e := s.BrowseEntities(viewer, BrowseRequest{Library: "reading", Limit: 1})
		if e != nil {
			t.Fatal(e)
		}
		cursor, e := s.readSignedBrowseCursor(page.PageInfo.NextCursor)
		if e != nil {
			t.Fatal(e)
		}
		cursor.Total = 0
		cursor.TotalPresent = false
		raw, e := s.encodeCursor(cursor)
		if e != nil {
			t.Fatal(e)
		}
		return BrowseRequest{Library: "reading", Limit: 1, Cursor: raw}
	}
	one, two := makeRequest(first), makeRequest(second)
	h1, e := s.SubmitReadingBrowseCount("cursor-caller-one", first, one, time.Time{})
	if e != nil {
		t.Fatal(e)
	}
	h2, e := s.SubmitReadingBrowseCount("cursor-caller-two", second, two, time.Time{})
	if e != nil {
		t.Fatal(e)
	}
	if store.Stats().Entries != 1 {
		t.Fatal("private cursor fields entered content identity", store.Stats())
	}
	result, e := countPollForTest(t, store, "cursor-caller-two", h2)
	if e != nil {
		t.Fatal(e)
	}
	defer result.Release()
	if strings.Contains(string(result.Payload), one.Cursor) || strings.Contains(string(result.Payload), two.Cursor) || strings.Contains(string(result.Payload), first.Fence) {
		t.Fatal("signed authority cursor cached")
	}
	var payload ReadingBrowseCountPayload
	if e = json.Unmarshal(result.Payload, &payload); e != nil {
		t.Fatal(e)
	}
	page, e := s.BrowsePreparedReadingCountPayload(second, payload, result.OriginalExpiry, "server", nil)
	if e != nil || page.PageInfo.Start != 1 || page.PageInfo.Total != 4 || len(page.Entries) != 1 {
		t.Fatal(page.PageInfo, e)
	}
	if page.PageInfo.NextCursor == "" {
		t.Fatal("continued page lost next cursor")
	}
	signed, e := s.readSignedBrowseCursor(page.PageInfo.NextCursor)
	if e != nil || signed.Scope.Viewer != second.Fence || signed.Expires > result.OriginalExpiry.Unix() {
		t.Fatal("cursor not bound to current authority/original expiry", signed, e)
	}
	if e = store.Cancel(context.Background(), "cursor-caller-one", h1); e != nil {
		t.Fatal(e)
	}
}

func TestReadingCountLegacyContinuationKeepsRelativeInstant(t *testing.T) {
	c, s, v, _ := readingBrowseFixture(t, "book")
	c.Drain()
	c.Exec(`UPDATE catalog_browse_rows SET added_text='2026-10-19T00:00:00Z'`)
	fixed := *s
	fixed.browseInstant = time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	query := &BrowseNode{Field: "dateAdded", Operator: "within-last-days", Value: 2}
	page, e := fixed.BrowseEntities(v, BrowseRequest{Library: "reading", Limit: 1, Query: query})
	if e != nil || page.PageInfo.NextCursor == "" {
		t.Fatal(page, e)
	}
	cursor, e := s.readSignedBrowseCursor(page.PageInfo.NextCursor)
	if e != nil {
		t.Fatal(e)
	}
	cursor.Total = 0
	cursor.TotalPresent = false
	raw, e := s.encodeCursor(cursor)
	if e != nil {
		t.Fatal(e)
	}
	request := BrowseRequest{Library: "reading", Limit: 1, Query: query, Cursor: raw}
	if _, present, e := s.BrowseReadingCarriedTotal(v, request); e != nil || present {
		t.Fatal("relative legacy proof used a fresh instant", present, e)
	}
	r, e := s.NewReadingCountReduction(v, request)
	if e != nil {
		t.Fatal(e)
	}
	q, _, _ := finishReadingCount(t, s, v, r, defaultReadingCountBudget)
	if q.Total != 4 {
		t.Fatal("relative count moved its clock", q)
	}
}
