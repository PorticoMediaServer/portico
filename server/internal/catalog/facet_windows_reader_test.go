package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

func facetWindowFixture(t *testing.T, db *sql.DB, check FacetWindowCheck) (*FacetWindows, *readprep.Store) {
	t.Helper()
	store, err := readprep.New(readprep.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if check == nil {
		check = func(context.Context, readprep.Caller, FacetBaseSnapshot) error { return nil }
	}
	w, err := NewFacetWindows(db, store, check)
	if err != nil {
		t.Fatal(err)
	}
	return w, store
}
func facetWindowReady(t *testing.T, w *FacetWindows, caller readprep.Caller, out SimpleFacetResult, err error) SimpleFacetPage {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for out.Preparing != nil {
		if out.Page != nil {
			t.Fatal("pending carried ready payload")
		}
		if time.Now().After(deadline) {
			t.Fatal("preparation never completed")
		}
		time.Sleep(time.Millisecond)
		queryID := out.Preparing.QueryID
		out.Release()
		out, err = w.Poll(context.Background(), caller, queryID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if out.Page == nil {
		t.Fatal("missing page")
	}
	defer out.Release()
	return *out.Page
}
func facetWindowInitial(t *testing.T, w *FacetWindows, scope FacetBaseScope, q string, limit int) SimpleFacetPage {
	t.Helper()
	r, e := w.Initial(context.Background(), "caller", scope, q, limit)
	return facetWindowReady(t, w, "caller", r, e)
}
func facetWindowNext(t *testing.T, w *FacetWindows, token string) SimpleFacetPage {
	t.Helper()
	r, e := w.Continue(context.Background(), "caller", token)
	return facetWindowReady(t, w, "caller", r, e)
}
func facetWindowValues(p SimpleFacetPage) []string {
	v := []string{}
	for _, f := range p.Values {
		v = append(v, f.Value)
	}
	return v
}
func TestFacetWindowsExactTotalsAndFold(t *testing.T) {
	db, scope := facetBaseFixture(t)
	facetBaseBuild(t, db, scope, []FacetBaseFact{{"a", "Café", 9}, {"b", "Drama", 8}, {"c", "Noir", 7}, {"d", "日本語", 6}, {"e", "With\x00NUL", 5}})
	w, store := facetWindowFixture(t, db, nil)
	for _, tc := range []struct {
		q      string
		total  int64
		values []string
	}{
		{"a", 2, []string{"a", "b"}}, {"CAFÉ", 1, []string{"a"}}, {"日", 1, []string{"d"}}, {"z", 0, []string{}}, {"", 5, []string{"a", "b", "c", "d", "e"}}, {" \u0301 ", 5, []string{"a", "b", "c", "d", "e"}}, {"\x00", 1, []string{"e"}},
	} {
		p := facetWindowInitial(t, w, scope, tc.q, 10)
		if p.Total != tc.total || !reflect.DeepEqual(facetWindowValues(p), tc.values) {
			t.Fatalf("q=%q total=%d values=%q", tc.q, p.Total, facetWindowValues(p))
		}
	}
	if store.Stats().Deliveries != 0 {
		t.Fatal("unreleased Poll payload")
	}
}
func TestFacetWindowsBidirectionalAnchorsAndContentChurn(t *testing.T) {
	db, scope := facetBaseFixture(t)
	facts := []FacetBaseFact{{"Drama", "Drama", 5}, {"drama", "drama", 5}, {"x", "Éclair", 4}, {"y", "Zala", 3}, {"z", "Last", 2}}
	id := facetBaseBuild(t, db, scope, facts)
	allowed := true
	w, _ := facetWindowFixture(t, db, func(_ context.Context, _ readprep.Caller, s FacetBaseSnapshot) error {
		if !allowed || s.Authority != "authority:1" {
			return readprep.ErrStale
		}
		return nil
	})
	for _, q := range []string{"", "a"} {
		first := facetWindowInitial(t, w, scope, q, 2)
		if first.Total != 5 {
			t.Fatal(first.Total)
		}
		if got := facetWindowValues(first); !reflect.DeepEqual(got, []string{"Drama", "drama"}) {
			t.Fatal(got)
		}
		second := facetWindowNext(t, w, first.Window.Next)
		back := facetWindowNext(t, w, second.Window.Previous)
		if !reflect.DeepEqual(first.Values, back.Values) {
			t.Fatal("before order", first.Values, back.Values)
		}
		reload := facetWindowNext(t, w, second.Window.Reload)
		if !reflect.DeepEqual(second.Values, reload.Values) {
			t.Fatal("inclusive reload lost row")
		}
		if _, err := w.Continue(context.Background(), "other", first.Window.Next); !errors.Is(err, readprep.ErrStale) {
			t.Fatal("cross caller", err)
		}
		tampered := first.Window.Next[:len(first.Window.Next)-2] + "!!"
		if _, err := w.Continue(context.Background(), "caller", tampered); !errors.Is(err, readprep.ErrStale) {
			t.Fatal(err)
		}
	}
	first := facetWindowInitial(t, w, scope, "", 2)
	facetBaseBuild(t, db, scope, []FacetBaseFact{{"new", "New", 100}})
	second := facetWindowNext(t, w, first.Window.Next)
	if second.Revision != fmt.Sprint(id) || second.Total != 5 {
		t.Fatal("mixed content basis", second)
	}
	allowed = false
	if _, err := w.Poll(context.Background(), "caller", first.Window.QueryID); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("authority did not win", err)
	}
	allowed = true
	fresh, _ := facetWindowFixture(t, db, nil)
	if _, err := fresh.Poll(context.Background(), "caller", first.Window.QueryID); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("restart token survived", err)
	}
}
func TestFacetWindowsByteShortBeforeAdjacencyAndExceptionalScalar(t *testing.T) {
	db, scope := facetBaseFixture(t)
	facts := []FacetBaseFact{}
	for i := 0; i < 4; i++ {
		v := fmt.Sprint(i)
		facts = append(facts, FacetBaseFact{v, strings.Repeat(v, 24000), int64(10 - i)})
	}
	facetBaseBuild(t, db, scope, facts)
	w, store := facetWindowFixture(t, db, nil)
	first := facetWindowInitial(t, w, scope, "", 4)
	if len(first.Values) != 1 || first.Values[0].Value != "0" {
		t.Fatal(facetWindowValues(first))
	}
	second := facetWindowNext(t, w, first.Window.Next)
	third := facetWindowNext(t, w, second.Window.Next)
	fourth := facetWindowNext(t, w, third.Window.Next)
	back := facetWindowNext(t, w, fourth.Window.Previous)
	if got := facetWindowValues(back); !reflect.DeepEqual(got, []string{"2"}) {
		t.Fatal("before must remain adjacent", got)
	}
	if back.Window.Previous == "" || back.Window.Next == "" {
		t.Fatal("byte-short page lost edge")
	}
	// One huge legal scalar is uncached/transient, returned losslessly alone.
	huge := strings.Repeat("é", 300000)
	facetBaseBuild(t, db, scope, []FacetBaseFact{{"huge", huge, 1}})
	p := facetWindowInitial(t, w, scope, "é", 200)
	if len(p.Values) != 1 || p.Values[0].Label != huge || p.Total != 1 {
		t.Fatal("oversize scalar was dropped/truncated")
	}
	if store.Stats().Bytes > 200000 {
		t.Fatal("scalar leaked into memory cache", store.Stats())
	}
}
func TestFacetWindowsRejectsUnsafeWireCountAndEvictedBasis(t *testing.T) {
	db, scope := facetBaseFixture(t)
	id := facetBaseBuild(t, db, scope, []FacetBaseFact{{"v", "v", 9007199254740992}})
	w, _ := facetWindowFixture(t, db, nil)
	r, err := w.Initial(context.Background(), "caller", scope, "", 10)
	if err != nil && !errors.Is(err, ErrFacetBaseInput) {
		t.Fatal(err)
	}
	for r.Preparing != nil {
		time.Sleep(time.Millisecond)
		r, err = w.Poll(context.Background(), "caller", r.Preparing.QueryID)
		if err != nil {
			break
		}
	}
	if !errors.Is(err, ErrFacetBaseInput) {
		t.Fatal("unsafe JSON count accepted", err)
	}
	facetBaseBuild(t, db, scope, []FacetBaseFact{{"x", "x", 1}})
	b := &FacetBases{DB: db}
	facetBaseTx(t, db, func(tx *sql.Tx) error { _, err := b.RetireStep(context.Background(), tx, id); return err })
	if _, err = w.authorize(context.Background(), "caller", id); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("evicted basis usable", err)
	}
}
func TestFacetWindowsEmptySeekUsesMaintainedIndex(t *testing.T) {
	db, scope := facetBaseFixture(t)
	id := facetBaseBuild(t, db, scope, []FacetBaseFact{{"a", "A", 2}, {"b", "B", 1}})
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT b.value_id FROM catalog_facet_base_rows b INDEXED BY catalog_facet_base_order WHERE b.base_id=? AND `+facetWindowTuple+`>`+facetWindowAnchor+` ORDER BY `+facetWindowOrder+` LIMIT 201`, id, id, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var a, b, c int
		var detail string
		if err = rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "TEMP B-TREE") || strings.Contains(detail, "SCAN b") {
			t.Fatal("unbounded empty query plan", detail)
		}
		found = found || strings.Contains(detail, "catalog_facet_base_order")
	}
	if !found {
		t.Fatal("missing maintained order seek")
	}
}

func TestFacetWindowsFilteredScanCrossesByteQuanta(t *testing.T) {
	db, scope := facetBaseFixture(t)
	facts := make([]FacetBaseFact, 0, 17)
	for i := 0; i < 17; i++ {
		facts = append(facts, FacetBaseFact{fmt.Sprint(i), "x" + strings.Repeat("a", 140000), int64(30 - i)})
	}
	facetBaseBuild(t, db, scope, facts)
	w, store := facetWindowFixture(t, db, nil)
	p := facetWindowInitial(t, w, scope, "x", 200)
	if p.Total != 17 || len(p.Values) != 1 {
		t.Fatal("partial scan became exact success", p.Total, len(p.Values))
	}
	p = facetWindowNext(t, w, p.Window.Next)
	if p.Total != 17 || p.Values[0].Value != "1" {
		t.Fatal("filtered after scan", p.Total, facetWindowValues(p))
	}
	back := facetWindowNext(t, w, p.Window.Previous)
	if back.Total != 17 || back.Values[0].Value != "0" {
		t.Fatal("filtered before adjacency", back.Total, facetWindowValues(back))
	}
	if store.Stats().Bytes > 150000 {
		t.Fatal("text retained in compact reducer", store.Stats())
	}
}

func TestFacetWindowsContinuationKeepsActualOriginalLease(t *testing.T) {
	db, scope := facetBaseFixture(t)
	facetBaseBuild(t, db, scope, []FacetBaseFact{{"a", "a", 3}, {"b", "b", 2}, {"c", "c", 1}})
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(now.UnixNano())
	current := func() time.Time { return time.Unix(0, clock.Load()) }
	store, err := readprep.New(readprep.Config{Workers: 1, OriginalTTL: 2 * time.Minute, IdleTTL: time.Minute, CompleteTTL: 5 * time.Minute, Now: current})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	w, err := NewFacetWindows(db, store, func(context.Context, readprep.Caller, FacetBaseSnapshot) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	w.now = current
	first := facetWindowInitial(t, w, scope, "", 1)
	t1, err := w.decode(first.Window.Next)
	if err != nil {
		t.Fatal(err)
	}
	if t1.expires != now.Add(2*time.Minute).Unix() {
		t.Fatal("assumed default rather than actual lease", t1.expires)
	}
	clock.Store(now.Add(30 * time.Second).UnixNano())
	second := facetWindowNext(t, w, first.Window.Next)
	t2, err := w.decode(second.Window.QueryID)
	if err != nil {
		t.Fatal(err)
	}
	expiry, err := store.LeaseExpiry(context.Background(), "caller", t2.handle)
	if err != nil || t2.expires != t1.expires || expiry.Unix() != t1.expires {
		t.Fatal("continuation extended original lease", t1, t2, expiry, err)
	}
	clock.Store(now.Add(121 * time.Second).UnixNano())
	if _, err = w.Poll(context.Background(), "caller", second.Window.QueryID); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("old opening survived", err)
	}
}

func TestFacetWindowsDeliveryOwnsHydrationThroughRelease(t *testing.T) {
	db, scope := facetBaseFixture(t)
	facetBaseBuild(t, db, scope, []FacetBaseFact{{"value", strings.Repeat("x", 20000), 1}})
	w, store := facetWindowFixture(t, db, nil)
	out, err := w.Initial(context.Background(), "caller", scope, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	for out.Preparing != nil {
		q := out.Preparing.QueryID
		out.Release()
		time.Sleep(time.Millisecond)
		out, err = w.Poll(context.Background(), "caller", q)
		if err != nil {
			t.Fatal(err)
		}
	}
	before := store.Stats()
	if out.Page == nil || before.Deliveries != 1 || before.DeliveryBytes < 100000 {
		out.Release()
		t.Fatal("hydration escaped in-flight budget", before)
	}
	// Serialize while delivery stays charged, as the HTTP composition root does.
	if _, err = json.Marshal(out.Page); err != nil {
		out.Release()
		t.Fatal(err)
	}
	out.Release()
	out.Release()
	after := store.Stats()
	if after.Deliveries != 0 || after.DeliveryBytes != 0 || after.Bytes >= before.Bytes {
		t.Fatal("delivery reservation leaked", before, after)
	}
}
func TestFacetWindowsOversizedScalarUsesExplicitUncachedDelivery(t *testing.T) {
	db, scope := facetBaseFixture(t)
	huge := strings.Repeat("x", 300000)
	facetBaseBuild(t, db, scope, []FacetBaseFact{{"large", huge, 1}})
	store, err := readprep.New(readprep.Config{Workers: 1, MaxBytes: 128 << 10})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	w, err := NewFacetWindows(db, store, func(context.Context, readprep.Caller, FacetBaseSnapshot) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	p := facetWindowInitial(t, w, scope, "x", 10)
	if p.Total != 1 || p.Values[0].Label != huge {
		t.Fatal("legal scalar refused or truncated")
	}
	if store.Stats().Bytes > 128<<10 {
		t.Fatal("exceptional scalar cached", store.Stats())
	}
}
