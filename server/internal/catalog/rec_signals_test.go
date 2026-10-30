package catalog

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestRecLargeProfileSignalsStayCandidateSized(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	w.watch("p", w.scifi[0])
	w.c.Drain()
	w.c.Exec(`WITH RECURSIVE ids(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM ids WHERE i<100000) INSERT INTO rec_profile_signals(profile_id,work_id,weight,engaged,hidden,finished,at,long,short) SELECT 'p',900000000+i,0,1,1,0,'2026-01-01T00:00:00Z',0,0 FROM ids`)
	w.c.Exec(`WITH RECURSIVE ids(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM ids WHERE i<100000) INSERT INTO rec_profile_jobs(profile_id,work_id) SELECT 'p',900000000+i FROM ids`)
	fenceStart := time.Now()
	fence, err := w.s.recBrowseFence("p")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("recBrowseFence 100k pending jobs: %s; fence=%s", time.Since(fenceStart), fence)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	taste, err := w.s.recLoadTaste("p", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	elapsed := time.Since(start)
	if !taste.bulkPending || len(taste.engaged) != 0 || len(taste.hidden) != 0 {
		t.Fatalf("history hydrated before candidate selection: engaged=%d hidden=%d bulk=%v", len(taste.engaged), len(taste.hidden), taste.bulkPending)
	}
	if err := w.s.recLoadSignals("p", &taste, []int64{w.scifi[0].ID, 900100000, 800000000}); err != nil {
		t.Fatal(err)
	}
	if !taste.engaged[w.scifi[0].ID] || taste.hidden[w.scifi[0].ID] {
		t.Fatal("stored watched signal changed")
	}
	if !taste.engaged[900100000] || !taste.hidden[900100000] {
		t.Fatal("a job beyond the overlay limit was not excluded")
	}
	if taste.engaged[800000000] || taste.hidden[800000000] {
		t.Fatal("a work with no signal was excluded")
	}
	if len(taste.loaded) != 3 || len(taste.engaged) != 3 || len(taste.hidden) != 3 {
		t.Fatalf("candidate maps escaped their set: %+v", taste.loaded)
	}

	t.Logf("recLoadTaste, 100k signals +100k jobs: %s, allocation=%d; candidate hydration then loaded engaged=%d hidden=%d", elapsed, after.TotalAlloc-before.TotalAlloc, len(taste.engaged), len(taste.hidden))
}

// Overlays can turn a stored true flag back off. Lazy candidate hydration must
// not restore an old watch/dislike over the worker's exact pending computation.
func TestRecSignalHydrationPreservesExactRetractions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	work := w.scifi[0]
	w.personal("p", work, "watched", 1)
	w.personal("p", work, "not_interested", 1)
	w.c.Drain()
	w.personal("p", work, "watched", 0)
	w.personal("p", work, "not_interested", 0)
	taste, err := w.s.recLoadTaste("p", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !taste.pending || taste.bulkPending {
		t.Fatal("small backlog did not use the exact overlay")
	}
	if err := w.s.recLoadSignals("p", &taste, []int64{work.ID}); err != nil {
		t.Fatal(err)
	}
	if taste.engaged[work.ID] || taste.hidden[work.ID] {
		t.Fatal("stored flags overrode an exact retraction")
	}
}

func TestRecMoreLikeMemoStillPlacesWatchedLast(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	w.watch("p", w.scifi[0])
	w.personal("p", w.scifi[1], "not_interested", 1)
	w.c.Drain()
	r := HomeRequest{Profile: "p", Libraries: []string{"m"}, Now: time.Now()}
	for pass := 0; pass < 2; pass++ {
		base, err := w.s.recSession(r)
		if err != nil {
			t.Fatal(err)
		}
		if len(base.taste.loaded) != 0 {
			t.Fatal("new session hydrated full profile history")
		}
		ranked, err := w.s.recMoreLike(base, w.scifi[2].ID)
		if err != nil {
			t.Fatal(err)
		}
		if !rankedHas(ranked, w.scifi[0].Public) || rankedHas(ranked, w.scifi[1].Public) {
			t.Fatalf("pass%d: watched/hidden title semantics changed", pass)
		}
		seen := false
		for _, c := range ranked {
			id := recCandidateWorks([]recCandidate{c})[0]
			if base.taste.engaged[id] {
				seen = true
			} else if seen {
				t.Fatalf("pass%d: unwatched title follows watched title", pass)
			}
		}
	}
}

func TestRecNextBookLoadsSeriesSignals(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	library := w.c.Library("books", "Books", "audiobook", "/books")
	public := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		book := w.c.Book(library, fmt.Sprintf("Book %d", i), "Author")
		part := w.c.BookFile(book, 1, fmt.Sprintf("/books/b%d.mp3", i))
		var token string
		if err := w.c.DB.QueryRow(`SELECT a.token FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=?`, part.ID).Scan(&token); err != nil {
			t.Fatal(err)
		}
		w.c.Exec(`INSERT INTO audio_tag_evidence(library_id,asset_id,field,source,value) VALUES('books',?,'series','embedded','Story'),('books',?,'series_index','embedded',?)`, token, token, fmt.Sprint(i+1))
		public = append(public, book.Public)
		if i == 0 {
			// Watched is the viewer's recorded intent, as marking a book finished writes it.
			w.c.Exec(`INSERT INTO personal_watched_intents(profile_id,item_id,watched,authored_at) VALUES('p',?,1,'2026-09-01T00:00:00.000000000Z')`, book.ID)
		}
		if i == 1 {
			w.personal("p", book, "not_interested", 1)
		}
	}
	w.c.Drain()
	x, err := w.s.recSession(HomeRequest{Profile: "p", Libraries: []string{"books"}, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	ranked, err := x.seriesRow()
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) != 1 || ranked[0].ID != public[2] {
		t.Fatalf("next book must skip watched/hidden books: %+v", ranked)
	}
}

func TestRecSignalsUseCandidateAndPositiveSeedIndexes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	plans := []struct {
		query string
		args  []any
		want  []string
	}{
		{recSeedSQL, []any{"p", recSeeds}, []string{"USING INDEX rec_profile_signals_positive_recent"}},
		{recSignalsSQL, []any{true, "p", idsJSON64([]int64{1, 2}), "p"}, []string{"SEARCH s USING PRIMARY KEY (profile_id=? AND work_id=?)", "SEARCH p USING PRIMARY KEY (profile_id=? AND work_id=?)"}},
	}
	for _, plan := range plans {
		rows, err := w.c.DB.Query("EXPLAIN QUERY PLAN "+plan.query, plan.args...)
		if err != nil {
			t.Fatal(err)
		}
		var details []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			details = append(details, detail)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(details, "\n")
		for _, want := range plan.want {
			if !strings.Contains(joined, want) {
				t.Fatalf("missing %q in plan:\n%s", want, joined)
			}
		}
		if strings.Contains(joined, "USE TEMP B-TREE") {
			t.Fatalf("seed lookup sorted profile history: %s", joined)
		}
	}
}

// Compare the lazy path against the former complete history/backlog view. The
// oracle reads every flag without a cap; only the production reader is bounded.
func TestRecCandidateHydrationMatchesCompleteHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	w.watch("p", w.scifi[0])
	w.watch("p", w.scifi[1])
	w.personal("p", w.scifi[2], "not_interested", 1)
	w.personal("p", w.scifi[3], "favorite", 1)
	w.c.Drain()
	r := HomeRequest{Profile: "p", Libraries: []string{"m"}, Now: time.Now()}
	for _, phase := range []string{"settled", "small overlay", "bulk overlay"} {
		t.Run(phase, func(t *testing.T) {
			if phase == "small overlay" {
				w.personal("p", w.scifi[2], "not_interested", 0)
				w.watch("p", w.scifi[4])
				w.personal("p", w.scifi[5], "not_interested", 1)
			}
			if phase == "bulk overlay" {
				w.c.Exec(`WITH RECURSIVE ids(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM ids WHERE i<1000) INSERT INTO rec_profile_jobs(profile_id,work_id) SELECT 'p',900000000+i FROM ids`)
			}
			lazy, err := w.s.recSession(r)
			if err != nil {
				t.Fatal(err)
			}
			eager, err := w.s.recSession(r)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := w.c.DB.Query(`SELECT work_id,engaged,hidden FROM rec_profile_signals WHERE profile_id='p'`)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var work int64
				var engaged, hidden bool
				if err := rows.Scan(&work, &engaged, &hidden); err != nil {
					t.Fatal(err)
				}
				if !eager.taste.loaded[work] {
					eager.taste.engaged[work], eager.taste.hidden[work], eager.taste.loaded[work] = engaged, hidden, true
				}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if eager.taste.bulkPending {
				rows, err := w.c.DB.Query(`SELECT work_id FROM rec_profile_jobs WHERE profile_id='p'`)
				if err != nil {
					t.Fatal(err)
				}
				for rows.Next() {
					var work int64
					if err := rows.Scan(&work); err != nil {
						t.Fatal(err)
					}
					eager.taste.engaged[work], eager.taste.hidden[work], eager.taste.loaded[work] = true, true, true
				}
				rows.Close()
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
			}
			for _, options := range []recOptions{{diversify: true}, {includeEngaged: true}, {works: []int64{w.scifi[0].ID, w.scifi[2].ID, w.scifi[4].ID, w.scifi[5].ID}, facets: []string{}, noFill: true, noSimilar: true, includeEngaged: true, anyMatch: true, all: true}} {
				got, err := lazy.rank(options)
				if err != nil {
					t.Fatal(err)
				}
				want, err := eager.rank(options)
				if err != nil {
					t.Fatal(err)
				}
				// Existing floating-point norms sum map entries in unspecified
				// order. Compare scores within rounding noise while requiring
				// every candidate, position, facet and other field to match.
				if len(got) == len(want) {
					for i := range got {
						if math.Abs(got[i].Score-want[i].Score) <= 1e-12 {
							got[i].Score = want[i].Score
						}
					}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("lazy ranking differs from complete history:\n got %+v\nwant %+v", got, want)
				}
			}
		})
	}
}

func TestRecForYouMemoStillPlacesWatchedLast(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	w.watch("p", w.scifi[0])
	w.personal("p", w.scifi[1], "not_interested", 1)
	w.c.Drain()
	p := pageShape{request: BrowseRequest{Profile: "p", Library: "m"}, where: "e.kind=1", total: len(w.scifi) + len(w.romance) + len(w.dramas) + len(w.others)}
	var first []personalRow
	for pass := 0; pass < 2; pass++ {
		ranked, err := w.s.recForYouValued(p)
		if err != nil {
			t.Fatal(err)
		}
		seen := false
		found := false
		for _, row := range ranked {
			if row.entity == w.scifi[1].ID {
				t.Fatal("hidden title enters the personalized ranking")
			}
			if row.entity == w.scifi[0].ID {
				seen = true
				found = true
			} else if seen {
				t.Fatalf("pass%d: unwatched title follows watched title", pass)
			}
		}
		if !found {
			t.Fatal("watched title disappeared from the browse ranking")
		}
		if pass == 0 {
			first = ranked
		} else if !reflect.DeepEqual(first, ranked) {
			t.Fatal("memo changes browse personalized ordering")
		}
	}
}

// A rating is kept from 0 to 10 and read as stars out of five: three stars is
// neutral, more adds to a title's weight and fewer takes away.
func TestRecSignalReadsRatingsAsStars(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	w := newRecWorld(t)
	for i, c := range []struct {
		rating int
		weight float64
	}{{10, 1.6}, {6, 0}, {2, -1.6}, {0, -2.4}} {
		work := w.romance[i]
		w.personal("p", work, "rating", c.rating)
		got, err := compactcatalog.RecSignalFor(context.Background(), w.c.DB, "p", work.ID, time.Now())
		if err != nil || math.Abs(got.Weight-c.weight) > 1e-9 || !got.Engaged {
			t.Fatalf("rating %d: weight %v, want %v (%v)", c.rating, got.Weight, c.weight, err)
		}
	}
}
