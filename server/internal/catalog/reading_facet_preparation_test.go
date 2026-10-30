package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

func facetPreparationReady(t *testing.T, store *readprep.Store, caller readprep.Caller, h readprep.Handle) readprep.Result {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := store.Poll(context.Background(), caller, h)
		if err != nil {
			t.Fatal(err)
		}
		if r.State == readprep.Ready {
			return r
		}
		r.Release()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("facet preparation did not finish")
	return readprep.Result{}
}

func TestReadingFacetPreparationKeyStoreAndAccounting(t *testing.T) {
	c, s, viewer, _ := readingBrowseFixture(t, "book")
	c.Drain()
	store, err := readprep.New(readprep.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.UseReadPreparations(store)
	var authorizations atomic.Int32
	access := ReadingFacetAccess{RetainedBytes: 4096, Check: func(context.Context) (Viewer, error) { authorizations.Add(1); return viewer, nil }}
	instant := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	request := FacetRequest{Library: "reading", Field: "author", Limit: 1, Q: "A"}
	var key, same readprep.Key
	var plan readprep.Plan
	err = dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		ss := s.WithContext(ctx)
		var err error
		key, plan, err = ss.PlanReadingFacets(viewer, request, instant, access)
		if err != nil {
			return err
		}
		request.Q, request.Sort = "a", "title"
		same, _, err = ss.PlanReadingFacets(viewer, request, instant.Add(time.Hour), access)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if key != same || key.Basis == "" {
		t.Fatalf("equivalent canonical requests differ: %+v %+v", key, same)
	}
	h, err := store.Submit(context.Background(), "caller", key, plan)
	if err != nil {
		t.Fatal(err)
	}
	r := facetPreparationReady(t, store, "caller", h)
	var page FacetPage
	if err = json.Unmarshal(r.Payload, &page); err != nil {
		t.Fatal(err)
	}
	if page.Revision == "" || page.Values == nil {
		t.Fatalf("not exact FacetPage: %+v", page)
	}
	if got := store.Stats(); got.Deliveries != 1 || got.DeliveryBytes < int64(len(r.Payload)) || got.Bytes > store.MaxBytes() {
		t.Fatalf("uncharged output: %+v", got)
	}
	r.Release()
	before := authorizations.Load()
	for i := 0; i < 3; i++ {
		r = facetPreparationReady(t, store, "caller", h)
		r.Release()
	}
	if authorizations.Load() != before {
		t.Fatal("ready poll recomputed the reduction")
	}
	if store.Stats().Deliveries != 0 {
		t.Fatal("delivery leaked")
	}
}

func TestReadingFacetPreparationRelativeInstantAndQuantumDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c, s, viewer, _ := readingBrowseFixture(t, "book")
	c.Drain()
	store, err := readprep.New(readprep.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.UseReadPreparations(store)
	instant := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	var checks atomic.Int32
	access := ReadingFacetAccess{Check: func(context.Context) (Viewer, error) { checks.Add(1); return viewer, nil }}
	request := FacetRequest{Library: "reading", Field: "author", Limit: 1, Query: &BrowseNode{Field: "dateAdded", Operator: "within-last-days", Value: float64(1)}}
	// Reuse the oracle's parser-supported relative node rather than silently
	// changing validation; this checks both canonical instant and revision fence.
	request.Query, err = ParseBrowseQuery(json.RawMessage(`{"variant":"relative","field":"dateAdded","operator":"within-last-days","value":1}`), "query")
	if err != nil {
		t.Fatal(err)
	}
	var one, two readprep.Key
	var plan readprep.Plan
	err = dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		ss := s.WithContext(ctx)
		var err error
		one, plan, err = ss.PlanReadingFacets(viewer, request, instant, access)
		if err != nil {
			return err
		}
		two, _, err = ss.PlanReadingFacets(viewer, request, instant.Add(time.Hour), access)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if one.Canonical == two.Canonical {
		t.Fatal("relative instant omitted from key")
	}
	// Real synchronous mutation after admission and before the first Step.
	c.Exec(`UPDATE catalog_browse_predicate_epoch SET revision=revision+1 WHERE singleton=1`)
	h, err := store.Submit(context.Background(), "caller", one, plan)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := store.Poll(context.Background(), "caller", h)
		r.Release()
		if errors.Is(err, readprep.ErrStale) {
			if checks.Load() == 0 {
				t.Fatal("no Step authority check")
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if r.State == readprep.Ready {
			t.Fatal("drift became ready")
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("drift did not retire preparation")
}

func TestReadingFacetPreparationFactoryReservesBeforeSourceConstruction(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c, s, viewer, _ := readingBrowseFixture(t, "book")
	c.Drain()
	store, err := readprep.New(readprep.Config{Workers: 1, MaxBytes: 64 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.UseReadPreparations(store)
	var accessCalls atomic.Int32
	access := ReadingFacetAccess{Check: func(context.Context) (Viewer, error) { accessCalls.Add(1); return viewer, nil }}
	var key readprep.Key
	var plan readprep.Plan
	err = dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		var err error
		key, plan, err = s.WithContext(ctx).PlanReadingFacets(viewer, FacetRequest{Library: "reading", Field: "author", Limit: 1}, time.Now(), access)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	h, err := store.Submit(context.Background(), "caller", key, plan)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := store.Poll(context.Background(), "caller", h)
		r.Release()
		if errors.Is(err, readprep.ErrCapacity) {
			if accessCalls.Load() != 0 {
				t.Fatal("source constructor began before peak admission")
			}
			if stats := store.Stats(); stats.Bytes > store.MaxBytes() || stats.Deliveries != 0 {
				t.Fatalf("capacity leak: %+v", stats)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("factory did not refuse insufficient reservation")
}
