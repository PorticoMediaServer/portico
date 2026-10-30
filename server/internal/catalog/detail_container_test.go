package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestLibraryForDetailNamespacesPlayableAndContainers(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("movies", "Movies", "movie", "/movies")
	tv := c.Library("tv", "TV", "tv", "/tv")
	music := c.Library("music", "Music", "music", "/music")
	books := c.Library("books", "Books", "audiobook", "/books")
	movie := c.Movie(movies, "/movies/Heat.mkv", "Heat", 1995)
	show := c.Show(tv, "Night Show", 2019)
	season := c.Season(show, 1)
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Album", 2026)
	book := c.Book(books, "Book", "Author")
	collection := c.Collection(movies, "Saga", movie)
	c.Drain()
	s := New(c.DB)
	for _, test := range []struct {
		id, library, namespace string
	}{
		{movie.Public, "movies", "item"},
		{show.Public, "tv", "show"},
		{season.Public, "tv", "season"},
		{artist.Public, "music", "artist"},
		{album.Public, "music", "album"},
		{book.Public, "books", "book"},
		{collection.Public, "movies", "collection"},
	} {
		library, namespace, err := s.LibraryForDetail(test.id)
		if err != nil || library != test.library || namespace != test.namespace {
			t.Fatalf("%s: got %q %q %v, want %q %q", test.id, library, namespace, err, test.library, test.namespace)
		}
	}
	if _, _, err := s.LibraryForDetail("not-an-entity"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown detail root: %v", err)
	}
	if DetailNamespace("movie", true) != "item" || DetailNamespace("show", false) != "show" {
		t.Fatal("DetailNamespace does not map playable and container namespaces")
	}
}

func TestContainerDetailProjectsStoredHeaderWithoutChildren(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("movies", "Movies", "movie", "/movies")
	tv := c.Library("tv", "TV", "tv", "/tv")
	music := c.Library("music", "Music", "music", "/music")
	books := c.Library("books", "Books", "audiobook", "/books")
	movie := c.Movie(movies, "/movies/Saga.mkv", "Saga", 1990)
	show := c.Show(tv, "Night Show", 2019)
	season := c.Season(show, 1)
	c.Episode(show, season, 1, "/tv/night-01.mkv")
	c.Episode(show, season, 2, "/tv/night-02.mkv")
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Album", 2026)
	c.Song(album, 1, "/music/one.flac", "One")
	c.Song(album, 2, "/music/two.flac", "Two")
	book := c.Book(books, "Book", "Author")
	c.BookFile(book, 1, "/books/one.m4b")
	c.Fields(show.ID, map[string]any{"overview": "Show overview", "original_title": "Original Show", "tagline": "A tagline", "content_rating": "TV-14", "studio": "Studio", "network": "Network", "country": "US"})
	c.Fields(season.ID, map[string]any{"overview": "Season overview"})
	c.Fields(artist.ID, map[string]any{"overview": "Artist overview", "country": "UK"})
	c.Fields(album.ID, map[string]any{"overview": "Album overview", "release_date": "2026-02-03"})
	c.Fields(book.ID, map[string]any{"overview": "Book overview", "edition": "Unabridged", "year": 2001})
	collection := c.Collection(movies, "Saga", movie)
	c.Fields(collection.ID, map[string]any{"overview": "Collection overview"})
	c.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,revision) VALUES('p',?,1,1,1)`, album.ID)
	c.Exec(`INSERT INTO container_personal_state(profile_id,kind,container_id,watched,watermark,revision,cleared_through) VALUES('p','album',?,1,'2026-01-01T00:00:00.000Z',1,'2026-01-01T00:00:00.000Z')`, album.ID)
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"movies", "tv", "music", "books"}}
	for _, test := range []struct {
		name, id, library, kind, title, overview string
		year                                     int
	}{
		{"show", show.Public, "tv", "show", "Night Show", "Show overview", 2019},
		{"season", season.Public, "tv", "season", "Night Show · Season 1", "Season overview", 2019},
		{"artist", artist.Public, "music", "artist", "Artist", "Artist overview", 0},
		{"album", album.Public, "music", "album", "Album", "Album overview", 2026},
		{"book", book.Public, "books", "book", "Book", "Book overview", 2001},
		{"collection", collection.Public, "movies", "collection", "Saga", "Collection overview", 0},
	} {
		detail, err := s.Detail(viewer, "server", test.id, false)
		if err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		revision, err := s.ContentRevision(test.library, "p")
		if err != nil {
			t.Fatal(err)
		}
		if detail.Scope != (DetailScope{ServerID: "server", LibraryID: test.library, ItemID: test.id, ViewerFence: "f"}) || detail.Revision != revision {
			t.Fatalf("%s scope/revision: %+v %+v", test.name, detail.Scope, detail.Revision)
		}
		if detail.Item.ID != test.id || detail.Item.LibraryID != test.library || detail.Item.Kind != test.kind || detail.Item.Title != test.title || detail.Item.Year != test.year || detail.Item.Overview != test.overview {
			t.Fatalf("%s header: %+v", test.name, detail.Item)
		}
		// A container publishes no playback surface: no file/child arrays, no
		// availability, no advertised actions or version Options.
		if detail.Item.Available || len(detail.Item.Sources) != 0 || detail.Item.Song != nil || detail.Item.BookFile != nil || detail.Item.Episode != nil || len(detail.Actions) != 0 || detail.Related != nil || detail.Extras != nil {
			t.Fatalf("%s playback surface: %+v", test.name, detail)
		}
		if detail.Markers == nil || detail.Collections == nil || detail.Languages.Audio == nil || detail.Languages.Subtitles == nil || detail.Metadata.Sources == nil || detail.Metadata.Ratings == nil || detail.Metadata.Genres == nil || detail.Metadata.Credits == nil || detail.Personal.Conflicts == nil {
			t.Fatalf("%s empty arrays are not truthful arrays: %+v", test.name, detail)
		}
		if detail.Personal.Status != "current" || detail.Metadata.Status != "available" {
			t.Fatalf("%s status: personal=%q metadata=%q", test.name, detail.Personal.Status, detail.Metadata.Status)
		}
	}
	albumDetail, err := s.Detail(viewer, "server", album.Public, false)
	if err != nil {
		t.Fatal(err)
	}
	if !albumDetail.Personal.Watchlisted || !albumDetail.Personal.Favorite || !albumDetail.Personal.Watched {
		t.Fatalf("container personal state lost: %+v", albumDetail.Personal)
	}
	showDetail, err := s.Detail(viewer, "server", show.Public, false)
	if err != nil {
		t.Fatal(err)
	}
	if showDetail.Facts == nil || showDetail.Facts.OriginalTitle != "Original Show" || showDetail.Facts.Tagline != "A tagline" || showDetail.Facts.ContentRating != "TV-14" || showDetail.Facts.Studio != "Studio" || showDetail.Facts.Network != "Network" || showDetail.Facts.Country != "US" {
		t.Fatalf("show facts: %+v", showDetail.Facts)
	}
	albumDetail, err = s.Detail(viewer, "server", album.Public, false)
	if err != nil || albumDetail.Facts == nil || albumDetail.Facts.ReleaseDate != "2026-02-03" {
		t.Fatalf("album facts: %+v %v", albumDetail.Facts, err)
	}
	bookDetail, err := s.Detail(viewer, "server", book.Public, false)
	if err != nil || bookDetail.Facts == nil || bookDetail.Facts.Edition != "Unabridged" {
		t.Fatalf("book facts: %+v %v", bookDetail.Facts, err)
	}
}

func TestContainerDetailDoesNotReadEveryChild(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	music := c.Library("music", "Music", "music", "/music")
	books := c.Library("books", "Books", "audiobook", "/books")
	showOne := c.Show(tv, "One Episode", 2020)
	seasonOne := c.Season(showOne, 1)
	c.Episode(showOne, seasonOne, 1, "/tv/one-01.mkv")
	showMany := c.Show(tv, "Many Episodes", 2020)
	seasonMany := c.Season(showMany, 1)
	for i := 1; i <= 40; i++ {
		c.Episode(showMany, seasonMany, i, fmt.Sprintf("/tv/many-%02d.mkv", i))
	}
	artist := c.Artist(music, "Artist")
	albumOne := c.Album(artist, "One Track", 2020)
	c.Song(albumOne, 1, "/music/one.flac", "One")
	albumMany := c.Album(artist, "Many Tracks", 2020)
	for i := 1; i <= 40; i++ {
		c.Song(albumMany, i, fmt.Sprintf("/music/many-%02d.flac", i), fmt.Sprintf("Track %d", i))
	}
	bookOne := c.Book(books, "One Part", "Author")
	c.BookFile(bookOne, 1, "/books/one.m4b")
	bookMany := c.Book(books, "Many Parts", "Author")
	for i := 1; i <= 40; i++ {
		c.BookFile(bookMany, i, fmt.Sprintf("/books/many-%02d.m4b", i))
	}
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"tv", "music", "books"}}
	cost := func(id string) dbwork.Cost {
		t.Helper()
		ctx, measured := dbwork.Measure(context.Background())
		if _, err := s.WithContext(ctx).Detail(viewer, "server", id, false); err != nil {
			t.Fatal(err)
		}
		return measured()
	}
	for _, pair := range [][2]string{
		{showOne.Public, showMany.Public},
		{albumOne.Public, albumMany.Public},
		{bookOne.Public, bookMany.Public},
	} {
		one, many := cost(pair[0]), cost(pair[1])
		if one.Statements != many.Statements {
			t.Fatalf("detail statements grow with children: one=%d many=%d", one.Statements, many.Statements)
		}
		if many.Statements > 40 {
			t.Fatalf("container detail %d statements exceeds the bounded detail surface", many.Statements)
		}
	}
}

func TestContainerDetailConcealsContainerWithNoVisibleMember(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	music := c.Library("music", "Music", "music", "/music")
	hiddenShow := c.Show(tv, "Hidden Show", 2020)
	hiddenSeason := c.Season(hiddenShow, 1)
	hiddenEpisode := c.Episode(hiddenShow, hiddenSeason, 1, "/tv/hidden-01.mkv")
	mixedShow := c.Show(tv, "Mixed Show", 2020)
	mixedSeason := c.Season(mixedShow, 1)
	hiddenMixedEpisode := c.Episode(mixedShow, mixedSeason, 1, "/tv/mixed-01.mkv")
	visibleMixedEpisode := c.Episode(mixedShow, mixedSeason, 2, "/tv/mixed-02.mkv")
	artist := c.Artist(music, "Artist")
	hiddenAlbum := c.Album(artist, "Hidden Album", 2020)
	hiddenSong := c.Song(hiddenAlbum, 1, "/music/hidden.flac", "Hidden")
	mixedAlbum := c.Album(artist, "Mixed Album", 2020)
	hiddenMixedSong := c.Song(mixedAlbum, 1, "/music/mixed-hidden.flac", "Hidden")
	visibleMixedSong := c.Song(mixedAlbum, 2, "/music/mixed-visible.flac", "Visible")
	for _, item := range []catalogtest.Item{hiddenEpisode, hiddenMixedEpisode, hiddenSong, hiddenMixedSong} {
		c.Attributes(item.ID, "contentRating", "R")
	}
	c.Attributes(visibleMixedEpisode.ID, "contentRating", "G")
	c.Attributes(visibleMixedSong.ID, "contentRating", "G")
	c.Exec(`INSERT INTO content_rating_pending(value_key,value) VALUES('g','G'),('r','R') ON CONFLICT(value_key) DO UPDATE SET value=excluded.value`)
	c.Drain()
	s := New(c.DB)
	if _, err := s.ClassifyPendingRatings(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Drain()
	age := 13
	viewer := Viewer{Profile: "p", Fence: "restricted", Libraries: []string{"tv", "music"}, Restrictions: identity.ContentRestrictions{MaximumAge: &age, BlockUnrated: true}}
	for _, hidden := range []string{hiddenShow.Public, hiddenSeason.Public, hiddenAlbum.Public} {
		if _, err := s.Detail(viewer, "server", hidden, false); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("hidden container %s returned a detail: %v", hidden, err)
		}
	}
	for _, visible := range []string{mixedShow.Public, mixedSeason.Public, mixedAlbum.Public} {
		if _, err := s.Detail(viewer, "server", visible, false); err != nil {
			t.Fatalf("mixed container %s is hidden: %v", visible, err)
		}
	}
}

func TestContainerDetailWaitsForItsFI1Head(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "Night Show", 2019)
	season := c.Season(show, 1)
	c.Episode(show, season, 1, "/tv/night-01.mkv")
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"tv"}}
	if _, err := s.Detail(viewer, "server", show.Public, false); err != nil {
		t.Fatal(err)
	}
	c.Exec(`UPDATE catalog_entities SET info_desired_epoch=info_desired_epoch+1 WHERE id=?`, show.ID)
	if _, err := s.Detail(viewer, "server", show.Public, false); !errors.Is(err, compactcatalog.ErrInfoPreparing) {
		t.Fatalf("unready container composed a detail: %v", err)
	}
	c.Exec(`UPDATE catalog_entities SET info_applied_epoch=info_desired_epoch WHERE id=?`, show.ID)
	if _, err := s.Detail(viewer, "server", show.Public, false); err != nil {
		t.Fatalf("ready container did not compose: %v", err)
	}
}

func TestPlayableDetailIsUnchangedByContainerDispatch(t *testing.T) {
	c := catalogtest.Open(t)
	movies := c.Library("movies", "Movies", "movie", "/movies")
	movie := c.Movie(movies, "/movies/Heat.mkv", "Heat", 1995)
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"movies"}}
	detail, err := s.Detail(viewer, "server", movie.Public, false)
	if err != nil {
		t.Fatal(err)
	}
	play := false
	for _, action := range detail.Actions {
		if action.ID == "play" && action.Playback != nil && action.Playback.ItemID == movie.Public {
			play = true
		}
	}
	if !play || !detail.Item.Available || detail.Item.Kind != "movie" {
		t.Fatalf("playable detail changed: %+v", detail)
	}
}
