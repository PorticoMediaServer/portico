package metadata

import (
	"context"
	"errors"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

func (s *Service) discoverTVDBArtworkFor(ctx context.Context, id, kind, locale string) ([]discoveredArtwork, error) {
	p, ok := s.tvdb.(interface {
		Artwork(context.Context, int64) ([]metadataprovider.ArtworkImage, error)
	})
	if !ok {
		return nil, errors.New("artwork_provider_unavailable")
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n <= 0 {
		return nil, ErrRepairInput
	}
	var images []metadataprovider.ArtworkImage
	if modern, ok := s.tvdb.(interface {
		ArtworkForKind(context.Context, string, int64, string) ([]metadataprovider.ArtworkImage, error)
	}); ok {
		typ := "show"
		if kind != "show" {
			typ = "movie"
		}
		images, err = modern.ArtworkForKind(ctx, typ, n, locale)
	} else {
		images, err = p.Artwork(ctx, n)
	}
	if err != nil {
		return nil, err
	}
	out := []discoveredArtwork{}
	for _, im := range images {
		if validArtworkRole(im.Role) && artworkURLAllowed(im.URL, "tvdb") {
			out = append(out, discoveredArtwork{role: im.Role, provider: "tvdb", imageID: strconv.FormatInt(im.ID, 10), origin: im.URL, locale: im.Language, attribution: artworkAttribution("tvdb"), rank: im.Score, votes: -1})
		}
	}
	// Season posters and cast photos (Spec — Page Content §2).
	if kind == "show" {
		out = append(out, s.tvdbPageArtwork(ctx, n)...)
	}
	return out, nil
}
