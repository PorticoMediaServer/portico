package catalog

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func facetInstallDraftOnBaseline(t *testing.T, c *catalogtest.Catalog) {
	t.Helper()
	var installed bool
	if err := c.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='catalog_facet_base_usage')`).Scan(&installed); err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Fatal("maintained facet BASE facts are missing from the production baseline")
	}
}
func facetIntegratedPlan(t *testing.T, c *catalogtest.Catalog, libraryID int64, library, field string, r identity.ContentRestrictions) FacetBasePlan {
	t.Helper()
	var plan FacetBasePlan
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		plan, err = CaptureSimpleFacetBasePlan(ctx, tx, libraryID, library, field, r)
		return err
	})
	return plan
}
func facetIntegratedBuild(t *testing.T, c *catalogtest.Catalog, plan FacetBasePlan) SimpleFacetPage {
	t.Helper()
	b := &FacetBases{DB: c.DB, Producer: plan.Producer, MaxBytes: 64 << 20}
	var id int64
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		id, err = b.Begin(ctx, tx, plan.Scope, plan.Witness, "integration-authority")
		return err
	})
	for step := 0; step < 500; step++ {
		done := false
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var err error
			done, err = b.Step(ctx, tx, id)
			return err
		})
		if done {
			w, _ := facetWindowFixture(t, c.DB, nil)
			return facetWindowInitial(t, w, plan.Scope, "", 200)
		}
	}
	t.Fatal("baseline class source failed to complete")
	return SimpleFacetPage{}
}

// Uses the production baseline, write API, real producer drain, actual integer
// catalogue kind, public library kind and a real published visibility class.
// This guards the SQL/type boundary that minimal algorithm fixtures cannot.
func TestFacetBaseIntegrationProductionKindDispatchAndExactWindows(t *testing.T) {
	c := catalogtest.Open(t)
	facetInstallDraftOnBaseline(t, c)
	movies := c.Library("movies", "Movies", "movie", "/movies")
	movie := c.Movie(movies, "/movies/one.mkv", "One", 2001)
	c.Genres(movie.ID, "fixture", "Drama")
	c.Attributes(movie.ID, "contentRating", "PG")
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "Show", 2002)
	season := c.Season(show, 1)
	safe := c.Episode(show, season, 1, "/tv/safe.mkv")
	hidden := c.Episode(show, season, 2, "/tv/hidden.mkv")
	c.Genres(show.ID, "fixture", "Show genre")
	c.Genres(safe.ID, "fixture", "Episode genre")
	c.Genres(hidden.ID, "fixture", "Hidden genre")
	c.Attributes(safe.ID, "contentRating", "PG")
	c.Attributes(hidden.ID, "contentRating", "R")
	anime := c.Library("anime", "Anime", "anime", "/anime")
	animeShow := c.Show(anime, "Anime Show", 2003)
	animeSeason := c.Season(animeShow, 1)
	animeEpisode := c.Episode(animeShow, animeSeason, 1, "/anime/one.mkv")
	c.Genres(animeShow.ID, "fixture", "Anime genre")
	c.Genres(animeEpisode.ID, "fixture", "Anime episode")
	c.Attributes(animeEpisode.ID, "contentRating", "PG")
	music := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Album", 2004)
	song := c.Song(album, 1, "/music/one.flac", "Song")
	c.Genres(song.ID, "fixture", "Jazz")
	c.Attributes(song.ID, "contentRating", "PG")
	reading := c.Library("reading", "Reading", "book", "/reading")
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('pg',8),('r',17) ON CONFLICT(value_key) DO UPDATE SET minimum_age=excluded.minimum_age`)
	c.Drain()
	ceiling := 13
	r := identity.ContentRestrictions{MaximumAge: &ceiling, BlockUnrated: true}
	for _, tc := range []struct {
		library    string
		id         int64
		want       []string
		restricted []string
	}{
		{"movies", movies, []string{"Drama"}, []string{"Drama"}},
		{"tv", tv, []string{"Show genre"}, []string{"Episode genre", "Show genre"}},
		{"anime", anime, []string{"Anime genre"}, []string{"Anime episode", "Anime genre"}},
		{"music", music, []string{"Jazz"}, []string{"Jazz"}},
	} {
		t.Run(tc.library, func(t *testing.T) {
			var storageType, publicKind string
			if err := c.DB.QueryRow(`SELECT typeof(c.kind),l.kind FROM catalog_libraries c JOIN libraries l ON l.id=c.library_id WHERE c.id=?`, tc.id).Scan(&storageType, &publicKind); err != nil {
				t.Fatal(err)
			}
			if storageType != "integer" {
				t.Fatal("fixture shadowed production kind", storageType)
			}
			plan := facetIntegratedPlan(t, c, tc.id, tc.library, "genre", identity.ContentRestrictions{})
			if plan.Scope.Source != UnrestrictedFacetBaseSource {
				t.Fatal(publicKind, plan.Scope.Source)
			}
			p := facetIntegratedBuild(t, c, plan)
			if !reflect.DeepEqual(facetWindowValues(p), tc.want) {
				t.Fatal("unrestricted baseline values", facetWindowValues(p), tc.want)
			}
			plan = facetIntegratedPlan(t, c, tc.id, tc.library, "genre", r)
			wantSource := RestrictedMemberFacetBaseSource
			if publicKind == "tv" || publicKind == "anime" {
				wantSource = RestrictedShowFacetBaseSource
			}
			if plan.Scope.Source != wantSource {
				t.Fatal("public kind selected wrong policy branch", publicKind, plan.Scope.Source, wantSource)
			}
			p = facetIntegratedBuild(t, c, plan)
			if !reflect.DeepEqual(facetWindowValues(p), tc.restricted) {
				t.Fatal("restricted baseline values", facetWindowValues(p), tc.restricted)
			}
			for _, f := range p.Values {
				if f.Count != 1 {
					t.Fatal("counted hidden sibling or members instead of holder", p.Values)
				}
			}
		})
	}
	for _, policy := range []identity.ContentRestrictions{{}, r} {
		_, err := CaptureSimpleFacetBasePlan(context.Background(), c.DB, reading, "reading", "genre", policy)
		if !errors.Is(err, ErrFacetBaseUnavailable) {
			t.Fatal("Reading entered SIMPLE", err)
		}
	}
	// No published class: live restricted show decade (no extra asset gate).
	plan := facetIntegratedPlan(t, c, tv, "tv", "decade", r)
	if plan.Scope.Source != RestrictedShowFacetBaseSource {
		t.Fatal(plan.Scope.Source)
	}
	p := facetIntegratedBuild(t, c, plan)
	if p.Total != 1 || p.Values[0].Value != "2000" || p.Values[0].Count != 1 {
		t.Fatal(p)
	}
	// Real legacy publication chooses the separate exact linked predicate basis.
	if err := New(c.DB).RebuildVisibilityClass(context.Background(), "tv", r); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	plan = facetIntegratedPlan(t, c, tv, "tv", "decade", r)
	if plan.Scope.Source != LinkedDecadeFacetBaseSource {
		t.Fatal("published class did not change source predicate", plan.Scope.Source)
	}
	p = facetIntegratedBuild(t, c, plan)
	clause, args := visibilityMembershipSQL("e.entity_id", r)
	var exact int
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id WHERE e.library_id=? AND e.kind=2 AND ce.retired=0 AND e.year BETWEEN 1800 AND 2199 AND `+clause, append([]any{tv}, args...)...).Scan(&exact); err != nil {
		t.Fatal(err)
	}
	if p.Total != 1 || p.Values[0].Count != exact || exact != 1 {
		t.Fatal("linked baseline oracle", p, exact)
	}
}
