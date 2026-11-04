package catalog

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// The original production query is retained as the before measurement and
// small-library parity oracle, against the very same normalized fixture.
func originalMoreLikeOrder(s *Service, viewer Viewer, work int64) ([]int64, error) {
	visible, args := viewer.entityVisibilitySQL("i.id", "l.library_id")
	visible += ` AND NOT EXISTS(SELECT 1 FROM personal_items negative WHERE negative.item_id=i.id AND negative.profile_id=? AND (negative.not_interested=1 OR negative.rating<=4))`
	args = append(args, viewer.Profile)
	return scanInt64s(s.read().Query(`SELECT id FROM (SELECT i.id,count(DISTINCT seed.facet) score,max(candidate.quality) quality FROM catalog_rec_facets seed
 JOIN catalog_rec_postings candidate ON candidate.facet=seed.facet JOIN catalog_entities i ON i.id=candidate.entity_id JOIN catalog_libraries l ON l.id=i.library_id
 WHERE seed.entity_id=? AND i.library_id=(SELECT library_id FROM catalog_entities WHERE id=?) AND i.id<>? AND `+visible+` GROUP BY i.id) ORDER BY score DESC,quality DESC,id`, append([]any{work, work, work}, args...)...))
}

func measureMoreLike(t *testing.T, s *Service, viewer Viewer, work int64, large bool) *relationOrdered {
	t.Helper()
	ctx, beforeTrace := dbwork.TraceQueryPlans(context.Background())
	at := time.Now()
	before, err := originalMoreLikeOrder(s.WithContext(ctx), viewer, work)
	beforeTime := time.Since(at)
	if err != nil {
		t.Fatal(err)
	}
	ctx, afterTrace := dbwork.TraceQueryPlans(context.Background())
	at = time.Now()
	after, err := s.WithContext(ctx).moreLikeOrder(viewer, work)(1)
	afterTime := time.Since(at)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("more-like fixture large=%v before_statements=%d before_elapsed=%s before_titles=%d after_statements=%d after_elapsed=%s after_titles=%d", large, len(beforeTrace()), beforeTime, len(before), len(afterTrace()), afterTime, after.len())
	if !after.complete {
		t.Fatal("complete describes the selected ranked set")
	}
	if large {
		if len(before) <= recRanked || after.len() == 0 || after.len() > recRanked {
			t.Fatal("large ranked set bound", len(before), after.len())
		}
	} else if !reflect.DeepEqual(after.ids, before) {
		t.Fatal("small global order changed", before, after.ids)
	}
	for _, query := range afterTrace() {
		if strings.Contains(query, "count(DISTINCT seed.facet)") && !strings.Contains(query, "FROM json_each(?) requested") {
			t.Error("full scorer has no bounded candidate keys")
		}
	}
	return after
}

func TestMoreLikeSmallLibraryRetainsExactOrder(t *testing.T) {
	db, names := compactRecommendationFixture(t)
	c := catalogtest.New(t, db)
	library := c.Handle("movies")
	next := c.Movie(library, "/movies/next.mkv", "Next", 2001)
	c.Genres(next.ID, "tmdb", "Animation")
	last := c.Movie(library, "/movies/last.mkv", "Last", 2022)
	c.Genres(last.ID, "tmdb", "Animation")
	other := c.Library("other", "Other", "movie", "/other")
	outside := c.Movie(other, "/other/outside.mkv", "Outside", 2001)
	c.Genres(outside.ID, "tmdb", "Animation")
	c.Drain()
	s := New(db)
	viewer := Viewer{Profile: "p", Libraries: []string{"movies", "other"}}
	got := measureMoreLike(t, s, viewer, names["m1"].ID, false)
	if !reflect.DeepEqual(got.ids, []int64{names["m2"].ID, next.ID, last.ID}) {
		t.Fatal("small fixture", got.ids)
	}
	// Recomputing after cache eviction must keep the same prefix too.
	again, err := New(db).moreLikeOrder(viewer, names["m1"].ID)(1000)
	if err != nil || !reflect.DeepEqual(again.ids, got.ids) {
		t.Fatal("ranking depends on requested page depth", err)
	}
}

func checkLargeMoreLikePages(t *testing.T, c *catalogtest.Catalog, s *Service, viewer Viewer, work int64) {
	t.Helper()
	pool, large, err := s.moreLikeCandidates(work)
	if err != nil || !large || len(pool) == 0 || len(pool) > recRescore {
		t.Fatal("full rescore keys not bounded", len(pool), large, err)
	}
	order := measureMoreLike(t, s, viewer, work, true)
	spec := relationSpec{id: "more_like:local:test", relation: "more_like", caching: relationRevision, order: s.moreLikeOrder(viewer, work)}
	owner := relationOwner{kind: "item", id: c.Public(work), library: "films", itemKind: "movie"}
	want := []string{}
	for _, id := range order.ids {
		want = append(want, c.Public(id))
	}
	got := []string{}
	cursor := ""
	firstNext := ""
	var second HomeRow
	for n := 0; n <= recRanked; n++ {
		page, err := s.relationPage(viewer, owner, spec, cursor, 37)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total == nil || *page.Total != len(want) {
			t.Fatal("total is not selected-set size", page.Total)
		}
		for _, entry := range page.Entries {
			got = append(got, entry.ID)
		}
		if n == 0 {
			firstNext = page.NextCursor
		}
		if n == 1 {
			second = page
		}
		cursor = page.NextCursor
		if !page.HasMore {
			if cursor != "" {
				t.Fatal("terminal cursor")
			}
			break
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("ranked-set paging repeated or skipped titles", len(got), len(want))
	}
	replay, err := s.relationPage(viewer, owner, spec, firstNext, 37)
	if err != nil || !reflect.DeepEqual(replay.Entries, second.Entries) {
		t.Fatal("unchanged continuation moved", err)
	}
	// The existing cache must hold this order, within its original byte budget.
	if order.cost() > relationOrderBytes/4 {
		t.Fatal("selected order cannot fit cache", order.cost())
	}
	again, err := New(c.DB).moreLikeOrder(viewer, work)(recRanked + 1)
	if err != nil || !reflect.DeepEqual(again.ids, order.ids) {
		t.Fatal("evicted ranking reshuffled", err)
	}
}
