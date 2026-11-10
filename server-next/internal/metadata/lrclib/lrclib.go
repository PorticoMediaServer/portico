// Package lrclib asks LRCLIB (lrclib.net) for a song's lyrics: the lyrics provider a music
// library can use after its files (definition rule 64). It needs no key.
package lrclib

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/fetch"
)

const (
	// Origin is where LRCLIB's API is.
	Origin = "https://lrclib.net"
	// Every is the spacing the server keeps between two requests. LRCLIB states no limit;
	// two a second is polite for a library-wide fetch.
	Every = 500 * time.Millisecond
)

// Client asks LRCLIB. Make one with New and share it.
type Client struct {
	origin string
	http   *fetch.Client
}

// New makes a client. origin is lrclib.Origin with Every, or a test's own server with no spacing.
func New(origin string, every time.Duration) *Client {
	return &Client{origin: strings.TrimRight(origin, "/"), http: fetch.New("lrclib", every, 30*time.Second)}
}

func (c *Client) Name() string { return "lrclib" }

type record struct {
	ID           int64   `json:"id"`
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	Duration     float64 `json:"duration"`
	Instrumental bool    `json:"instrumental"`
	PlainLyrics  string  `json:"plainLyrics"`
	SyncedLyrics string  `json:"syncedLyrics"`
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	raw, err := c.http.Do(req)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// Lyrics answers a song's lyrics, timed where LRCLIB has them timed. With the album and the
// length it asks for that exact recording; without them it searches by title and artist and
// takes the best answer.
func (c *Client) Lyrics(ctx context.Context, q metadata.LyricsQuery) (metadata.FoundLyrics, error) {
	var r record
	if q.Album != "" && q.DurationMS > 0 {
		params := url.Values{"track_name": {q.Title}, "artist_name": {q.Artist}, "album_name": {q.Album},
			"duration": {strconv.FormatInt((q.DurationMS+500)/1000, 10)}}
		if err := c.get(ctx, "/api/get", params, &r); err != nil {
			return metadata.FoundLyrics{}, err
		}
	} else {
		var results []record
		if err := c.get(ctx, "/api/search", url.Values{"track_name": {q.Title}, "artist_name": {q.Artist}}, &results); err != nil {
			return metadata.FoundLyrics{}, err
		}
		if len(results) == 0 {
			return metadata.FoundLyrics{}, metadata.ErrNotFound
		}
		r = results[0]
	}
	switch {
	case r.Instrumental:
		return metadata.FoundLyrics{}, nil
	case strings.TrimSpace(r.SyncedLyrics) != "":
		return metadata.FoundLyrics{Body: r.SyncedLyrics, Timed: true}, nil
	case strings.TrimSpace(r.PlainLyrics) != "":
		return metadata.FoundLyrics{Body: r.PlainLyrics}, nil
	}
	return metadata.FoundLyrics{}, metadata.ErrNotFound
}
