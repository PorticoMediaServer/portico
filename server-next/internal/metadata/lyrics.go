package metadata

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// LyricsQuery is what a lyrics provider is asked about one song.
type LyricsQuery struct {
	Title      string
	Artist     string
	Album      string
	DurationMS int64 // 0 when not known
}

// FoundLyrics is a lyrics provider's answer. An empty Body says the song has no words (an
// instrumental).
type FoundLyrics struct {
	Body     string
	Timed    bool // LRC lines with times
	Language string
}

// LyricsProvider is the one online source of lyrics, LRCLIB. Lyrics is a network call: never
// call it while a database transaction is open. It fails with ErrNotFound when the provider
// has nothing for the song.
type LyricsProvider interface {
	Name() string
	Lyrics(ctx context.Context, q LyricsQuery) (FoundLyrics, error)
}

// askAgainAfter is how long a song the provider had nothing for waits before it is asked again
// without anyone asking.
const askAgainAfter = 30 * 24 * time.Hour

// ErrNoLyricsProvider: this server has no lyrics provider to ask.
var ErrNoLyricsProvider = errors.New("no lyrics provider is set up")

// FetchLyrics asks the lyrics provider for one song's lyrics because a person asked, in any
// library, one set to local metadata only included. Pasted lyrics and lyrics from the song's
// file are kept. It reports whether new lyrics were stored.
func (s *Service) FetchLyrics(ctx context.Context, itemID int64) (bool, error) {
	if s.Lyrics == nil {
		return false, ErrNoLyricsProvider
	}
	song, err := s.DB.Song(ctx, itemID)
	if err != nil {
		return false, err
	}
	return s.fetchLyrics(ctx, db.User, song)
}

// FetchMissingLyrics asks the lyrics provider about each song of a music library that has no
// lyrics, and returns how many got some. byHand is a person's "Fetch missing lyrics": it runs
// in any music library and asks again about songs the provider had nothing for. Without a
// person asking it runs only where the owner chose "Files, then LRCLIB", never in a library set
// to local metadata only, and asks again about a song only after 30 days.
//
// An error means the provider could not be asked; the songs not reached are as they were.
func (s *Service) FetchMissingLyrics(ctx context.Context, libraryID int64, byHand bool) (int, error) {
	src, err := s.DB.LibrarySource(ctx, libraryID)
	if err != nil {
		return 0, err
	}
	if !byHand && (src.Lyrics != db.LyricsLRCLIB || src.Provider == db.ProviderLocal) {
		return 0, nil
	}
	if s.Lyrics == nil {
		if byHand {
			return 0, ErrNoLyricsProvider
		}
		return 0, nil
	}
	notSince, priority := time.Now().Add(-askAgainAfter), db.Background
	if byHand {
		notSince = time.Now()
	}
	found := 0
	var after *db.LyricsSong
	for {
		songs, err := s.DB.SongsWithoutLyrics(ctx, libraryID, notSince, after, 50)
		if err != nil || len(songs) == 0 {
			return found, err
		}
		for i := range songs {
			after = &songs[i]
			stored, err := s.fetchLyrics(ctx, priority, songs[i])
			if err != nil {
				return found, err
			}
			if stored {
				found++
			}
		}
	}
}

// fetchLyrics asks about one song with no transaction open, then stores the answer. A
// provider with nothing for the song is recorded, so the song is not asked about again at once.
func (s *Service) fetchLyrics(ctx context.Context, p db.Priority, song db.LyricsSong) (bool, error) {
	answer, err := s.Lyrics.Lyrics(ctx, LyricsQuery{Title: song.Title, Artist: song.Artist, Album: song.Album, DurationMS: song.DurationMS})
	if err != nil && !errors.Is(err, ErrNotFound) {
		return false, err
	}
	stored, err := s.DB.SetLyrics(ctx, p, song.ID, db.Lyrics{Source: db.LyricsFromProvider, Body: answer.Body, Timed: answer.Timed,
		Language: answer.Language})
	return stored && answer.Body != "", err
}
