package catalog

import (
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type listeningCandidate struct {
	id        string
	available bool
	score     int
	rank      string
	key       listeningOrderKey
}

type listeningOrderKey struct {
	Title string `json:"title"`
	Album int64  `json:"album"`
	Disc  int    `json:"disc"`
	Track int    `json:"track"`
	ID    int64  `json:"id"`
}

type listeningCounts struct{ total, missing int }

// orderedListeningPage lets SQLite seek and page the ordered relation. Only the
// requested page crosses the database boundary; a 750,000-song library never
// becomes a Go slice just to select its first hundred tracks. Its counts are
// exact (maintained counts, the class's, or the selection's own count).
func (s *Service) orderedListeningPage(t ListeningTarget, where string, args []any, anchor string, after *listeningOrderKey, known *listeningCounts, restrictions identity.ContentRestrictions, limit int) ([]listeningCandidate, int, int, error) {
	unrestricted := !restrictions.Active()
	// The anchor lookup starts from the item primary key. The page walk below
	// instead starts from the order indexes; using that walk for an anchor would
	// scan all preceding songs in a large library.
	anchorFrom := ` FROM catalog_entities i LEFT JOIN catalog_songs s ON s.entity_id=i.id LEFT JOIN catalog_entities a ON a.id=s.album_id LEFT JOIN catalog_browse_rows ar ON ar.entity_id=a.id LEFT JOIN catalog_book_files f ON f.entity_id=i.id WHERE i.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND ` + where
	anchorParams := append([]any{t.LibraryID}, args...)
	key := `COALESCE(ar.sort_key,''),COALESCE(a.id,0),COALESCE(s.disc_number,f.disc_number,1),COALESCE(s.track_number,f.part_number,0),i.id`
	from, params := anchorFrom, append([]any{}, anchorParams...)
	pageFields := `COALESCE(ar.sort_key,''),COALESCE(a.id,0),COALESCE(s.disc_number,f.disc_number,1),COALESCE(s.track_number,f.part_number,0)`
	order := `COALESCE(ar.sort_key,'') COLLATE NOCASE,COALESCE(a.id,0),COALESCE(s.disc_number,f.disc_number,1),COALESCE(s.track_number,f.part_number,0),i.id`
	seek := order
	seekArgs := func(k listeningOrderKey) []any { return []any{k.Title, k.Album, k.Disc, k.Track, k.ID} }
	// A row-value seek spanning two tables cannot start an index walk; the
	// whole-library order repeats its leading key as a plain bound so a
	// continuation starts at its album instead of the library's first.
	leadBound := ""
	leadArgs := func(listeningOrderKey) []any { return nil }
	switch t.Kind {
	case "library":
		// CROSS JOIN preserves the index walk: album sort key, album ID, then
		// disc, track and song ID. Restriction and availability checks happen
		// on each candidate before LIMIT, without sorting the whole library.
		//
		// The song's entity is a LEFT JOIN whose conditions sit inside
		// COALESCE: with a plain join and conditions on it, SQLite builds a
		// Bloom filter over every entity of every library before the walk's
		// first row, which made each page cost the whole catalogue. A missing
		// entity fails the COALESCE exactly as the join would.
		from = ` FROM catalog_browse_rows ar INDEXED BY catalog_browse_title
			CROSS JOIN catalog_albums a ON a.entity_id=ar.entity_id
			CROSS JOIN catalog_songs s INDEXED BY catalog_songs_listening_order ON s.album_id=a.entity_id
			LEFT JOIN catalog_entities i ON i.id=s.entity_id
			WHERE ar.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND ar.kind=6 AND COALESCE(i.library_id=ar.library_id AND (` + where + `),0)`
		params = append([]any{t.LibraryID}, args...)
		leadBound = ` AND ar.sort_key COLLATE NOCASE>=?`
		leadArgs = func(k listeningOrderKey) []any { return []any{k.Title} }
		pageFields = `COALESCE(ar.sort_key,''),ar.entity_id,COALESCE(s.disc_number,1),COALESCE(s.track_number,0)`
		order = `ar.sort_key COLLATE NOCASE,ar.entity_id,COALESCE(s.disc_number,1),COALESCE(s.track_number,0),s.entity_id`
		seek = order
	case "album", "disc":
		album := t.ID
		if t.Kind == "disc" {
			var err error
			album, _, err = discTarget(t.ID)
			if err != nil {
				return nil, 0, 0, err
			}
		}
		from = ` FROM catalog_songs s INDEXED BY catalog_songs_listening_order
			CROSS JOIN catalog_entities i ON i.id=s.entity_id
			CROSS JOIN catalog_entities a ON a.id=s.album_id
			LEFT JOIN catalog_browse_rows ar ON ar.entity_id=a.id
			WHERE s.album_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?) AND kind=6) AND i.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND ` + where
		params = append([]any{album, t.LibraryID}, args...)
		pageFields = `COALESCE(ar.sort_key,''),a.id,COALESCE(s.disc_number,1),COALESCE(s.track_number,0)`
		order = `COALESCE(s.disc_number,1),COALESCE(s.track_number,0),s.entity_id`
		seek = order
		seekArgs = func(k listeningOrderKey) []any { return []any{k.Disc, k.Track, k.ID} }
	case "artist":
		// Start from the artist's own songs (album-artist albums, then credited
		// songs, each on its index) instead of testing every song of the
		// library. The artist's songs are sorted, which is bounded by the
		// artist, not the library (B7).
		from = ` FROM (SELECT s2.entity_id FROM catalog_albums a2 INDEXED BY catalog_albums_artist CROSS JOIN catalog_songs s2 ON s2.album_id=a2.entity_id WHERE a2.artist_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?) AND kind=5)
			UNION SELECT sa2.song_id FROM catalog_song_artists sa2 INDEXED BY catalog_song_artists_artist WHERE sa2.artist_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?) AND kind=5)) member
			CROSS JOIN catalog_entities i ON i.id=member.entity_id
			CROSS JOIN catalog_songs s ON s.entity_id=i.id CROSS JOIN catalog_entities a ON a.id=s.album_id
			LEFT JOIN catalog_browse_rows ar ON ar.entity_id=a.id
			WHERE i.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND ` + where
		params = append([]any{t.ID, t.ID, t.LibraryID}, args...)
		pageFields = `COALESCE(ar.sort_key,''),a.id,COALESCE(s.disc_number,1),COALESCE(s.track_number,0)`
		order = `COALESCE(ar.sort_key,'') COLLATE NOCASE,a.id,COALESCE(s.disc_number,1),COALESCE(s.track_number,0),s.entity_id`
		seek = order
	case "book":
		from = ` FROM catalog_book_files f INDEXED BY catalog_book_files_listening_order
			CROSS JOIN catalog_entities i ON i.id=f.entity_id
			WHERE f.book_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?) AND kind=8) AND i.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND ` + where
		params = append([]any{t.ID, t.LibraryID}, args...)
		pageFields = `i.sort_key,0,COALESCE(f.disc_number,1),COALESCE(f.part_number,0)`
		key = `i.sort_key,0,COALESCE(f.disc_number,1),COALESCE(f.part_number,0),i.id`
		order = `COALESCE(f.disc_number,1),COALESCE(f.part_number,0),i.sort_key,f.entity_id`
		seek = order
		seekArgs = func(k listeningOrderKey) []any { return []any{k.Disc, k.Track, k.Title, k.ID} }
	}
	const available = `EXISTS(SELECT 1 FROM catalog_item_availability v WHERE v.entity_id=i.id AND v.available=1 AND v.retired=0)`
	if anchor != "" {
		var title string
		var album, id int64
		var disc, track int
		var ready bool
		e := s.read().QueryRow(`SELECT `+key+`,`+available+anchorFrom+` AND i.public_id=pid_blob(?)`, append(append([]any{}, anchorParams...), anchor)...).Scan(&title, &album, &disc, &track, &id, &ready)
		if e != nil {
			return nil, 0, 0, e
		}
		if !ready {
			return nil, 0, 0, errors.New("the selected part is unavailable; choose another item")
		}
		anchorKey := listeningOrderKey{title, album, disc, track, id}
		values := seekArgs(anchorKey)
		from += ` AND (` + seek + `) >= (` + placeholders(len(values)) + `)` + leadBound
		params = append(append(params, values...), leadArgs(anchorKey)...)
	}
	var total, missing int
	if known != nil {
		total, missing = known.total, known.missing
	} else if t.Kind == "library" && unrestricted && anchor == "" {
		// The same maintained availability buckets Home uses answer a whole
		// music library's first-page totals without walking every song. A
		// start anchor changes the denominator and retains the indexed count.
		if e := s.read().QueryRow(`SELECT COALESCE(sum(CASE WHEN available=1 THEN total ELSE 0 END),0),
		 COALESCE(sum(CASE WHEN available=0 THEN total ELSE 0 END),0)
		 FROM catalog_home_buckets WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND kind=7 AND retired=0`, t.LibraryID).Scan(&total, &missing); e != nil {
			return nil, 0, 0, e
		}
	} else if counted, e := s.classListeningCounts(t, restrictions, anchor, &total, &missing); e != nil {
		return nil, 0, 0, e
	} else if counted {
		// A restricted viewer's whole-library totals come from the
		// published visibility class (0105), not a walk of every song.
	} else {
		// The selection's exact counts, over its whole scope (restriction and
		// anchor included): counted once when the selection starts, and
		// carried by its continuations.
		var rows, avail int
		if e := s.read().QueryRow(`SELECT count(*),COALESCE(sum(av),0) FROM (SELECT `+available+` AS av`+from+`)`, params...).Scan(&rows, &avail); e != nil {
			return nil, 0, 0, e
		}
		total, missing = avail, rows-avail
	}
	if after != nil {
		values := seekArgs(*after)
		from += ` AND (` + seek + `)>(` + placeholders(len(values)) + `)` + leadBound
		params = append(append(params, values...), leadArgs(*after)...)
	}
	// CASE keeps the availability test a per-row check: as a bare EXISTS
	// SQLite turns it into a join and first builds a Bloom filter over every
	// row of the availability table.
	rows, e := s.read().Query(`SELECT pid(i.public_id),`+pageFields+`,i.id`+from+` AND CASE WHEN `+available+` THEN 1 ELSE 0 END ORDER BY `+order+` LIMIT ?`, append(append([]any{}, params...), limit)...)
	if e != nil {
		return nil, 0, 0, e
	}
	defer rows.Close()
	out := make([]listeningCandidate, 0, limit)
	for rows.Next() {
		var c listeningCandidate
		if e = rows.Scan(&c.id, &c.key.Title, &c.key.Album, &c.key.Disc, &c.key.Track, &c.key.ID); e != nil {
			return nil, 0, 0, e
		}
		c.available = true
		out = append(out, c)
	}
	if e = rows.Err(); e != nil {
		return nil, 0, 0, e
	}
	return out, total, missing, nil
}

// classListeningCounts answers a restricted viewer's whole music library
// totals (songs, and unavailable songs) from its published visibility class.
// It reports false when the class is not published yet; a small library then
// counts directly, a large one answers that visibility is being prepared.
func (s *Service) classListeningCounts(t ListeningTarget, restrictions identity.ContentRestrictions, anchor string, total, missing *int) (bool, error) {
	if t.Kind != "library" || anchor != "" || !restrictions.Active() {
		return false, nil
	}
	key, generation, ready, err := s.publishedVisibilityClass(t.LibraryID, restrictions)
	if err != nil {
		return false, err
	}
	if !ready {
		small, err := s.visibilitySmallLibrary(t.LibraryID)
		if err != nil || small {
			return false, err
		}
		return false, ErrVisibilityBuilding
	}
	var all, unavailable int
	if err = s.read().QueryRow(`SELECT COALESCE((SELECT sum(vc.total) FROM compact_visibility_counts vc WHERE vc.class_id=c.id AND vc.generation=? AND vc.library_id=l.id AND vc.kind=7),0),
	 COALESCE((SELECT unavailable.total FROM compact_visibility_unavailable_counts unavailable WHERE unavailable.class_id=c.id AND unavailable.generation=? AND unavailable.library_id=l.id AND unavailable.kind=7),0)
			 FROM compact_visibility_classes c JOIN catalog_libraries l ON l.id=c.library_id WHERE c.class_key=? AND l.library_id=? AND c.retired=0`,
		generation, generation, key, t.LibraryID).Scan(&all, &unavailable); err != nil {
		return false, err
	}
	*total, *missing = all-unavailable, unavailable
	return true, nil
}
