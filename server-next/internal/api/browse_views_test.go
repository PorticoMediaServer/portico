package api

import (
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
