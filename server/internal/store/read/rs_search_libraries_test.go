package read

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

func TestRSSearchLibrariesGlobalKeyset(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, _ := newRSSearchFixture(t)
		query, _ := search.PlanQuery("hello", true)
		a, b := f.input, f.input
		a.Kind = 0
		b.Kind = 0
		b.Library = 2
		r, _ := NewRSSearchReader(f.authority, f.snapshots)
		calls := 0
		r.read = func(_ context.Context, req governor.Request, _ time.Duration, out BrowseOutput, in FullSearchInput, q search.Query) (FullSearchPage, *BrowseReadResult, error) {
			calls++
			page := FullSearchPage{Disposition: SearchResults}
			// Independent known rank order: IDs descend, odd IDs in library one and
			// even IDs in library two. Each library has fifty canonical results.
			for id := int64(100); id >= 1; id-- {
				library := int64(2)
				if id%2 == 1 {
					library = 1
				}
				if library != in.View.Library || in.After != nil && id >= in.After.after.key.id {
					continue
				}
				if len(page.Hits) == 20 {
					break
				}
				page.Hits = append(page.Hits, rsSearchTestHit(1, id, library))
			}
			page.Exhausted = len(page.Hits) < 20
			if !page.Exhausted {
				page.Next = &FullSearchCursor{query: q.Fold(), bank: 1, revision: 1, after: hitRank(page.Hits[len(page.Hits)-1])}
			}
			rsSearchTestStamp(f, out, req, in, &page, q)
			return page, nil, nil
		}
		in := RSSearchInput{Views: []BrowseInput{b, a}, Query: query, Group: 1, Full: true, Order: "relevance", Capacity: 64}
		var got []int64
		for n := 0; n < 3; n++ {
			p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(12*time.Second), 2<<20, in)
			if e != nil || ref != nil {
				t.Fatal(e, ref)
			}
			var page RSSearchPage
			for poll := 0; poll < 20; poll++ {
				fg, req, out := liveRequests("rs-multi-keyset")
				before := calls
				_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
					var e error
					page, _, e = r.Poll(ctx, req, time.Second, out, p)
					return e
				})
				if e != nil || calls-before != 1 {
					t.Fatal("one native call per library quantum", calls-before, e)
				}
				if page.Complete {
					break
				}
			}
			_ = p.Close()
			if !page.Complete || page.Partial || len(page.Groups) != 1 || len(page.Groups[0].Hits) != min(40, 100-len(got)) {
				t.Fatal(n, page)
			}
			for _, h := range page.Groups[0].Hits {
				got = append(got, h.ID)
			}
			in.After = page.Groups[0].Next
			if n < 2 && (in.After == nil || in.After.rank != nil || in.After.order == nil || len(in.After.anchors) != 2) {
				t.Fatal("global source-bound continuation", in.After)
			}
		}
		want := make([]int64, 100)
		for i := range want {
			want[i] = 100 - int64(i)
		}
		if !reflect.DeepEqual(got, want) || in.After != nil {
			t.Fatal("lost or duplicated cross-library boundary", got)
		}
	})
}

// Every hit in both non-empty libraries shares one title key, so the global
// merge is decided entirely by the canonical ID/library tie break. Library two
// proves no match and still contributes a revalidated empty source-scope anchor
// to the continuation that survives the cross-library page boundary.
func TestRSSearchLibrariesEmptyAnchorsAndGlobalTies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, _ := newRSSearchFixture(t)
		query, _ := search.PlanQuery("hello", true)
		a, none, c := f.input, f.input, f.input
		a.Kind, none.Kind, c.Kind = 0, 0, 0
		none.Library, c.Library = 2, 3
		r, _ := NewRSSearchReader(f.authority, f.snapshots)
		calls := 0
		read := rsSearchDescendingProvider(f, map[int64][2]int64{1: {100, 71}, 3: {70, 41}}, 2)
		r.read = func(ctx context.Context, req governor.Request, budget time.Duration, out BrowseOutput, in FullSearchInput, q search.Query) (FullSearchPage, *BrowseReadResult, error) {
			calls++
			return read(ctx, req, budget, out, in, q)
		}
		in := RSSearchInput{Views: []BrowseInput{c, none, a}, Query: query, Group: 1, Full: true, Order: "title", Direction: "asc", Capacity: 64}
		p, ref, err := r.Begin(t.Context(), 1, time.Now().Add(12*time.Second), 2<<20, in)
		if err != nil || ref != nil {
			t.Fatal(err, ref)
		}
		var page RSSearchPage
		for poll := 0; poll < 6; poll++ {
			fg, req, out := liveRequests("rs-empty-anchor-ties")
			before := calls
			_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
				var e error
				page, _, e = r.Poll(ctx, req, time.Second, out, p)
				return e
			})
			if err != nil || calls-before != 1 {
				t.Fatal("one native call per poll", calls-before, err)
			}
			if page.Complete {
				break
			}
		}
		_ = p.Close()
		if !page.Complete || page.Partial || len(page.Groups) != 1 || len(page.Groups[0].Hits) != 40 || !page.Groups[0].HasMore {
			t.Fatal("cross-library page incomplete", page)
		}
		first := page.Groups[0].Hits
		for i, want := range []struct{ id, library int64 }{{41, 3}, {70, 3}, {71, 1}, {80, 1}} {
			at := []int{0, 29, 30, 39}[i]
			if first[at].ID != want.id || first[at].Library != want.library {
				t.Fatal("global tie keyset", at, first[at].ID, first[at].Library, want)
			}
		}
		next := page.Groups[0].Next
		if next == nil || next.rank != nil || next.order == nil || len(next.anchors) != 3 {
			t.Fatal("global source-bound continuation", next)
		}
		for i, anchor := range next.anchors {
			if anchor == nil || anchor.fence.permission.libraryID != int64(i+1) {
				t.Fatal("missing per-library source anchor", i, anchor)
			}
		}
		if empty := next.anchors[1]; len(empty.policies) != 0 || len(empty.hits) != 0 {
			t.Fatal("empty library anchor claimed match evidence", empty)
		}
		in.After = next
		p, ref, err = r.Begin(t.Context(), 1, time.Now().Add(12*time.Second), 2<<20, in)
		if err != nil || ref != nil {
			t.Fatal(err, ref)
		}
		defer p.Close()
		for poll := 0; poll < 6; poll++ {
			fg, req, out := liveRequests("rs-empty-anchor-ties")
			before := calls
			_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
				var e error
				page, _, e = r.Poll(ctx, req, time.Second, out, p)
				return e
			})
			if err != nil || calls-before != 1 {
				t.Fatal("one native call per poll", calls-before, err)
			}
			if page.Complete {
				break
			}
		}
		if !page.Complete || len(page.Groups) != 1 || len(page.Groups[0].Hits) != 20 || page.Groups[0].HasMore || page.Groups[0].Next != nil {
			t.Fatal("final cross-library page", page)
		}
		for i, hit := range page.Groups[0].Hits {
			if hit.ID != int64(81+i) || hit.Library != 1 {
				t.Fatal("lost or duplicated cross-library tie boundary", i, hit.ID, hit.Library)
			}
		}
	})
}

func TestRSSearchCursorScopeLimitOrderDrift(t *testing.T) {
	f, _ := newRSSearchFixture(t)
	query, _ := search.PlanQuery("hello", true)
	a, b := f.input, f.input
	a.Kind, b.Kind = 0, 0
	b.Library = 2
	r, _ := NewRSSearchReader(f.authority, f.snapshots)
	r.read = rsSearchDescendingProvider(f, map[int64][2]int64{1: {100, 41}}, 2)
	base := RSSearchInput{Views: []BrowseInput{a, b}, Query: query, Group: 1, Full: true, Order: "title", Direction: "asc", Capacity: 64}
	p, ref, err := r.Begin(t.Context(), 1, time.Now().Add(12*time.Second), 2<<20, base)
	if err != nil || ref != nil {
		t.Fatal(err, ref)
	}
	var page RSSearchPage
	for poll := 0; poll < 4; poll++ {
		fg, req, out := liveRequests("rs-cursor-drift")
		_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
			var e error
			page, _, e = r.Poll(ctx, req, time.Second, out, p)
			return e
		})
		if err != nil || page.Complete {
			break
		}
	}
	_ = p.Close()
	if err != nil || !page.Complete || len(page.Groups) != 1 || page.Groups[0].Next == nil {
		t.Fatal("cursor precondition", page, err)
	}
	after := page.Groups[0].Next
	for _, tc := range []struct {
		name   string
		change func(*RSSearchInput)
	}{
		{"library-scope", func(i *RSSearchInput) { i.Views = i.Views[:1] }},
		{"database-scope", func(i *RSSearchInput) { i.Views[0].Database, i.Views[1].Database = "other-db", "other-db" }},
		{"viewer-scope", func(i *RSSearchInput) { i.Views[0].TokenHash, i.Views[1].TokenHash = "other-token", "other-token" }},
		{"retained-limit", func(i *RSSearchInput) { i.Capacity = 32 }},
		{"group", func(i *RSSearchInput) { i.Group = 2 }},
		{"order", func(i *RSSearchInput) { i.Order = "dateAdded" }},
		{"direction", func(i *RSSearchInput) { i.Direction = "desc" }},
		{"native-limit", func(i *RSSearchInput) {
			c := *after
			c.order = nil
			c.rank = &FullSearchCursor{query: query.Fold(), bank: 1, revision: 1, native: &nativeContinuation{limit: 21}}
			i.After = &c
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			in.After = after
			tc.change(&in)
			if _, _, err := r.Begin(t.Context(), 1, time.Now().Add(time.Second), 1<<20, in); !errors.Is(err, ErrStale) {
				t.Fatal(err)
			}
		})
	}
}

func TestRSSearchLibrariesPreviewBudgetAndPeopleUnion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, _ := newRSSearchFixture(t)
		query, _ := search.PlanQuery("hello", true)
		a, b := f.input, f.input
		a.Kind = 0
		b.Kind = 0
		b.Library = 2
		r, _ := NewRSSearchReader(f.authority, f.snapshots)
		calls, spent := 0, 0
		r.read = func(_ context.Context, req governor.Request, _ time.Duration, out BrowseOutput, in FullSearchInput, q search.Query) (FullSearchPage, *BrowseReadResult, error) {
			calls++
			if !in.Preview || in.CandidateBudget != 1000 || len(in.Retained) > 128 {
				t.Fatal("shared bounded preview", in)
			}
			spent += in.CandidateBudget
			page := FullSearchPage{Disposition: SearchResults, Partial: true, NativeCandidates: 1000, Next: &FullSearchCursor{query: q.Fold(), bank: 1, revision: 1, native: &nativeContinuation{preview: true}}}
			// The same person is backed by visible work in both libraries. It must
			// appear once; its proof may be narrowed to the better source-library tie.
			page.Hits = append(page.Hits, rsSearchTestHit(RSSearchPeople, 100, in.View.Library))
			for id := int64(1); id <= 8; id++ {
				page.Hits = append(page.Hits, rsSearchTestHit(1, id+in.View.Library*100, in.View.Library))
			}
			sort.Slice(page.Hits, func(i, j int) bool { return compareFullRank(hitRank(page.Hits[i]), hitRank(page.Hits[j])) < 0 })
			rsSearchTestStamp(f, out, req, in, &page, q)
			return page, nil, nil
		}
		p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(12*time.Second), 2<<20, RSSearchInput{Views: []BrowseInput{a, b}, Query: query, Order: "relevance", Capacity: 128})
		if e != nil || ref != nil {
			t.Fatal(e, ref)
		}
		defer p.Close()
		var page RSSearchPage
		for i := 0; i < 2; i++ {
			fg, req, out := liveRequests("rs-multi-preview")
			before := calls
			_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
				var e error
				page, _, e = r.Poll(ctx, req, time.Second, out, p)
				return e
			})
			if e != nil || calls-before != 1 {
				t.Fatal(e, calls-before)
			}
		}
		if !page.Complete || !page.Partial || !page.BudgetStopped || spent != 2000 || page.NativeCandidates != 2000 {
			t.Fatal(page, spent)
		}
		for _, g := range page.Groups {
			if g.Group == RSSearchPeople && (len(g.Hits) != 1 || g.Hits[0].Library != 1) {
				t.Fatal("duplicate person or wrong source tie", g)
			}
			if g.Group == 1 && (len(g.Hits) != 8 || g.Hits[0].Library != 2 || !g.HasMore) {
				t.Fatal("global best verified preview", g)
			}
			if g.Group == 2 && (len(g.Hits) != 0 || g.HasMoreKnown || g.CountStatus != SearchCountUnknown) {
				t.Fatal("empty library inferred exactness", g)
			}
		}
	})
}

// rsSearchDescendingProvider models the native callable's shape: at most twenty
// ranked hits per page, an exclusive rank continuation and one empty scope.
func rsSearchDescendingProvider(f *browseFixture, ranges map[int64][2]int64, emptyLibrary int64) FullSearchRead {
	return func(_ context.Context, req governor.Request, _ time.Duration, out BrowseOutput, in FullSearchInput, q search.Query) (FullSearchPage, *BrowseReadResult, error) {
		page := FullSearchPage{Disposition: SearchResults}
		bounds, ok := ranges[in.View.Library]
		if !ok {
			if in.View.Library != emptyLibrary {
				return FullSearchPage{}, nil, ErrInput
			}
			page.Exhausted = true
			rsSearchTestStamp(f, out, req, in, &page, q)
			return page, nil, nil
		}
		start := bounds[0]
		if in.After != nil {
			start = in.After.after.key.id - 1
		}
		for id := start; id >= bounds[1] && len(page.Hits) < 20; id-- {
			hit := rsSearchTestHit(1, id, in.View.Library)
			hit.SortKey = "tie"
			page.Hits = append(page.Hits, hit)
		}
		last := start
		if n := len(page.Hits); n > 0 {
			last = page.Hits[n-1].ID
		}
		page.Exhausted = last <= bounds[1]
		if !page.Exhausted {
			page.Next = &FullSearchCursor{query: q.Fold(), bank: 1, revision: 1, after: hitRank(page.Hits[len(page.Hits)-1])}
		}
		rsSearchTestStamp(f, out, req, in, &page, q)
		return page, nil, nil
	}
}
