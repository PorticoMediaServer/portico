package metadata

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

// Page artwork (Spec — Page Content §2): each episode's own still, each
// season's poster and the cast's photos. All of it comes through the library's
// agent (never for a local-only library), is bounded per show, and is fetched
// and cached by the ordinary artwork worker like every other image.

// seasonImageTarget sends a discovered image to the show's season of that
// number, when the show uses that TVDB episode order.
type seasonImageTarget struct {
	number int
	order  string
}

// seedArtworkCandidate records one image as a candidate for a slot and queues
// it unless the shared source preference or an explicit owner choice holds the slot.
func seedArtworkCandidate(ctx context.Context, tx *sql.Tx, t RepairTarget, role, subject, provider, imageID, origin, attribution, fence, now string, rank float64) error {
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	locale := ""
	if provider != "local" {
		locale = "und"
	}
	id, err := insertArtworkCandidate(ctx, tx, t, role, subject, provider, imageID, origin, locale, attribution, fence, now, rank)
	if err != nil {
		return err
	}
	occupied, err := automaticArtworkOccupied(ctx, tx, t, entity, role, subject, provider, id, fence, true)
	if err != nil {
		return err
	}
	if occupied {
		return nil
	}
	return queueArtworkCandidate(ctx, tx, t, id, "", now, false, false)
}

// seedEpisodeStill turns the TVDB episode image already stored with the
// episode's accepted evidence into a still candidate. No request is made here:
// the image came with the episode page the TVDB agent fetched.
func seedEpisodeStill(ctx context.Context, tx *sql.Tx, t RepairTarget, fence, now string) error {
	if t.Kind != "item" {
		return nil
	}
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	var library, image string
	err = tx.QueryRowContext(ctx, `SELECT cl.library_id,COALESCE(json_extract(pe.payload,'$.image'),'') FROM provider_evidence pe JOIN catalog_entities i ON i.id=pe.item_id JOIN catalog_libraries cl ON cl.id=i.library_id WHERE pe.item_id=? AND pe.provider='tvdb' AND i.kind=4`, entity).Scan(&library, &image)
	if errors.Is(err, sql.ErrNoRows) || err == nil && image == "" {
		return nil
	}
	if err != nil {
		return err
	}
	if local, err := LibraryLocalOnlyTx(ctx, tx, library); err != nil || local {
		return err
	}
	if strings.HasPrefix(image, "/banners/") {
		image = "https://artworks.thetvdb.com" + image
	}
	if !artworkURLAllowed(image, "tvdb") {
		return nil
	}
	return seedArtworkCandidate(ctx, tx, t, "still", "", "tvdb", publicationDigest(image), image, artworkAttribution("tvdb"), fence, now, 0)
}

// seedSeasonArtwork records a season poster discovered with its show.
func seedSeasonArtwork(ctx context.Context, tx *sql.Tx, show RepairTarget, im discoveredArtwork, now string) error {
	showID, err := resolveEntity(ctx, tx, show.ID)
	if err != nil {
		return err
	}
	var order string
	err = tx.QueryRowContext(ctx, `SELECT episode_order FROM tvdb_jobs WHERE show_id=?`, showID).Scan(&order)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if order == "" || order == "default" {
		order = "official"
	}
	if im.season.order != order {
		return nil
	}
	var season int64
	err = tx.QueryRowContext(ctx, `SELECT entity_id FROM catalog_seasons WHERE show_id=? AND number=?`, showID, im.season.number).Scan(&season)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	t := RepairTarget{"season", entityPublic(ctx, tx, season)}
	fence, err := artworkFence(ctx, tx, t)
	if err != nil {
		return err
	}
	return seedArtworkCandidate(ctx, tx, t, im.role, "", im.provider, im.imageID, im.origin, im.attribution, fence, now, im.rank)
}

// tvdbPageArtwork is the season posters and cast photos of a TVDB series, read
// from the extended record the TVDB agent already fetches.
func (s *Service) tvdbPageArtwork(ctx context.Context, id int64) []discoveredArtwork {
	p, ok := s.tvdb.(interface {
		SeriesPageArtwork(context.Context, int64) (metadataprovider.SeriesPageArtwork, error)
	})
	if !ok {
		return nil
	}
	page, err := p.SeriesPageArtwork(ctx, id)
	if err != nil {
		// Series artwork already found stays valid; the page extras are retried
		// with the next discovery.
		return nil
	}
	out := []discoveredArtwork{}
	attribution := artworkAttribution("tvdb")
	for _, person := range page.People {
		if artworkURLAllowed(person.URL, "tvdb") {
			out = append(out, discoveredArtwork{role: "portrait", subject: "tvdb:" + strconv.FormatInt(person.PersonID, 10), provider: "tvdb", imageID: publicationDigest(person.URL), origin: person.URL, locale: "und", attribution: attribution, votes: -1})
		}
	}
	for _, season := range page.Seasons {
		if artworkURLAllowed(season.URL, "tvdb") {
			out = append(out, discoveredArtwork{role: "poster", provider: "tvdb", imageID: publicationDigest(season.URL), origin: season.URL, locale: "und", attribution: attribution, votes: -1, season: &seasonImageTarget{season.Number, season.Order}})
		}
	}
	return out
}

// seedLocalArtwork turns an entity's scanned sidecars (local_artwork, 0263)
// into provider='local' candidates: show posters, backdrops and logos,
// season posters, artist portraits and backdrops, movie logos. Only rows whose
// entity still exists seed anything, so deleted entities leave no candidates.
// The primary upserts and the accepted-alternative page share one budget of
// localArtworkCandidatePage candidate writes per call. It reports whether every
// accepted slot reached a short page in this call.
func seedLocalArtwork(ctx context.Context, tx *sql.Tx, t RepairTarget, fence, now string) (bool, error) {
	switch t.Kind {
	case "show", "season", "artist", "item":
	default:
		return true, nil
	}
	entity, err := resolveEntity(ctx, tx, t.ID)
	if err != nil {
		return false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT role,art_key FROM local_artwork WHERE kind=? AND entity_id=? AND (
	 (?='show' AND EXISTS(SELECT 1 FROM catalog_entities e WHERE e.id=local_artwork.entity_id AND e.kind=2)) OR
	 (?='season' AND EXISTS(SELECT 1 FROM catalog_entities e WHERE e.id=local_artwork.entity_id AND e.kind=3)) OR
	 (?='artist' AND EXISTS(SELECT 1 FROM catalog_entities e WHERE e.id=local_artwork.entity_id AND e.kind=5)) OR
	 (?='item' AND EXISTS(SELECT 1 FROM catalog_entities e WHERE e.id=local_artwork.entity_id)))`, t.Kind, entity, t.Kind, t.Kind, t.Kind, t.Kind)
	if err != nil {
		return false, err
	}
	type local struct{ role, key string }
	locals := []local{}
	for rows.Next() {
		var v local
		if err = rows.Scan(&v.role, &v.key); err != nil {
			rows.Close()
			return false, err
		}
		locals = append(locals, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	writes := 0
	for _, v := range locals {
		if writes >= localArtworkCandidatePage {
			break
		}
		origin := "local:" + v.key
		if err = seedArtworkCandidate(ctx, tx, t, v.role, "", "local", publicationDigest(origin), origin, artworkAttribution("local"), fence, now, 0); err != nil {
			return false, err
		}
		writes++
	}
	return seedLocalArtworkAlternatives(ctx, tx, t, entity, fence, now, localArtworkCandidatePage-writes)
}

// localArtworkCandidatePage bounds the materialization work of one discovery
// call: at most this many candidates are written for a slot and committed,
// including the primary upserts.
const localArtworkCandidatePage = 8

// seedLocalArtworkAlternatives seeds the numbered sidecar images the scan
// accepted for a head's slot (inventory_artwork_candidates), after the primary
// local_artwork images above. One worker call materializes at most one bounded
// missing page for one slot; the caller keeps the target's dirty row until every
// slot reports no missing accepted image, so later calls continue from committed
// progress.
func seedLocalArtworkAlternatives(ctx context.Context, tx *sql.Tx, t RepairTarget, entity int64, fence, now string, budget int) (bool, error) {
	if t.Kind != "item" && t.Kind != "show" && t.Kind != "season" {
		return true, nil
	}
	complete := true
	seeded := false
	for _, role := range []string{"poster", "backdrop", "logo"} {
		if !seeded {
			done, count, err := seedLocalArtworkCandidateCall(ctx, tx, t, entity, role, fence, now, budget)
			if err != nil {
				return false, err
			}
			if count > 0 {
				seeded = true
			}
			if !done {
				complete = false
			}
		} else {
			// This role was not examined in this call; the dirty row keeps it
			// for the next one.
			complete = false
		}
		// Always let the central helper re-evaluate the preferred default: a
		// rank/name-only change among already-materialized keys moves it even
		// when no candidate is missing.
		if err := queueAcceptedLocalArtworkDefaultTx(ctx, tx, t, role, fence, now); err != nil {
			return false, err
		}
	}
	return complete, nil
}

// seedLocalArtworkCandidateCall materializes one bounded page of the ordered
// accepted images that have no current-fence local candidate yet, and reports
// whether that was the whole missing page. The domain reader exempts committed
// tuples, so one query per slot is enough: no history map, no locate loop and no
// stored cursor, and a late accepted image below or inside the committed range
// still surfaces first. It only records candidates; the checked ranked-default
// helper (invoked by the caller for every slot) queues the preferred one.
func seedLocalArtworkCandidateCall(ctx context.Context, tx *sql.Tx, t RepairTarget, entity int64, role, fence, now string, budget int) (bool, int, error) {
	limit := budget
	if limit < 1 {
		limit = 1
	}
	page, err := catalog.ReadUnmaterializedLocalArtworkCandidatePageTx(ctx, tx, t.Kind, entity, role, fence, nil, limit)
	if err != nil {
		return false, 0, err
	}
	seed := 0
	if budget > 0 {
		seed = len(page)
	}
	for _, candidate := range page[:seed] {
		origin := "local:" + candidate.Key
		if _, err = insertArtworkCandidate(ctx, tx, t, role, "", "local", publicationDigest(origin), origin, "", artworkAttribution("local"), fence, now, 0); err != nil {
			return false, 0, err
		}
	}
	return len(page) < limit, seed, nil
}
