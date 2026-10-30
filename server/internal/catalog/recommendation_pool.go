package catalog

import (
	"database/sql"
)

const recommendationPoolCutover = 1024
const recommendationPoolLimit = 1024

// recommendationPool bounds the expensive work/facet scorer on a large
// catalogue. Each source is an indexed, capped seek: personal activity, genres
// of that activity, and recent work in each permitted library. The scorer still
// applies the full viewer restriction to every candidate. Small libraries keep
// the complete candidate relation so local and short-library results are exact.
func (s *Service) recommendationPool(r HomeRequest) ([]string, bool, error) {
	if err := s.compactProjectionReady(18, 32); err != nil {
		return nil, false, err
	}
	ctx := s.Context()
	small, whole, err := s.smallLibraryItems(r, recommendationPoolCutover)
	if err != nil || whole {
		// A small library is scored whole, but still through the same
		// explicit key list: exact, and never an open-ended scan.
		return small, true, err
	}
	var rows *sql.Rows

	seen := map[string]bool{}
	pool := make([]string, 0, recommendationPoolLimit)
	add := func(id string) {
		if id != "" && !seen[id] && len(pool) < recommendationPoolLimit {
			seen[id] = true
			pool = append(pool, id)
		}
	}
	seeds := []string{}
	for _, query := range []string{
		// Each walk starts from the viewer's own rows (CROSS JOIN fixes the order):
		// the planner otherwise starts from the entities and probes the history
		// once per catalogue item.
		`SELECT pid(i.public_id) FROM personal_items p INDEXED BY personal_last_played CROSS JOIN catalog_entities i ON i.id=p.item_id CROSS JOIN catalog_libraries l ON l.id=i.library_id
		 WHERE p.profile_id=? AND l.library_id IN(SELECT value FROM json_each(?)) AND
		 (watched=1 OR watchlisted=1 OR favorite=1 OR rating IS NOT NULL)
		 ORDER BY last_played_at DESC,p.item_id LIMIT 64`,
		`SELECT pid(i.public_id) FROM progress p CROSS JOIN catalog_entities i ON i.id=p.item_id CROSS JOIN catalog_libraries l ON l.id=i.library_id
		 WHERE p.profile_id=? AND l.library_id IN(SELECT value FROM json_each(?)) AND position>0
		 ORDER BY p.item_id LIMIT 64`,
	} {
		rows, err = s.read().QueryContext(ctx, query, r.Profile, idsJSON(r.Libraries))
		if err != nil {
			return nil, true, err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				break
			}
			if !seen[id] {
				seeds = append(seeds, id)
			}
			add(id)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, true, err
		}
	}
	if len(seeds) > 0 {
		facets, err := s.read().QueryContext(ctx, `SELECT DISTINCT g.source_name FROM catalog_term_sources g JOIN catalog_entities e ON e.id=g.entity_id
			WHERE e.public_id IN(SELECT pid_blob(value) FROM json_each(?)) LIMIT 12`, idsJSON(seeds))
		if err != nil {
			return nil, true, err
		}
		keys := []string{}
		for facets.Next() {
			var name string
			if err = facets.Scan(&name); err != nil {
				break
			}
			keys = append(keys, name)
		}
		if err == nil {
			err = facets.Err()
		}
		facets.Close()
		if err != nil {
			return nil, true, err
		}
		for _, name := range keys {
			// Ordered as the index is (an item's row is its own entity), so the
			// walk stops at the limit instead of sorting every match.
			rows, err = s.read().QueryContext(ctx, `SELECT pid(e.public_id) FROM catalog_term_sources g INDEXED BY catalog_term_sources_name_seek
				JOIN catalog_entities e ON e.id=g.entity_id
				WHERE g.source_name COLLATE NOCASE=? ORDER BY g.entity_id LIMIT 32`, name)
			if err != nil {
				return nil, true, err
			}
			for rows.Next() {
				var id string
				if err = rows.Scan(&id); err != nil {
					break
				}
				add(id)
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			if err != nil {
				return nil, true, err
			}
		}
	}
	recentPerLibrary := max(1, (recCandidateLimit-len(pool))/max(1, len(r.Libraries)))
	for _, library := range r.Libraries {
		var libraryKind int
		if err = s.read().QueryRowContext(ctx, `SELECT kind FROM catalog_libraries WHERE library_id=? AND retired=0`, library).Scan(&libraryKind); err == sql.ErrNoRows {
			continue
		} else if err != nil {
			return nil, true, err
		}
		kind := 0
		switch libraryKind {
		case 1:
			kind = 1
		case 2, 3:
			kind = 4
		case 4:
			kind = 7
		case 5:
			kind = 9
		default:
			continue
		}
		rows, err = s.read().QueryContext(ctx, `SELECT pid(i.public_id) FROM catalog_browse_rows b INDEXED BY catalog_browse_recent_kind
			JOIN catalog_entities i ON i.id=b.entity_id
			WHERE b.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND b.kind=? AND b.item_id IS NOT NULL
			ORDER BY COALESCE(b.added_text,'') DESC,b.entity_id DESC LIMIT ?`, library, kind, recentPerLibrary)
		if err != nil {
			return nil, true, err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				break
			}
			add(id)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, true, err
		}
	}
	return pool, true, nil
}

const relatedPoolLimit = 384

// relatedPool bounds "related" scoring (item pages, listening pages) to the
// items that share at least one persisted facet with the target, found by
// index: its genres, credited people, collections, show, album artist and
// credited artists. A large library is never scored whole (ARCH-SRV-04);
// a small one keeps the complete relation.
func (s *Service) relatedPool(r HomeRequest, target string) ([]string, bool, error) {
	ctx := s.Context()
	small, whole, err := s.smallLibraryItems(r, relatedPoolLimit)
	if err != nil || whole {
		return small, true, err
	}
	seen := map[string]bool{}
	pool := []string{target}
	seen[target] = true

	for _, query := range relatedPoolQueries {
		rows, err := s.read().QueryContext(ctx, query, target)
		if err != nil {
			return nil, true, err
		}
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				break
			}
			if !seen[id] && len(pool) < relatedPoolLimit {
				seen[id] = true
				pool = append(pool, id)
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, true, err
		}
	}
	return pool, true, nil
}

var relatedPoolQueries = []string{
	// Same genre name (up to eight of the target's genres).
	`SELECT pid(e2.public_id) FROM (SELECT DISTINCT g.source_name FROM catalog_entities seed JOIN catalog_term_sources g ON g.entity_id=seed.id WHERE seed.public_id=pid_blob(?) LIMIT 8) g
		  CROSS JOIN catalog_term_sources g2 INDEXED BY catalog_term_sources_name_seek ON g2.source_name=g.source_name COLLATE NOCASE JOIN catalog_entities e2 ON e2.id=g2.entity_id LIMIT 96`,
	// Same credited people (up to twelve).
	`SELECT pid(i.public_id) FROM (SELECT DISTINCT c.provider,c.provider_person_id FROM catalog_entities seed CROSS JOIN catalog_credits c NOT INDEXED ON c.entity_id=seed.id WHERE seed.public_id=pid_blob(?) AND c.provider_person_id<>'' LIMIT 12) p
		  CROSS JOIN catalog_credits c2 INDEXED BY catalog_credits_provider_person ON c2.provider=p.provider AND c2.provider_person_id=p.provider_person_id
		  JOIN catalog_entities i ON i.id=c2.entity_id WHERE c2.provider_person_id<>'' LIMIT 128`,
	// Same collections.
	`SELECT pid(i.public_id) FROM catalog_entities seed JOIN catalog_collection_members c ON c.item_id=seed.id
		  CROSS JOIN catalog_collection_members c2 ON c2.collection_id=c.collection_id JOIN catalog_entities i ON i.id=c2.item_id
		  WHERE seed.public_id=pid_blob(?) LIMIT 64`,
	// Other episodes of the same show (the target's own work).
	`SELECT pid(i.public_id) FROM catalog_entities seed JOIN catalog_episodes e ON e.entity_id=seed.id
		  CROSS JOIN catalog_episodes e2 ON e2.show_id=e.show_id JOIN catalog_entities i ON i.id=e2.entity_id
		  WHERE seed.public_id=pid_blob(?) LIMIT 48`,
	// The album artist's and credited artists' songs.
	`SELECT pid(i.public_id) FROM catalog_entities seed JOIN catalog_songs s ON s.entity_id=seed.id JOIN catalog_albums a ON a.entity_id=s.album_id
		  CROSS JOIN catalog_albums a2 INDEXED BY catalog_albums_artist ON a2.artist_id=a.artist_id
		  CROSS JOIN catalog_entities a2e ON a2e.id=a2.entity_id AND a2e.retired=0
		  CROSS JOIN catalog_songs s2 ON s2.album_id=a2.entity_id JOIN catalog_entities i ON i.id=s2.entity_id
		  WHERE seed.public_id=pid_blob(?) LIMIT 64`,
	`SELECT pid(i.public_id) FROM catalog_entities seed JOIN catalog_song_artists sa ON sa.song_id=seed.id
		  CROSS JOIN catalog_song_artists sa2 INDEXED BY catalog_song_artists_artist ON sa2.artist_id=sa.artist_id JOIN catalog_entities i ON i.id=sa2.song_id
		  WHERE seed.public_id=pid_blob(?) LIMIT 64`,
	// Other books by the author, using the library/author index.
	`SELECT pid(i.public_id) FROM catalog_entities seed JOIN catalog_book_files bf ON bf.entity_id=seed.id JOIN catalog_books b ON b.entity_id=bf.book_id
		  CROSS JOIN catalog_books b2 INDEXED BY catalog_books_author_seek ON b2.library_id=b.library_id AND b2.author=b.author COLLATE NOCASE
		  CROSS JOIN catalog_entities b2e ON b2e.id=b2.entity_id AND b2e.retired=0
		  CROSS JOIN catalog_book_files bf2 ON bf2.book_id=b2.entity_id JOIN catalog_entities i ON i.id=bf2.entity_id
		  WHERE seed.public_id=pid_blob(?) AND b.author<>'' LIMIT 64`,
}

// smallLibraryItems returns every scorable item id in r.Libraries when there
// are at most limit of them (whole=true), found by one capped index read;
// otherwise it reports whole=false after reading limit+1 keys.
func (s *Service) smallLibraryItems(r HomeRequest, limit int) ([]string, bool, error) {
	rows, err := s.read().QueryContext(s.Context(), `SELECT pid(e.public_id) FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id JOIN catalog_kinds k ON k.id=e.kind WHERE l.library_id IN(SELECT value FROM json_each(?)) AND k.playable=1 AND e.kind<>11 LIMIT ?`, idsJSON(r.Libraries), limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, false, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	if len(ids) > limit {
		return nil, false, nil
	}
	return ids, true, nil
}
