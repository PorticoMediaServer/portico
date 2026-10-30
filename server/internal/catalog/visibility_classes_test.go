package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
)

func TestVisibilityRebuilderAppliesLoggedChangesWithoutReplacingPublishedClass(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	one := c.Movie(movies, "/m/one.mkv", "First", 2020)
	c.Drain()
	s := New(c.DB)
	r := classesRestrictionOf(classesCeiling(13), false)
	if err := s.RebuildVisibilityClass(context.Background(), "m", r); err != nil {
		t.Fatal(err)
	}
	classID, _, generation := classesVisibilityClass(t, c, "m", r)

	c.Fields(one.ID, map[string]any{"title": "Second"})
	c.Drain()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.RunVisibilityRebuilder(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	dirty := 1
	for time.Now().Before(deadline) {
		if err := c.DB.QueryRow(`SELECT count(*) FROM compact_visibility_dirty WHERE class_id=?`, classID).Scan(&dirty); err != nil {
			t.Fatal(err)
		}
		if dirty == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if dirty != 0 {
		t.Fatal("background visibility refresh did not drain the class journal")
	}
	var currentGeneration int64
	if err := c.DB.QueryRow(`SELECT active_generation FROM compact_visibility_classes WHERE id=?`, classID).Scan(&currentGeneration); err != nil {
		t.Fatal(err)
	}
	if currentGeneration != generation {
		t.Fatalf("worker replaced generation: %d → %d", generation, currentGeneration)
	}
	var classSort, entitySort string
	if err := c.DB.QueryRow(`SELECT sort_key FROM compact_visibility_rows WHERE class_id=? AND generation=? AND entity_id=?`, classID, generation, one.ID).Scan(&classSort); err != nil {
		t.Fatal(err)
	}
	if err := c.DB.QueryRow(`SELECT sort_key FROM catalog_entities WHERE id=?`, one.ID).Scan(&entitySort); err != nil {
		t.Fatal(err)
	}
	if classSort != entitySort {
		t.Fatalf("background refresh kept sort key %q, want current entity key %q", classSort, entitySort)
	}
}

func TestVisibilityClassBuildsRestrictedItemsAndContainersOffRequest(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	safeShow := c.Show(tv, "A Show", 2020)
	adultShow := c.Show(tv, "Z Show", 2020)
	safeSeason := c.Season(safeShow, 1)
	adultSeason := c.Season(adultShow, 1)
	safeEpisode := c.Episode(safeShow, safeSeason, 1, "/tv/safe.mkv")
	adultEpisode := c.Episode(adultShow, adultSeason, 1, "/tv/adult.mkv")
	c.Fields(safeEpisode.ID, map[string]any{"year": 2020})
	c.Fields(adultEpisode.ID, map[string]any{"year": 2020})
	c.Attributes(safeEpisode.ID, "contentRating", "PG")
	c.Attributes(adultEpisode.ID, "contentRating", "R")
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('pg',8),('r',17)`)
	c.Drain()

	s := New(c.DB)
	r := classesRestrictionOf(classesCeiling(13), true)
	if err := s.RebuildVisibilityClass(context.Background(), "tv", r); err != nil {
		t.Fatal(err)
	}
	classID, _, generation := classesVisibilityClass(t, c, "tv", r)
	if generation != 1 {
		t.Fatalf("class publication generation=%d, want 1", generation)
	}
	if _, err := c.DB.Exec(`UPDATE content_rating_ages SET minimum_age=12 WHERE value_key='r'`); err != nil {
		t.Fatal(err)
	}
	if _, staleGeneration, ready, err := s.publishedVisibilityClass("tv", r); err != nil || !ready || staleGeneration != generation {
		t.Fatalf("rating age change must keep the last published class readable: generation=%d ready=%v err=%v", staleGeneration, ready, err)
	}
	if err := s.RebuildVisibilityClass(context.Background(), "tv", r); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DB.Exec(`UPDATE content_rating_ages SET minimum_age=17 WHERE value_key='r'`); err != nil {
		t.Fatal(err)
	}
	if err := s.RebuildVisibilityClass(context.Background(), "tv", r); err != nil {
		t.Fatal(err)
	}
	_, _, generation = classesVisibilityClass(t, c, "tv", r)
	for _, kind := range []int{2, 4} {
		var count int
		if err := c.DB.QueryRow(`SELECT COALESCE(sum(total),0) FROM compact_visibility_counts WHERE class_id=? AND generation=? AND kind=?`, classID, generation, kind).Scan(&count); err != nil || count != 1 {
			t.Fatalf("kind %d count %d: %v", kind, count, err)
		}
	}
	viewer := Viewer{Profile: "p", Fence: "restricted", Libraries: []string{"tv"}, Restrictions: r}
	page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "tv", Pivot: "shows", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.PageInfo.Total != 1 || len(page.Entries) != 1 || page.Entries[0].ID != safeShow.Public || len(page.PositionIndex) != 1 || page.PositionIndex[0].Index != 0 {
		t.Fatalf("published restricted page: %+v", page)
	}
	page, err = s.BrowseEntities(viewer, BrowseRequest{Library: "tv", Pivot: "episodes", Limit: 1})
	if err != nil || page.PageInfo.Total != 1 || len(page.Entries) != 1 || page.Entries[0].ID != safeEpisode.Public {
		t.Fatalf("published restricted episode page: %+v, %v", page, err)
	}
	decades, err := s.Facets(viewer, FacetRequest{Library: "tv", Field: "decade"})
	if err != nil || len(decades.Values) != 1 || decades.Values[0].Value != "2020" || decades.Values[0].Count != 1 {
		t.Fatalf("restricted decade must count the one visible show once: %+v, %v", decades, err)
	}
	member := Viewer{Profile: "member", Fence: "member", Libraries: []string{"tv"}, Restrictions: identity.ContentRestrictions{}.Narrow(8, true, nil, 0)}
	if err := s.RebuildVisibilityClass(context.Background(), "tv", member.EffectiveRestrictions()); err != nil {
		t.Fatal(err)
	}
	decades, err = s.Facets(member, FacetRequest{Library: "tv", Field: "decade"})
	if err != nil || len(decades.Values) != 1 || decades.Values[0].Value != "2020" || decades.Values[0].Count != 1 {
		t.Fatalf("member ceiling decade count: %+v, %v", decades, err)
	}
	legacy, next, total, err := s.contentEntities(ContentRequest{Viewer: viewer, Library: "tv", Profile: "p", ViewerFence: "restricted", View: "browse", Limit: 1}, "", 1)
	if err != nil || total != 1 || len(legacy) != 1 || legacy[0].ID != safeShow.Public || next != "" {
		t.Fatalf("legacy hierarchy must use class total and page: %v, %q, %d, %v", legacy, next, total, err)
	}
	var hidden int
	if err = c.DB.QueryRow(`SELECT count(*) FROM compact_visibility_rows WHERE class_id=? AND generation=? AND entity_id IN(?,?)`, classID, generation, adultEpisode.ID, adultShow.ID).Scan(&hidden); err != nil || hidden != 0 {
		t.Fatalf("restricted entities leaked into class: %d, %v", hidden, err)
	}
	c.Attributes(adultEpisode.ID, "contentRating", "PG")
	c.Drain()
	if err = s.RebuildVisibilityClass(context.Background(), "tv", r); err != nil {
		t.Fatal(err)
	}
	_, _, generation = classesVisibilityClass(t, c, "tv", r)
	if generation != 4 {
		t.Fatalf("new class generation %d: want 4", generation)
	}
	var shows int
	if err = c.DB.QueryRow(`SELECT COALESCE(sum(total),0) FROM compact_visibility_counts WHERE class_id=? AND generation=? AND kind=2`, classID, generation).Scan(&shows); err != nil || shows != 2 {
		t.Fatalf("rebuild did not publish visible show: %d, %v", shows, err)
	}
}

func TestVisibilityClassRefreshesOnlyDirtyEntitiesAndKeepsItsGeneration(t *testing.T) {
	testtier.Media(t, "a large visibility class rebuilt per test")
	c := catalogtest.Open(t)
	moviesLibrary := c.Library("m", "Movies", "movie", "/m")
	items := make([]catalogtest.Item, 600)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := range items {
			name := fmt.Sprintf("m%03d", i)
			path := "/m/" + name + ".mkv"
			id, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{
				Library: moviesLibrary,
				Kind:    compactcatalog.Movie,
				Key:     compactcatalog.ItemKey("/m", path, 0),
				Title:   fmt.Sprintf("Movie %03d", i),
				Year:    2020,
			})
			if err != nil {
				return err
			}
			items[i].ID = id
		}
		return nil
	})
	names := catalogtest.Names{}
	for i := range items {
		name := fmt.Sprintf("m%03d", i)
		items[i].Public = c.Public(items[i].ID)
		names[name] = items[i]
	}
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('r',17)`)
	c.Drain()

	s := New(c.DB)
	r := classesRestrictionOf(classesCeiling(13), false)
	if err := s.RebuildVisibilityClass(context.Background(), "m", r); err != nil {
		t.Fatal(err)
	}
	classID, libraryID, generation := classesVisibilityClass(t, c, "m", r)
	c.Fields(items[0].ID, map[string]any{"title": "Zzz final"})
	c.Drain()
	c.Attributes(items[300].ID, "contentRating", "R")
	c.Drain()
	var revision int64
	if err := c.DB.QueryRow(`SELECT revision FROM library_revisions WHERE library_id='m'`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if _, currentGeneration, ready, err := s.publishedVisibilityClass("m", r); err != nil || !ready || currentGeneration != 1 {
		t.Fatalf("stale generation unavailable: %d %v %v", currentGeneration, ready, err)
	}
	viewer := Viewer{Profile: "p", Fence: "restricted", Libraries: []string{"m"}, Restrictions: r}
	stalePage, err := s.BrowseEntities(viewer, BrowseRequest{Library: "m", Pivot: "movies", Limit: 20, Range: &BrowseRange{Start: 295}})
	// A class one revision behind serves its last published generation, but
	// each row is rechecked: the newly R-rated title is never listed.
	if err != nil {
		t.Fatalf("a stale class failed browse: %v", err)
	}
	for _, entry := range stalePage.Entries {
		if entry.ID == names["m300"].Public {
			t.Fatal("a stale class listed a title now above the ceiling")
		}
	}
	// The changes above are journaled for the class until a refresh applies them.
	var dirty int
	if err = c.DB.QueryRow(`SELECT count(*) FROM compact_visibility_dirty WHERE class_id=?`, classID).Scan(&dirty); err != nil || dirty < 1 {
		t.Fatalf("missing change log: %d %v", dirty, err)
	}
	if err = s.refreshVisibilityClass(context.Background(), "m", r, classID, libraryID, generation); err != nil {
		t.Fatal(err)
	}
	var refreshedGeneration, built int64
	if err = c.DB.QueryRow(`SELECT active_generation,catalog_revision FROM compact_visibility_classes WHERE id=?`, classID).Scan(&refreshedGeneration, &built); err != nil || refreshedGeneration != generation || built != revision {
		t.Fatalf("incremental publication generation=%d revision=%d: %v", refreshedGeneration, built, err)
	}
	summary, ready, err := s.visibilitySummary("m", []string{"movie"}, r)
	if err != nil || !ready || summary.total != 599 {
		t.Fatalf("summary: %+v ready=%v err=%v", summary, ready, err)
	}
	for name, want := range map[string]int{"m000": 598, "m001": 0, "m299": 298} {
		shape := pageShape{request: BrowseRequest{Library: "m", Restrictions: r}, sorts: []BrowseSortSelection{{Field: "title", Direction: "asc"}}, kind: 1, summary: summary, total: summary.total}
		got, found, err := s.browseAnchorRank(shape, names[name].Public)
		if err != nil || !found || got != want {
			t.Fatalf("rank %s=%d found=%v want=%d err=%v", name, got, found, want, err)
		}
	}
	if err = s.refreshVisibilityClass(context.Background(), "m", r, classID, libraryID, generation); err != nil {
		t.Fatal(err)
	}
	if err = c.DB.QueryRow(`SELECT count(*) FROM compact_visibility_dirty WHERE class_id=?`, classID).Scan(&dirty); err != nil || dirty != 0 {
		t.Fatalf("processed change log remained: %d %v", dirty, err)
	}
	if _, err = c.DB.Exec(`UPDATE content_rating_ages SET minimum_age=12 WHERE value_key='r'`); err != nil {
		t.Fatal(err)
	}
	if err = c.DB.QueryRow(`SELECT count(*) FROM compact_visibility_dirty WHERE class_id=?`, classID).Scan(&dirty); err != nil || dirty != 1 {
		t.Fatalf("rating age did not dirty only its entity: %d %v", dirty, err)
	}
	if err = c.DB.QueryRow(`SELECT revision FROM library_revisions WHERE library_id='m'`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if err = s.refreshVisibilityClass(context.Background(), "m", r, classID, libraryID, generation); err != nil {
		t.Fatal(err)
	}
	summary, ready, err = s.visibilitySummary("m", []string{"movie"}, r)
	if err != nil || !ready || summary.total != 600 {
		t.Fatalf("incremental rating classification: %+v ready=%v err=%v", summary, ready, err)
	}
}

func TestVisibilityClassRangeSeeksFromBoundedAnchor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	moviesLibrary := c.Library("m", "Movies", "movie", "/m")
	items := make([]catalogtest.Item, 300)
	names := catalogtest.Names{}
	for i := range items {
		name := fmt.Sprintf("m%03d", i)
		items[i] = c.Movie(moviesLibrary, "/m/"+name+".mkv", fmt.Sprintf("Movie %03d", i), 2020)
		names[name] = items[i]
	}
	c.Drain()
	s := New(c.DB)
	r := classesRestrictionOf(classesCeiling(13), false)
	if err := s.RebuildVisibilityClass(context.Background(), "m", r); err != nil {
		t.Fatal(err)
	}
	viewer := Viewer{Profile: "p", Fence: "restricted", Libraries: []string{"m"}, Restrictions: r}
	page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "m", Pivot: "movies", Limit: 5, Range: &BrowseRange{Start: 265}})
	if err != nil {
		t.Fatal(err)
	}
	if page.PageInfo.Total != 300 || page.PageInfo.Start != 265 || len(page.Entries) != 5 || page.Entries[0].ID != names["m265"].Public {
		t.Fatalf("anchored range: %+v, first=%v", page.PageInfo, page.Entries)
	}
	first, err := s.BrowseEntities(viewer, BrowseRequest{Library: "m", Pivot: "movies", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.BrowseEntities(viewer, BrowseRequest{Library: "m", Pivot: "movies", Limit: 5, Cursor: first.PageInfo.NextCursor})
	if err != nil || len(second.Entries) != 5 || second.Entries[0].ID != names["m005"].Public {
		t.Fatalf("keyset continuation: %+v, %v", second, err)
	}
}

func classesVisibilityClass(t *testing.T, c *catalogtest.Catalog, library string, r identity.ContentRestrictions) (classID, libraryID, generation int64) {
	t.Helper()
	key, _ := visibilityClassKey(library, r)
	if err := c.DB.QueryRow(`SELECT id,library_id,active_generation FROM compact_visibility_classes WHERE class_key=?`, key).Scan(&classID, &libraryID, &generation); err != nil {
		t.Fatal(err)
	}
	return
}

func classesCeiling(age int) *int { return &age }

// classesRestrictionOf is a local copy of restrictionOf for isolated test-only.sh runs.
func classesRestrictionOf(max *int, blockUnrated bool, labels ...string) identity.ContentRestrictions {
	return identity.ContentRestrictions{MaximumAge: max, BlockUnrated: blockUnrated, BlockedLabels: labels, Revision: 2}
}
