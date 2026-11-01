package catalog

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func newServerPairs(t *testing.T, s *Service, r HomeRequest) ([][2]string, int) {
	t.Helper()
	src, err := s.homeNewServerSource(r.scoped())
	if err != nil {
		t.Fatal(err)
	}
	if src.total == nil || src.count != "" || src.walk != nil {
		t.Fatal("bounded shelf enabled a full count or fallback")
	}
	rows, err := s.read().Query(`SELECT id,ord FROM (`+src.base+`) ORDER BY ord DESC,id DESC`, src.args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := [][2]string{}
	for rows.Next() {
		var pair [2]string
		if err = rows.Scan(&pair[0], &pair[1]); err != nil {
			t.Fatal(err)
		}
		out = append(out, pair)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out, *src.total
}

func TestHomeNewServerCapsEveryPageAndTotalAt500(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("movies", "Films", "movie", "/movies")
	for i := 0; i < 610; i++ {
		path := fmt.Sprintf("/movies/%04d.mkv", i)
		item := c.Entity(compactcatalog.Entity{Library: lib, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/movies", path, 0), Title: fmt.Sprintf("Film %04d", i), Added: fmt.Sprintf("2026-10-01T00:%02d:%02d.000Z", i/60, i%60)}, nil)
		c.File(item.ID, path, 5400)
	}
	c.Drain()
	s := New(c.DB)
	r := HomeRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"movies"}}}.scoped()
	got, total := newServerPairs(t, s, r)
	if len(got) != 500 || total != 500 {
		t.Fatal("cap", len(got), total)
	}
	// The small fixture's old full query is an independent reference oracle.
	old, err := s.homeRecentPagedSource(r, false)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.read().Query(`SELECT id,ord FROM (`+old.base+`) ORDER BY ord DESC,id DESC LIMIT 500`, old.args...)
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]string{}
	for rows.Next() {
		var x [2]string
		if err = rows.Scan(&x[0], &x[1]); err != nil {
			t.Fatal(err)
		}
		want = append(want, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatal("capped shelf differs from newest 500")
	}
	ids := []string{}
	cursor := ""
	for page := 0; page < 12; page++ {
		row, e := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 50, Cursor: cursor})
		if e != nil {
			t.Fatal(e)
		}
		if row.Total == nil || *row.Total != 500 {
			t.Fatal("page total", row.Total)
		}
		for _, entry := range row.Entries {
			ids = append(ids, entry.ID)
		}
		cursor = row.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(ids) != 500 || cursor != "" {
		t.Fatal("paging escaped cap", len(ids), cursor)
	}
	for i, id := range ids {
		if id != want[i][0] {
			t.Fatalf("page %d: %s != %s", i, id, want[i][0])
		}
	}
}

func TestHomeNewServerPendingUsesPublishedOrderAndBoundedTotal(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	movies := c.Library("movies", "Films", "movie", "/movies")
	show := c.Show(tv, "Show", 2026)
	season := c.Season(show, 1)
	ep := c.Episode(show, season, 1, "/tv/one.mkv")
	c.Fields(ep.ID, map[string]any{"added_text": "2026-03-01T00:00:00.000Z"})
	film := c.Entity(compactcatalog.Entity{Library: movies, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/movies", "/movies/one.mkv", 0), Title: "Film", Added: "2026-02-01T00:00:00.000Z"}, nil)
	c.File(film.ID, "/movies/one.mkv", 5400)
	c.Drain()
	// A published January show head trails its new March member while the
	// bounded container derivation is queued. Readers must not run the full
	// current relation to discover that March addition.
	c.Exec(`UPDATE catalog_browse_rows SET recent_text='2026-01-01T00:00:00.000Z' WHERE entity_id=?`, show.ID)
	c.Exec(`INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(?,?,1) ON CONFLICT(domain,entity_id) DO UPDATE SET revision=revision+1`, compactcatalog.DomainContainerRefresh, show.ID)
	ctx, plans := dbwork.TraceQueryPlans(context.Background())
	ctx, cost := dbwork.Measure(ctx)
	s := New(c.DB).WithContext(ctx)
	r := HomeRequest{Viewer: Viewer{Profile: "p", Libraries: []string{"tv", "movies"}}}.scoped()
	got, total := newServerPairs(t, s, r)
	if total != 2 || len(got) != 2 || got[0][0] != film.Public || got[1][0] != show.Public || got[1][1] != "2026-01-01T00:00:00.000Z" {
		t.Fatal("pending order/total", got, total)
	}
	for _, q := range plans() {
		if strings.Contains(q, "GROUP BY work") || strings.Contains(q, "count(*)") {
			t.Fatal("whole-library fallback", q)
		}
	}
	if steps := cost().VMSteps; steps > 20000 {
		t.Fatalf("pending shelf exceeded bounded fixture VM budget: %d", steps)
	}
	// Publication order is authoritative in both directions while pending:
	// even a current member date below the published date must not rerank it.
	c.Exec(`UPDATE catalog_browse_rows SET recent_text='2026-04-01T00:00:00.000Z' WHERE entity_id=?`, show.ID)
	got, total = newServerPairs(t, s, r)
	if total != 2 || got[0][0] != show.Public || got[0][1] != "2026-04-01T00:00:00.000Z" {
		t.Fatal("pending publication was reranked from current metrics", got, total)
	}
	c.Drain()
	got, total = newServerPairs(t, s, r)
	if total != 2 || got[0][0] != show.Public || got[0][1] != "2026-03-01T00:00:00.000Z" {
		t.Fatal("settled order", got, total)
	}
	// Current availability applies even while publication is pending.
	c.Exec(`UPDATE catalog_item_availability SET available=0 WHERE entity_id=?`, ep.ID)
	got, total = newServerPairs(t, s, r)
	if total != 1 || got[0][0] != film.Public {
		t.Fatal("unavailable published work", got, total)
	}
}

func TestHomeNewServerExhaustedBudgetKeepsOnlyCompletedGroups(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("movies", "Films", "movie", "/movies")
	film := c.Entity(compactcatalog.Entity{Library: lib, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/movies", "/movies/one.mkv", 0), Title: "Film", Added: "2026-02-01T00:00:00.000Z"}, nil)
	c.File(film.ID, "/movies/one.mkv", 5400)
	c.Drain()
	r := HomeRequest{Viewer: Viewer{Profile: "p", Libraries: []string{"movies"}}}.scoped()
	for _, which := range []string{"edition", "member"} {
		t.Run(which, func(t *testing.T) {
			prior := &homeWalkGroup{id: "completed", ord: "2026-01-01"}
			w := homeNewServerWalk{s: New(c.DB), r: r, settled: true, candidates: homeWalkBudget, editions: homeWalkBudget, members: homeWalkBudget, seen: map[int64]bool{}, groups: map[string]*homeWalkGroup{"prior": prior}}
			if which == "edition" {
				w.editions = 0
			} else {
				w.members = 0
			}
			complete, err := w.add([]int64{film.ID})
			if err != nil || complete || len(w.groups) != 1 || w.groups["prior"] != prior {
				t.Fatal("budget fallback or partial group", complete, err, w.groups)
			}
		})
	}
}

// Publication may trail availability changes; projection must not turn a
// currently authorized show membership into an unavailable episode card.
func TestHomeNewServerDeliveryChecksCurrentAvailability(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "Show", 2026)
	season := c.Season(show, 1)
	old := c.Episode(show, season, 1, "/tv/old.mkv")
	latest := c.Episode(show, season, 2, "/tv/new.mkv")
	c.Fields(old.ID, map[string]any{"added_text": "2026-01-01T00:00:00.000Z"})
	c.Fields(latest.ID, map[string]any{"added_text": "2026-02-01T00:00:00.000Z"})
	c.Drain()
	c.Exec(`UPDATE catalog_item_availability SET available=0 WHERE entity_id=?`, latest.ID)
	r := HomeRequest{Viewer: Viewer{Profile: "p", Libraries: []string{"tv"}}}.scoped()
	targets, err := New(c.DB).homeRecentPageTargets(r, []string{show.Public})
	if err != nil || !slices.Equal(targets, []string{old.Public}) {
		t.Fatal("unavailable episode delivery", targets, err)
	}
}
