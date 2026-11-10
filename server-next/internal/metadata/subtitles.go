package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server-next/internal/atomicfile"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

// SubtitleQuery is what a subtitle search asks for: a film, or an episode of a show, in the
// languages the person wants.
type SubtitleQuery struct {
	Languages []string // ISO 639 codes, two or three letters, as the person's settings hold them
	Title     string   // the film's, or the episode's show's
	Year      int
	IMDbID    string // the film's or the episode's own ids, when known
	TMDBID    string
	// An episode's show's ids and its numbers. Season is -1 for a film.
	ShowIMDbID string
	ShowTMDBID string
	Season     int
	Episode    int
}

// SubtitleResult is one subtitle a search found. Ref is the provider's id for its file.
type SubtitleResult struct {
	Ref             string `json:"ref"`
	Language        string `json:"language"` // ISO 639-2, three letters, as the server's tracks have it
	Release         string `json:"release"`  // the release it was timed for
	HearingImpaired bool   `json:"sdh"`
	Forced          bool   `json:"forced"`
	Downloads       int    `json:"downloads"`
}

// SubtitleProvider is the one online source of subtitles, OpenSubtitles. Its methods are
// network calls: never call one while a database transaction is open.
type SubtitleProvider interface {
	Name() string
	Search(ctx context.Context, q SubtitleQuery) ([]SubtitleResult, error)
	// Download answers a subtitle's file and its format (srt).
	Download(ctx context.Context, ref string) ([]byte, string, error)
}

// ErrNoSubtitleProvider: the owner has not set up OpenSubtitles on this server.
var ErrNoSubtitleProvider = errors.New("subtitle search isn't set up on this server")

// SubtitlePath is where a downloaded subtitle's text is kept, under the folder for them.
func SubtitlePath(dir, hash string) string { return filepath.Join(dir, hash[:2], hash) }

// SearchSubtitles looks a film or an episode the viewer may play up at the subtitle provider.
// Anyone who may play a title may search, in a library set to local metadata only too: it is
// a person asking, never the server by itself.
func (s *Service) SearchSubtitles(ctx context.Context, v db.Viewer, itemPublicID string, languages []string) ([]SubtitleResult, error) {
	if s.Subtitles == nil {
		return nil, ErrNoSubtitleProvider
	}
	target, err := s.DB.SubtitleTarget(ctx, v, itemPublicID, "")
	if err != nil {
		return nil, err
	}
	q := SubtitleQuery{Languages: languages, Title: target.Title, Year: target.Year, Season: -1, Episode: -1}
	id := func(ids []db.ExternalID, provider string) string {
		for _, e := range ids {
			if e.Provider == provider {
				return e.ID
			}
		}
		return ""
	}
	q.IMDbID, q.TMDBID = id(target.IDs, "imdb"), id(target.IDs, "tmdb")
	if target.Kind == db.KindEpisode {
		q.Title, q.Year, q.Season, q.Episode = target.Show, 0, target.Season, target.Episode
		q.ShowIMDbID, q.ShowTMDBID = id(target.ShowIDs, "imdb"), id(target.ShowIDs, "tmdb")
	}
	return s.Subtitles.Search(ctx, q)
}

// DownloadSubtitle downloads a subtitle a search found and keeps it with a version of the
// title (versionPublicID, or "" for its first), where it is offered to everyone who may play
// it. The language and kinds are the chosen result's. Downloading the same one again answers
// the one already kept.
func (s *Service) DownloadSubtitle(ctx context.Context, v db.Viewer, itemPublicID, versionPublicID string, chosen SubtitleResult) (db.Subtitle, error) {
	if s.Subtitles == nil {
		return db.Subtitle{}, ErrNoSubtitleProvider
	}
	target, err := s.DB.SubtitleTarget(ctx, v, itemPublicID, versionPublicID)
	if err != nil {
		return db.Subtitle{}, err
	}
	raw, format, err := s.Subtitles.Download(ctx, chosen.Ref)
	if err != nil {
		return db.Subtitle{}, err
	}
	// Kept as UTF-8, and only when it reads as subtitles: never an error page or a binary.
	text, err := subtitles.DecodeSubtitleText(raw, chosen.Language)
	if err != nil {
		return db.Subtitle{}, fmt.Errorf("the downloaded subtitle cannot be read: %w", err)
	}
	if _, err := subtitles.ParseTolerant(text, format, 0); err != nil {
		return db.Subtitle{}, fmt.Errorf("the downloaded subtitle cannot be read: %w", err)
	}
	sum := sha256.Sum256(text)
	hash := hex.EncodeToString(sum[:])
	path := SubtitlePath(s.SubtitleDir, hash)
	if _, err := os.Stat(path); err != nil {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return db.Subtitle{}, err
		}
		if err := atomicfile.Write(path, text, 0o644); err != nil {
			return db.Subtitle{}, err
		}
	}
	return s.DB.AddSubtitle(ctx, db.Subtitle{MediaID: target.MediaID, Provider: s.Subtitles.Name(), ProviderRef: chosen.Ref, FileHash: hash,
		Format: format, Language: chosen.Language, Title: chosen.Release, Forced: chosen.Forced, HearingImpaired: chosen.HearingImpaired})
}

// RemoveSubtitle removes a downloaded subtitle, and its file when no other version uses it.
// Only the owner and administrators may; the route checks.
func (s *Service) RemoveSubtitle(ctx context.Context, publicID string) error {
	hash, stillUsed, err := s.DB.RemoveSubtitle(ctx, publicID)
	if err != nil || stillUsed {
		return err
	}
	if err := os.Remove(SubtitlePath(s.SubtitleDir, hash)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
