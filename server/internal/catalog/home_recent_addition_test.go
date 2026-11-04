package catalog

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Recently Added in Plex's shape: a show stands for what was added to it. One
// new episode reads as that episode, a season delivered together as the
// season, and several seasons at once as the show with its season count.
func recentDeliveryFixture(t *testing.T) (*catalogtest.Catalog, []string, []string) {
	t.Helper()
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV Shows", "tv", "/tv")
	n := 0
	episode := func(show, season catalogtest.Item, number int, added string) catalogtest.Item {
		n++
		path := fmt.Sprintf("/tv/%03d.mkv", n)
		it := c.Entity(compactcatalog.Entity{Library: tv, Kind: compactcatalog.Episode, Parent: season.ID, Key: compactcatalog.ItemKey("/tv", path, 0), Title: fmt.Sprintf("Episode %d", number), Added: added},
			map[string]any{"show_id": show.ID, "season_id": season.ID, "numbering": "seasonal", "number": number})
		c.File(it.ID, path, 2700)
		return it
	}
	// Weekly: three episodes in January, the fourth a week later.
	weekly := c.Show(tv, "Weekly", 2020)
	weeklyOne := c.Season(weekly, 1)
	for i := 1; i <= 3; i++ {
		episode(weekly, weeklyOne, i, fmt.Sprintf("2026-01-01T00:00:0%d.000Z", i))
	}
	newest := episode(weekly, weeklyOne, 4, "2026-01-08T20:00:00.000Z")
	// Season pack: season 1 in January, all of season 2 together in February.
	pack := c.Show(tv, "Season Pack", 2021)
	packOne, packTwo := c.Season(pack, 1), c.Season(pack, 2)
	episode(pack, packOne, 1, "2026-01-02T00:00:00.000Z")
	episode(pack, packOne, 2, "2026-01-02T00:00:01.000Z")
	for i := 1; i <= 3; i++ {
		episode(pack, packTwo, i, fmt.Sprintf("2026-02-01T00:00:0%d.000Z", i))
	}
	// Whole show: two seasons arriving in one scan.
	whole := c.Show(tv, "Whole Show", 2022)
	wholeOne, wholeTwo := c.Season(whole, 1), c.Season(whole, 2)
	for i := 1; i <= 2; i++ {
		episode(whole, wholeOne, i, fmt.Sprintf("2026-03-01T00:00:0%d.000Z", i))
		episode(whole, wholeTwo, i, fmt.Sprintf("2026-03-01T00:00:1%d.000Z", i))
	}
	c.Drain()
	return c, []string{whole.Public, pack.Public, weekly.Public}, []string{whole.Public, packTwo.Public, newest.Public}
}

func TestRecentlyAddedNamesTheEpisodeOrSeasonThatArrived(t *testing.T) {
	c, works, targets := recentDeliveryFixture(t)
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Libraries: []string{"tv"}}
	for _, rowID := range []string{"new_on_server", "recent_tv"} {
		t.Run(rowID, func(t *testing.T) {
			row, err := s.homeRowFor(HomeRequest{Viewer: viewer, ServerID: "server", Profile: "p", Libraries: []string{"tv"}}.scoped(), homeRowSpec{ID: rowID, LibraryID: "tv", Direction: "desc"}, ContentRevision{}, HomeRowPage{Limit: 12}, false, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(row.Entries) != 3 {
				t.Fatalf("recently added: %d entries, want 3", len(row.Entries))
			}
			got := row.Entries
			if got[0].Kind != "show" || got[0].ID != works[0] || got[0].SeasonCount == nil || *got[0].SeasonCount != 2 {
				t.Fatalf("several seasons at once read as the show with its seasons: %+v", got[0])
			}
			if got[1].Kind != "season" || got[1].ID != targets[1] || got[1].Subtitle != "Season Pack" || got[1].SeasonNumber == nil || *got[1].SeasonNumber != 2 || got[1].Navigation == nil || got[1].Navigation.View != "season" {
				t.Fatalf("a season delivered together reads as the season under its show: %+v", got[1])
			}
			if got[2].Kind != "episode" || got[2].ID != targets[2] || got[2].Subtitle != "Weekly" || got[2].EpisodeNumber == nil || *got[2].EpisodeNumber != 4 {
				t.Fatalf("one new episode reads as that episode: %+v", got[2])
			}
		})
	}
	discovery, err := s.Discover("tv", "p")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, section := range discovery.Sections {
		if section.ID != "recently_added" {
			continue
		}
		found = true
		ids := []string{}
		for _, entry := range section.Items {
			ids = append(ids, entry.ID)
		}
		if !slices.Equal(ids, works) {
			t.Fatalf("Discover whole-work order changed: %v want%v", ids, works)
		}
	}
	if !found {
		t.Fatal("Discover lost recent deliveries")
	}

}

func TestHomeRecentDeliveryAnchorAndCursorKeepRealTargets(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "Delivered", 2026)
	season := c.Season(show, 2)
	one := c.Episode(show, season, 1, "/tv/one.mkv")
	two := c.Episode(show, season, 2, "/tv/two.mkv")
	c.Fields(one.ID, map[string]any{"added_text": "2026-10-20T00:00:01.000Z"})
	c.Fields(two.ID, map[string]any{"added_text": "2026-10-20T00:00:02.000Z"})
	older := c.Show(tv, "Older", 2025)
	olderSeason := c.Season(older, 1)
	oldEpisode := c.Episode(older, olderSeason, 1, "/tv/old.mkv")
	c.Fields(oldEpisode.ID, map[string]any{"added_text": "2025-10-20T00:00:01.000Z"})
	c.Drain()
	s := New(c.DB)
	r := HomeRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"tv"}}}
	first, err := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 1})
	if err != nil || len(first.Entries) != 1 || first.Entries[0].ID != season.Public || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	next, err := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(next.Entries) != 1 || next.Entries[0].ID != oldEpisode.Public || next.NextCursor != "" {
		t.Fatal(next, err)
	}
	anchored, err := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 1, AnchorID: oldEpisode.Public})
	if err != nil || anchored.Start != 1 || len(anchored.Entries) != 1 || anchored.Entries[0].ID != oldEpisode.Public {
		t.Fatal("delivery anchor lost its canonical work", anchored, err)
	}
	if len(first.DisplayTargets) != 1 || first.DisplayTargets[0].MembershipID != show.Public || first.DisplayTargets[0].EntryID != season.Public {
		t.Fatal("missing Home-origin mapping", first.DisplayTargets)
	}
	projected, err := s.ProjectHomeRowDisplayTargets(r, "new_on_server", []string{show.Public, older.Public})
	if err != nil || len(projected) != 2 || projected[0].MembershipID != show.Public || projected[0].Entry.ID != season.Public || projected[1].Entry.ID != oldEpisode.Public {
		t.Fatal("Home See all projection", projected, err)
	}
	r.Viewer.Libraries = nil
	if _, err = s.ProjectHomeRowDisplayTargets(r, "new_on_server", []string{show.Public}); !errors.Is(err, ErrStaleContinuation) {
		t.Fatal("projection admitted a revoked membership", err)
	}
}

// The existing mixed delivery fixture exercises episode, season and show
// projection. Cost includes source construction, a real 12-card SQL page and
// delivery projection, but excludes fixture construction and card hydration.
func TestRecentlyAddedResolvesOnlyThePageInOneBatch(t *testing.T) {
	c, works, targets := recentDeliveryFixture(t)
	r := HomeRequest{Viewer: Viewer{Profile: "p", Libraries: []string{"tv"}}}.scoped()
	measure := func(pageSize int) (int64, time.Duration, []string) {
		t.Helper()
		ctx, plans := dbwork.TraceQueryPlans(context.Background())
		ctx, cost := dbwork.Measure(ctx)
		s := New(c.DB).WithContext(ctx)
		started := time.Now()
		src, err := s.homeRecentSource(r, "tv")
		if err != nil {
			t.Fatal(err)
		}
		rows, err := s.read().Query(`SELECT id FROM (`+src.base+`) ORDER BY ord DESC,id DESC LIMIT ?`, append(src.args, pageSize)...)
		if err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		pageIDs := append([]string{}, ids...)
		ids, err = s.homeRecentPageTargets(r, ids)
		if err != nil {
			t.Fatal(err)
		}
		elapsed, statements := time.Since(started), cost().Statements
		for _, query := range plans() {
			if !strings.Contains(query, "m.position,m.item_id") {
				continue
			}
			_, args := r.Viewer.itemVisibilitySQL("m.item_id")
			bound := []any{}
			for _, id := range pageIDs {
				bound = append(bound, id)
			}
			plan, err := c.DB.Query(`EXPLAIN QUERY PLAN `+query, append(bound, args...)...)
			if err != nil {
				t.Fatal(err)
			}
			details := []string{}
			for plan.Next() {
				var id, parent, unused int
				var detail string
				if err = plan.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				details = append(details, detail)
			}
			err = plan.Err()
			plan.Close()
			if err != nil {
				t.Fatal(err)
			}
			text := strings.Join(details, "; ")
			if strings.Contains(text, "SCAN r USING INDEX catalog_browse_metric_added") || !strings.Contains(text, "catalog_browse_metric_added (entity_id=?") {
				t.Fatal("delivery batch lost bounded indexed seeks", text)
			}
		}
		return statements, elapsed, ids
	}
	one, oneTime, gotOne := measure(1)
	all, allTime, gotAll := measure(12)
	t.Logf("existing mixed delivery fixture: page1=%d statements %s; page12=%d statements %s", one, oneTime, all, allTime)
	if !slices.Equal(gotOne, targets[:1]) || !slices.Equal(gotAll, targets) {
		t.Fatalf("delivery order changed: %v / %v; canonical=%v", gotOne, gotAll, works)
	}
	if one != 4 || all != 4 {
		t.Fatalf("delivery resolution still scales with source/page shows: page1=%d page12=%d, want4 each", one, all)
	}
}

func TestRecentlyAddedEqualDatesUseStableWorkIDs(t *testing.T) {
	c, works, targets := recentDeliveryFixture(t)
	for _, id := range works {
		c.Exec(`UPDATE catalog_browse_rows SET recent_text='2026-04-01T00:00:00.000Z' WHERE entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, id)
	}
	byWork := map[string]string{}
	for i, id := range works {
		byWork[id] = targets[i]
	}
	slices.Sort(works)
	slices.Reverse(works)
	s := New(c.DB)
	r := HomeRequest{Viewer: Viewer{Profile: "p", Libraries: []string{"tv"}}}
	row, err := s.homeRowFor(r.scoped(), homeRowSpec{ID: "recent_tv", LibraryID: "tv", Direction: "desc"}, ContentRevision{}, HomeRowPage{Limit: 12}, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(row.Entries) != len(works) {
		t.Fatal(row)
	}
	for i, entry := range row.Entries {
		if entry.ID != byWork[works[i]] || row.DisplayTargets[i].MembershipID != works[i] {
			t.Fatal("equal-date order depends on delivery target", row)
		}
	}
}

func TestRecentlyAddedBatchRechecksPolicyAndCurrentMembership(t *testing.T) {
	c, works, targets := recentDeliveryFixture(t)
	var episode int64
	var season string
	if err := c.DB.QueryRow(`SELECT e.entity_id,pid(se.public_id) FROM catalog_entities i JOIN catalog_episodes e ON e.entity_id=i.id JOIN catalog_entities se ON se.id=e.season_id WHERE i.public_id=pid_blob(?)`, targets[2]).Scan(&episode, &season); err != nil {
		t.Fatal(err)
	}
	c.Attributes(episode, "label", "hidden")
	c.Drain()
	r := HomeRequest{Viewer: Viewer{Profile: "p", Libraries: []string{"tv"}}}.scoped()
	r.Viewer.Restrictions.BlockedLabels = []string{"hidden"}
	r.Restrictions = r.Viewer.Restrictions
	s := New(c.DB)
	got, err := s.homeRecentPageTargets(r, works)
	// The hidden weekly episode cannot redirect the card. The earlier delivery
	// is its January season; unrelated shows retain their own delivery state.
	want := []string{targets[0], targets[1], season}
	if err != nil || !slices.Equal(got, want) {
		t.Fatal("current policy", got, want, err)
	}
	r.Viewer.Restrictions.BlockedLabels = nil
	r.Restrictions = r.Viewer.Restrictions
	// Leave a stale metric deliberately: current membership must win.
	c.Exec(`DELETE FROM catalog_browse_memberships WHERE entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND item_id=?`, works[2], episode)
	got, err = s.homeRecentPageTargets(r, works)
	if err != nil || !slices.Equal(got, want) {
		t.Fatal("stale member delivery", got, want, err)
	}
}
