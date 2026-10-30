package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The real store worker retains a reducer pointer solely for test assertions:
// output parity alone would not prove that candidate reuse actually happened.
func facetPrefixScan(t *testing.T, w *FacetWindows, caller readprep.Caller, basis FacetBaseSnapshot, spec facetWindowSpec, until time.Time) (*facetWindowScan, facetWindowSummary, readprep.Handle) {
	t.Helper()
	ctx := context.Background()
	var scan atomic.Pointer[facetWindowScan]
	var expiry atomic.Int64
	key := facetWindowKey(basis, spec)
	initial := int64(8192 + len(key.Canonical) + len(spec.Needle) + len(caller))
	h, err := w.store.SubmitUntil(ctx, caller, key, readprep.Plan{InitialBytes: initial, New: func(context.Context, *readprep.Budget) (readprep.Computation, error) {
		s := newFacetWindowScan(w, caller, basis, spec, initial, &expiry)
		scan.Store(s)
		return s, nil
	}}, until)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.store.Cancel(ctx, caller, h) })
	expires, err := w.store.LeaseExpiry(ctx, caller, h)
	if err != nil {
		t.Fatal(err)
	}
	expiry.Store(expires.UnixNano())
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		r, err := w.store.Poll(ctx, caller, h)
		if err != nil {
			t.Fatal(err)
		}
		if r.State == readprep.Ready {
			var summary facetWindowSummary
			err = json.Unmarshal(r.Payload, &summary)
			r.Release()
			if err != nil {
				t.Fatal(err)
			}
			if scan.Load() == nil {
				t.Fatal("test expected a fresh reducer key")
			}
			return scan.Load(), summary, h
		}
		r.Release()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("prefix preparation did not complete")
	return nil, facetWindowSummary{}, ""
}

func TestFacetWindowsPrefixCompleteSetBeyondTopPageAndAcrossCallers(t *testing.T) {
	db, scope := facetBaseFixture(t)
	id := facetBaseBuild(t, db, scope, []FacetBaseFact{{"1", "Alpha", 9}, {"2", "Amber", 8}, {"3", "Azalea", 7}, {"4", "Azure", 6}, {"5", "No match", 5}})
	w, store := facetWindowFixture(t, db, nil)
	basis, err := w.authorize(context.Background(), "first", id)
	if err != nil {
		t.Fatal(err)
	}
	_, parent, h := facetPrefixScan(t, w, "first", basis, facetWindowSpec{Needle: "a", Limit: 1}, time.Time{})
	if parent.Total != 5 || len(parent.IDs) != 1 || !parent.CompleteMatchSet || len(parent.CompleteMatchIDs) != 5 {
		t.Fatal("top page confused with complete matches", parent)
	}
	if err := store.Cancel(context.Background(), "first", h); err != nil {
		t.Fatal(err)
	}
	before := store.Stats()
	until := time.Now().Add(15 * time.Second)
	scan, child, handle := facetPrefixScan(t, w, "second", basis, facetWindowSpec{Needle: "az", Limit: 1}, until)
	if !scan.prefix.seeded || scan.prefix.position != 5 || scan.cursor != 0 || child.Total != 2 || !reflect.DeepEqual(child.IDs, parent.CompleteMatchIDs[2:3]) {
		t.Fatal("did not filter complete candidates beyond top page", scan.prefix.seeded, scan.cursor, child)
	}
	if expiry, _ := store.LeaseExpiry(context.Background(), "second", handle); expiry.After(until) {
		t.Fatal("original expiry extended", expiry, until)
	}
	if after := store.Stats(); after.Handles != before.Handles+1 || after.Deliveries != 0 || after.Bytes > store.MaxBytes() {
		t.Fatal("seed handle/copy escaped quantum or cap", before, after)
	}
	page := facetWindowInitial(t, w, scope, "AZ", 2)
	if page.Total != 2 || !reflect.DeepEqual(facetWindowValues(page), []string{"3", "4"}) {
		t.Fatal(page)
	}
}

func TestFacetWindowsPrefixWorkingCapacityFallsBackToRaw(t *testing.T) {
	db, scope := facetBaseFixture(t)
	id := facetBaseBuild(t, db, scope, []FacetBaseFact{{"one", "match", 1}})
	store, err := readprep.New(readprep.Config{MaxBytes: 1 << 20, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	w, err := NewFacetWindows(db, store, func(context.Context, readprep.Caller, FacetBaseSnapshot) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	page := facetWindowInitial(t, w, scope, "", 1)
	token, err := w.decode(page.Window.QueryID)
	if err != nil {
		t.Fatal(err)
	}
	held, err := store.Poll(context.Background(), "caller", token.handle)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	if err = held.ResizeDelivery(780 << 10); err != nil {
		t.Fatal(err)
	}
	basis, err := w.authorize(context.Background(), "caller", id)
	if err != nil {
		t.Fatal(err)
	}
	scan, summary, _ := facetPrefixScan(t, w, "caller", basis, facetWindowSpec{Needle: "ma", Limit: 1}, time.Time{})
	if scan.prefix.store != nil || summary.CompleteMatchSet || summary.Total != 1 || !reflect.DeepEqual(summary.IDs, []int64{1}) {
		t.Fatal("optional seed reservation refused or changed raw answer", summary)
	}
	if store.Stats().Bytes > store.MaxBytes() {
		t.Fatal("capacity bypass", store.Stats())
	}
}

func TestFacetWindowsPrefixBoundariesAndOverflowFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, n := range []int{4096, 4097} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			db, scope := facetBaseFixture(t)
			// Exercise the real decoder peak with 4096 nineteen-digit IDs,
			// rather than accidentally relying on tiny fixture identities.
			if _, err := db.Exec(`INSERT INTO sqlite_sequence(name,seq) VALUES('catalog_facet_base_values',9000000000000000000)`); err != nil {
				t.Fatal(err)
			}
			facts := make([]FacetBaseFact, n)
			for i := range facts {
				label := fmt.Sprintf("alpha %04d", i)
				if i%2 == 0 {
					label = fmt.Sprintf("azalea %04d", i)
				}
				facts[i] = FacetBaseFact{fmt.Sprint(i), label, int64(n - i)}
			}
			id := facetBaseBuild(t, db, scope, facts)
			w, store := facetWindowFixture(t, db, nil)
			basis, _ := w.authorize(context.Background(), "caller", id)
			_, parent, _ := facetPrefixScan(t, w, "caller", basis, facetWindowSpec{Needle: "a", Limit: 2}, time.Time{})
			wantSeed := n <= facetWindowPrefixLimit
			if parent.Total != int64(n) || parent.CompleteMatchSet != wantSeed || len(parent.CompleteMatchIDs) > facetWindowPrefixLimit || (!wantSeed && len(parent.CompleteMatchIDs) != 0) {
				t.Fatal("complete set boundary", parent.Total, parent.CompleteMatchSet, len(parent.CompleteMatchIDs))
			}
			scan, child, _ := facetPrefixScan(t, w, "caller", basis, facetWindowSpec{Needle: "az", Limit: 2}, time.Time{})
			if scan.prefix.seeded != wantSeed || child.Total != int64((n+1)/2) || len(child.IDs) != 2 || !child.Later {
				t.Fatal("seed/fallback count/page", scan.prefix.seeded, child)
			}
			// Initial/Poll and Continue must reserve the larger decode peak,
			// then discard seed IDs before resizing the hydration reservation.
			if wantSeed {
				page := facetWindowInitial(t, w, scope, "a", 2)
				next := facetWindowNext(t, w, page.Window.Next)
				if page.Total != int64(n) || next.Total != page.Total || len(next.Values) != 2 {
					t.Fatal("nineteen-digit seed decoding changed total/page", page.Total, next.Total)
				}
			}
			if store.Stats().Deliveries != 0 || store.Stats().Bytes > store.MaxBytes() {
				t.Fatal(store.Stats())
			}
		})
	}
}

func TestFacetWindowsPrefixScopeAndLiteralFoldCompatibility(t *testing.T) {
	db, scope := facetBaseFixture(t)
	id := facetBaseBuild(t, db, scope, []FacetBaseFact{{"one", "az", 2}, {"two", "a\x00z", 1}})
	w, _ := facetWindowFixture(t, db, nil)
	basis, _ := w.authorize(context.Background(), "caller", id)
	facetPrefixScan(t, w, "caller", basis, facetWindowSpec{Needle: "a", Limit: 1}, time.Time{})
	variants := []struct {
		name string
		edit func(*FacetBaseSnapshot, *facetWindowSpec)
	}{
		{"authority", func(b *FacetBaseSnapshot, _ *facetWindowSpec) { b.Authority += "other" }},
		{"witness", func(b *FacetBaseSnapshot, _ *facetWindowSpec) { b.Witness += "other" }},
		{"policy", func(b *FacetBaseSnapshot, _ *facetWindowSpec) { b.Scope.Policy += "other" }},
		{"source", func(b *FacetBaseSnapshot, _ *facetWindowSpec) { b.Scope.Source += "other" }},
		{"library", func(b *FacetBaseSnapshot, _ *facetWindowSpec) { b.Scope.LibraryID++ }},
		{"field", func(b *FacetBaseSnapshot, _ *facetWindowSpec) { b.Scope.Field = "tag" }},
		{"limit", func(_ *FacetBaseSnapshot, s *facetWindowSpec) { s.Limit = 2 }},
		{"direction", func(_ *FacetBaseSnapshot, s *facetWindowSpec) { s.Direction = 3 }},
		{"not-literal-prefix", func(_ *FacetBaseSnapshot, s *facetWindowSpec) { s.Needle = "z" }},
	}
	for _, tc := range variants {
		t.Run(tc.name, func(t *testing.T) {
			b, spec := basis, facetWindowSpec{Needle: "az", Limit: 1}
			tc.edit(&b, &spec)
			scan, _, _ := facetPrefixScan(t, w, "caller", b, spec, time.Time{})
			if scan.prefix.seeded {
				t.Fatal("incompatible prefix reused")
			}
		})
	}
	scan, summary, _ := facetPrefixScan(t, w, "caller", basis, facetWindowSpec{Needle: "a\x00", Limit: 1}, time.Time{})
	if !scan.prefix.seeded || summary.Total != 1 {
		t.Fatal("literal NUL seed", scan.prefix.seeded, summary)
	}
}

func TestFacetWindowsPrefixRejectsTopPageOnlyAndUsesBoundedIDPlan(t *testing.T) {
	db, scope := facetBaseFixture(t)
	id := facetBaseBuild(t, db, scope, []FacetBaseFact{{"a", "Alpha", 2}, {"z", "Azalea", 1}})
	w, store := facetWindowFixture(t, db, nil)
	basis, _ := w.authorize(context.Background(), "caller", id)
	spec := facetWindowSpec{Needle: "a", Limit: 1}
	_, summary, h := facetPrefixScan(t, w, "caller", basis, spec, time.Time{})
	store.Cancel(context.Background(), "caller", h)
	store.Invalidate(facetWindowKey(basis, spec))
	summary.CompleteMatchSet, summary.CompleteMatchIDs = false, nil
	payload, _ := json.Marshal(summary)
	h, err := store.Submit(context.Background(), "caller", facetWindowKey(basis, spec), readprep.Plan{InitialBytes: 8192, New: func(context.Context, *readprep.Budget) (readprep.Computation, error) {
		return &facetPrefixPayload{payload}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Cancel(context.Background(), "caller", h) })
	deadline := time.Now().Add(5 * time.Second)
	for {
		r, err := store.Poll(context.Background(), "caller", h)
		if err != nil {
			t.Fatal(err)
		}
		ready := r.State == readprep.Ready
		r.Release()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("page-only fixture timed out")
		}
		time.Sleep(time.Millisecond)
	}
	scan, child, _ := facetPrefixScan(t, w, "caller", basis, facetWindowSpec{Needle: "az", Limit: 1}, time.Time{})
	if scan.prefix.seeded || child.Total != 1 || reflect.DeepEqual(child.IDs, summary.IDs) {
		t.Fatal("top page became complete seed", child)
	}
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT b.value_id,length(CAST(x.folded AS BLOB)),instr(CAST(x.folded AS BLOB),CAST(? AS BLOB))>0,1 FROM catalog_facet_base_rows b JOIN catalog_facet_base_texts x ON x.id=b.text_id WHERE b.base_id=? AND b.value_id=?`, "az", id, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	pk := false
	for rows.Next() {
		var a, b, c int
		var detail string
		if err := rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		pk = pk || strings.Contains(detail, "SEARCH b USING PRIMARY KEY (base_id=? AND value_id=?)")
		if strings.Contains(detail, "SCAN b") || strings.Contains(detail, "TEMP B-TREE") {
			t.Fatal("candidate plan scanned base", detail)
		}
	}
	if err := rows.Err(); err != nil || !pk {
		t.Fatal("missing bounded PK probe", pk, err)
	}
}

type facetPrefixPayload struct{ payload []byte }

func (p *facetPrefixPayload) AccountedBytes() int64 { return 8192 }
func (p *facetPrefixPayload) Step(context.Context, *readprep.Budget) (readprep.StepResult, error) {
	return readprep.StepResult{Done: true, Payload: p.payload}, nil
}

func TestFacetWindowsPrefixAnchoredDirectionsMatchColdReducer(t *testing.T) {
	db, scope := facetBaseFixture(t)
	id := facetBaseBuild(t, db, scope, []FacetBaseFact{{"one", "Azalea", 9}, {"two", "Alpha", 8}, {"three", "Azure", 7}, {"four", "Amber", 6}, {"five", "Aztec", 5}})
	w, _ := facetWindowFixture(t, db, nil)
	basis, _ := w.authorize(context.Background(), "caller", id)
	var anchor int64
	if err := db.QueryRow(`SELECT b.value_id FROM catalog_facet_base_rows b JOIN catalog_facet_base_values v ON v.id=b.value_id WHERE b.base_id=? AND v.value='four'`, id).Scan(&anchor); err != nil {
		t.Fatal(err)
	}
	for _, direction := range []byte{1, 2, 3} {
		t.Run(fmt.Sprint(direction), func(t *testing.T) {
			parent := facetWindowSpec{Needle: "a", Limit: 1, Anchor: anchor, Direction: direction}
			facetPrefixScan(t, w, "caller", basis, parent, time.Time{})
			child := parent
			child.Needle = "az"
			seeded, actual, _ := facetPrefixScan(t, w, "caller", basis, child, time.Time{})
			cold, _ := facetWindowFixture(t, db, nil)
			raw, expected, _ := facetPrefixScan(t, cold, "caller", basis, child, time.Time{})
			if !seeded.prefix.seeded || raw.prefix.seeded || !reflect.DeepEqual(actual, expected) {
				t.Fatal("seed altered anchored count/order/edges", actual, expected)
			}
		})
	}
}

func TestFacetWindowsPrefixHandleCapacityFallsBackToRawScan(t *testing.T) {
	db, scope := facetBaseFixture(t)
	id := facetBaseBuild(t, db, scope, []FacetBaseFact{{"one", "Alpha", 2}, {"two", "Azalea", 1}})
	store, err := readprep.New(readprep.Config{Workers: 1, MaxHandles: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	w, err := NewFacetWindows(db, store, func(context.Context, readprep.Caller, FacetBaseSnapshot) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	basis, _ := w.authorize(context.Background(), "caller", id)
	facetPrefixScan(t, w, "caller", basis, facetWindowSpec{Needle: "a", Limit: 1}, time.Time{})
	scan, child, _ := facetPrefixScan(t, w, "caller", basis, facetWindowSpec{Needle: "az", Limit: 1}, time.Time{})
	if scan.prefix.seeded || child.Total != 1 || store.Stats().Handles != 2 || store.Stats().Deliveries != 0 {
		t.Fatal("handle contention did not fall back cleanly", child, store.Stats())
	}
}

func TestFacetWindowsPrefixUnicodeUsesActualFoldedPrefixes(t *testing.T) {
	db, scope := facetBaseFixture(t)
	id := facetBaseBuild(t, db, scope, []FacetBaseFact{{"jamo", "ᄀ", 3}, {"composed", "가", 2}, {"japanese", "日本", 1}})
	w, _ := facetWindowFixture(t, db, nil)
	basis, _ := w.authorize(context.Background(), "caller", id)
	facetPrefixScan(t, w, "caller", basis, facetWindowSpec{Needle: sorttext.Fold("ᄀ"), Limit: 1}, time.Time{})
	// Appending a vowel composes the original Hangul jamo into a different rune.
	// Raw typed-prefix compatibility therefore does not prove Fold compatibility.
	scan, summary, _ := facetPrefixScan(t, w, "caller", basis, facetWindowSpec{Needle: sorttext.Fold("가"), Limit: 1}, time.Time{})
	if scan.prefix.seeded || summary.Total != 1 {
		t.Fatal("assumed Fold preserved raw prefix", scan.prefix.seeded, summary)
	}
	facetPrefixScan(t, w, "caller", basis, facetWindowSpec{Needle: sorttext.Fold("日"), Limit: 1}, time.Time{})
	scan, summary, _ = facetPrefixScan(t, w, "caller", basis, facetWindowSpec{Needle: sorttext.Fold("日本"), Limit: 1}, time.Time{})
	if !scan.prefix.seeded || summary.Total != 1 {
		t.Fatal("lost compatible multibyte prefix", scan.prefix.seeded, summary)
	}
}

type facetPrefixProbeMeter struct {
	scan           *facetWindowScan
	steps, largest atomic.Int64
}

func (m *facetPrefixProbeMeter) AccountedBytes() int64 { return m.scan.AccountedBytes() }
func (m *facetPrefixProbeMeter) Step(ctx context.Context, b *readprep.Budget) (readprep.StepResult, error) {
	before := m.scan.prefix.probe
	out, err := m.scan.Step(ctx, b)
	delta := int64(before - m.scan.prefix.probe)
	for old := m.largest.Load(); delta > old && !m.largest.CompareAndSwap(old, delta); old = m.largest.Load() {
	}
	m.steps.Add(1)
	return out, err
}
func TestFacetWindowsPrefixExpandedQueryProbeQuantumIsBounded(t *testing.T) {
	db, scope := facetBaseFixture(t)
	id := facetBaseBuild(t, db, scope, []FacetBaseFact{{"one", "ordinary", 1}})
	w, store := facetWindowFixture(t, db, nil)
	basis, _ := w.authorize(context.Background(), "caller", id)
	spec := facetWindowSpec{Needle: strings.Repeat("x", 300), Limit: 1}
	var expiry atomic.Int64
	expiry.Store(time.Now().Add(time.Minute).UnixNano())
	var meter facetPrefixProbeMeter
	h, err := store.Submit(context.Background(), "caller", facetWindowKey(basis, spec), readprep.Plan{InitialBytes: 8192, New: func(context.Context, *readprep.Budget) (readprep.Computation, error) {
		meter.scan = newFacetWindowScan(w, "caller", basis, spec, 8192, &expiry)
		return &meter, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Cancel(context.Background(), "caller", h) })
	deadline := time.Now().Add(5 * time.Second)
	for {
		r, err := store.Poll(context.Background(), "caller", h)
		if err != nil {
			t.Fatal(err)
		}
		ready := r.State == readprep.Ready
		r.Release()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("probe loop did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	if meter.steps.Load() < 38 || meter.largest.Load() > 8 || store.Stats().Handles != 1 || store.Stats().Deliveries != 0 {
		t.Fatal("unbounded probe quantum or leaked seed lease", meter.steps.Load(), meter.largest.Load(), store.Stats())
	}
}
