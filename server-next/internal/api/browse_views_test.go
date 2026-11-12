package api

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Each kind of library offers the lists its tabs read: a film or show library its collections,
// an anime library a show library's, and a comic library its comics and their series.
func TestBrowseViews(t *testing.T) {
	for kind, want := range map[string][]View{
		db.LibraryMovies:     {{"movie"}, {"collection"}},
		db.LibraryShows:      {{"show"}, {"episode"}, {"collection"}},
		db.LibraryAnime:      {{"show"}, {"episode"}, {"collection"}},
		db.LibraryMusic:      {{"artist"}, {"album"}, {"track"}},
		db.LibraryAudiobooks: {{"audiobook"}, {"series"}},
		db.LibraryBooks:      {{"book"}, {"series"}},
		db.LibraryComics:     {{"comic"}, {"series"}},
	} {
		if views, _, _ := browseBy(kind); !slices.Equal(views, want) {
			t.Errorf("a %s library offers %v, want %v", kind, views, want)
		}
	}
	// An anime library sorts and filters as a show library does, Recently aired included.
	_, showSorts, showFilters := browseBy(db.LibraryShows)
	_, animeSorts, animeFilters := browseBy(db.LibraryAnime)
	if !slices.Equal(showSorts, animeSorts) || !reflect.DeepEqual(showFilters, animeFilters) {
		t.Errorf("an anime library's sorts and filters differ from a show library's")
	}
}

// A comic library's tab (kind=comic,series) holds each series as one card and the comics that are
// in no series beside them, never a series' issues again: the scan files a comic in a series'
// folder as an issue under the series, and only a comic in no series as a comic.
func TestAComicLibrarysTabHoldsSeriesAndLooseComics(t *testing.T) {
	b := newBookShelf(t)
	page := pngPicture(t, 64, 96)
	zipFile(t, filepath.Join(b.comics, "One Shot.cbz"), [2]string{"p1.png", page})
	zipFile(t, filepath.Join(b.comics, "Saga", "Saga 002.cbz"), [2]string{"p1.png", page})
	b.scan()
	var comics Library
	for _, lib := range get[Libraries](t, b.s, b.owner.AccessToken, "/v1/libraries").Items {
		if lib.Kind == db.LibraryComics {
			comics = lib
		}
	}
	if !slices.Equal(get[Library](t, b.s, b.owner.AccessToken, "/v1/libraries/"+comics.ID).Views, []View{{"comic"}, {"series"}}) {
		t.Fatalf("the comic library's views are not its comics and series")
	}
	var cards []string
	for _, it := range get[ItemPage](t, b.s, b.owner.AccessToken, "/v1/items?library="+comics.ID+"&kind=comic,series&sort=title").Items {
		cards = append(cards, it.Kind+" "+it.Title)
	}
	if !slices.Equal(cards, []string{"comic One Shot", "series Saga"}) {
		t.Errorf("the comic library's tab holds %v, want the loose comic and the series once each", cards)
	}
	// The series' card opens its issues, in order.
	var issues []string
	for _, it := range get[ItemPage](t, b.s, b.owner.AccessToken, "/v1/items?series="+b.find(db.KindSeries, "Saga")+"&sort=order").Items {
		issues = append(issues, it.Kind+" "+it.Title)
	}
	if !slices.Equal(issues, []string{"issue Saga 001", "issue Saga 002"}) {
		t.Errorf("the series opens %v", issues)
	}
}
