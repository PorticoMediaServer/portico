package catalog

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// Integration fixtures require root-composed production baseline. Never install
// a private shadow schema here: missing baseline tables must fail the test.
func assertGenreBaseline(t *testing.T, c *catalogtest.Catalog) {
	t.Helper()
	for _, name := range []string{"catalog_facet_base_usage", "catalog_search_genre_state", "catalog_search_genre_ready", "catalog_search_genre_targets", "catalog_search_genre_candidates", "catalog_search_genre_value_walk"} {
		var exists bool
		if err := c.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name=?)`, name).Scan(&exists); err != nil || !exists {
			t.Fatalf("production baseline missing %s: %v", name, err)
		}
	}
}
func genreTestRuntime(t *testing.T, c *catalogtest.Catalog) (*GenreRuntime, *FacetRuntime, *readprep.Store) {
	t.Helper()
	assertGenreBaseline(t, c)
	store, err := readprep.New(readprep.Config{Workers: 2})
	if err != nil {
		t.Fatal(err)
	}
	check := func(context.Context, readprep.Caller, FacetBaseSnapshot) error { return nil }
	bases, err := NewFacetRuntime(c.DB, store, check, 128<<20)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	r, err := NewGenreRuntime(c.DB, store, check, bases)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- bases.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
		store.Close()
	})
	return r, bases, store
}
func genreReady(t *testing.T, r *GenreRuntime, v Viewer, req SearchGenreRequest) SearchGenrePage {
	t.Helper()
	ctx := context.Background()
	out, err := r.Initial(ctx, "caller", v, req)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for out.Page == nil {
		var id string
		if out.Preparing != nil {
			id = out.Preparing.QueryID
		} else if out.Fallback != nil {
			id = out.Fallback.QueryID
		} else {
			out.Release()
			t.Fatal("no genre state")
		}
		out.Release()
		if time.Now().After(deadline) {
			t.Fatal("genre preparation did not complete")
		}
		time.Sleep(time.Millisecond)
		out, err = r.Poll(ctx, "caller", id)
		if err != nil {
			t.Fatal(err)
		}
	}
	defer out.Release()
	if out.Page.QueryID == "" {
		t.Fatal("ready handle missing")
	}
	return *out.Page
}
func TestGenreRuntimeRealBaselineMembershipAndSlotPreview(t *testing.T) {
	c := catalogtest.Open(t)
	assertGenreBaseline(t, c)
	movies := c.Library("m", "Movies", "movie", "/m")
	tv := c.Library("tv", "TV", "tv", "/tv")
	c.Library("anime", "Anime", "anime", "/anime")
	c.Library("reading", "Reading", "book", "/reading")
	a := c.Movie(movies, "/m/a.mkv", "A", 2001)
	b := c.Movie(movies, "/m/b.mkv", "B", 2002)
	c.Genres(a.ID, "fixture", "Shared", "No covers")
	c.Genres(b.ID, "fixture", "Shared")
	show := c.Show(tv, "Show", 2003)
	season := c.Season(show, 1)
	episode := c.Episode(show, season, 1, "/tv/e.mkv")
	c.Genres(show.ID, "fixture", "Shared")
	c.Genres(episode.ID, "fixture", "Episode only")
	empty := c.Show(tv, "Empty", 2004)
	c.Genres(empty.ID, "fixture", "Empty only")
	for _, item := range []catalogtest.Item{a, b} {
		selectSearchGenreArt(t, c, item, "item", "poster")
	}
	selectSearchGenreArt(t, c, a, "item", "backdrop")
	c.Drain()
	r, _, _ := genreTestRuntime(t, c)
	v := Viewer{Libraries: []string{"m", "tv", "anime", "reading"}}
	scopes, err := captureGenreScopes(context.Background(), c.DB, v)
	if err != nil || len(scopes) != 3 {
		t.Fatal("production public-kind selector must admit movie/tv/anime and exclude Reading", scopes, err)
	}
	for _, scope := range scopes {
		var storage string
		if err = c.DB.QueryRow(`SELECT typeof(kind) FROM catalog_libraries WHERE id=?`, scope.LibraryID).Scan(&storage); err != nil || storage != "integer" {
			t.Fatal("fixture shadowed integer catalogue kind", storage, err)
		}
	}

	page := genreReady(t, r, v, SearchGenreRequest{Limit: 50})
	values := []string{}
	counts := map[string]int{}
	for _, g := range page.Genres {
		values = append(values, g.Value)
		counts[g.Value] = g.Count
	}
	if !reflect.DeepEqual(values, []string{"Shared", "Empty only", "Episode only", "No covers"}) || counts["Shared"] != 3 {
		t.Fatal(values, counts)
	}
	cover := genreReady(t, r, v, SearchGenreRequest{Limit: 50, Preview: "covers"})
	if cover.HasGenres == nil || !*cover.HasGenres || len(cover.Genres) != 1 || cover.Genres[0].Value != "Shared" || len(cover.Genres[0].Covers) != 2 || cover.NextCursor != "" {
		t.Fatal(cover)
	}
	backdrop := genreReady(t, r, v, SearchGenreRequest{Limit: 50, Preview: "backdrop"})
	if len(backdrop.Genres) != 2 || backdrop.Genres[0].Value != "Shared" || backdrop.Genres[1].Value != "No covers" {
		t.Fatal(backdrop)
	}
	first := genreReady(t, r, v, SearchGenreRequest{Limit: 2})
	if first.NextCursor == "" || first.ReloadCursor == "" {
		t.Fatal(first)
	}
	second := genreReady(t, r, v, SearchGenreRequest{Cursor: first.NextCursor})
	if len(second.Genres) != 2 || second.PreviousCursor == "" {
		t.Fatal(second)
	}
	back := genreReady(t, r, v, SearchGenreRequest{Cursor: second.PreviousCursor})
	if !reflect.DeepEqual(first.Genres, back.Genres) {
		t.Fatal("reverse/reload order", first.Genres, back.Genres)
	}
	none := genreReady(t, r, Viewer{}, SearchGenreRequest{Preview: "covers"})
	if none.HasGenres == nil || *none.HasGenres || len(none.Genres) != 0 {
		t.Fatal(none)
	}
}

func TestGenreRuntimeHiddenNonemptyShowHasNoOwnFallback(t *testing.T) {
	c := catalogtest.Open(t)
	assertGenreBaseline(t, c)
	library := c.Library("movies", "Movies", "movie", "/movies")
	movie := c.Movie(library, "/movies/a.mkv", "Movie", 2000)
	c.Genres(movie.ID, "fixture", "Movie")
	tv := c.Library("tv", "TV", "tv", "/tv")
	hidden := c.Show(tv, "Hidden nonempty", 2000)
	c.Genres(hidden.ID, "fixture", "Forbidden fallback")
	season := c.Season(hidden, 1)
	episode := c.Episode(hidden, season, 1, "/tv/hidden.mkv")
	c.Genres(episode.ID, "fixture", "Hidden episode")
	c.Attributes(episode.ID, "label", " Adult")
	empty := c.Show(tv, "Actually empty", 2000)
	c.Genres(empty.ID, "fixture", "Empty fallback")
	c.Attributes(empty.ID, "label", " Adult")
	c.Drain()
	r, _, _ := genreTestRuntime(t, c)
	v := Viewer{Libraries: []string{"movies", "tv"}, Restrictions: identity.ContentRestrictions{MemberDeniedLabels: []string{" Adult"}}}
	page := genreReady(t, r, v, SearchGenreRequest{})
	oracle, err := New(c.DB).searchGenresReference(v, SearchGenreRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(page.Genres, oracle.Genres) || len(page.Genres) != 2 || page.Genres[0].Value != "Empty fallback" {
		t.Fatal("hidden-member self fallback or policy trimming changed oracle", page.Genres, oracle.Genres)
	}
	v.Restrictions.MemberDeniedLabels = []string{"Adult"}
	untrimmed := genreReady(t, r, v, SearchGenreRequest{})
	if len(untrimmed.Genres) != 4 {
		t.Fatal("typed policy classes merged whitespace-distinct member labels", untrimmed.Genres)
	}
}
