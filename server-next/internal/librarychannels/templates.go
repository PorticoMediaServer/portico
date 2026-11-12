package librarychannels

import (
	"slices"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Template is a whole channel filled in for the owner to review. Nothing is made until it is saved.
type Template struct {
	Key         string
	Name        string
	Description string
	Builder     LibraryChannelBuilder
}

func condition(field string, values ...string) db.LibraryChannelCondition {
	return db.LibraryChannelCondition{Field: field, Values: values}
}

// Templates are the nine starting points, filled in with this server's libraries of each kind.
// libraryKinds maps each library's public id to its kind.
func Templates(libraryKinds map[string]string) []Template {
	var movies, shows, anime []string
	for id, kind := range libraryKinds {
		switch kind {
		case db.LibraryMovies:
			movies = append(movies, id)
		case db.LibraryShows:
			shows = append(shows, id)
		case db.LibraryAnime:
			anime = append(anime, id)
		}
	}
	for _, list := range [][]string{movies, shows, anime} {
		slices.Sort(list)
	}
	film := func(extra LibraryChannelSelection) LibraryChannelSelection {
		extra.Libraries, extra.Kinds = movies, []string{"movie"}
		if extra.Order == "" {
			extra.Order = "shuffle"
		}
		return extra
	}
	return []Template{
		{"movies", "Movie channel", "Every movie, shuffled, with no repeats.", LibraryChannelBuilder{LibraryChannelSelection: film(LibraryChannelSelection{})}},
		{"television", "Television channel", "Shows from your TV libraries, each in episode order, rotating among shows.",
			LibraryChannelBuilder{LibraryChannelSelection: LibraryChannelSelection{Libraries: shows, Kinds: []string{"show"}, Order: "shuffle", Episodes: "showOrder"}}},
		{"eighties", "Eighties movies", "Movies released from 1980 through 1989.",
			LibraryChannelBuilder{LibraryChannelSelection: film(LibraryChannelSelection{Conditions: []db.LibraryChannelCondition{condition("year", "1980..1989")}})}},
		{"nineties", "Nineties movies", "Movies released from 1990 through 1999.",
			LibraryChannelBuilder{LibraryChannelSelection: film(LibraryChannelSelection{Conditions: []db.LibraryChannelCondition{condition("year", "1990..1999")}})}},
		{"thriller", "Thriller channel", "Thrillers from your movies.",
			LibraryChannelBuilder{LibraryChannelSelection: film(LibraryChannelSelection{Conditions: []db.LibraryChannelCondition{condition("genre", "Thriller")}})}},
		{"family", "Family movies", "Movies in the Family genre.",
			LibraryChannelBuilder{LibraryChannelSelection: film(LibraryChannelSelection{Conditions: []db.LibraryChannelCondition{condition("genre", "Family")}})}},
		{"anime", "Anime channel", "Shows from your anime libraries, each in episode order.",
			LibraryChannelBuilder{LibraryChannelSelection: LibraryChannelSelection{Libraries: anime, Kinds: []string{"show"}, Order: "shuffle", Episodes: "showOrder"}}},
		{"recent", "Recently added movies", "Movies added in the last 30 days, newest first.",
			LibraryChannelBuilder{LibraryChannelSelection: film(LibraryChannelSelection{AddedWithinDays: 30, Order: "inOrder", Sort: "recent"})}},
		{"marathons", "TV marathons", "Up to twelve episodes of one show in a row, then another show.",
			LibraryChannelBuilder{LibraryChannelSelection: LibraryChannelSelection{Libraries: shows, Kinds: []string{"show"}, Order: "shuffle", Episodes: "marathon", Marathon: 12}}},
	}
}
