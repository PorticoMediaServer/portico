package metadata_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb/tmdbtest"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

var ctx = context.Background()

type prober struct{}

func (prober) Probe(context.Context, string) (scan.Facts, error) {
	return scan.Facts{Container: "mkv", DurationMS: 60_000, Streams: []db.Stream{{Kind: db.StreamVideo, Codec: "h264"}}}, nil
}

// fixture is one scanned library and a service whose TMDB answers from files.
type fixture struct {
	t      *testing.T
	d      *db.DB
	lib    db.Library
	viewer db.Viewer
	tmdb   *tmdbtest.Server
	s      *metadata.Service
}

// newFixture makes a library of the given kind holding the given files and scans it.
func newFixture(t *testing.T, kind string, files ...string) *fixture {
	t.Helper()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	root := filepath.Join(t.TempDir(), "library")
	for i, rel := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, make([]byte, 100+i), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return scanned(t, d, kind, root, tmdbtest.New(t))
}

// scanned makes a library over a folder in an empty database and scans it.
func scanned(t *testing.T, d *db.DB, kind, root string, server *tmdbtest.Server) *fixture {
	t.Helper()
	if _, err := d.CreateLibrary(ctx, "Library", kind, "en", []string{root}); err != nil {
		t.Fatal(err)
	}
	viewers, err := seed.Profiles(ctx, d, 1)
	if err != nil {
		t.Fatal(err)
	}
	libraries, err := d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&scan.Scanner{DB: d, Prober: prober{}}).Scan(ctx, libraries[0]); err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, d: d, lib: libraries[0], viewer: viewers[0], tmdb: server,
		s: &metadata.Service{DB: d, Providers: map[string]metadata.Provider{"tmdb": tmdb.New("test-token", server.URL)}}}
}

func (f *fixture) match() metadata.MatchStats {
	f.t.Helper()
	stats, err := f.s.MatchLibrary(ctx, f.lib.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.d.Check(ctx); err != nil {
		f.t.Fatal(err)
	}
	return stats
}

// title is the detail of the library's one item of a kind with this title.
func (f *fixture) title(kind db.ItemKind, title string) db.ItemDetail {
	f.t.Helper()
	page, err := f.d.Items(ctx, f.viewer, db.ItemQuery{Library: f.lib.PublicID, Kinds: []db.ItemKind{kind}, Limit: 200})
	if err != nil {
		f.t.Fatal(err)
	}
	var have []string
	for _, item := range page.Items {
		have = append(have, item.Title)
		if item.Title == title {
			detail, err := f.d.Item(ctx, f.viewer, item.PublicID)
			if err != nil {
				f.t.Fatal(err)
			}
			return detail
		}
	}
	f.t.Fatalf("no %s %q; have %q", kind, title, have)
	return db.ItemDetail{}
}

func tagNames(detail db.ItemDetail, kind string) []string {
	var out []string
	for _, tag := range detail.Tags {
		if tag.Kind == kind {
			out = append(out, tag.Name)
		}
	}
	slices.Sort(out)
	return out
}

func TestAFilmIsMatchedAndGetsItsDetails(t *testing.T) {
	f := newFixture(t, db.LibraryMovies, "the.matrix.1999.1080p.mkv", "Heat.mkv", "Zzyzx Home Video (2011).mkv")
	before := f.title(db.KindMovie, "the matrix")

	// A provider that cannot be asked changes nothing, and the run says so.
	f.tmdb.Down.Store(true)
	if _, err := f.s.MatchLibrary(ctx, f.lib.ID); err == nil {
		t.Fatal("a provider that is down was not reported")
	}
	if got := f.title(db.KindMovie, "the matrix"); got.Summary != "" || len(got.Credits) != 0 {
		t.Fatalf("a failed lookup changed the title: %+v", got)
	}
	f.tmdb.Down.Store(false)

	if stats := f.match(); stats.Matched != 1 || stats.Unmatched != 2 {
		t.Fatalf("matched %d, unmatched %d; want 1 and 2", stats.Matched, stats.Unmatched)
	}
	film := f.title(db.KindMovie, "The Matrix")
	if film.PublicID != before.PublicID {
		t.Error("matching gave the film a new id")
	}
	if film.Year != 1999 || film.ReleaseDate != "1999-03-31" || film.Tagline != "Believe the unbelievable." ||
		!strings.HasPrefix(film.Summary, "Set in the 22nd century") || film.ContentRating != "R" || film.CommunityRating != 8.2 {
		t.Errorf("details: %+v", film)
	}
	if got := tagNames(film, db.TagGenre); !slices.Equal(got, []string{"Action", "Science Fiction"}) {
		t.Errorf("genres: %q", got)
	}
	if got := tagNames(film, db.TagStudio); !slices.Equal(got, []string{"Village Roadshow Pictures", "Warner Bros. Pictures"}) {
		t.Errorf("studios: %q", got)
	}
	if !slices.Contains(film.ExternalIDs, db.ExternalID{Provider: "tmdb", ID: "603"}) ||
		!slices.Contains(film.ExternalIDs, db.ExternalID{Provider: "imdb", ID: "tt0133093"}) {
		t.Errorf("ids: %+v", film.ExternalIDs)
	}
	// Two directors, a writer who is one of them, a producer, then four actors. The director
	// of photography is not listed.
	if film.CreditTotal != 8 || film.Credits[0].Name != "Lana Wachowski" || film.Credits[0].Role != db.RoleDirector {
		t.Fatalf("credits: %d, %+v", film.CreditTotal, film.Credits)
	}
	neo := film.Credits[4]
	if neo.Name != "Keanu Reeves" || neo.Role != db.RoleActor || neo.Character != "Neo" {
		t.Errorf("first actor: %+v", neo)
	}
	// Lana Wachowski directed and wrote: one person, two credits.
	if film.Credits[0].PersonID != film.Credits[2].PersonID {
		t.Error("one person was stored twice")
	}

	// What could not be matched stays in the library under its own name, and is listed.
	f.title(db.KindMovie, "Heat")
	unmatched, err := f.d.Unmatched(ctx, f.lib.ID, nil, 10)
	if err != nil || len(unmatched) != 2 {
		t.Fatalf("unmatched: %+v, %v", unmatched, err)
	}
	heat, zzyzx := unmatched[0], unmatched[1]
	if heat.Title != "Heat" || heat.Reason != metadata.ReasonSeveralTitles || strings.Count(heat.Matches, `"provider":"tmdb"`) != 3 {
		t.Errorf("Heat: %+v", heat)
	}
	if zzyzx.Reason != metadata.ReasonNoResults || zzyzx.Matches != "[]" {
		t.Errorf("Zzyzx: %+v", zzyzx)
	}
	if next, err := f.d.Unmatched(ctx, f.lib.ID, &unmatched[0], 10); err != nil || len(next) != 1 || next[0].Title != zzyzx.Title {
		t.Errorf("second page: %+v, %v", next, err)
	}

	// Everything has been asked about once: a second run asks nothing.
	asked := f.tmdb.Requests.Load()
	if stats := f.match(); stats != (metadata.MatchStats{}) || f.tmdb.Requests.Load() != asked {
		t.Errorf("a second run did work: %+v, %d requests", stats, f.tmdb.Requests.Load()-asked)
	}
}

func TestAnIDInAnNFOFileIsExact(t *testing.T) {
	f := newFixture(t, db.LibraryMovies, "Some Film/film.mkv")
	nfo := `<movie><title>My Own Title</title><uniqueid type="imdb">tt0133093</uniqueid></movie>`
	if err := os.WriteFile(filepath.Join(f.lib.Paths[0].Path, "Some Film", "movie.nfo"), []byte(nfo), 0o644); err != nil {
		t.Fatal(err)
	}
	// The NFO file was written after the first scan made the title: start again from nothing.
	f = newFixtureAt(t, f)
	if stats := f.match(); stats.Matched != 1 {
		t.Fatalf("matched %d; want 1", stats.Matched)
	}
	page, err := f.d.Items(ctx, f.viewer, db.ItemQuery{Library: f.lib.PublicID, Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("items: %+v, %v", page.Items, err)
	}
	film, err := f.d.Item(ctx, f.viewer, page.Items[0].PublicID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(film.ExternalIDs, db.ExternalID{Provider: "tmdb", ID: "603"}) || film.Tagline == "" {
		t.Errorf("the film was not matched by its IMDb id: %+v", film)
	}
}

// newFixtureAt scans the same folder into a new database.
func newFixtureAt(t *testing.T, old *fixture) *fixture {
	t.Helper()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return scanned(t, d, old.lib.Kind, old.lib.Paths[0].Path, old.tmdb)
}

func TestAShowIsMatchedWithItsEpisodes(t *testing.T) {
	f := newFixture(t, db.LibraryShows, "Breaking Bad/Season 01/Breaking Bad - S01E01.mkv", "Breaking Bad/Season 01/Breaking Bad - S01E02.mkv",
		"Breaking Bad/Season 09/Breaking Bad - S09E01.mkv")
	if stats := f.match(); stats.Matched != 1 || stats.Unmatched != 0 {
		t.Fatalf("matched %d, unmatched %d", stats.Matched, stats.Unmatched)
	}
	show := f.title(db.KindShow, "Breaking Bad")
	if show.Year != 2008 || show.ContentRating != "TV-MA" || show.Tagline != "Change the equation." {
		t.Errorf("show: %+v", show)
	}
	if got := tagNames(show, db.TagNetwork); !slices.Equal(got, []string{"AMC"}) {
		t.Errorf("networks: %q", got)
	}
	if show.CreditTotal != 5 || show.Credits[2].Name != "Bryan Cranston" || show.Credits[2].Character != "Walter White" {
		t.Errorf("credits: %d, %+v", show.CreditTotal, show.Credits)
	}
	pilot := f.title(db.KindEpisode, "Pilot")
	if pilot.ReleaseDate != "2008-01-20" || !strings.HasPrefix(pilot.Summary, "When an unassuming") {
		t.Errorf("pilot: %+v", pilot)
	}
	// An episode has its own director, writer and guest cast.
	var roles []db.CreditRole
	for _, credit := range pilot.Credits {
		roles = append(roles, credit.Role)
	}
	if !slices.Equal(roles, []db.CreditRole{db.RoleDirector, db.RoleWriter, db.RoleGuest}) {
		t.Errorf("pilot credits: %+v", pilot.Credits)
	}
	f.title(db.KindEpisode, "Cat's in the Bag...")
	// A season the provider does not know keeps the name its file gave.
	f.title(db.KindEpisode, "Episode 1")
}
