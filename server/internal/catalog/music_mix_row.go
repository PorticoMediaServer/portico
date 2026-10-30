package catalog

import (
	"context"
	"time"
	"unicode/utf16"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

type MusicMixStyle struct{ Tag, Model, Version string }
type MusicMixNameSource struct {
	Style  *MusicMixStyle
	Artist *ContentEntityRef
}
type MusicMixCover struct {
	Album     ContentEntityRef
	PosterURL string
}

// MusicMixCard intentionally contains no song keys. It is the one domain row
// used by Home and Music Discover; the HTTP projection owns its wire shape.
type MusicMixCard struct {
	ID         string
	NameSource MusicMixNameSource
	Tint       string
	Covers     []MusicMixCover
	Artists    []ContentEntityRef
	More       bool
	SongCount  int
}

// Keep the row absent until actual completed listening and current-model
// library coverage are substantial. These are recommendation thresholds, not
// a runtime-availability check or a new persisted setting.
func musicMixesEligible(mixes MusicMixes) bool {
	if mixes.Model == nil || mixes.AnalyzedHistoryTracks < 6 || mixes.LibraryTracks == 0 || mixes.AnalyzedLibraryTracks*5 < mixes.LibraryTracks*4 {
		return false
	}
	plays := 0.0
	for _, mix := range mixes.Mixes {
		plays += mix.Cluster.Weight
	}
	return plays >= 12
}
func musicMixEligible(mix MusicMix) bool {
	return len(mix.Cluster.Tracks) >= 3 && len(mix.Cluster.Artists) > 0 && len(mix.Cluster.CoverAlbums) > 0
}

// No cover palette is currently published by the catalogue. §41's explicit
// fallback is the exact §33 UTF-16/signed-wrap channelTint of the first album.
func musicMixTint(album string) string {
	palette := [...]string{"#28587a", "#985243", "#796120", "#28665d", "#854d73", "#376f8c", "#4a596a", "#685294"}
	var hash int32
	for _, unit := range utf16.Encode([]rune(album)) {
		hash = hash*31 + int32(unit)
	}
	index := int64(hash)
	if index < 0 {
		index = -index
	}
	return palette[index%int64(len(palette))]
}

func (s *Service) MusicMixRow(viewer Viewer, at time.Time) ([]MusicMixCard, error) {
	out := []MusicMixCard{}
	if dbwork.Snapshot(s.Context()) == nil && s.readTx == nil {
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).MusicMixRow(viewer, at)
			return err
		})
		return out, err
	}
	mixes, err := s.MusicMixes(viewer, at)
	if err != nil || !musicMixesEligible(mixes) {
		return out, err
	}
	albums := []string{}
	for _, mix := range mixes.Mixes {
		for _, album := range mix.Cluster.CoverAlbums {
			albums = append(albums, album.ID)
		}
	}
	entries, err := s.homeEngineEntries(viewer.Profile, albums)
	if err != nil {
		return out, err
	}
	posters := map[string]string{}
	for _, entry := range entries {
		posters[entry.ID] = entry.PosterURL
	}
	used := map[string]bool{}
	for _, mix := range mixes.Mixes {
		if !musicMixEligible(mix) {
			continue
		}
		group := mix.Cluster
		card := MusicMixCard{ID: mix.ID, Tint: musicMixTint(group.CoverAlbums[0].ID), Covers: []MusicMixCover{}, Artists: append([]ContentEntityRef(nil), group.Artists...), More: group.MoreArtists, SongCount: len(group.Tracks)}
		scores := []float64{}
		for _, tag := range group.Tags {
			scores = append(scores, tag.Score)
		}
		if tag, ok := sonic.MixStyle(*mixes.Model, scores, used); ok {
			card.NameSource.Style = &MusicMixStyle{Tag: tag, Model: mixes.Model.ID, Version: mixes.Model.Version}
			used[tag] = true
		} else {
			artist := group.Artists[0]
			card.NameSource.Artist = &artist
		}
		// Sample actual albums, preferring distinct ones. A cluster confined to
		// one or two albums repeats those real covers to fill the drawn fan.
		for n := 0; n < 3; n++ {
			album := group.CoverAlbums[n%len(group.CoverAlbums)]
			card.Covers = append(card.Covers, MusicMixCover{Album: album, PosterURL: posters[album.ID]})
		}
		out = append(out, card)
	}
	return out, nil
}
