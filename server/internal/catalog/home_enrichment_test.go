package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestHomeTitleContextBatchesAuthorizedParentsAndCountsOnlyVisibleSeasons(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	show, episode := homeItem(t, db, "show"), homeItem(t, db, "e101")
	c.Genres(show.ID, "tmdb", "Drama", "Mystery", "Crime")
	c.Genres(show.ID, "nfo", "Drama")
	for _, name := range []string{"e102", "e201", "e202"} {
		c.Attributes(homeItem(t, db, name).ID, "label", "adult")
	}
	c.Season(show, 3) // An empty regular season contributes no episode facts.
	specials := c.Season(show, 0)
	c.Episode(show, specials, 1, "/tv/special.mkv")
	retired := c.Season(show, 4)
	c.Episode(show, retired, 1, "/tv/retired-season.mkv")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.RetireEntityTx(ctx, tx, retired.ID)
	})
	c.Drain()
	v := homeRequestFixture("tv").Viewer
	v.Restrictions = visibilityRestrictionOf(nil, false, "adult")
	makeRows := func() []HomeRow {
		rows := []HomeRow{{Entries: []ContentEntry{
			{ID: episode.Public, LibraryID: "tv", Kind: "episode"},
			{ID: episode.Public, LibraryID: "private", Kind: "episode"},
			{ID: homeItem(t, db, "e201").Public, LibraryID: "tv", Kind: "episode"},
			{ID: episode.Public, LibraryID: "tv", Kind: "movie", HomeTitleContext: &HomeTitleContext{Kind: "show"}},
		}}}
		for range 40 {
			rows = append(rows, HomeRow{Entries: []ContentEntry{{ID: episode.Public, LibraryID: "tv", Kind: "episode"}}})
		}
		return rows
	}
	read := func() []HomeRow {
		t.Helper()
		rows := makeRows()
		ctx, measured := dbwork.Measure(context.Background())
		ctx, queries := dbwork.TraceQueryPlans(ctx)
		if err := dbwork.WithReadSnapshot(ctx, db, func(ctx context.Context) error {
			return s.WithContext(ctx).homeTitleContextRows(v, rows)
		}); err != nil {
			t.Fatal(err)
		}
		if cost := measured(); cost.Statements != 2 || cost.Transactions != 1 || cost.Acquisitions != 1 {
			t.Fatalf("parent projection must use one generation read and one batch: %+v", cost)
		}
		trace := queries()
		if len(trace) != 2 || trace[0] != "SELECT retaining FROM catalog_generations WHERE singleton=1" || !strings.Contains(trace[1], "FROM json_each(?) requested CROSS JOIN catalog_entities episode ON episode.public_id=pid_blob(requested.value)") || !strings.Contains(trace[1], "home_context_facts AS MATERIALIZED") || !strings.Contains(trace[1], "SELECT DISTINCT show_id,library_id FROM home_context_requested") {
			t.Fatalf("parent lookup lost its exact returned-ID/distinct-parent batch: %v", trace)
		}
		return rows
	}
	check := func(want *HomeTitleContext) {
		t.Helper()
		rows := read()
		for i, row := range rows {
			if !reflect.DeepEqual(row.Entries[0].HomeTitleContext, want) {
				t.Fatalf("parent facts differ in row %d: got %+v want %+v", i, row.Entries[0].HomeTitleContext, want)
			}
		}
		for _, entry := range rows[0].Entries[1:] {
			if entry.HomeTitleContext != nil {
				t.Fatalf("hidden/foreign/mistyped episode borrowed parent facts: %+v", entry)
			}
		}
		raw, err := json.Marshal(rows)
		if err != nil || strings.Contains(string(raw), "/tv/") || strings.Contains(string(raw), "privatePath") {
			t.Fatalf("private parent evidence leaked: %s %v", raw, err)
		}
	}
	year, count := 2020, 1
	want := &HomeTitleContext{Kind: "show", Year: &year, Genres: []string{"Drama", "Mystery"}, SeasonCount: &count}
	check(want)
	for n := 2; n <= 81; n++ {
		item := c.Episode(show, homeItem(t, db, "s1"), n+100, fmt.Sprintf("/tv/hidden-extra-%d.mkv", n))
		c.Attributes(item.ID, "label", "adult")
	}
	c.Drain()
	check(want) // Same query/transport bound with more than 64 hidden members.
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, episode.Asset, false)
	})
	c.Drain()
	check(want) // Visibility is not widened or narrowed by physical availability.
	c.Attributes(episode.ID, "label", "adult")
	c.Drain()
	check(nil) // Current episode restrictions remove the whole optional context.
	c.Attributes(episode.ID, "label")
	c.Drain()
	check(want)
	v.Libraries = []string{"movies"}
	check(nil) // A known parent id never widens the actual library authority.
	v.Libraries = []string{"tv"}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.RetireEntityTx(ctx, tx, show.ID)
	})
	c.Drain()
	check(nil)
	if err := s.homeTitleContextRows(v, makeRows()); !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("parent context outside Home snapshot: %v", err)
	}
}

func TestHomeTitleContextSparseFactsStayInTheHomeSnapshot(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	show := c.Show(c.Handle("tv"), "Sparse show", 0)
	specials := c.Season(show, 0)
	episode := c.Episode(show, specials, 1, "/tv/sparse-special.mkv")
	c.Drain()
	v := homeRequestFixture("tv").Viewer
	read := func(scoped *Service) *HomeTitleContext {
		t.Helper()
		entries := []*ContentEntry{{ID: episode.Public, LibraryID: "tv", Kind: "episode"}}
		if err := scoped.homeTitleContexts(v, entries); err != nil {
			t.Fatal(err)
		}
		if entries[0].HomeTitleContext == nil {
			t.Fatal("genuine sparse parent lost its optional Home context")
		}
		return entries[0].HomeTitleContext
	}
	if err := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
		scoped := s.WithContext(ctx)
		before := read(scoped)
		if before.Year != nil || before.Genres == nil || len(before.Genres) != 0 || before.SeasonCount == nil || *before.SeasonCount != 0 {
			t.Fatalf("sparse context fabricated parent metadata: %+v", before)
		}
		// A real canonical write on another WAL connection cannot change the
		// parent facts inside this already-open Home read snapshot.
		c.Fields(show.ID, map[string]any{"year": 2024})
		if after := read(scoped); !reflect.DeepEqual(after, before) {
			t.Fatalf("parent facts escaped the Home snapshot: before=%+v after=%+v", before, after)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
		current := read(s.WithContext(ctx))
		if current.Year == nil || *current.Year != 2024 {
			t.Fatalf("new Home snapshot retained stale parent facts: %+v", current)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHomeTitleContextContinueKeepsEpisodeMetadataAndMatchesDocumentAndSpotlight(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	show, episode := homeItem(t, db, "show"), homeItem(t, db, "e101")
	c.Genres(show.ID, "tmdb", "Drama", "Mystery", "Crime")
	c.Genres(episode.ID, "nfo", "Comedy")
	c.Fields(episode.ID, map[string]any{"year": 2026})
	for _, name := range []string{"e201", "e202"} {
		c.Attributes(homeItem(t, db, name).ID, "label", "adult")
	}
	c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,playback_id) VALUES('p',?,120000,0,'parent-context-play')`, episode.ID)
	c.Exec(`INSERT INTO progress_activity VALUES('p','tv',?,'2026-09-02T00:00:00.000Z','paused')`, episode.ID)
	c.Drain()
	r := homeRequestFixture("tv")
	r.Viewer.Restrictions = visibilityRestrictionOf(nil, false, "adult")
	row, err := s.HomeSingleRow(r, "continue", HomeRowPage{Limit: 12})
	if err != nil || len(row.Entries) != 1 {
		t.Fatalf("actual Continue Watching episode absent: %+v %v", row, err)
	}
	entry := row.Entries[0]
	context := entry.HomeTitleContext
	if entry.ID != episode.Public || entry.Navigation == nil || entry.Navigation.EntityID != episode.Public || entry.Playback == nil || entry.Playback.ItemID != episode.Public || entry.Year == nil || *entry.Year != 2026 || !reflect.DeepEqual(entry.Genres, []string{"Comedy"}) || context == nil || context.Kind != "show" || context.Year == nil || *context.Year != 2020 || !reflect.DeepEqual(context.Genres, []string{"Drama", "Mystery"}) || context.SeasonCount == nil || *context.SeasonCount != 1 {
		t.Fatalf("Home replaced child facts/identity instead of publishing genuine parent context: %+v parent=%+v", entry, context)
	}
	document, err := s.HomeRows(r)
	if err != nil {
		t.Fatal(err)
	}
	continued := homeRowByID(document.Rows, "continue")
	if continued == nil || len(continued.Entries) != 1 || !reflect.DeepEqual(continued.Entries[0], entry) {
		t.Fatalf("document and paged Continue context differ: %+v", continued)
	}
	manifest, err := s.HomeManifest(r)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, candidate := range manifest.Spotlight {
		if candidate.Entry.ID == show.Public {
			found = true
			if candidate.Entry.HomeTitleContext != nil || candidate.ResumeTarget == nil || candidate.ResumeTarget.ID != episode.Public || !reflect.DeepEqual(candidate.ResumeTarget.HomeTitleContext, context) {
				t.Fatalf("spotlight changed selected parent/resume child context: %+v", candidate)
			}
		}
	}
	if !found {
		t.Fatal("real recent/continued show did not reach spotlight")
	}
	generic, err := s.homeEntries("p", []string{episode.Public})
	if err != nil || len(generic) != 1 || generic[0].HomeTitleContext != nil {
		t.Fatalf("generic entry hydration acquired Home-only metadata: %+v %v", generic, err)
	}
}

func TestHomePreviewDecorationBatchesRowsAndKeepsPerRowFields(t *testing.T) {
	db, s, names := tl10RestrictionFixture(t)
	c := catalogtest.New(t, db)
	c.Genres(names["kids"].ID, "tmdb", "Comedy", "Family", "Adventure")
	c.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,revision) VALUES('p',?,1,0,1)`, names["kids"].ID)
	c.Drain()
	makeRows := func() []HomeRow {
		return []HomeRow{
			{Entries: []ContentEntry{{ID: names["kids"].Public, Kind: "movie"}, {ID: names["show"].Public, Kind: "show"}}},
			{Entries: []ContentEntry{}},
			{Entries: []ContentEntry{{ID: names["family"].Public, Kind: "movie"}, {ID: names["kids"].Public, Kind: "movie"}}},
		}
	}
	expected, actual := makeRows(), makeRows()
	for _, row := range expected {
		if err := s.homeEnrich("p", row.Entries); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cost := dbwork.Measure(context.Background())
	if err := s.WithContext(ctx).homeEnrichRows("p", actual); err != nil {
		t.Fatal(err)
	}
	if cost().Statements != 4 {
		t.Fatalf("preview decoration cost %d statements, want four for all rows", cost().Statements)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("batched preview fields differ from individual row decoration:\nactual: %+v\nexpected: %+v", actual, expected)
	}
	first := actual[0].Entries[0]
	if first.Year == nil || *first.Year != 2000 || first.ContentRating != "G" || !reflect.DeepEqual(first.Genres, []string{"Comedy", "Family"}) || first.Watchlisted == nil || !*first.Watchlisted {
		t.Fatalf("item decoration missing: %+v", first)
	}
	if actual[0].Entries[1].Watchlisted != nil {
		t.Fatal("container received item watchlist state")
	}
	c.Exec(`UPDATE personal_items SET watchlisted=0 WHERE profile_id='p' AND item_id=?`, names["kids"].ID)
	fresh := makeRows()
	if err := s.homeEnrichRows("p", fresh); err != nil {
		t.Fatal(err)
	}
	if fresh[0].Entries[0].Watchlisted == nil || *fresh[0].Entries[0].Watchlisted {
		t.Fatal("a later preview reused old personal state")
	}
}

func TestHomeDocumentAndSingleRowCarryTheSameDecoration(t *testing.T) {
	db := homeFixture(t)
	s := New(db)
	c := catalogtest.New(t, db)
	movie := homeItem(t, db, "m1")
	c.Attributes(movie.ID, "contentRating", "PG")
	c.Genres(movie.ID, "tmdb", "Comedy", "Family", "Adventure")
	c.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,revision) VALUES('p',?,1,0,1)`, movie.ID)
	c.Drain()
	r := homeRequestFixture()
	document, err := s.HomeRows(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Rows) < 2 {
		t.Fatal("fixture needs multiple preview rows")
	}
	for _, row := range document.Rows {
		single, err := s.HomeSingleRow(r, row.ID, HomeRowPage{Limit: row.Limit})
		if err != nil {
			t.Fatal(row.ID, err)
		}
		if !reflect.DeepEqual(row.Entries, single.Entries) {
			t.Fatalf("Home and row endpoint decoration disagree for %s", row.ID)
		}
	}
}
