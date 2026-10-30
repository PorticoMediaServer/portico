package catalog

import (
	"context"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestRecHydrationSharesSourceReadsAndKeepsIndependentRowRankings(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	for _, work := range w.scifi[:3] {
		w.watch("p", work)
	}
	w.c.Drain()
	r := HomeRequest{Profile: "p", Libraries: []string{"m"}, Now: time.Now()}
	x, err := w.s.recSession(r)
	if err != nil {
		t.Fatal(err)
	}
	options := []recOptions{
		{diversify: true},
		x.facetRow("g:science fiction"),
		{noFill: true, diversify: true, keep: func(c *recScored) bool { return c.quality >= 7.2 && c.votes >= 20 && c.votes < 1500 }},
		{diversify: true, keep: func(c *recScored) bool { return c.quality >= 7.8 && c.votes >= 1500 }},
		{includeEngaged: true, anyMatch: true, diversify: true},
	}
	var sharedCost, independentCost int64
	for i, option := range options {
		independent, err := w.s.recSession(r)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cost := dbwork.Measure(context.Background())
		independent.s = w.s.WithContext(ctx)
		want, err := independent.rank(option)
		if err != nil {
			t.Fatal(err)
		}
		independentCost += cost().Statements
		ctx, cost = dbwork.Measure(context.Background())
		x.s = w.s.WithContext(ctx)
		got, err := x.rank(option)
		if err != nil {
			t.Fatal(err)
		}
		sharedCost += cost().Statements
		assertRecRankingEqual(t, i, got, want)
	}
	if sharedCost >= independentCost {
		t.Fatalf("overlapping rows did not reduce source reads: shared=%d independent=%d", sharedCost, independentCost)
	}
	t.Logf("same row rankings: shared source statements=%d, independent=%d", sharedCost, independentCost)
}

func assertRecRankingEqual(t *testing.T, row int, got, want []recCandidate) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("row %d size changed: got %d want %d", row, len(got), len(want))
	}
	for i := range got {
		if math.Abs(got[i].Score-want[i].Score) > 1e-12 {
			t.Fatalf("row %d position %d score changed: %.16g != %.16g", row, i, got[i].Score, want[i].Score)
		}
		got[i].Score = want[i].Score
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("row %d position %d changed: %+v != %+v", row, i, got[i], want[i])
		}
	}
}

func TestRecHydrationBoundedMissingReadsAndFreshSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	r := HomeRequest{Profile: "p", Libraries: []string{"m"}, Now: time.Now()}
	x, err := w.s.recSession(r)
	if err != nil {
		t.Fatal(err)
	}
	known := w.scifi[4].ID
	ctx, cost := dbwork.Measure(context.Background())
	x.s = w.s.WithContext(ctx)
	first := []*recScored{{work: known, quality: -1}, {work: 900000000, quality: 8}}
	if err = x.recRescore(first, 0); err != nil {
		t.Fatal(err)
	}
	if len(x.hydration.works) != 2 || first[1].quality != 8 || first[1].votes != 0 || first[1].facets != nil {
		t.Fatal("absent posting/rating behavior changed or whole catalogue hydrated")
	}
	ctx, cost = dbwork.Measure(context.Background())
	x.s = w.s.WithContext(ctx)
	second := []*recScored{{work: known, quality: -1}, {work: 900000000, quality: 9}}
	if err = x.recRescore(second, 0); err != nil {
		t.Fatal(err)
	}
	if cost().Statements != 0 || second[1].quality != 9 || !reflect.DeepEqual(first[0].facets, second[0].facets) {
		t.Fatalf("already hydrated source read again or missing quality replaced: statements=%d", cost().Statements)
	}
	w.c.Exec(`INSERT INTO metadata_ratings(item_id,provider,value,scale,votes,source_url,observed_at) VALUES(?,'tmdb',8,10,987,'','2026-01-01T00:00:00Z')`, known)
	fresh, err := w.s.recSession(r)
	if err != nil {
		t.Fatal(err)
	}
	current := []*recScored{{work: known, quality: -1}}
	if err = fresh.recRescore(current, 0); err != nil {
		t.Fatal(err)
	}
	if current[0].votes != 987 || len(fresh.hydration.works) != 1 {
		t.Fatal("a new request reused prior source metadata")
	}
}

func TestRecTitleSessionDoesNotReuseViewerSeedEdges(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	w.watch("p", w.scifi[0])
	w.c.Drain()
	r := HomeRequest{Profile: "p", Libraries: []string{"m"}, Now: time.Now()}
	base, err := w.s.recSession(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = base.rank(recOptions{diversify: true}); err != nil {
		t.Fatal(err)
	}
	if len(base.hydration.similar) == 0 || len(base.hydration.works) == 0 {
		t.Fatal("test did not hydrate viewer session")
	}
	title, err := w.s.recTitleSession(base, w.romance[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if title.hydration.similar != nil || title.hydration.works != nil || title.hydration.postings != nil {
		t.Fatal("title taste retained viewer source hydration")
	}
	independent, err := w.s.recSession(r)
	if err != nil {
		t.Fatal(err)
	}
	independentTitle, err := w.s.recTitleSession(independent, w.romance[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := title.rank(recOptions{anyMatch: true, diversify: true})
	if err != nil {
		t.Fatal(err)
	}
	want, err := independentTitle.rank(recOptions{anyMatch: true, diversify: true})
	if err != nil {
		t.Fatal(err)
	}
	assertRecRankingEqual(t, 0, got, want)
}
