package read

import (
	"context"
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
