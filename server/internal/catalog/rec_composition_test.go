package catalog

import (
	"context"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestRecCompositionSourceScopeRequiresExactSnapshotAndViewer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	r := HomeRequest{Profile: "p", ViewerFence: "viewer", Viewer: Viewer{Fence: "viewer"}, Libraries: []string{"m"}}
	var first *recHydration
	err := dbwork.WithReadSnapshot(context.Background(), w.c.DB, func(ctx context.Context) error {
		s := w.s.WithContext(ctx)
		s.recComposition = newRecCompositionSources(ctx)
		first = s.recHydrationFor(r)
		if first != s.recHydrationFor(r) {
			t.Fatal("matching composition scope did not share source inputs")
		}
		age := 13
		variants := []HomeRequest{r, r, r, r, r}
		variants[0].Profile = "other"
		variants[1].ViewerFence = "other"
		variants[2].Viewer.Fence = "other"
		variants[3].Libraries = []string{"other"}
		variants[4].Restrictions.MaximumAge = &age
		for _, other := range variants {
			if first == s.recHydrationFor(other) {
				t.Fatal("source store crossed viewer/library/restriction scope")
			}
		}
		fresh := s.WithContext(ctx)
		fresh.recComposition = newRecCompositionSources(ctx)
		if first == fresh.recHydrationFor(r) {
			t.Fatal("separate composition retained an earlier source store")
		}
		// A clone carrying another transaction cannot retain this registry.
		return dbwork.WithReadSnapshot(context.Background(), w.c.DB, func(otherCtx context.Context) error {
			if dbwork.Snapshot(otherCtx) == dbwork.Snapshot(ctx) {
				t.Fatal("test did not open a distinct read snapshot")
			}
			if first == s.WithContext(otherCtx).recHydrationFor(r) {
				t.Fatal("source store crossed read snapshots")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	s := w.s.WithContext(context.Background())
	s.recComposition = &recCompositionSources{snapshot: first.snapshot}
	if s.recHydrationFor(r) == s.recHydrationFor(r) {
		t.Fatal("calls without a snapshot shared source inputs")
	}
}

func TestRecCompositionKeepsHomeAndTitleRankingsWithFewerReads(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	for _, work := range w.scifi[:3] {
		w.watch("p", work)
	}
	w.c.Drain()
	// Pending taste disables process memo results, exposing every source read.
	w.personal("p", w.scifi[5], "favorite", 1)
	r := HomeRequest{Profile: "p", Libraries: []string{"m"}, Now: time.Now()}
	type result struct {
		recommended, more, person []recCandidate
		personal                  []recRow
	}
	run := func(s *Service) (result, error) {
		var out result
		var err error
		if out.recommended, err = s.recRank(r); err != nil {
			return out, err
		}
		if out.personal, err = s.recPersonalRows(r); err != nil {
			return out, err
		}
		base, err := s.recSession(r)
		if err != nil {
			return out, err
		}
		if out.more, err = s.recMoreLike(base, w.scifi[4].ID); err != nil {
			return out, err
		}
		facets, err := compactcatalog.RecWorkFacets(s.Context(), s.read(), w.scifi[4].ID)
		if err != nil {
			return out, err
		}
		for _, facet := range facets {
			if strings.HasPrefix(facet, "p:") {
				out.person, err = s.recFacetTitles(base, w.scifi[4].ID, facet)
				break
			}
		}
		return out, err
	}
	err := dbwork.WithReadSnapshot(context.Background(), w.c.DB, func(ctx context.Context) error {
		measured, independentCost := dbwork.Measure(ctx)
		want, err := run(w.s.WithContext(measured))
		if err != nil {
			return err
		}
		measured, sharedCost := dbwork.Measure(ctx)
		s := w.s.WithContext(measured)
		s.recComposition = newRecCompositionSources(measured)
		got, err := run(s)
		if err != nil {
			return err
		}
		if len(want.recommended) == 0 || len(want.personal) == 0 || len(want.more) == 0 || len(want.person) == 0 {
			t.Fatal("oracle did not exercise nonempty Home and title/person rows")
		}
		assertRecRankingEqual(t, 0, got.recommended, want.recommended)
		assertRecRankingEqual(t, 1, got.more, want.more)
		assertRecRankingEqual(t, 2, got.person, want.person)
		if len(got.personal) != len(want.personal) {
			t.Fatal("personal row selection changed")
		}
		for i := range got.personal {
			assertRecRankingEqual(t, 3+i, got.personal[i].items, want.personal[i].items)
			if math.Abs(got.personal[i].strength-want.personal[i].strength) > 1e-12 {
				t.Fatal("personal row strength changed")
			}
			got.personal[i].strength = want.personal[i].strength
			if !reflect.DeepEqual(got.personal[i], want.personal[i]) {
				t.Fatal("personal row metadata/order changed")
			}
		}
		if sharedCost().Statements >= independentCost().Statements {
			t.Fatalf("composition source reuse did not reduce reads: shared=%d independent=%d", sharedCost().Statements, independentCost().Statements)
		}
		t.Logf("same Home/title rankings: shared=%d independent=%d statements", sharedCost().Statements, independentCost().Statements)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRecCompositionTitleSharesMetadataButKeepsTasteAndEdgesIndependent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	w.c.Exec(`INSERT INTO catalog_external_ids VALUES(?,'tmdb','movie','target')`, w.scifi[4].ID)
	w.c.Exec(`INSERT INTO catalog_similar(entity_id,source,rank,provider,provider_kind,provider_id) VALUES(?,'fixture',1,'tmdb','movie','target')`, w.scifi[0].ID)
	w.c.Exec(`INSERT INTO catalog_similar(entity_id,source,rank,provider,provider_kind,provider_id) VALUES(?,'fixture',2,'tmdb','movie','target')`, w.romance[0].ID)
	err := dbwork.WithReadSnapshot(context.Background(), w.c.DB, func(ctx context.Context) error {
		base, err := w.s.WithContext(ctx).recSession(HomeRequest{Profile: "p", Libraries: []string{"m"}})
		if err != nil {
			return err
		}
		base.taste.seeds = []recSeed{{w.scifi[0].ID, 2}}
		var edges []recPosting
		if err = base.recSimilar(func(work int64, weight float64) { edges = append(edges, recPosting{work, weight}) }); err != nil {
			return err
		}
		if len(edges) != 1 || edges[0].value != 2 {
			t.Fatal("seed fixture did not resolve")
		}
		if err = base.hydrateScored([]*recScored{{work: w.scifi[4].ID, quality: -1}}); err != nil {
			return err
		}
		title, err := base.s.recTitleSession(base, w.romance[0].ID)
		if err != nil {
			return err
		}
		if title.hydration != base.hydration {
			t.Fatal("same snapshot title discarded immutable catalogue inputs")
		}
		title.idf["test-only"] = 123
		if _, exists := base.idf["test-only"]; exists {
			t.Fatal("title IDF map changed viewer IDF map")
		}
		ctx, cost := dbwork.Measure(ctx)
		title.s = base.s.WithContext(ctx)
		edges = nil
		if err = title.recSimilar(func(work int64, weight float64) { edges = append(edges, recPosting{work, weight}) }); err != nil {
			return err
		}
		if cost().Statements != 1 || len(edges) != 1 || math.Abs(edges[0].value-1/1.15) > 1e-12 {
			t.Fatal("title reused viewer seed weights/edges")
		}
		ctx, cost = dbwork.Measure(ctx)
		title.s = base.s.WithContext(ctx)
		if err = title.recSimilar(func(int64, float64) {}); err != nil {
			return err
		}
		if cost().Statements != 0 {
			t.Fatal("exact seed edges were reread")
		}
		title.taste.seeds[0].weight = 3
		edges = nil
		if err = title.recSimilar(func(work int64, weight float64) { edges = append(edges, recPosting{work, weight}) }); err != nil {
			return err
		}
		if len(edges) != 1 || math.Abs(edges[0].value-3/1.15) > 1e-12 {
			t.Fatal("different seed weight reused old edges")
		}
		// Cached candidates still need the new title's own rarity/IDF map.
		title.idf = map[string]float64{}
		if err = title.hydrateScored([]*recScored{{work: w.scifi[4].ID, quality: -1}}); err != nil {
			return err
		}
		for _, facet := range title.hydration.works[w.scifi[4].ID].facets {
			if _, exists := title.idf[facet]; !exists {
				t.Fatal("cached metadata omitted title's independent facet rarity")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
