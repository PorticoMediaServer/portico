package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func integrationReadPage(t *testing.T, c *catalogtest.Catalog, v Viewer, q IntegrationItemQuery, paths bool) IntegrationItemPage {
	t.Helper()
	q.IncludeFiles = true
	var page IntegrationItemPage
	if err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		var err error
		page, err = New(c.DB).WithContext(ctx).QueryIntegrationItems(v, q, paths)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return page
}

func TestIntegrationReadItemsCountFiltersAndProviderFacts(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("movies", "Movies", "movie", "/movies")
	hidden := c.Library("private", "Private", "movie", "/private")
	for i := 0; i < 7; i++ {
		c.Movie(lib, fmt.Sprintf("/movies/%d.mkv", i), fmt.Sprintf("Beacon %d", i), 2000+i)
	}
	c.Movie(hidden, "/private/hidden.mkv", "Beacon secret", 2000)
	tv := c.Library("tv", "Television", "tv", "/tv")
	show := c.Show(tv, "Beacon Show", 2020)
	season := c.Season(show, 1)
	ep := c.Episode(show, season, 1, "/tv/s01e01.mkv")
	c.Exec(`INSERT INTO catalog_external_ids(entity_id,provider,provider_kind,provider_id) VALUES(?,'tvdb','episode','9001')`, ep.ID)
	c.Drain()
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"movies", "tv"}}
	cursor := ""
	seen := map[string]bool{}
	for pageNumber := 0; pageNumber < 5; pageNumber++ {
		page := integrationReadPage(t, c, v, IntegrationItemQuery{LibraryID: "movies", Kind: "movie", Search: "Beacon", Cursor: cursor, Limit: 2}, false)
		if page.Total != 7 || len(page.Items) > 2 {
			t.Fatalf("inexact/big page: %+v", page)
		}
		for _, item := range page.Items {
			if seen[item.ID] || len(item.Paths) != 0 || len(item.MediaSources) != 1 || item.MediaSources[0].Path != "" {
				t.Fatalf("duplicate/path leak: %+v", item)
			}
			seen[item.ID] = true
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 7 || cursor != "" {
		t.Fatalf("walk not exhausted: %d %q", len(seen), cursor)
	}
	page := integrationReadPage(t, c, v, IntegrationItemQuery{Provider: "tvdb", ProviderID: "9001", Limit: 1}, true)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != ep.Public || page.Items[0].ProviderIDs["tvdb"] != "9001" || page.Items[0].Episode == nil || page.Items[0].Episode.SeasonID != season.Public || len(page.Items[0].Paths) != 1 {
		t.Fatalf("episode provider facts: %+v", page)
	}
	exact := integrationReadPage(t, c, v, IntegrationItemQuery{LibraryID: "movies", LibraryKind: "movie", Year: 2003, Search: "Beacon"}, false)
	if exact.Total != 1 || exact.Items[0].Year != 2003 {
		t.Fatalf("year/search: %+v", exact)
	}
	exact = integrationReadPage(t, c, v, IntegrationItemQuery{IDs: []string{ep.Public}, LibraryKind: "movie"}, false)
	if exact.Total != 0 {
		t.Fatalf("library kind ignored: %+v", exact)
	}
}

func TestIntegrationReadHierarchyIncludesEverySeason(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(lib, "Show", 2020)
	one := c.Season(show, 1)
	two := c.Season(show, 2)
	a := c.Episode(show, one, 1, "/tv/one.mkv")
	b := c.Episode(show, two, 1, "/tv/two.mkv")
	// Real unassigned_absolute row, not a seasonal episode with a blank ID.
	loose := c.Episode(show, one, 2, "/tv/loose.mkv")
	c.Exec(`UPDATE catalog_entities SET parent_id=? WHERE id=?`, show.ID, loose.ID)
	c.Exec(`UPDATE catalog_episodes SET season_id=NULL,numbering='absolute',number=3 WHERE entity_id=?`, loose.ID)
	c.Drain()
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"tv"}}
	page := integrationReadPage(t, c, v, IntegrationItemQuery{Kind: "episode", ParentID: show.Public}, false)
	if page.Total != 3 {
		t.Fatalf("assigned episodes dropped: %+v (%s %s)", page, a.Public, b.Public)
	}
	n := 2
	page = integrationReadPage(t, c, v, IntegrationItemQuery{Kind: "episode", ParentID: show.Public, SeasonNumber: &n}, false)
	if page.Total != 1 || page.Items[0].ID != b.Public || *page.Items[0].Episode.SeasonNumber != 2 {
		t.Fatalf("season number did not resolve native season: %+v", page)
	}
	page = integrationReadPage(t, c, v, IntegrationItemQuery{Kind: "season", ParentID: show.Public}, false)
	if page.Total != 2 || page.Items[0].Season == nil {
		t.Fatalf("seasons: %+v", page)
	}
}

func TestIntegrationReadRestrictionBeforeCountAndPaging(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	allowed := c.Movie(lib, "/m/g.mkv", "Allowed", 2000)
	blocked := c.Movie(lib, "/m/r.mkv", "Blocked", 2000)
	c.Attributes(allowed.ID, "contentRating", "G")
	c.Attributes(blocked.ID, "contentRating", "R")
	c.Drain()
	if _, err := New(c.DB).ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	age := 12
	v := Viewer{Profile: "child", Fence: "restriction", Libraries: []string{"m"}, Restrictions: identity.ContentRestrictions{MaximumAge: &age, BlockUnrated: true}}
	page := integrationReadPage(t, c, v, IntegrationItemQuery{LibraryID: "m", Kind: "movie", Limit: 1}, false)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != allowed.Public || page.NextCursor != "" {
		t.Fatalf("restricted rows leaked into count/page: %+v", page)
	}
	page = integrationReadPage(t, c, v, IntegrationItemQuery{IDs: []string{blocked.Public}}, false)
	if page.Total != 0 {
		t.Fatalf("explicit ID bypass: %+v", page)
	}
}

func TestIntegrationReadCursorBindingAndSourceExhaustion(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	movie := c.Movie(lib, "/m/main.mkv", "Film", 2000)
	c.Movie(lib, "/m/other.mkv", "Other", 2001)
	// Link existing scanned native assets as versions without creating fake URLs.
	for i := 0; i < 18; i++ {
		asset, _ := c.File(movie.ID, fmt.Sprintf("/m/v%d.mkv", i), 120)
		if i == 17 {
			c.Exec(`UPDATE catalog_assets SET width=3840,height=2160 WHERE id=?`, asset)
		}
	}
	c.Drain()
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	page := integrationReadPage(t, c, v, IntegrationItemQuery{IDs: []string{movie.Public}}, true)
	item := page.Items[0]
	if len(item.MediaSources) != 16 || item.MediaSourcesNextCursor == "" || item.Width == nil || item.Height == nil || *item.Width != 3840 || *item.Height != 2160 {
		t.Fatalf("inline truncation unmarked: %+v", item)
	}
	var info IntegrationMediaInfo
	err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		var e error
		info, e = New(c.DB).WithContext(ctx).IntegrationMediaInfo(v, movie.Public, item.MediaSourcesNextCursor, 16, true)
		return e
	})
	if err != nil || info.Total != 19 || len(info.MediaSources) != 3 || info.NextCursor != "" {
		t.Fatalf("source tail: %+v %v", info, err)
	}
	first := integrationReadPage(t, c, v, IntegrationItemQuery{LibraryID: "m", Kind: "movie", Limit: 1}, false)
	bad := IntegrationItemQuery{IncludeFiles: true, LibraryID: "m", Kind: "movie", Year: 2001, Limit: 1, Cursor: first.NextCursor}
	err = dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		_, e := New(c.DB).WithContext(ctx).QueryIntegrationItems(v, bad, false)
		return e
	})
	if !errors.Is(err, ErrCursor) {
		t.Fatalf("changed filter accepted cursor: %v", err)
	}
	if _, err = IntegrationIDs(strings.Repeat("x,", 100) + "x"); err == nil {
		t.Fatal("unbounded IDs accepted")
	}
}

func TestIntegrationReadLatestUsesBoundedIndexedRuns(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("movies", "Movies", "movie", "/movies")
	tv := c.Library("tv", "TV", "tv", "/tv")
	old := c.Movie(movies, "/movies/old.mkv", "Old", 2000)
	newest := c.Movie(movies, "/movies/new.mkv", "New", 2001)
	show := c.Show(tv, "Middle", 2002)
	season := c.Season(show, 1)
	episode := c.Episode(show, season, 1, "/tv/episode.mkv")
	for _, dated := range []struct {
		id   int64
		date string
	}{{old.ID, "2026-01-01T00:00:00.000Z"}, {newest.ID, "2026-01-03T00:00:00.000Z"}, {episode.ID, "2026-01-02T00:00:00.000Z"}} {
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return compactcatalog.SetFieldsTx(ctx, tx, dated.id, compactcatalog.Automatic, map[string]any{"added_text": dated.date})
		})
	}
	c.Drain()
	v := Viewer{Profile: "p", Fence: "latest", Libraries: []string{"movies", "tv"}}
	q := IntegrationItemQuery{Kind: "movie,show", Sort: "added", Order: "desc", AddedAfter: "2026-01-01T00:00:00Z", Limit: 1}
	page := integrationReadPage(t, c, v, q, false)
	if page.Total != 2 || len(page.Items) != 1 || page.Items[0].ID != newest.Public || page.NextCursor == "" {
		t.Fatalf("latest first: %+v", page)
	}
	q.Cursor = page.NextCursor
	page = integrationReadPage(t, c, v, q, false)
	if page.Total != 2 || len(page.Items) != 1 || page.Items[0].ID != show.Public || page.NextCursor != "" {
		t.Fatalf("latest tail: %+v", page)
	}
	var slim IntegrationItemPage
	err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		var e error
		slim, e = New(c.DB).WithContext(ctx).QueryIntegrationItems(v, IntegrationItemQuery{IDs: []string{newest.Public}}, true)
		return e
	})
	if err != nil || len(slim.Items) != 1 || len(slim.Items[0].MediaSources) != 0 || len(slim.Items[0].Paths) != 0 {
		t.Fatalf("file facts were not opt-in: %+v %v", slim, err)
	}
}

// Rows retain the native cursor recovery behavior, but totals belong to the
// current visibility snapshot, never an earlier page's count.
func TestIntegrationReadContinuationRecountsAfterVisibilityChange(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	items := []catalogtest.Item{}
	for i := 0; i < 4; i++ {
		item := c.Movie(lib, fmt.Sprintf("/m/%d.mkv", i), fmt.Sprintf("Film %d", i), 2000)
		c.Attributes(item.ID, "contentRating", "G")
		items = append(items, item)
	}
	c.Drain()
	if _, err := New(c.DB).ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	age := 12
	v := Viewer{Profile: "child", Fence: "restriction", Libraries: []string{"m"}, Restrictions: identity.ContentRestrictions{MaximumAge: &age, BlockUnrated: true}}
	q := IntegrationItemQuery{LibraryID: "m", Kind: "movie", Limit: 1}
	first := integrationReadPage(t, c, v, q, false)
	if first.Total != 4 || len(first.Items) != 1 || first.Items[0].ID != items[0].Public || first.NextCursor == "" {
		t.Fatalf("first: %+v", first)
	}
	c.Attributes(items[2].ID, "contentRating", "R")
	// Restriction facts take effect before background serving publication.
	q.Cursor = first.NextCursor
	pending := integrationReadPage(t, c, v, q, false)
	if pending.Total != 3 || len(pending.Items) != 1 || pending.Items[0].ID != items[1].Public {
		t.Fatalf("unpublished visibility change reused stale count: %+v", pending)
	}
	c.Drain()
	if _, err := New(c.DB).ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	q.Cursor = first.NextCursor
	for _, want := range []string{items[1].Public, items[3].Public} {
		page := integrationReadPage(t, c, v, q, false)
		if page.Total != 3 || len(page.Items) != 1 || page.Items[0].ID != want {
			t.Fatalf("stale total or hidden row: %+v", page)
		}
		q.Cursor = page.NextCursor
	}
	if q.Cursor != "" {
		t.Fatal("unexpected continuation")
	}
}

func TestIntegrationReadContinuationRecountsExpiredAndLegacyTotals(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	for i := 0; i < 3; i++ {
		c.Movie(lib, fmt.Sprintf("/m/%d.mkv", i), fmt.Sprintf("Film %d", i), 2000)
	}
	c.Drain()
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	q := IntegrationItemQuery{LibraryID: "m", Kind: "movie", Limit: 1}
	first := integrationReadPage(t, c, v, q, false)
	for _, mode := range []string{"expired", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			s := New(c.DB)
			signed, err := s.readSignedBrowseCursor(first.NextCursor)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "expired" {
				signed.Expires = time.Now().Add(-time.Minute).Unix()
			} else {
				signed.TotalPresent = false
			}
			// A legitimate old signed count must not be reused without its proof.
			signed.Value = "99"
			revision, err := s.homeRevision(v.Libraries, v.Profile)
			if err != nil {
				t.Fatal(err)
			}
			q.Cursor, err = s.encodeRevisionCursor(signed, revision)
			if err != nil {
				t.Fatal(err)
			}
			page := integrationReadPage(t, c, v, q, false)
			if page.Total != 3 || len(page.Items) != 1 || page.Items[0].ID == first.Items[0].ID {
				t.Fatalf("old total reused or rows did not continue: %+v", page)
			}
		})
	}
}

// A raw write may precede page one while its serving row appears only later.
// homeRevision stays unchanged across that publication, so raw/viewer revisions
// and a total-presence bit alone cannot authorize count reuse.
func TestIntegrationReadContinuationRecountsAfterServingPublication(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	for i := 0; i < 3; i++ {
		c.Movie(lib, fmt.Sprintf("/m/%d.mkv", i), fmt.Sprintf("Film %d", i), 2000)
	}
	c.Drain()
	added := c.Entity(compactcatalog.Entity{Library: lib, Kind: compactcatalog.Movie, Key: "later-serving-entity", Title: "Published later", Year: 2001}, nil)
	// Search publication also advances the raw revision. Give it a real worker
	// turn before page one, leaving browse publication queued for the next turn.
	w := compactcatalog.NewWorker(c.DB)
	c.Exec(`UPDATE catalog_derivations SET served_ms=0 WHERE domain=?`, compactcatalog.DomainSearch)
	if _, err := w.Step(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	var serving, pendingSearch int
	if err := c.DB.QueryRow(`SELECT (SELECT count(*) FROM catalog_browse_rows WHERE entity_id=?),(SELECT count(*) FROM catalog_dirty WHERE domain=? AND entity_id=?)`, added.ID, compactcatalog.DomainSearch, added.ID).Scan(&serving, &pendingSearch); err != nil || serving != 0 || pendingSearch != 0 {
		t.Fatalf("fixture must have search published and browse queued: serving=%d search=%d err=%v", serving, pendingSearch, err)
	}

	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	q := IntegrationItemQuery{LibraryID: "m", Kind: "movie", Limit: 1}
	before, err := New(c.DB).homeRevision(v.Libraries, v.Profile)
	if err != nil {
		t.Fatal(err)
	}
	first := integrationReadPage(t, c, v, q, false)
	if first.Total != 3 || first.NextCursor == "" {
		t.Fatalf("raw-only row was already serving: %+v", first)
	}
	c.Exec(`UPDATE catalog_derivations SET served_ms=0 WHERE domain=?`, compactcatalog.DomainBrowseRows)
	if _, err := w.Step(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_browse_rows WHERE entity_id=?`, added.ID).Scan(&serving); err != nil || serving != 1 {
		t.Fatalf("browse worker did not publish the queued row: %d %v", serving, err)
	}
	after, err := New(c.DB).homeRevision(v.Libraries, v.Profile)
	if err != nil || before != after {
		t.Fatalf("fixture must publish without raw/viewer revision drift: before=%+v after=%+v err=%v", before, after, err)
	}
	q.Cursor = first.NextCursor
	seenAdded := false
	for pages := 0; pages < 3; pages++ {
		page := integrationReadPage(t, c, v, q, false)
		if page.Total != 4 || len(page.Items) != 1 {
			t.Fatalf("serving publication reused stale total: %+v", page)
		}
		if page.Items[0].ID == added.Public {
			seenAdded = true
		}
		q.Cursor = page.NextCursor
	}
	if q.Cursor != "" || !seenAdded {
		t.Fatal("row continuation did not reach the new serving identity")
	}
}
