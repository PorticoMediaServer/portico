package read

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// Synthetic accepted-source model, NOT a bank/FTS/visibility certificate. Native
// ordinary reverse keysets are independently exercised in owner_reverse_test.
func modelFullPage(t *testing.T, q search.Query, after *FullSearchCursor, limit int, complete bool) (FullSearchPage, error) {
	t.Helper()
	s, e := newFullQueryScope(q, 1, 1, 0)
	if e != nil {
		return FullSearchPage{}, e
	}
	a := fieldProof(t, s, search.ManifestEntity, 10, 1, "Matrix")
	b := fieldProof(t, s, search.ManifestEntity, 15, 1, "Matrix elsewhere")
	c := fieldProof(t, s, search.ManifestEntity, 15, 6, "Keanu")
	d := fieldProof(t, s, search.ManifestEntity, 20, 1, "Matrix Keanu")
	p := fieldProof(t, s, search.ManifestPerson, 20, 1, "Matrix Keanu")
	owner := fieldProof(t, s, search.ManifestPerson, 2, 1, "Keanu")
	lead, e := chooseLead(s, populations(t, s, a, b, c, d, p, owner))
	if e != nil {
		return FullSearchPage{}, e
	}
	if lead.term != "keanu" || lead.count != 4 {
		t.Fatal("owner fanout changed logical lead", lead)
	}
	ownerRoute := fullRouteKey{relation: personWorks, owner: 2, ordinal: 1, image: 1}
	top, e := newFullSearchTop(s, limit, after)
	if e != nil {
		return FullSearchPage{}, e
	}
	for _, v := range []struct {
		key     fullResultKey
		sources []candidateSource
		item    string
	}{
		{fullResultKey{search.ManifestEntity, 10, 1}, []candidateSource{{proof: a, own: true}, {proof: owner, route: ownerRoute}, {proof: owner, route: fullRouteKey{relation: personWorks, owner: 2, ordinal: 4, image: 1}}}, "Matrix"},
		{fullResultKey{search.ManifestEntity, 15, 1}, []candidateSource{{proof: b, own: true}, {proof: c, own: true}}, "Matrix elsewhere"},
		{fullResultKey{search.ManifestPerson, 20, 0}, []candidateSource{{proof: p, own: true}}, "Matrix Keanu"},
		{fullResultKey{search.ManifestEntity, 20, 1}, []candidateSource{{proof: d, own: true}, {proof: owner, route: ownerRoute}}, "Matrix Keanu"},
	} {
		proof, yes, e := proveCandidate(s, v.key, lead, v.sources, true)
		if e != nil {
			return FullSearchPage{}, e
		}
		if !yes {
			t.Fatal("model member lost", v.key)
		}
		hit := FullSearchHit{Subject: v.key.family, ID: v.key.id, Library: v.key.library, Tier: proof.tier, PeopleSubtier: proof.subtier}
		if v.key.family == search.ManifestEntity {
			hit.Item = BrowseItem{ID: v.key.id, Title: v.item}
		} else {
			hit.PersonName = v.item
		}
		// Every candidate route is visited, including duplicate/noncanonical
		// credit ordinals. Only the minimum complete lead-source/route emits.
		for _, route := range v.sources {
			if e = top.observe(proof, route.proof.key, route.route, hit); e != nil {
				return FullSearchPage{}, e
			}
		}
	}
	return top.finish(complete)
}

func TestFullSearchOrderedTraversalOracle(t *testing.T) {
	q, e := search.PlanQuery("Matrix Keanu", false)
	if e != nil {
		t.Fatal(e)
	}
	var got []struct {
		family search.ManifestSubject
		id     int64
		tier   uint8
	}
	var after *FullSearchCursor
	for turns := 0; turns < 4; turns++ {
		p, e := modelFullPage(t, q, after, 2, true)
		if e != nil {
			t.Fatal(e)
		}
		for _, h := range p.Hits {
			got = append(got, struct {
				family search.ManifestSubject
				id     int64
				tier   uint8
			}{h.Subject, h.ID, h.Tier})
		}
		if p.Exhausted {
			break
		}
		if p.Next == nil {
			t.Fatal("lost rank keyset")
		}
		after = p.Next
	}
	want := []struct {
		family search.ManifestSubject
		id     int64
		tier   uint8
	}{{search.ManifestEntity, 20, 0}, {search.ManifestPerson, 20, 3}, {search.ManifestEntity, 15, 4}, {search.ManifestEntity, 10, 5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("independent membership/order oracle", got, want)
	}
	p, e := modelFullPage(t, q, nil, 20, false)
	if e != nil || p.Disposition != SearchPending || len(p.Hits) != 0 || p.Exhausted || p.Next != nil {
		t.Fatal("incomplete frontier claimed results", p, e)
	}
	q2, _ := search.PlanQuery("Keanu Nolan", false)
	changed, _ := newFullQueryScope(q2, 1, 1, 0)
	if _, e = newFullSearchTop(changed, 2, after); !errors.Is(e, ErrStale) {
		t.Fatal("query drift cursor accepted", e)
	}
}

func TestFullSearchDispatcherUsesDedicatedFullSeam(t *testing.T) {
	f, requests, short := searchDispatcherFixture(t, 1, nil)
	var calls int
	d, e := NewFullSearchDispatcher(requests, f.governor, f.authority, f.snapshots, nil, func(ctx context.Context, purpose governor.Request, budget time.Duration, out BrowseOutput, in FullSearchInput, q search.Query) (FullSearchPage, *BrowseReadResult, error) {
		calls++
		assertLiveUsage(t, f, 1, 0)
		if q.Mode() != search.ReadFull || budget <= 0 || in.View.Limit != 2 {
			t.Fatal("wrong full input")
		}
		if deadline, ok := ctx.Deadline(); !ok || !deadline.Equal(purpose.Deadline) {
			t.Fatal("original deadline lost")
		}
		p, e := modelFullPage(t, q, in.After, in.View.Limit, true)
		return p, nil, e
	})
	if e != nil {
		t.Fatal(e)
	}
	short.titles = func(context.Context, governor.Request, time.Duration, BrowseOutput, BrowseInput, search.Query) (TitleSearchPage, *BrowseReadResult, error) {
		t.Fatal("full approximated by titles")
		return TitleSearchPage{}, nil, nil
	}
	var after *FullSearchCursor
	var ids []int64
	for n := 0; n < 2; n++ {
		q, r, o := liveRequests("full-model")
		in := dispatcherInput(f, "Matrix Keanu")
		in.View.Limit = 2
		in.FullAfter = after
		result, e := d.Consume(t.Context(), q, r, time.Second, o, in, func(ctx context.Context, response SearchResponse) (func(context.Context) error, error) {
			assertLiveUsage(t, f, 1, 0)
			if response.Full == nil || len(response.Items) != 0 {
				t.Fatal("full/title families mixed")
			}
			for _, h := range response.Full.Hits {
				ids = append(ids, h.ID)
			}
			after = response.Full.Next
			return func(context.Context) error { assertLiveUsage(t, f, 1, 0); return nil }, nil
		})
		if e != nil || !result.Published || result.Mode != search.ReadFull || result.Read != nil {
			t.Fatal("model claimed native evidence or failed", result, e)
		}
		assertLiveUsage(t, f, 0, 0)
	}
	if calls != 2 || !slices.Equal(ids, []int64{20, 20, 15, 10}) {
		t.Fatal(calls, ids)
	}
	q, r, o := liveRequests("old-title-only-constructor")
	result, e := short.Consume(t.Context(), q, r, time.Second, o, dispatcherInput(f, "Matrix Keanu"), func(_ context.Context, p SearchResponse) (func(context.Context) error, error) {
		if p.Disposition != SearchUnavailable || p.Reason != SearchFullModeUnsupported {
			t.Fatal("missing full source approximated")
		}
		return func(context.Context) error { return nil }, nil
	})
	if e != nil || !result.Published {
		t.Fatal(result, e)
	}
}

func TestFullSearchOutputShapeAndCursorDrift(t *testing.T) {
	s := fullScope(t, "Matrix Keanu")
	q := s.query
	p, e := modelFullPage(t, q, nil, 2, true)
	if e != nil {
		t.Fatal(e)
	}
	s.revision++
	if _, e = newFullSearchTop(s, 2, p.Next); !errors.Is(e, ErrStale) {
		t.Fatal("serving fence drift", e)
	}
	p.Hits[0].Tier = 5
	if _, e = ownFullSearchPage(p, 2); e == nil {
		t.Fatal("unsorted ranks accepted")
	}
	if _, e = ownFullSearchPage(FullSearchPage{Disposition: SearchPending, Hits: []FullSearchHit{{Subject: search.ManifestEntity, ID: 1}}}, 20); e == nil {
		t.Fatal("partial success accepted")
	}
}
