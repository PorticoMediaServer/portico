package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/fetch"
)

// A song's lyrics, set by the owner and administrators (definition 04, rule 64): pasted
// lyrics are what everyone sees and no fetch replaces them; Fetch lyrics asks LRCLIB for one
// song, in any library. Fetching for a whole library is the fetch_lyrics job.

// lyricsCredit names LRCLIB where its lyrics are shown (definition 04, rule 67).
const lyricsCredit = "Lyrics provided by LRCLIB"

// LyricsPaste is lyrics the owner pastes for a song: plain text, or LRC with [mm:ss.xx] times.
type LyricsPaste struct {
	Text string `json:"text"`
	// Language is the lyrics' language, as a two or three letter code; optional.
	Language string `json:"language,omitempty"`
}

// LyricsFetched says whether new lyrics were found and stored.
type LyricsFetched struct {
	Found bool `json:"found"`
}

func (s *Server) lyricsAdminRoutes() {
	library := func(r *Route) { r.Scope = ScopeLibrary }
	library(Handle(s, "PUT /v1/admin/items/{id}/lyrics", Admin, s.pasteLyrics))
	library(Handle(s, "POST /v1/admin/items/{id}/lyrics/fetch", Admin, s.fetchLyrics))
}

func (s *Server) pasteLyrics(r *http.Request, req LyricsPaste) (Page[Lyrics], error) {
	song, err := s.db.VisibleSong(r.Context(), viewer(r), r.PathValue("id"))
	if err != nil {
		return Page[Lyrics]{}, refuse(err)
	}
	text := strings.TrimSpace(strings.ReplaceAll(req.Text, "\r\n", "\n"))
	language := strings.ToLower(strings.TrimSpace(req.Language))
	switch {
	case text == "":
		return Page[Lyrics]{}, ErrField("text", "Paste the lyrics.")
	case len(text) > db.MaxLyrics:
		return Page[Lyrics]{}, ErrField("text", "Lyrics can be at most 256 KB.")
	case language != "" && !languageCode.MatchString(language):
		return Page[Lyrics]{}, ErrField("language", "Give the language as a two or three letter code.")
	}
	for _, line := range strings.Split(text, "\n") {
		if err := checkLabel("text", strings.ReplaceAll(line, "\t", " "), db.MaxLyrics); err != nil {
			return Page[Lyrics]{}, err
		}
	}
	_, _, timed := parseLyrics(text)
	if _, err := s.db.SetLyrics(r.Context(), db.User, song, db.Lyrics{Source: db.LyricsPasted, Body: text, Timed: timed, Language: language}); err != nil {
		return Page[Lyrics]{}, refuse(err)
	}
	return s.itemLyrics(r, None{})
}

func (s *Server) fetchLyrics(r *http.Request, _ None) (LyricsFetched, error) {
	song, err := s.db.VisibleSong(r.Context(), viewer(r), r.PathValue("id"))
	if err != nil {
		return LyricsFetched{}, refuse(err)
	}
	found, err := s.metadata.FetchLyrics(r.Context(), song)
	switch {
	case errors.Is(err, metadata.ErrNoLyricsProvider):
		return LyricsFetched{}, ErrConflict("lyrics_lookup_not_set_up", "Lyrics lookup isn't set up on this server.")
	case err != nil:
		var pause *fetch.SlowDown
		if errors.As(err, &pause) {
			return LyricsFetched{}, ErrTooMany("provider_paused", "LRCLIB asked to wait a little. Try again shortly.", time.Until(pause.Until))
		}
		if errors.Is(err, db.ErrNotFound) {
			return LyricsFetched{}, ErrNotFound()
		}
		return LyricsFetched{}, refusal(http.StatusBadGateway, "provider_unavailable", "LRCLIB did not answer. Try again later.", true)
	}
	return LyricsFetched{Found: found}, nil
}
