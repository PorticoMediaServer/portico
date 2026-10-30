package metadata

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// The Portico Music agent's artist chain (Spec — Page Content §8): a
// MusicBrainz artist's Wikidata relation leads to the Wikipedia biography and
// the Wikimedia Commons portrait. It runs when MusicBrainzStep has no album
// or song work, a bounded page per step, and stores everything in the
// additive music_artist_evidence table (0261). No separate provider, no key, no
// per-source toggles: the chain is internal to the Portico Music agent.

// musicArtistSource is the one MusicBrainz call the chain needs. It is
// asserted, not part of MusicBrainzProvider, so the album/song fakes keep
// working; production's *MusicBrainz implements it.
type musicArtistSource interface {
	Artist(context.Context, string) (metadataprovider.MusicArtist, error)
}

type wikidataArtistSource interface {
	EntityArtist(context.Context, string) (metadataprovider.WikidataArtist, error)
}

type wikipediaSource interface {
	Summary(context.Context, string) (metadataprovider.WikipediaSummary, error)
}

type commonsSource interface {
	ImageInfo(context.Context, string) (metadataprovider.CommonsImage, error)
}

// musicArtistPage bounds one step: the chain is up to four upstream requests
// per artist, each on a 1 request/second transport.
const musicArtistPage = 2

// musicArtistWindow is how many artists one step examines: the walk moves a
// window along catalog_artists and wraps, so an idle worker reads a bounded
// slice per step, never every album or artist of the library.
const musicArtistWindow = 64

// musicArtistAttempts bounds retries for an artist the chain cannot supply.
// musicArtistRefresh is how long a completed row is trusted before the chain
// runs again for it.
const musicArtistAttempts = 5

const musicArtistRefresh = 90 * 24 * time.Hour

type musicArtistClaim struct {
	artist        int64
	public        string
	library, mbid string
}

type musicArtistFacts struct {
	name, country, wikidata string
	artistType              string
	begin, end              int
	bio, bioSource, bioURL  string
	imageURL, imageLicence  string
	complete                bool
}

// musicArtistStep enriches a bounded page of MusicBrainz-matched artists.
func (s *Service) musicArtistStep(ctx context.Context) error {
	ctx = s.lookupContext(ctx)
	if s.wikidata == nil || s.wikipedia == nil || s.commons == nil {
		return nil
	}
	mb, ok := s.mb.(musicArtistSource)
	if !ok {
		return nil
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	var windowEnd sql.NullInt64
	after := s.musicArtistCursor.Load()
	for {
		if err = tx.QueryRowContext(ctx, `SELECT max(entity_id) FROM (SELECT entity_id FROM catalog_artists WHERE entity_id>? ORDER BY entity_id LIMIT ?)`, after, musicArtistWindow).Scan(&windowEnd); err != nil {
			return err
		}
		if windowEnd.Valid || after == 0 {
			break
		}
		after = 0 // past the last artist: the walk starts again
	}
	if !windowEnd.Valid {
		return nil // no artists
	}
	rows, err := tx.QueryContext(ctx, `SELECT ar.id,pid(ar.public_id),cl.library_id,lower(c.artist_id) FROM catalog_artists window_artist
	 CROSS JOIN catalog_entities ar ON ar.id=window_artist.entity_id
	 JOIN catalog_libraries cl ON cl.id=ar.library_id
	 JOIN mb_album_links l ON l.album_id=(SELECT min(ab.entity_id) FROM catalog_albums ab WHERE ab.artist_id=ar.id)
	 JOIN mb_release_credits c ON c.revision_id=l.release_revision AND c.ordinal=0
	 JOIN libraries li ON li.id=cl.library_id AND li.kind='music'
	 JOIN mb_provider_policies p ON p.library_id=cl.library_id AND p.enabled=1
	 LEFT JOIN music_artist_evidence e ON e.artist_id=ar.id AND e.mbid=lower(c.artist_id)
	 WHERE window_artist.entity_id>? AND window_artist.entity_id<=? AND ar.kind=5 AND (e.artist_id IS NULL OR (e.complete=0 AND e.attempts<?) OR e.observed_at<?)
	 ORDER BY ar.id LIMIT ?`, after, windowEnd.Int64, musicArtistAttempts, s.publicationTime().Add(-musicArtistRefresh).Format(time.RFC3339), musicArtistPage)
	if err != nil {
		return err
	}
	claims := []musicArtistClaim{}
	// A full page resumes after its last artist, so the rest of this window is
	// not skipped; a short page has seen the whole window.
	next, read := windowEnd.Int64, 0
	for rows.Next() {
		var c musicArtistClaim
		if err = rows.Scan(&c.artist, &c.public, &c.library, &c.mbid); err != nil {
			rows.Close()
			return err
		}
		if read++; read == musicArtistPage {
			next = c.artist
		}
		if !mbIdentifier.MatchString(c.mbid) {
			continue
		}
		claims = append(claims, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	s.musicArtistCursor.Store(next)
	if err = gated.Commit(); err != nil {
		return err
	}
	// Fetches run outside the claim transaction: a slow encyclopedia must not
	// hold the write gate while it answers.
	type result struct {
		facts musicArtistFacts
		err   error
	}
	results := make([]result, len(claims))
	for i, c := range claims {
		if err = ctx.Err(); err != nil {
			return err
		}
		results[i].facts, results[i].err = s.fetchMusicArtist(ctx, mb, c.mbid)
	}
	return dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		if err := checkLookupContextTx(ctx, tx); err != nil {
			return err
		}
		stamp := tvdbStamp(s.publicationTime())
		now := s.publicationTime().Format(time.RFC3339Nano)
		for i, c := range claims {
			if results[i].err != nil {
				if _, err := tx.ExecContext(ctx, `INSERT INTO music_artist_evidence(artist_id,mbid,observed_at,attempts) VALUES(?,?,?,1) ON CONFLICT(artist_id,mbid) DO UPDATE SET observed_at=excluded.observed_at,attempts=music_artist_evidence.attempts+1`, c.artist, c.mbid, stamp); err != nil {
					return err
				}
				continue
			}
			f := results[i].facts
			complete := 0
			if f.complete {
				complete = 1
			}
			var begin, end any
			if f.begin > 0 {
				begin = f.begin
			}
			if f.end > 0 {
				end = f.end
			}
			var artistType any
			if f.artistType == "Group" || f.artistType == "Person" {
				artistType = f.artistType
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO music_artist_evidence(artist_id,mbid,observed_at,name,artist_type,country,begin_year,end_year,wikidata_id,bio,bio_source,bio_source_url,image_url,image_licence,complete,attempts) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0) ON CONFLICT(artist_id,mbid) DO UPDATE SET observed_at=excluded.observed_at,name=excluded.name,artist_type=excluded.artist_type,country=excluded.country,begin_year=excluded.begin_year,end_year=excluded.end_year,wikidata_id=excluded.wikidata_id,bio=excluded.bio,bio_source=excluded.bio_source,bio_source_url=excluded.bio_source_url,image_url=excluded.image_url,image_licence=excluded.image_licence,complete=excluded.complete,attempts=0`, c.artist, c.mbid, stamp, f.name, artistType, f.country, begin, end, f.wikidata, f.bio, f.bioSource, f.bioURL, f.imageURL, f.imageLicence, complete); err != nil {
				return err
			}
			if f.imageURL != "" {
				if err := seedMusicArtistPortrait(ctx, tx, c.public, c.library, f.imageURL, now); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// fetchMusicArtist runs one chain. A missing link ends it with what is known
// (MusicBrainz facts are always kept); only a failed request is retried.
func (s *Service) fetchMusicArtist(ctx context.Context, mb musicArtistSource, mbid string) (musicArtistFacts, error) {
	ctx = s.lookupContext(ctx)
	var out musicArtistFacts
	artist, err := mb.Artist(ctx, mbid)
	if err != nil {
		return out, err
	}
	out = musicArtistFacts{artistType: artist.Type, name: artist.Name, country: artist.Country, begin: artist.BeginYear, end: artist.EndYear, wikidata: artist.WikidataID, complete: true}
	if artist.WikidataID == "" {
		return out, nil
	}
	entity, err := s.wikidata.EntityArtist(ctx, artist.WikidataID)
	if err != nil {
		return musicArtistFacts{}, err
	}
	if entity.WikipediaTitle == "" {
		return out, nil
	}
	summary, err := s.wikipedia.Summary(ctx, entity.WikipediaTitle)
	if err != nil {
		var problem *metadataprovider.Error
		if errors.As(err, &problem) && problem.Status == 404 {
			return out, nil
		}
		return musicArtistFacts{}, err
	}
	if strings.TrimSpace(summary.Extract) != "" {
		out.bio, out.bioSource, out.bioURL = summary.Extract, "Wikipedia", summary.PageURL
	}
	// A portrait failure keeps the biography: the next refresh retries the
	// image, and the page is complete without one.
	if entity.ImageFile != "" {
		if image, err := s.commons.ImageInfo(ctx, entity.ImageFile); err == nil && image.FileURL != "" {
			out.imageURL, out.imageLicence = image.FileURL, image.Licence
		}
	}
	return out, nil
}

// seedMusicArtistPortrait records the Commons portrait as a candidate for the
// artist's portrait slot. Local art wins by the ordinary occupancy rule; the
// artwork worker downloads and caches the file like every other image. artist
// is the public id: RepairTarget still takes public ids (the artwork lane
// resolves the integer entity id when it converts).
func seedMusicArtistPortrait(ctx context.Context, tx *sql.Tx, artist, library, origin, now string) error {
	if local, err := LibraryLocalOnlyTx(ctx, tx, library); err != nil || local {
		return err
	}
	if !artworkURLAllowed(origin, "commons") {
		return nil
	}
	t := RepairTarget{"artist", artist}
	fence, err := artworkFence(ctx, tx, t)
	if err != nil {
		return err
	}
	return seedArtworkCandidate(ctx, tx, t, "portrait", "", "commons", publicationDigest(origin), origin, artworkAttribution("commons"), fence, now, 0)
}
