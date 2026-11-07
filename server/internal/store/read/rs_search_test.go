package read

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
	"sync/atomic"
	"testing"
	"time"
)

// Fake-provider contract; no native search certification.
func TestRSSearchComposition(t *testing.T) {
	f, _ := newRSSearchFixture(t)
	query, _ := search.PlanQuery("hello", true)
	v := f.input
	v.Kind = 0
	for _, tc := range []struct {
		name     string
		n        int
		group    RSSearchGroup
		failure  error
		capacity int
		verify   bool
	}{
		{"full41", 41, 1, nil, 64, true}, {"preview9", 9, 20, nil, 64, true}, {"reading21", 9, 21, nil, 64, true}, {"people", 9, RSSearchPeople, nil, 64, true}, {"missing-custody", 41, 1, ErrUnqualified, 64, false}, {"source", 41, 1, ErrStale, 64, true}, {"visibility", 41, 1, ErrDenied, 64, true}, {"capacity", 41, 1, ErrUnqualified, 20, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := RSSearchInput{Views: []BrowseInput{v}, Query: query, Group: tc.group, Full: tc.group == 1, Order: "relevance", Capacity: tc.capacity}
			calls := 0
			provider := func(_ context.Context, _ governor.Request, _ time.Duration, _ BrowseOutput, input FullSearchInput, q search.Query) (FullSearchPage, *BrowseReadResult, error) {
				calls++
				start := 0
				if input.After != nil {
					start = 100 - int(input.After.after.key.id)
				}
				if input.View.Limit != 20 || input.View.Kind != rsSearchExpectedNativeKind(tc.group) {
					t.Fatal(input)
				}
				page := FullSearchPage{Disposition: SearchResults, Exhausted: start+20 >= tc.n}
				for id := start + 1; id <= min(start+20, tc.n); id++ {
					h := FullSearchHit{Subject: search.ManifestEntity, ID: int64(100 - id), Library: v.Library, Kind: int64(tc.group), Item: BrowseItem{ID: int64(100 - id)}}
					if tc.group == RSSearchPeople {
						h.Subject = search.ManifestPerson
						h.Kind = 0
						h.Tier = 3
						h.Item = BrowseItem{}
					}
					page.Hits = append(page.Hits, h)
				}
				if !page.Exhausted {
					page.Next = &FullSearchCursor{query: q.Fold(), bank: 1, revision: 1, after: hitRank(page.Hits[len(page.Hits)-1])}
				}
				return page, nil, nil
			}
			var verify rsSearchCustody
			if tc.verify {
				verify = func(_ context.Context, _ BrowseOutput, _ FullSearchInput, _ FullSearchPage, h []FullSearchHit, _ *FullSearchReadStamp) error {
					if len(h) > 0 && (tc.failure == ErrStale || tc.failure == ErrDenied) {
						return tc.failure
					}
					return nil
				}
			}
			r, _ := newRSSearchReaderWithRead(f.authority, provider, verify)
			p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(10*time.Second), 1<<20, in)
			if e != nil || ref != nil {
				t.Fatal(e, ref)
			}
			defer p.Close()
			var out RSSearchPage
			for i := 0; i < 4; i++ {
				before := calls
				q, req, o := liveRequests("rs-search")
				_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, q, o, func(ctx context.Context, o BrowseOutput) error {
					var e error
					out, _, e = r.Poll(ctx, req, time.Second, o, p)
					return e
				})
				if calls-before != 1 {
					t.Fatal("read count", calls-before)
				}
				if e != nil || out.Complete {
					break
				}
				if !out.Pending || len(out.Groups) > 0 {
					t.Fatal("partial result", out)
				}
			}
			if tc.failure != nil {
				if !errors.Is(e, tc.failure) || len(out.Groups) > 0 {
					t.Fatal(out, e)
				}
				return
			}
			limit := 8
			if in.Full {
				limit = 40
			}
			if e != nil || !out.Complete || len(out.Groups) != 1 || len(out.Groups[0].Hits) != limit || !out.Groups[0].HasMore {
				t.Fatal(out, e)
			}
			c := out.Groups[0].Next
			if c == nil {
				t.Fatal("cursor")
			}
			bad := *c
			bad.identity[0] ^= 1
			in.After = &bad
			if _, _, e = r.Begin(t.Context(), 1, time.Now().Add(time.Second), 1<<20, in); !errors.Is(e, ErrStale) {
				t.Fatal(e)
			}
		})
	}
}

func TestRSSearchRefusalsAndExpiry(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	f, _ := newRSSearchFixture(t, func() time.Time { return time.Unix(0, clock.Load()) })
	q, _ := search.PlanQuery("hello", true)
	v := f.input
	v.Kind = 0
	in := RSSearchInput{Views: []BrowseInput{v}, Query: q, Group: 1, Full: true, Order: "relevance", Capacity: 64}
	r, _ := NewRSSearchReader(f.authority, f.snapshots)
	// A synthetic page has no native stamp; the production custody binding refuses.
	r.read = func(context.Context, governor.Request, time.Duration, BrowseOutput, FullSearchInput, search.Query) (FullSearchPage, *BrowseReadResult, error) {
		return FullSearchPage{Disposition: SearchResults, Exhausted: true}, nil, nil
	}
	for _, change := range []func(*RSSearchInput){func(i *RSSearchInput) { i.Views = append(i.Views, v) }, func(i *RSSearchInput) { i.Group = RSSearchLiveTV }, func(i *RSSearchInput) { i.Order = "title" }} {
		c := in
		change(&c)
		if _, _, e := r.Begin(t.Context(), 1, time.Now().Add(time.Second), 1<<20, c); !errors.Is(e, ErrUnqualified) {
			t.Fatal(e)
		}
	}
	p, _, e := r.Begin(t.Context(), 1, time.Now().Add(10*time.Second), 1<<20, in)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	live, req, out := liveRequests("native-unavailable")
	_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, out, func(ctx context.Context, out BrowseOutput) error {
		_, _, e := r.Poll(ctx, req, time.Second, out, p)
		return e
	})
	if !errors.Is(e, ErrUnqualified) {
		t.Fatal(e)
	}
	p, _, e = r.Begin(t.Context(), 1, time.Now().Add(10*time.Second), 1<<20, in)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	clock.Add(int64(20 * time.Second))
	live, req, out = liveRequests("expired-preparation")
	_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, live, out, func(ctx context.Context, out BrowseOutput) error {
		page, _, e := r.Poll(ctx, req, time.Second, out, p)
		if page.Complete || len(page.Groups) > 0 {
			t.Fatal(page)
		}
		return e
	})
	if e == nil {
		t.Fatal("released lease accepted")
	}
}

func newRSSearchFixture(t *testing.T, clocks ...func() time.Time) (*browseFixture, *BrowseReader) {
	t.Helper()
	clock := time.Now
	if len(clocks) > 0 {
		clock = clocks[0]
	}
	cfg := readfixture.LiveConfig(clock)
	cfg.Preparation = &governor.PreparationProfile{MaxActive: 4, MaxBytes: 64 << 20, MaxJobBytes: 16 << 20, ProtectedBytes: 16 << 20}
	for i := range cfg.Partitions {
		cfg.Partitions[i][governor.MemoryBytes] = 128 << 20
	}
	f := composeReadFixture(t, newBrowseSource(t), browseDefinitions(), cfg, false)
	return f, f.reader
}

func rsSearchExpectedNativeKind(g RSSearchGroup) int64 {
	if g == 20 || g == 21 || g < 0 {
		return 0
	}
	return int64(g)
}
