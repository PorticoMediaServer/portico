package read

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// Only orchestration is certified here. These stamps model RD's issued shape;
// no native FTS, population, visibility or engine certification is claimed.
func rsSearchTestStamp(f *browseFixture, out BrowseOutput, req governor.Request, in FullSearchInput, page *FullSearchPage, query search.Query) {
	at := time.Now()
	s := &FullSearchReadStamp{pool: f.snapshots, output: out.Grant, query: query.Fold(), clock: at, started: at, deadline: req.Deadline, fence: nativeSearchFence{bank: 1, revision: 1, permission: browseFence{expiresAt: at.Add(time.Hour).Format(time.RFC3339Nano), horizon: at.Add(2 * time.Hour).Format(time.RFC3339Nano)}}}
	for _, prior := range in.Retained {
		s.fence = prior.fence
		s.checked = append(s.checked, prior.policies...)
		s.hits = append(s.hits, prior.hits...)
	}
	for i, h := range page.Hits {
		proof := nativePolicyWitness{valid: true, id: h.ID, library: h.Library, kind: h.Kind}
		page.Hits[i].permission = proof
		s.policies = append(s.policies, proof)
		s.checked = append(s.checked, proof)
		s.hits = append(s.hits, nativeStampedHit{nativeHitIdentity(page.Hits[i]), proof})
	}
	page.Stamp = s
}
func rsSearchTestHit(group RSSearchGroup, id, library int64) FullSearchHit {
	h := FullSearchHit{Subject: search.ManifestEntity, ID: id, Library: library, Kind: int64(group), Item: BrowseItem{ID: id, Title: fmt.Sprint(id)}, SortKey: fmt.Sprintf("%04d", id)}
	if group == RSSearchPeople {
		h.Subject = search.ManifestPerson
		h.Kind = 0
		h.Tier = 3
		h.Item = BrowseItem{}
		h.PersonName = fmt.Sprint(id)
	}
	return h
}
func TestRSSearchBoundedPreview(t *testing.T) {
	for _, mode := range []string{"all-eight", "candidate-cap", "pending-progress"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, _ := newRSSearchFixture(t)
				query, _ := search.PlanQuery("hello", true)
				view := f.input
				view.Kind = 0
				r, _ := NewRSSearchReader(f.authority, f.snapshots)
				calls, spent, offset := 0, 0, 0
				var all []FullSearchHit
				for i, g := range rsSearchGroups {
					for n := 0; n < 8; n++ {
						all = append(all, rsSearchTestHit(g, int64(1000-i*10-n), view.Library))
					}
				}
				r.read = func(_ context.Context, req governor.Request, _ time.Duration, out BrowseOutput, in FullSearchInput, q search.Query) (FullSearchPage, *BrowseReadResult, error) {
					calls++
					if !in.Preview || in.CandidateBudget != NativePreviewCandidates-spent {
						t.Fatal("preview work allowance lost", in.Preview, in.CandidateBudget, spent)
					}
					if len(in.Retained) > 0 && len(in.Retained[0].policies) > 99 {
						t.Fatal("discarded preview payload retained", len(in.Retained[0].policies))
					}
					p := FullSearchPage{Disposition: SearchResults, Partial: true, CountStatus: SearchCountUnknown, NativeCandidates: 1000, Next: &FullSearchCursor{query: q.Fold(), bank: 1, revision: 1, native: &nativeContinuation{preview: true}}}
					switch mode {
					case "all-eight":
						p.NativeCandidates = 300
						end := min(offset+20, len(all))
						p.Hits = append(p.Hits, all[offset:end]...)
						offset = end
					case "candidate-cap":
						for n := 0; n < 20; n++ {
							p.Hits = append(p.Hits, rsSearchTestHit(1, int64(calls*100+n), view.Library))
						}
					case "pending-progress":
						if calls == 1 {
							p.Disposition = SearchPending
							p.NativeCandidates = 750
						} else {
							p.NativeCandidates = 1250
							p.Next = nil
						}
					}
					sort.Slice(p.Hits, func(i, j int) bool { return compareFullRank(hitRank(p.Hits[i]), hitRank(p.Hits[j])) < 0 })
					if len(p.Hits) > 0 {
						p.CountStatus = SearchCountLowerBound
						p.Count = uint64(len(p.Hits))
					}
					spent += p.NativeCandidates
					rsSearchTestStamp(f, out, req, in, &p, q)
					return p, nil, nil
				}
				p, ref, e := r.Begin(t.Context(), 1, time.Now().Add(12*time.Second), 2<<20, RSSearchInput{Views: []BrowseInput{view}, Query: query, Order: "relevance", Capacity: 128})
				if e != nil || ref != nil {
					t.Fatal(e, ref)
				}
				defer p.Close()
				var page RSSearchPage
				for poll := 0; poll < 8; poll++ {
					fg, req, out := liveRequests("rs-bounded-preview")
					before := calls
					_, e = ConsumeForeground(t.Context(), f.governor, f.authority, f.snapshots, fg, out, func(ctx context.Context, out BrowseOutput) error {
						var err error
						page, _, err = r.Poll(ctx, req, time.Second, out, p)
						return err
					})
					if e != nil || calls-before != 1 {
						t.Fatal("one preview call per LIVE poll", calls-before, e)
					}
					if page.Complete {
						break
					}
					if !page.Pending || len(page.Groups) != 0 || page.NativeCandidates != spent {
						t.Fatal("pending work hidden or prefix published", page, spent)
					}
				}
				if !page.Complete || !page.Partial || len(page.Groups) != len(rsSearchGroups) || page.NativeCandidates != spent {
					t.Fatal("preview completeness/exactness", page, spent)
				}
				for _, g := range page.Groups {
					if len(g.Hits) > 8 || g.CountStatus == SearchCountExact || len(g.Hits) == 0 && (g.CountStatus != SearchCountUnknown || g.HasMoreKnown) {
						t.Fatal("bounded preview inferred exact/zero", g)
					}
					if mode == "all-eight" && len(g.Hits) != 8 {
						t.Fatal("missing group", g.Group, len(g.Hits))
					}
					if mode == "candidate-cap" && g.Group == 1 && (!g.HasMore || !g.HasMoreKnown || g.Hits[0].ID != 219) {
						t.Fatal("verified best top/more", g)
					}
				}
				if mode == "all-eight" && (calls != 5 || page.BudgetStopped || spent != 1500) {
					t.Fatal("waited for census/exhaustion", calls, page.BudgetStopped, spent)
				}
				if mode != "all-eight" && (calls != 2 || !page.BudgetStopped || spent != 2000) {
					t.Fatal("MATCH budget lost", calls, page.BudgetStopped, spent)
				}
			})
		})
	}
}
