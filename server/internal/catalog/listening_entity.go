package catalog

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"regexp"
	"strings"
	"time"
)

// Identity/artwork for the entity being opened, independent of pagination.
// An album cover is never inferred from whichever track happens to sort first.
func (s *Service) listeningEntity(r ContentRequest, title string) (*ContentEntry, error) {
	kind, id := r.View, r.EntityID
	if kind == "author" || kind == "book_series" {
		return &ContentEntry{ID: id, Kind: kind, LibraryID: r.Library, Title: title}, nil
	}
	if kind != "artist" && kind != "album" && kind != "book" && kind != "disc" {
		return nil, nil
	}
	artKind, artID := kind, id
	var disc int
	if kind == "disc" {
		var e error
		artID, disc, e = discTarget(id)
		if e != nil {
			return nil, e
		}
		artKind = "album"
	}
	out := &ContentEntry{ID: id, Kind: kind, LibraryID: r.Library, Title: title}
	var e error
	n := 0
	// Counts and durations cover only what this viewer may see.
	visible, visibleArgs := r.Viewer.itemVisibilitySQL("counted.song")
	switch artKind {
	case "artist":
		e = s.read().QueryRow(`SELECT count(*) FROM (SELECT s.entity_id AS song FROM catalog_entities artist CROSS JOIN catalog_albums a INDEXED BY catalog_albums_artist ON a.artist_id=artist.id CROSS JOIN catalog_songs s INDEXED BY catalog_songs_album_order ON s.album_id=a.entity_id WHERE artist.public_id=pid_blob(?) AND artist.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) UNION SELECT sa.song_id FROM catalog_entities artist CROSS JOIN catalog_song_artists sa INDEXED BY catalog_song_artists_artist ON sa.artist_id=artist.id WHERE artist.public_id=pid_blob(?) AND artist.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?)) counted WHERE `+visible, append([]any{artID, r.Library, artID, r.Library}, visibleArgs...)...).Scan(&n)
	case "album":
		var artistID string
		var year int
		e = s.read().QueryRow(`SELECT pid(ar.public_id),ar.title,a.year FROM catalog_entities a JOIN catalog_albums detail ON detail.entity_id=a.id JOIN catalog_entities ar ON ar.id=detail.artist_id JOIN catalog_libraries l ON l.id=a.library_id WHERE a.public_id=pid_blob(?) AND l.library_id=? AND a.retired=0`, artID, r.Library).Scan(&artistID, &out.Subtitle, &year)
		if e == nil {
			discFilter := -1
			if kind == "disc" {
				discFilter = disc
			}
			e = s.read().QueryRow(`SELECT count(*) FROM (SELECT s.entity_id AS song FROM catalog_songs s JOIN catalog_entities a ON a.id=s.album_id WHERE a.public_id=pid_blob(?) AND (?<0 OR COALESCE(s.disc_number,1)=?)) counted WHERE `+visible,
				append([]any{artID, discFilter, disc}, visibleArgs...)...).Scan(&n)
		}
		if e == nil {
			if year > 0 {
				out.Year = &year
			}
			out.Artist = &ContentEntityRef{ID: artistID, Name: out.Subtitle}
			durationWhere := `a.public_id=pid_blob(?)`
			durationArgs := []any{artID}
			if kind == "disc" {
				durationWhere += ` AND COALESCE(s.disc_number,1)=?`
				durationArgs = append(durationArgs, disc)
			}
			var total float64
			var all, known int
			e = s.read().QueryRow(`SELECT COALESCE(SUM(duration),0),count(*),count(duration) FROM (SELECT (SELECT max(NULLIF(COALESCE(ia.end_seconds,ast.duration)-ia.start_seconds,0)) FROM catalog_asset_links ia JOIN catalog_assets ast ON ast.id=ia.asset_id WHERE ia.entity_id=counted.song) duration FROM (SELECT s.entity_id AS song FROM catalog_songs s JOIN catalog_entities a ON a.id=s.album_id WHERE `+durationWhere+`) counted WHERE `+visible+`)`, append(durationArgs, visibleArgs...)...).Scan(&total, &all, &known)
			if e == nil && all == known && known > 0 {
				out.Duration = &total
			}
		}
	case "book":
		e = s.read().QueryRow(`SELECT detail.author FROM catalog_entities b JOIN catalog_books detail ON detail.entity_id=b.id JOIN catalog_libraries l ON l.id=b.library_id WHERE b.public_id=pid_blob(?) AND l.library_id=? AND b.retired=0`, artID, r.Library).Scan(&out.Subtitle)
		if e == nil {
			e = s.read().QueryRow(`SELECT count(*) FROM catalog_book_files f JOIN catalog_entities b ON b.id=f.book_id WHERE b.public_id=pid_blob(?)`, artID).Scan(&n)
		}
	}
	if e != nil {
		return nil, e
	}
	if artKind == "artist" {
		// The Portico Music agent's biography, country and active years. An
		// owner's editor text wins over provider text; the portrait comes
		// from the artwork resolver (owner upload, local file, Commons).
		if err := s.listeningArtistFacts(r.Viewer, artID, out); err != nil {
			return nil, err
		}
	}
	if artKind == "album" {
		// The album's label, release-group type and MusicBrainz genres.
		if err := s.listeningAlbumFacts(r.Viewer, artID, out); err != nil {
			return nil, err
		}
		// Whole-album format descriptors do not depend on the current track page.
		if kind == "album" {
			out.Badges, e = s.albumMusicBadges(r, artID)
			if e != nil {
				return nil, e
			}
		}
	}
	if artKind == "book" {
		if err := s.listeningBookFacts(r, out); err != nil {
			return nil, err
		}
	}
	if kind == "album" || kind == "book" {
		counts, err := s.ContainerPersonalCounts(r.Viewer, kind, id)
		if err != nil {
			return nil, err
		}
		out.WatchedCount, out.UnwatchedCount = &counts.WatchedCount, &counts.UnwatchedCount
		n = int(counts.WatchedCount + counts.UnwatchedCount)
	}
	out.Count = &n

	arts, err := s.resolveArtwork([]artworkTarget{{artKind, artID}})
	if err != nil {
		return nil, err
	}
	art := arts[artworkTarget{artKind, artID}]
	out.PosterURL, out.BackdropURL = art.PosterURL, art.BackdropURL

	if out.PosterURL == "" && (artKind == "album" || artKind == "book") {
		table, col := "catalog_songs", "album_id"
		if artKind == "book" {
			table, col = "catalog_book_files", "book_id"
		}
		var itemID string
		visible, args := r.Viewer.itemVisibilitySQL("i.id")
		e = s.read().QueryRow(`SELECT pid(i.public_id) FROM catalog_entities parent JOIN `+table+` f ON f.`+col+`=parent.id JOIN catalog_entities i ON i.id=f.entity_id JOIN catalog_item_details detail ON detail.entity_id=i.id JOIN catalog_libraries l ON l.id=i.library_id WHERE parent.public_id=pid_blob(?) AND l.library_id=? AND i.retired=0 AND detail.poster_url<>'' AND `+visible+` AND EXISTS(SELECT 1 FROM catalog_item_availability av WHERE av.entity_id=i.id AND av.available=1 AND av.retired=0) ORDER BY i.id LIMIT 1`, append([]any{artID, r.Library}, args...)...).Scan(&itemID)
		if e != nil && e != sql.ErrNoRows {
			return nil, e
		}
		if e == nil {
			out.PosterURL = "/v1/items/" + url.PathEscape(itemID) + "/art/poster"
		}
	}
	return out, s.describeItemArt(&out.PosterURL)
}

// listeningArtistFacts fills the artist entity's biography, source, country
// and active years from the Portico Music agent's evidence (music_artist_evidence,
// 0261). An owner's editor text (artists.overview) wins over the Wikipedia
// biography; the portrait itself resolves through artwork like every image.
func (s *Service) listeningArtistFacts(viewer Viewer, id string, out *ContentEntry) error {
	out.Genres = nil
	var editor string
	if err := s.read().QueryRow(`SELECT a.overview FROM catalog_entities e JOIN catalog_artists a ON a.entity_id=e.id WHERE e.public_id=pid_blob(?) AND e.retired=0`, id).Scan(&editor); err != nil && err != sql.ErrNoRows {
		return err
	}
	var bio, source, sourceURL, country string
	var begin, end sql.NullInt64
	err := s.read().QueryRow(`SELECT bio,bio_source,bio_source_url,country,begin_year,end_year,COALESCE(artist_type,'') FROM music_artist_evidence WHERE artist_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) ORDER BY observed_at DESC LIMIT 1`, id).Scan(&bio, &source, &sourceURL, &country, &begin, &end, &out.ArtistType)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if editor != "" {
		out.Overview = editor
	} else if bio != "" {
		out.Overview, out.OverviewSource, out.OverviewSourceURL = bio, source, sourceURL
	}
	out.Country = country
	if begin.Valid {
		year := int(begin.Int64)
		out.ActiveBeginYear = &year
	}
	if end.Valid {
		year := int(end.Int64)
		out.ActiveEndYear = &year
	}
	visible, visibleArgs := viewer.itemVisibilitySQL("song.entity_id")
	var albums int
	if err := s.read().QueryRow(`SELECT count(DISTINCT song.album_id) FROM (SELECT s.entity_id FROM catalog_entities a CROSS JOIN catalog_albums al INDEXED BY catalog_albums_artist ON al.artist_id=a.id CROSS JOIN catalog_songs s INDEXED BY catalog_songs_album_order ON s.album_id=al.entity_id WHERE a.public_id=pid_blob(?) UNION SELECT sa.song_id FROM catalog_entities a CROSS JOIN catalog_song_artists sa INDEXED BY catalog_song_artists_artist ON sa.artist_id=a.id WHERE a.public_id=pid_blob(?)) mine CROSS JOIN catalog_songs song ON song.entity_id=mine.entity_id WHERE `+visible, append([]any{id, id}, visibleArgs...)...).Scan(&albums); err != nil {
		return err
	}
	out.AlbumCount = &albums
	// The artist's top local genres across all its songs, most-shared first. The
	// songs come from the artist's own indexes (the songs crediting it, the album
	// artist's albums), so the read is the artist's — larger for a prolific
	// artist, never the library's.
	// Published compact facts: the artist's songs (catalog_song_artists_artist,
	// catalog_albums_artist) and their genre term sources.
	rows, err := s.read().Query(`WITH artist(id) AS (SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)),
	mine(song) AS (
	 SELECT sa.song_id song FROM catalog_song_artists sa INDEXED BY catalog_song_artists_artist WHERE sa.artist_id=(SELECT id FROM artist)
	 UNION SELECT s.entity_id FROM catalog_albums a INDEXED BY catalog_albums_artist CROSS JOIN catalog_entities ae ON ae.id=a.entity_id AND ae.retired=0 CROSS JOIN catalog_songs s INDEXED BY catalog_songs_album_order ON s.album_id=a.entity_id WHERE a.artist_id=(SELECT id FROM artist))
	SELECT g.source_name FROM mine JOIN catalog_term_sources g ON g.entity_id=mine.song JOIN catalog_terms t ON t.id=g.term_id AND t.vocab=?
	WHERE g.provider='local' AND trim(g.source_name)<>'' AND `+strings.ReplaceAll(visible, "song.entity_id", "mine.song")+` GROUP BY lower(g.source_name) ORDER BY count(*) DESC,min(g.source_name) LIMIT 3`, append([]any{id, int(compactcatalog.VocabGenre)}, visibleArgs...)...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		out.Genres = append(out.Genres, name)
	}
	err = rows.Err()
	rows.Close()
	return err
}

// listeningAlbumFacts fills the album entity's label, release-group type and
// observed MusicBrainz type/genres. Unknown type stays absent. When the
// release has no genres, permitted local song metadata supplies them.
func (s *Service) listeningAlbumFacts(viewer Viewer, id string, out *ContentEntry) error {
	out.Genres = nil
	if err := s.read().QueryRow(`SELECT a.label,a.release_date FROM catalog_entities e JOIN catalog_albums a ON a.entity_id=e.id WHERE e.public_id=pid_blob(?) AND e.retired=0`, id).Scan(&out.Label, &out.ReleaseDate); err != nil && err != sql.ErrNoRows {
		return err
	}
	if _, err := time.Parse("2006-01-02", out.ReleaseDate); err != nil {
		out.ReleaseDate = ""
	}
	var primary string
	var compilation bool
	entityID := `(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`
	err := s.read().QueryRow(`SELECT COALESCE((SELECT e.primary_type FROM mb_album_links l JOIN mb_release_evidence e ON e.revision_id=l.release_revision WHERE l.album_id=`+entityID+`),''),EXISTS(SELECT 1 FROM mb_album_links l JOIN mb_release_group_types t ON t.release_revision=l.release_revision WHERE l.album_id=`+entityID+` AND t.value='Compilation' COLLATE NOCASE)`, id, id).Scan(&primary, &compilation)
	if err != nil {
		return err
	}
	out.AlbumType = primary

	if compilation {
		out.AlbumType = "Compilation"
	}
	rows, err := s.read().Query(`SELECT DISTINCT COALESCE(json_extract(g.value,'$.name'),'') FROM mb_album_links l JOIN mb_release_evidence e ON e.revision_id=l.release_revision,json_each(json_extract(e.payload,'$.genres')) g WHERE l.album_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) ORDER BY g.key LIMIT 8`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		if name != "" {
			out.Genres = append(out.Genres, name)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(out.Genres) > 0 {
		return err
	}
	visible, args := viewer.itemVisibilitySQL("song.entity_id")
	rows, err = s.read().Query(`SELECT min(source.source_name) FROM catalog_songs song JOIN catalog_term_sources source ON source.entity_id=song.entity_id JOIN catalog_terms term ON term.id=source.term_id AND term.vocab=1 JOIN catalog_entities e ON e.id=song.entity_id JOIN catalog_libraries l ON l.id=e.library_id WHERE song.album_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND trim(source.source_name)<>'' AND (source.provider<>'local' OR COALESCE((SELECT local_mode FROM audio_metadata_policies WHERE library_id=l.library_id),'prefer')<>'off') AND `+visible+` GROUP BY term.id ORDER BY min(source.source_name) COLLATE NOCASE LIMIT 8`, append([]any{id}, args...)...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var genre string
		if err = rows.Scan(&genre); err != nil {
			break
		}
		out.Genres = append(out.Genres, genre)
	}
	if e := rows.Err(); err == nil {
		err = e
	}
	rows.Close()
	return err
}

var bookEditionSuffix = regexp.MustCompile(`(?i)\s*\((unabridged|abridged)\)\s*$`)

func (s *Service) listeningBookFacts(r ContentRequest, out *ContentEntry) error {
	var raw, author, narrator, overview, series, seriesPosition string
	var year int
	err := s.read().QueryRow(`SELECT CASE WHEN COALESCE((SELECT p.local_mode FROM audio_metadata_policies p WHERE p.library_id=(SELECT library_id FROM catalog_libraries WHERE id=b.library_id)),'prefer')='off' THEN '' ELSE b.local_metadata_payload END,b.author,b.narrator,b.overview,b.series,b.series_position,e.year FROM catalog_books b JOIN catalog_entities e ON e.id=b.entity_id WHERE e.public_id=pid_blob(?) AND e.retired=0`, out.ID).Scan(&raw, &author, &narrator, &overview, &series, &seriesPosition, &year)
	if err != nil {
		return err
	}
	var metadata LocalBookMetadata
	if raw != "" {
		if err = json.Unmarshal([]byte(raw), &metadata); err != nil {
			return err
		}
	}
	out.Author = author
	out.Narrators = metadata.Narrators
	if len(out.Narrators) == 0 && narrator != "" {
		out.Narrators = []string{narrator}
	}
	out.Series = metadata.Series
	if series != "" {
		out.Series = []AudioSeries{{series, seriesPosition}}
	}
	out.Publisher, out.Language, out.Edition = metadata.Publisher, metadata.Language, metadata.Edition
	out.Overview = overview
	if overview == "" {
		out.Overview = metadata.Description
	}
	out.PublishedYear = metadata.PublishedYear
	if year > 0 {
		out.PublishedYear = &year
	}
	// Recognizing an edition never shortens the stored title or manufactures
	// a display title. Titles retain their editions, subtitles and suffixes.
	if match := bookEditionSuffix.FindStringSubmatchIndex(out.Title); match != nil {
		if out.Edition == "" {
			out.Edition = strings.ToUpper(out.Title[match[2]:match[2]+1]) + strings.ToLower(out.Title[match[2]+1:match[3]])
		}
	}
	summary, err := s.bookTimelineSummary(r.Viewer, out.ID)
	if err != nil {
		return err
	}
	if summary.duration != nil {
		n := float64(*summary.duration) / 1000
		out.Duration = &n
	}
	var item string
	var position int64
	err = s.read().QueryRow(`SELECT pid(file.public_id),r.position FROM book_resume r JOIN catalog_entities book ON book.id=r.book_id JOIN catalog_entities file ON file.id=r.item_id JOIN catalog_book_files f ON f.entity_id=file.id AND f.book_id=book.id WHERE book.public_id=pid_blob(?) AND r.profile_id=? AND r.completed=0`, out.ID, r.Profile).Scan(&item, &position)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	out.BookProgress, err = s.bookProgressAt(r.Viewer, out.ID, item, position, summary)
	if out.BookProgress != nil && out.BookProgress.PositionMs != nil {
		n := float64(*out.BookProgress.PositionMs) / 1000
		out.ProgressSeconds = &n
	}
	return err
}

// EnrichListeningEntries fills only returned page IDs. Shared browse consumers
// call this in their existing snapshot; no client scans or per-card HTTP reads.
func (s *Service) EnrichListeningEntries(viewer Viewer, pages ...[]ContentEntry) error {
	for _, entries := range pages {
		if err := s.listeningPageFavorites(viewer.Profile, entries); err != nil {
			return err
		}
		for i := range entries {
			e := &entries[i]
			switch e.Kind {
			case "album":
				if err := s.listeningAlbumFacts(viewer, e.ID, e); err != nil {
					return err
				}
			case "artist":
				if err := s.listeningArtistFacts(viewer, e.ID, e); err != nil {
					return err
				}
			case "book":
				if err := s.listeningBookFacts(ContentRequest{Viewer: viewer, Profile: viewer.Profile, Library: e.LibraryID}, e); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
