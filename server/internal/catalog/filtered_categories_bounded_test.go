package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
)

type boundedTestMovie struct {
	id     string
	title  string
	year   int
	poster string
	genres []string
	studio string
	rating string
}

func addBoundedTestMovies(t *testing.T, db *sql.DB, names catalogtest.Names, movies []boundedTestMovie) {
	t.Helper()
	c := catalogtest.New(t, db)
	for _, m := range movies {
		names[m.id] = c.Movie(c.Handle("a"), "/a/"+m.id+".mkv", m.title, m.year)
		c.Fields(names[m.id].ID, map[string]any{"poster_url": m.poster})
		if len(m.genres) > 0 {
			c.Genres(names[m.id].ID, "tmdb", m.genres...)
		}
		if m.studio != "" {
			c.Attributes(names[m.id].ID, "studio", m.studio)
		}
		if m.rating != "" {
			c.Attributes(names[m.id].ID, "contentRating", m.rating)
		}
	}
	c.Drain()
}

func boundedCategoryMap(categories []Category) map[string]Category {
	out := map[string]Category{}
	for _, category := range categories {
		out[category.ID] = category
	}
	return out
}

func boundedGenreOrder(categories []Category) []string {
	out := []string{}
	for _, category := range categories {
		if strings.HasPrefix(category.ID, "genre:") {
			out = append(out, category.ID)
		}
	}
	return out
}

func boundedStudioOrder(categories []Category) []string {
	out := []string{}
	for _, category := range categories {
		if strings.HasPrefix(category.ID, "studio:") {
			out = append(out, category.ID)
		}
	}
	return out
}

// Every match counts: a category whose movies all sort late is still there,
// with its exact count.
func TestFilteredCategoriesCountEveryMatch(t *testing.T) {
	db, s, names := tl10BrowseFixture(t)
	// Title order is Alpha, Beta, Delta, Epsilon, Gamma, Zeta: the first 3
	// carry QFirst, the rest carry QLast.
	addBoundedTestMovies(t, db, names, []boundedTestMovie{
		{id: "q1", title: "QTest Alpha", year: 2001, genres: []string{"QFirst"}},
		{id: "q2", title: "QTest Beta", year: 2001, genres: []string{"QFirst"}},
		{id: "q3", title: "QTest Gamma", year: 2001, genres: []string{"QLast"}},
		{id: "q4", title: "QTest Delta", year: 2001, genres: []string{"QFirst"}},
		{id: "q5", title: "QTest Epsilon", year: 2001, genres: []string{"QLast"}},
		{id: "q6", title: "QTest Zeta", year: 2001, genres: []string{"QLast"}},
	})
	r := ContentRequest{Viewer: Viewer{Libraries: []string{"a"}}, Library: "a", Q: "QTest"}
	got, err := s.compactFilteredMovieCategories(r)
	if err != nil {
		t.Fatal(got, err)
	}
	byID := boundedCategoryMap(got)
	for _, id := range []string{"genre:QFirst", "genre:QLast"} {
		if category, ok := byID[id]; !ok || category.Count != 3 {
			t.Fatalf("%s: %+v", id, category)
		}
	}
}

func TestFilteredCategoriesRareCategoryCanLead(t *testing.T) {
	db, s, names := tl10BrowseFixture(t)
	// Zither is rare in the library (3) next to Action (m1, m2 plus x1..x3),
	// but every candidate carries it, so it must lead the genre ranking.
	addBoundedTestMovies(t, db, names, []boundedTestMovie{
		{id: "r1", title: "Rare One", year: 2001, genres: []string{"Zither", "Mid"}},
		{id: "r2", title: "Rare Two", year: 2001, genres: []string{"Zither", "Mid"}},
		{id: "r3", title: "Rare Three", year: 2001, genres: []string{"Zither"}},
		{id: "x1", title: "Xtra One", year: 2001, genres: []string{"Action"}},
		{id: "x2", title: "Xtra Two", year: 2001, genres: []string{"Action"}},
		{id: "x3", title: "Xtra Three", year: 2001, genres: []string{"Action"}},
	})
	var actionTotal, zitherTotal int
	if err := db.QueryRow(`SELECT COALESCE((SELECT total FROM catalog_movie_category_summaries WHERE library_id=? AND kind=1 AND value='Action'),0)`, catalogtest.New(t, db).Handle("a")).Scan(&actionTotal); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COALESCE((SELECT total FROM catalog_movie_category_summaries WHERE library_id=? AND kind=1 AND value='Zither'),0)`, catalogtest.New(t, db).Handle("a")).Scan(&zitherTotal); err != nil {
		t.Fatal(err)
	}
	if actionTotal != 5 || zitherTotal != 3 {
		t.Fatalf("fixture popularity: Action=%d Zither=%d", actionTotal, zitherTotal)
	}
	r := ContentRequest{Viewer: Viewer{Libraries: []string{"a"}}, Library: "a", Q: "Rare"}
	got, err := s.compactFilteredMovieCategories(r)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"genre:Zither", "genre:Mid"}; !reflect.DeepEqual(boundedGenreOrder(got), want) {
		t.Fatalf("genres %+v want %+v", boundedGenreOrder(got), want)
	}
	if _, ok := boundedCategoryMap(got)["genre:Action"]; ok {
		t.Fatalf("globally popular Action is not on any candidate: %+v", got)
	}
}

func TestFilteredCategoriesCompleteCountsAreExact(t *testing.T) {
	db, s, names := tl10BrowseFixture(t)
	// Title order is Alpha, Beta, Delta, Epsilon, Gamma, Zeta; Beta has no
	// poster, so the artwork is the first 4 non-empty posters in that order.
	addBoundedTestMovies(t, db, names, []boundedTestMovie{
		{id: "p1", title: "Poster Alpha", year: 2001, poster: "/p-a", genres: []string{"Epic"}, studio: "Acme"},
		{id: "p2", title: "Poster Beta", year: 2001, poster: "", genres: []string{"Epic"}, studio: "Acme"},
		{id: "p3", title: "Poster Gamma", year: 2001, poster: "/p-g", genres: []string{"Epic"}, studio: "Zulu"},
		{id: "p4", title: "Poster Delta", year: 2001, poster: "/p-d", genres: []string{"Epic"}, studio: "Acme"},
		{id: "p5", title: "Poster Epsilon", year: 2001, poster: "/p-e", genres: []string{"Epic"}, studio: "Acme"},
		{id: "p6", title: "Poster Zeta", year: 2001, poster: "/p-z", genres: []string{"Epic"}, studio: "Zulu"},
	})
	r := ContentRequest{Viewer: Viewer{Libraries: []string{"a"}}, Library: "a", Q: "Poster"}
	got, err := s.compactFilteredMovieCategories(r)
	if err != nil {
		t.Fatal(err)
	}
	byID := boundedCategoryMap(got)
	epic, ok := byID["genre:Epic"]
	if !ok || epic.Count != 6 {
		t.Fatalf("Epic exact count: %+v", epic)
	}
	if want := []string{"/p-a", "/p-d", "/p-e", "/p-g"}; !reflect.DeepEqual(epic.ArtworkPaths, want) {
		t.Fatalf("Epic posters %v want %v", epic.ArtworkPaths, want)
	}
	decade, ok := byID["decade:2000"]
	if !ok || decade.Count != 6 {
		t.Fatalf("decade exact count: %+v", decade)
	}
	if want := []string{"studio:Acme", "studio:Zulu"}; !reflect.DeepEqual(boundedStudioOrder(got), want) {
		t.Fatalf("studios %v want %v", boundedStudioOrder(got), want)
	}
	if acme := byID["studio:Acme"]; acme.Count != 4 {
		t.Fatalf("Acme exact count: %+v", acme)
	}
}

func TestFilteredCategoriesRestrictedClassHidesMovies(t *testing.T) {
	db, s, names := tl10BrowseFixture(t)
	addBoundedTestMovies(t, db, names, []boundedTestMovie{
		{id: "za", title: "Zed Allowed", year: 2001, poster: "/allowed-poster", genres: []string{"ZAllowed"}, rating: "G"},
		{id: "zb", title: "Zed Blocked", year: 2001, poster: "/blocked-poster", genres: []string{"ZBlocked"}, rating: "R"},
	})
	viewer := Viewer{Profile: "p", Libraries: []string{"a"}, Restrictions: restrictionOf(ceiling(13), true)}
	// Pending rating spellings count as unrated until classified.
	c := catalogtest.New(t, db)
	c.Exec(`INSERT INTO content_rating_pending(value_key,value) VALUES('g','G'),('r','R') ON CONFLICT(value_key) DO UPDATE SET value=excluded.value`)
	if _, err := s.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	catalogtest.New(t, db).Drain()
	if err := s.RebuildVisibilityClass(context.Background(), "a", viewer.EffectiveRestrictions()); err != nil {
		t.Fatal(err)
	}
	catalogtest.New(t, db).Drain()
	r := ContentRequest{Viewer: viewer, Library: "a", Q: "Zed"}
	got, err := s.compactFilteredMovieCategories(r)
	if err != nil {
		t.Fatal(err)
	}
	byID := boundedCategoryMap(got)
	allowed, ok := byID["genre:ZAllowed"]
	if !ok || allowed.Count != 1 {
		t.Fatalf("ZAllowed missing: %+v", got)
	}
	if !reflect.DeepEqual(allowed.ArtworkPaths, []string{"/allowed-poster"}) {
		t.Fatalf("ZAllowed posters: %+v", allowed)
	}
	if blocked, ok := byID["genre:ZBlocked"]; ok {
		t.Fatalf("blocked genre leaks: %+v", blocked)
	}
	for _, category := range got {
		for _, poster := range category.ArtworkPaths {
			if poster == "/blocked-poster" {
				t.Fatalf("blocked poster leaks in %+v", category)
			}
		}
	}
}

func TestContentFilterCountsEveryMatch(t *testing.T) {
	testtier.Media(t, "10,001 titles in one filtered category")
	db, _, _ := tl10BrowseFixture(t)
	c := catalogtest.New(t, db)
	library := c.Handle("a")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 1; i <= 10001; i++ {
			name := fmt.Sprintf("sat%05d", i)
			if _, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/a", "/a/"+name, 0), Title: fmt.Sprintf("Sat %05d", i), Year: 2001, Added: "2026-01-01T00:00:00.000Z"}); err != nil {
				return err
			}
		}
		return nil
	})
	c.Drain()
	s := New(db)
	viewer := Viewer{Profile: "p", Fence: "p", Libraries: []string{"a"}}
	content, err := s.Content(ContentRequest{Viewer: viewer, ServerID: "s", Library: "a", Profile: "p", ViewerFence: "p", View: "browse", Sort: "title", Direction: "asc", Limit: 5, Q: "Sat"})
	if err != nil {
		t.Fatal(err)
	}
	// A searched grid's categories count every match: no floor, no flag.
	if len(content.Filters) != 1 || len(content.Filters[0].Options) != 1 || content.Filters[0].Options[0].ID != "decade:2000" || content.Filters[0].Options[0].Count != 10001 {
		t.Fatalf("filter must count all 10,001 matches: %+v", content.Filters)
	}
}
