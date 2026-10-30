package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func recCandidateHasID(candidates []recCandidate, id string) bool {
	for _, candidate := range candidates {
		if candidate.ID == id {
			return true
		}
	}
	return false
}

func TestItemRecommendationsRecomputeFacetsOnReload(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalogtest.New(t, db)
	films := c.Library("movies", "Movies", "movie", "/movies")
	m1 := c.Movie(films, "/movies/one.mkv", "One", 0)
	m2 := c.Movie(films, "/movies/two.mkv", "Two", 0)
	c.Genres(m1.ID, "tmdb", "Drama")
	c.Genres(m2.ID, "tmdb", "Drama")
	c.Drain()
	s := New(db)
	item, err := s.Get("p", m1.Public)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.recTitleRelations(Viewer{Profile: "p", Fence: "p", Libraries: []string{"movies"}}, item, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 || !relationOrderHas(t, s, first[0], m2.ID) {
		t.Fatal("missing initial related item", first)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetTermsTx(ctx, tx, m2.ID, compactcatalog.VocabGenre, "tmdb", nil)
	})
	c.Drain()
	second, err := s.recTitleRelations(Viewer{Profile: "p", Fence: "p", Libraries: []string{"movies"}}, item, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) > 0 && relationOrderHas(t, s, second[0], m2.ID) {
		t.Fatal("reload served old facet scores")
	}
}

func TestListeningRecommendationsRecomputeFacetsOnReload(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalogtest.New(t, db)
	music := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(music, "Artist")
	other := c.Artist(music, "Other")
	album1 := c.Album(artist, "First release", 2020)
	// A different decade: the artist is the only facet the two albums share.
	album2 := c.Album(artist, "Second release", 1995)
	t1 := c.Song(album1, 1, "/music/First/track.flac", "Track 1")
	t3 := c.Song(album2, 1, "/music/Second/track.flac", "Track 3")
	c.Drain()
	s := New(db)
	item, err := s.Get("p", t1.Public)
	if err != nil {
		t.Fatal(err)
	}
	has := func(specs []relationSpec) bool {
		for _, spec := range specs {
			order, err := spec.order(1)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range order.public {
				if id == album2.Public {
					return true
				}
			}
		}
		return false
	}
	first, err := s.listeningRelations("p", item)
	if err != nil || !has(first) {
		t.Fatal("missing initial album", first, err)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := compactcatalog.SetFactsTx(ctx, tx, album2.ID, map[string]any{"artist_id": other.ID}); err != nil {
			return err
		}
		return compactcatalog.SetSongArtistsTx(ctx, tx, t3.ID, nil)
	})
	c.Drain()
	second, err := s.listeningRelations("p", item)
	if err != nil {
		t.Fatal(err)
	}
	if has(second) {
		t.Fatal("reload served old artist facets")
	}
}

func TestEmptyRelatedPoolDoesNotScanLibrary(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalogtest.New(t, db)
	c.Library("movies", "Movies", "movie", "/movies")
	s := New(db)
	r := HomeRequest{Libraries: []string{"movies"}}
	base, args := recBase(r, []string{})
	var n int
	if err := s.read().QueryRow(base+` SELECT count(*) FROM members`, args...).Scan(&n); err != nil || n != 0 {
		t.Fatal("empty candidates widened to library", n, err)
	}
}

func TestSharedRecommendationEntryPointIsFresh(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalogtest.New(t, db)
	movies := c.Library("movies", "Movies", "movie", "/movies")
	m1 := c.Movie(movies, "/movies/one.mkv", "One", 0)
	m2 := c.Movie(movies, "/movies/two.mkv", "Two", 0)
	c.Genres(m1.ID, "tmdb", "Drama")
	c.Genres(m2.ID, "tmdb", "Drama")
	c.Drain()
	s := New(db)
	viewer := Viewer{Profile: "p", Fence: "fence", Libraries: []string{"movies"}}
	r := HomeRequest{Viewer: viewer, ServerID: "server", Profile: viewer.Profile, ViewerFence: viewer.Fence, Libraries: viewer.Libraries}
	first, err := s.modelledCandidates(r, "recommended")
	if err != nil || !recCandidateHasID(first, m2.Public) {
		t.Fatal(first, err)
	}
	if _, err = db.Exec(`INSERT INTO personal_items(profile_id,item_id,not_interested,revision) VALUES(?,?,1,1)`, viewer.Profile, m2.ID); err != nil {
		t.Fatal(err)
	}
	next, err := s.modelledCandidates(r, "recommended")
	if err != nil {
		t.Fatal(err)
	}
	if recCandidateHasID(next, m2.Public) {
		t.Fatal("shared entry point returned stale personal state")
	}
}

// relationOrderHas reports whether a row's whole order names an entity.
func relationOrderHas(t *testing.T, s *Service, spec relationSpec, entity int64) bool {
	t.Helper()
	order, err := spec.order(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	if order.public == nil {
		return slices.Contains(order.ids, entity)
	}
	var public string
	if err = s.read().QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE id=?`, entity).Scan(&public); err != nil {
		t.Fatal(err)
	}
	return slices.Contains(order.public, public)
}
