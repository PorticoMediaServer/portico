package metadata

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Reuse the source a retained selection of the same provider image already
// published at the current policy. There is no new cache lifetime: the source
// lives only as long as the usual references.
func (s *Service) sharedArtwork(ctx context.Context, j artworkWork) (artworkMaterial, bool, error) {
	var m artworkMaterial
	if j.sizeRefresh || j.provider == "local" || j.provider == "upload" || j.actor != "" {
		return m, false, nil
	}
	edge, _ := artworkSlotLimits(ctx, artworkSlotLarge)
	var acceptedWidth, acceptedHeight int
	err := dbwork.QueryRow(ctx, s.db, `SELECT o.digest,o.accepted_width,o.accepted_height FROM artwork_candidates c JOIN artwork_selections s ON s.candidate_id=c.id JOIN artwork_sources o ON o.digest=s.digest WHERE c.provider=? AND c.origin=? AND o.large_edge=? AND o.encoder_revision=? LIMIT 1`, j.provider, j.origin, edge, artworkEncoderRevision).Scan(&m.source, &acceptedWidth, &acceptedHeight)
	if errors.Is(err, sql.ErrNoRows) {
		return m, false, nil
	}
	if err != nil {
		return m, false, err
	}
	if max(acceptedWidth, acceptedHeight) < edge && fullArtworkOrigin(j.origin, j.provider) != j.origin {
		// A retained provider thumbnail does not prove the original was small.
		return m, false, nil
	}
	if !s.publishedArtworkSource(ctx, m.source, edge) {
		return m, false, nil
	}
	m.reused, m.largeEdge = true, edge
	return m, true, nil
}

// acquireArtworkMaterial fetches and materialises the job's image. The work
// stays in the producer job until the source is published: re-fetched bytes
// with another hash are simply another source.
func (s *Service) acquireArtworkMaterial(ctx context.Context, j artworkWork) (artworkMaterial, error) {
	m, held, e := s.sharedArtwork(ctx, j)
	if e != nil || held {
		return m, e
	}
	origin := j.origin
	if !j.preview {
		origin = fullArtworkOrigin(origin, j.provider)
	}
	raw, e := s.fetchArtwork(ctx, origin, j.provider)
	if e != nil {
		return m, e
	}
	m, e = s.materializeArtwork(ctx, raw, map[string]bool{"jpeg": true, "png": true})
	if errors.Is(e, ErrArtworkSpace) {
		e = errArtworkStorage
	}
	return m, e
}

// Use the original of the same admitted TMDB image, never a larger host/origin
// permission. Candidate identity, source fence and stored origin are unchanged.
// Small discovery previews keep their bounded provider representation.
func fullArtworkOrigin(raw, provider string) string {
	if provider != "tmdb" || !artworkURLAllowed(raw, provider) {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parts := strings.SplitN(strings.TrimPrefix(u.Path, "/t/p/"), "/", 2)
	if len(parts) != 2 || parts[1] == "" || len(parts[0]) < 2 || len(parts[0]) > 6 || (parts[0][0] != 'w' && parts[0][0] != 'h') || parts[0][1] < '1' || parts[0][1] > '9' {
		return raw
	}
	for _, digit := range parts[0][1:] {
		if digit < '0' || digit > '9' {
			return raw
		}
	}
	u.Path = "/t/p/original/" + parts[1]
	u.RawPath = ""
	return u.String()
}
