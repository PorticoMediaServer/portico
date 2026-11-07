package read

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// Test-only stamp construction exercises orchestration and negative comparison
// controls. Only RD's native reader may issue production stamps; no SQL, engine
// or population certification is claimed by this synthetic provider.
func TestRSSearchReadJoinedStampAndPending(t *testing.T) {
	f, _ := newRSSearchFixture(t)
	query, _ := search.PlanQuery("hello", true)
	view := f.input
	view.Kind = 0
	for _, mode := range []string{"ready", "drift", "foreign-output", "nil-final"} {
		t.Run(mode, func(t *testing.T) {
			r, _ := NewRSSearchReader(f.authority, f.snapshots)
			calls := 0
			var first *FullSearchReadStamp
			r.read = func(_ context.Context, req governor.Request, _ time.Duration, out BrowseOutput, in FullSearchInput, _ search.Query) (FullSearchPage, *BrowseReadResult, error) {
				calls++
				at := time.Now()
				stamp := &FullSearchReadStamp{pool: f.snapshots, output: out.Grant, query: query.Fold(), started: at, clock: at, deadline: req.Deadline, fence: nativeSearchFence{bank: 1, revision: 1, permission: browseFence{expiresAt: at.Add(time.Hour).Format(time.RFC3339Nano), horizon: at.Add(2 * time.Hour).Format(time.RFC3339Nano)}}}
				// Identical accepted time values, even though each read has its own clock.
				if first != nil {
					stamp.fence = first.fence
				} else {
					first = stamp
				}
				if calls == 1 {
					return FullSearchPage{Disposition: SearchPending, Next: &FullSearchCursor{query: query.Fold(), bank: 1, revision: 1, native: &nativeContinuation{}}, Stamp: stamp}, nil, nil
				}
				if calls == 2 && (in.After == nil || in.After.native == nil) {
					t.Fatal("native pending cursor was not advanced")
				}
				if calls == 3 && mode == "drift" {
					stamp.fence.permission.dataRevision++
				}
				if calls == 3 && mode == "foreign-output" {
					stamp = first
				}
				if calls == 3 && mode == "nil-final" {
					stamp = nil
				}
				page := FullSearchPage{Disposition: SearchResults, Exhausted: calls == 3, Stamp: stamp}
				lo, hi := int64(21), int64(2)
				if calls == 3 {
					lo, hi = 1, 1
				}
				for id := lo; id >= hi; id-- {
					page.Hits = append(page.Hits, FullSearchHit{Subject: search.ManifestEntity, ID: id, Library: view.Library, Kind: 1, Item: BrowseItem{ID: id}})
				}
				if !page.Exhausted {
					page.Next = &FullSearchCursor{query: query.Fold(), bank: 1, revision: 1, after: hitRank(page.Hits[len(page.Hits)-1])}
				}
				return page, nil, nil
			}
			p, ref, err := r.Begin(t.Context(), 1, time.Now().Add(10*time.Second), 1<<20, RSSearchInput{Views: []BrowseInput{view}, Query: query, Group: 1, Full: true, Order: "relevance", Capacity: 64})
			if err != nil || ref != nil {
				t.Fatal(err, ref)
			}
			defer p.Close()
			var page RSSearchPage
			for n := 0; n < 3; n++ {
				fg, req, out := liveRequests("rs-native-stamp")
				_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
					var e error
					page, _, e = r.Poll(ctx, req, time.Second, out, p)
					return e
				})
				if n < 2 && (err != nil || !page.Pending || len(page.Groups) != 0) {
					t.Fatal("partial native page", page, err)
				}
			}
			if mode == "ready" {
				if err != nil || !page.Complete || len(page.Groups) != 1 || len(page.Groups[0].Hits) != 21 {
					t.Fatal(page, err)
				}
			} else {
				want := ErrStale
				if mode == "foreign-output" {
					want = ErrInput
				}
				if mode == "nil-final" {
					want = ErrUnqualified
				}
				if !errors.Is(err, want) || page.Complete || len(page.Groups) != 0 {
					t.Fatal("invalid retained result escaped", page, err)
				}
			}
		})
	}
}

func TestRSSearchRelevanceLookaheadAndKeyset(t *testing.T) {
	f, _ := newRSSearchFixture(t)
	query, _ := search.PlanQuery("hello", true)
	view := f.input
	view.Kind = 0
	calls := 0
	provider := func(_ context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, in FullSearchInput, _ search.Query) (FullSearchPage, *BrowseReadResult, error) {
		calls++
		hi := int64(200)
		if in.After != nil {
			hi = in.After.after.key.id - 1
		}
		lo := max(int64(101), hi-19)
		page := FullSearchPage{Disposition: SearchResults, Exhausted: lo == 101}
		for id := hi; id >= lo; id-- {
			page.Hits = append(page.Hits, FullSearchHit{Subject: search.ManifestEntity, ID: id, Library: view.Library, Kind: 1, Item: BrowseItem{ID: id}})
		}
		if !page.Exhausted {
			page.Next = &FullSearchCursor{query: query.Fold(), bank: 1, revision: 1, after: hitRank(page.Hits[len(page.Hits)-1])}
		}
		return page, nil, nil
	}
	r, _ := newRSSearchReaderWithRead(f.authority, provider, func(context.Context, BrowseOutput, FullSearchInput, FullSearchPage, []FullSearchHit, *FullSearchReadStamp) error {
		return nil
	})
	in := RSSearchInput{Views: []BrowseInput{view}, Query: query, Group: 1, Full: true, Order: "relevance", Capacity: 64}
	var all []int64
	for pageNumber := 0; pageNumber < 3; pageNumber++ {
		p, ref, err := r.Begin(t.Context(), 1, time.Now().Add(10*time.Second), 1<<20, in)
		if err != nil || ref != nil {
			t.Fatal(err, ref)
		}
		defer p.Close()
		var page RSSearchPage
		for poll := 0; poll < 4; poll++ {
			fg, req, out := liveRequests("rs-relevance-keyset")
			_, err = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
				var e error
				page, _, e = r.Poll(ctx, req, time.Second, out, p)
				return e
			})
			if err != nil {
				t.Fatal(err)
			}
			if page.Complete {
				break
			}
		}
		if !page.Complete || len(page.Groups) != 1 {
			t.Fatal(page)
		}
		for _, hit := range page.Groups[0].Hits {
			all = append(all, hit.ID)
		}
		in.After = page.Groups[0].Next
		if pageNumber < 2 && (in.After == nil || in.After.rank == nil) {
			t.Fatal("missing keyset continuation")
		}
	}
	if len(all) != 100 || in.After != nil || calls != 7 {
		t.Fatal("incorrect lookahead/keyset", len(all), calls, in.After)
	}
	for n, id := range all {
		if id != 200-int64(n) {
			t.Fatal("lost or duplicated boundary", n, id)
		}
	}
}

func TestRSSearchPublicReadingGroup(t *testing.T) {
	p := &RSSearchPreparation{input: RSSearchInput{Capacity: 64, Group: 0}, hits: []FullSearchHit{{Subject: search.ManifestEntity, Kind: 20, ID: 9}, {Subject: search.ManifestEntity, Kind: 21, ID: 8}}}
	page, err := p.compose()
	if err != nil || len(page.Groups) != 1 || page.Groups[0].Group != RSSearchReading || len(page.Groups[0].Hits) != 2 {
		t.Fatal("public Reading group split by storage kind", page, err)
	}
}
