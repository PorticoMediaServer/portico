// Package metadata gives the titles a scan found their details: it matches a title to the
// library's provider, fetches what the provider knows and hands it to the database to store.
//
// How the area is laid out:
//
//   - Provider (this file) is the one interface an online source implements. internal/metadata/tmdb
//     is the first. A source for music, books or comics is another implementation of it; nothing
//     else in the area knows which provider it is talking to.
//   - match.go is the matcher: it scores what a provider's search answered against the title's
//     name and year and says whether one answer is confident enough to use by itself.
//   - service.go ties them together, one title at a time: read the title from the database, ask
//     the provider, then store the answer.
//   - The write path is in internal/db (metadata.go there). One request or one background step is
//     one transaction. A provider is always asked OUTSIDE any transaction: the service first reads,
//     then makes every network call, then writes once. Work nobody is waiting for writes with
//     db.Background, so a person's tap never waits behind a refresh.
//
// What the database write decides, in one place, so no caller can get it wrong: a field or list
// a person locked is never replaced; a value an NFO file or the file's own tags gave is never
// replaced by a provider's; Tags and Labels are the owner's and are never touched; a provider
// that answered nothing blanks nothing.
//
// "Local metadata only" on a library means the provider is never asked about its titles. It is
// a promise not to replace local details, not a privacy promise: a subtitle or lyrics search a
// person starts by hand still runs there.
//
// What plugs in where, for the parts not built yet:
//
//   - Music, book and comic sources: implement Provider. Children answers an album's tracks the
//     way it answers a season's episodes.
//   - Subtitle search and download: a client for the one subtitle source, storing each download
//     with the subtitles table's write function and the picture store's twin for text files.
//     It is never started by the server itself in a local-only library.
//   - Lyrics from tags, a provider or pasted text: the lyrics table's one write function, which
//     keeps pasted lyrics over everything else.
//   - Title data (moods and similar titles): a step of the service that downloads the published
//     file, checks it against its signed list, and writes mood and theme tags by provider id
//     (item_external_ids). Similar titles are read from the file, not copied into the database.
//   - People's and profiles' pictures: images rows owned by a person or a profile, with the
//     bytes in the picture store.
//   - Track choice memory is not here: it is the audio and subtitle language kept in
//     user_item_state for a film, or for a show so that every episode follows it.
package metadata

import (
	"context"
	"errors"
)

// Ref names one thing at a provider: TMDB's film 603 is {"tmdb", "movie", "603"}.
type Ref struct {
	Provider string
	Kind     string // movie, show, episode, person, album, artist, book
	ID       string
}

// Locale is the library's metadata language and country. The country decides which content
// rating is taken.
type Locale struct {
	Language string // "en", "pt-BR"
	Region   string // "US"
}

// Query is what a title's files say about it.
type Query struct {
	Kind  string // movie or show, as in Ref
	Title string
	Year  int // 0 when the names give none
	// IDs are ids the files carry for the title at other providers (an IMDb id in an NFO
	// file). A provider that can look one up answers that title alone, marked Exact.
	IDs          []Ref
	Locale       Locale
	IncludeAdult bool
}

// Candidate is one answer to a search, with enough to show beside the others.
type Candidate struct {
	Ref           Ref
	Title         string
	OriginalTitle string
	Year          int
	Overview      string
	PosterURL     string
	Popularity    float64 // the provider's own order, larger first; only breaks ties
	Exact         bool    // found by an id, not by name: it is the title
}

// Credit is a person's part in a title, in the title's own order.
type Credit struct {
	Person    Ref // empty when the provider gives the person no id
	Name      string
	Role      string // actor, director, writer, producer, composer, author, narrator, artist, guest, crew
	Character string
}

// Picture is a picture the provider offers for a title or a person.
type Picture struct {
	Role     string // poster, backdrop, logo, thumb, banner, cover, portrait
	URL      string
	Language string
	Width    int
	Height   int
}

// Record is what a provider knows about one title. An empty field means the provider did not
// say, never "blank this".
type Record struct {
	Ref           Ref
	Title         string
	OriginalTitle string
	Overview      string
	Tagline       string
	Year          int
	ReleaseDate   string  // YYYY-MM-DD
	ContentRating string  // for the Locale's region
	Rating        float64 // out of 10
	Genres        []string
	Studios       []string
	Networks      []string
	Countries     []string
	Credits       []Credit
	Pictures      []Picture
	OtherIDs      []Ref // the same title at other providers: its IMDb id
	// Still running: a show that has not ended is refreshed sooner.
	Continuing bool
	// For a child: its number, and the number of what holds it (an episode's season).
	Index       int
	ParentIndex int
	// Groups are the numbers Children can be asked for: a show's seasons, an album's discs.
	Groups []int
	// Collection is the series of films this one belongs to, if any.
	Collection *Candidate
}

// Provider is one online source of details. Every method is a network call: never call one
// while a database transaction is open. A method fails with ErrNotFound when the provider
// answered and has no such thing, and with any other error when it could not be asked, in
// which case the title is left exactly as it was and asked about again later.
type Provider interface {
	// Name is the provider's name as libraries and external ids store it: "tmdb".
	Name() string
	// Search answers the titles that could be the one asked about, best first.
	Search(ctx context.Context, q Query) ([]Candidate, error)
	// Details answers everything about one title.
	Details(ctx context.Context, ref Ref, locale Locale) (Record, error)
	// Children answers what a title holds in one group: the episodes of a show's season, the
	// tracks of an album's disc.
	Children(ctx context.Context, parent Ref, group int, locale Locale) ([]Record, error)
}

// ErrNotFound is a provider's answer that it has no such title.
var ErrNotFound = errors.New("the provider has no such title")
