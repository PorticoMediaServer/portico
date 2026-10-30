package catalog

import (
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/metadatalanguage"
	"github.com/PorticoMediaServer/portico/server/internal/segmentmarkers"
)

// DetailNamespace names the membership namespace a Detail root reads in. A
// playable entity keeps the existing item namespace; a non-playable container
// reads in its own kind, because a container is visible only while it has a
// visible member. Callers pass the entity's catalog_kinds.playable flag rather
// than duplicating the kind list.
func DetailNamespace(kind string, playable bool) string {
	if playable {
		return "item"
	}
	return kind
}

// LibraryForDetail resolves the library and Detail namespace of any entity a
// Detail read may open. It is deliberately one point lookup and grants nothing:
// the caller still authorizes the library, and detailContainerOnce applies the
// same VisibleEntity membership predicate as container pages. Playable kinds
// answer "item" exactly as LibraryForItem does; every other kind answers its
// own name and LibraryForItem stays the strict playable-only lookup used by
// byte, personal and timed reads.
func (s *Service) LibraryForDetail(id string) (library, namespace string, err error) {
	var kind string
	var playable bool
	err = s.read().QueryRow(`SELECT l.library_id,k.name,k.playable
 FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind JOIN catalog_libraries l ON l.id=e.library_id
 WHERE e.public_id=pid_blob(?) AND e.retired=0 AND l.retired=0`, id).Scan(&library, &kind, &playable)
	if err != nil {
		return "", "", err
	}
	return library, DetailNamespace(kind, playable), nil
}

// containerHeader is the stored point projection of one non-playable Detail
// root. Every field is read from a published catalogue row or a stored
// provider/owner record; nothing here is composed from children, options,
// sources, tracks or titles arrays.
type containerHeader struct {
	title, overview                  string
	year                             int
	added                            sql.NullString
	detailsOverview                  string
	showScreenOverview, showTVDB     string
	showSide, seasonSide, artistSide string
	artistBio                        string
	albumSide, bookSide, bookLocal   string
	showOriginalTitle                string
	showTagline, showContentRating   string
	showStudio, showNetwork          string
	showCountry, albumReleaseDate    string
}

// containerDetailOnce is the non-playable Detail composition. The original
// playable path (detailOnce) is untouched: it keeps its playback actions,
// options-adjacent extras, related rows and asset-backed availability. A
// container instead publishes only what the catalogue already stores for the
// container itself, with the container page's own visibility rule.
func (s *Service) containerDetailOnce(viewer Viewer, server, library, namespace, item string) (Detail, error) {
	out := Detail{
		Actions:     []DetailAction{},
		Markers:     []segmentmarkers.Marker{},
		Collections: []DetailCollection{},
		Languages:   DetailLanguages{Audio: []string{}, Subtitles: []string{}},
		Metadata: DetailMetadata{
			Status:  "unavailable",
			Sources: []MetadataSource{},
			Ratings: []ProviderRating{},
			Genres:  []Genre{},
			Credits: []Credit{},
		},
	}
	switch namespace {
	case "show", "season", "artist", "album", "book", "collection":
	default:
		// Reading keeps its separate handler and no other non-playable kind
		// gains a Detail surface here.
		return out, sql.ErrNoRows
	}
	if err := s.VisibleEntity(s.Context(), viewer, namespace, item); err != nil {
		return out, err
	}
	libraryNow, actual, err := s.LibraryForDetail(item)
	if err != nil {
		return out, err
	}
	if libraryNow != library || actual != namespace {
		return out, sql.ErrNoRows
	}
	before, err := s.ContentRevision(library, viewer.Profile)
	if err != nil {
		return out, err
	}
	var entity int64
	if err = s.read().QueryRow(`SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)`, item).Scan(&entity); err != nil {
		return out, err
	}
	// FI1 is item-local. A container root waits for its own head, including
	// blocked children, before composition; the HTTP parent answers typed 202
	// before its own 304 decision. This check grants nothing and adopts no
	// separate content revision.
	head, err := compactcatalog.InfoHeadTx(s.Context(), s.read(), entity)
	if err != nil {
		return out, err
	}
	if !head.Ready() {
		return out, compactcatalog.ErrInfoPreparing
	}
	header, err := s.readContainerHeader(item, namespace)
	if err != nil {
		return out, err
	}
	out.Scope = DetailScope{ServerID: server, LibraryID: library, ItemID: item, ViewerFence: viewer.Fence}
	out.Revision = before
	out.Item = Item{
		ID:        item,
		LibraryID: library,
		Kind:      namespace,
		Title:     header.title,
		Year:      header.year,
		Overview:  header.overview,
		AddedAt:   nullableString(header.added),
	}
	target := artworkTarget{Kind: namespace, ID: item}
	art, err := s.resolveArtwork([]artworkTarget{target})
	if err != nil {
		return out, err
	}
	// A container has selected poster/backdrop only. StillURL names an
	// episode's own still and is never inherited from a show or season.
	out.Item.PosterURL, out.Item.BackdropURL = art[target].PosterURL, art[target].BackdropURL
	if out.Personal, err = readPersonal(s.read(), viewer.Profile, item); err != nil {
		return out, err
	}
	// The watched default of a show, season, album or book is the accepted
	// container state, not a member's progress; the personal row still owns
	// Watchlist and Favorite, exactly as SetContainerSaved writes them.
	if namespace == "show" || namespace == "season" || namespace == "album" || namespace == "book" {
		var watched bool
		err = s.read().QueryRow(`SELECT watched FROM container_personal_state WHERE profile_id=? AND kind=? AND container_id=?`, viewer.Profile, namespace, entity).Scan(&watched)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if err == nil {
			out.Personal.Watched = watched
		}
	}
	out.Item.ProgressSeconds = out.Personal.ProgressSeconds
	watched := out.Personal.Watched
	out.Item.Watched = &watched
	if out.Facts, err = s.containerDetailFacts(item, namespace, header); err != nil {
		return out, err
	}
	rows, err := s.itemGenres(item, namespace)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var genre Genre
		if err = rows.Scan(&genre.ID, &genre.Name, &genre.Provider); err != nil {
			rows.Close()
			return out, err
		}
		out.Metadata.Genres = append(out.Metadata.Genres, genre)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if out.Collections, err = s.detailCollections(viewer, item); err != nil {
		return out, err
	}
	if out.MissingSince, err = s.MissingSince(s.Context(), item); err != nil {
		return out, err
	}
	out.Item.Missing = out.MissingSince != ""
	// The container has no metadata_details provider row, so "available" here
	// means the catalogue actually holds a description or facts for it; an
	// all-empty container stays truthfully unavailable.
	if out.Facts != nil || out.Item.Overview != "" || len(out.Metadata.Genres) > 0 {
		out.Metadata.Status = "available"
	}
	after, err := s.ContentRevision(library, viewer.Profile)
	if err != nil {
		return out, err
	}
	if before != after {
		return out, ErrStaleContinuation
	}
	return out, nil
}

// readContainerHeader reads one non-playable root in one statement. The
// overview order follows the surface that already owns it: a show uses the
// published screen record then the stored TVDB candidate then its own row; an
// album, season and book use their own stored overview; an artist prefers the
// editor overview then the Portico Music evidence biography; a collection uses
// the details row its editor writes. Browse row title/year/added reuse the
// published card projection, so a season reads with its show's name.
func (s *Service) readContainerHeader(item, namespace string) (containerHeader, error) {
	var h containerHeader
	err := s.read().QueryRow(`SELECT COALESCE(NULLIF(b.title,''),e.title),COALESCE(NULLIF(b.year,0),e.year),COALESCE(b.added_text,d.added_text),
 COALESCE(d.overview,''),
 COALESCE((SELECT json_extract(value,'$') FROM screen_metadata_fields WHERE target_kind='show' AND target_id=e.id AND field='overview'),''),
 COALESCE(c.overview,''),COALESCE(sh.overview,''),COALESCE(se.overview,''),COALESCE(ar.overview,''),
 COALESCE((SELECT bio FROM music_artist_evidence WHERE artist_id=e.id ORDER BY observed_at DESC LIMIT 1),''),
 COALESCE(al.overview,''),COALESCE(bk.overview,''),
 CASE WHEN COALESCE((SELECT local_mode FROM audio_metadata_policies WHERE library_id=l.library_id),'prefer')<>'off'
   AND bk.local_metadata_payload<>'' AND json_valid(bk.local_metadata_payload)
  THEN COALESCE(json_extract(bk.local_metadata_payload,'$.description'),'') ELSE '' END,
 COALESCE(sh.original_title,''),COALESCE(sh.tagline,''),COALESCE(sh.content_rating,''),
 COALESCE(sh.studio,''),COALESCE(sh.network,''),COALESCE(sh.country,''),COALESCE(al.release_date,'')
 FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id
 LEFT JOIN catalog_browse_rows b ON b.entity_id=e.id
 LEFT JOIN catalog_item_details d ON d.entity_id=e.id
 LEFT JOIN catalog_shows sh ON sh.entity_id=e.id
 LEFT JOIN catalog_seasons se ON se.entity_id=e.id
 LEFT JOIN catalog_artists ar ON ar.entity_id=e.id
 LEFT JOIN catalog_albums al ON al.entity_id=e.id
 LEFT JOIN catalog_books bk ON bk.entity_id=e.id
 LEFT JOIN tvdb_jobs j ON j.show_id=e.id
 LEFT JOIN tvdb_series_candidates c ON c.show_id=e.id AND c.provider_id=j.provider_id
  AND NOT EXISTS(SELECT 1 FROM screen_metadata_work sw WHERE sw.target_kind='show' AND sw.target_id=e.id AND sw.accepted_publication<>'')
 WHERE e.public_id=pid_blob(?) AND e.retired=0 AND l.retired=0`, item).Scan(
		&h.title, &h.year, &h.added, &h.detailsOverview, &h.showScreenOverview, &h.showTVDB,
		&h.showSide, &h.seasonSide, &h.artistSide, &h.artistBio, &h.albumSide, &h.bookSide, &h.bookLocal,
		&h.showOriginalTitle, &h.showTagline, &h.showContentRating, &h.showStudio, &h.showNetwork,
		&h.showCountry, &h.albumReleaseDate)
	if err != nil {
		return h, err
	}
	switch namespace {
	case "show":
		h.overview = firstNonEmpty(h.showScreenOverview, h.showTVDB, h.showSide, h.detailsOverview)
	case "season":
		h.overview = firstNonEmpty(h.seasonSide, h.detailsOverview)
	case "artist":
		h.overview = firstNonEmpty(h.artistSide, h.artistBio, h.detailsOverview)
	case "album":
		h.overview = firstNonEmpty(h.albumSide, h.detailsOverview)
	case "book":
		h.overview = firstNonEmpty(h.bookSide, h.bookLocal, h.detailsOverview)
	default:
		h.overview = h.detailsOverview
	}
	return h, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// containerDetailFacts reuses the existing item-facts projection for fields
// the editor stores in catalog_item_details, then overlays only the fields a
// container's own side table owns. Absent values stay absent; an all-empty
// result is nil, as on a playable item with no facts row.
func (s *Service) containerDetailFacts(item, kind string, h containerHeader) (*TitleFacts, error) {
	facts, err := s.titleFacts(item)
	if err != nil {
		return nil, err
	}
	if facts == nil {
		facts = &TitleFacts{}
	}
	setWhenPresent := func(dst *string, value string) {
		if value != "" {
			*dst = value
		}
	}
	switch kind {
	case "show":
		setWhenPresent(&facts.OriginalTitle, h.showOriginalTitle)
		setWhenPresent(&facts.Tagline, h.showTagline)
		setWhenPresent(&facts.ContentRating, h.showContentRating)
		setWhenPresent(&facts.Studio, h.showStudio)
		setWhenPresent(&facts.Network, h.showNetwork)
		setWhenPresent(&facts.Country, h.showCountry)
	case "album":
		setWhenPresent(&facts.ReleaseDate, h.albumReleaseDate)
	}
	if *facts == (TitleFacts{}) {
		return nil, nil
	}
	if facts.OriginalLanguage == "" {
		if facts.OriginalLanguage, err = metadatalanguage.Published(s.Context(), s.read(), item); err != nil {
			return nil, err
		}
	}
	if *facts == (TitleFacts{}) {
		return nil, nil
	}
	return facts, nil
}
