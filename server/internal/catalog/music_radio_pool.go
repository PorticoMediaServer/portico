package catalog

import "database/sql"

// radioPool selects cheap keys before reading credits or sonic vectors. Small
// libraries keep their exact order. Larger ones combine the existing genre
// posting heads, the local co-listening albums and a rotating library-index
// window. All limits reuse the recommendation engine's existing budgets.
func (s *Service) radioPool(viewer Viewer, seed, pivot int64, genres []string, related []string) ([]string, error) {
	small, whole, err := s.smallLibraryItems(HomeRequest{Libraries: viewer.Libraries}, recommendationPoolLimit)
	if err != nil || whole {
		return small, err
	}
	return s.radioIndexedPool(viewer, seed, pivot, genres, related)
}

func (s *Service) radioIndexedPool(viewer Viewer, seed, pivot int64, genres, related []string) ([]string, error) {
	pool := []string{}
	seen := map[string]bool{}
	add := func(ids []string) {
		for _, id := range ids {
			if !seen[id] && len(pool) < recommendationPoolLimit {
				seen[id] = true
				pool = append(pool, id)
			}
		}
	}
	// The start song is always included, even on an artist's very large album.
	first, err := radioPoolKeys(s.read().Query(`SELECT pid(public_id) FROM catalog_entities WHERE id=?`, seed))
	if err != nil {
		return nil, err
	}
	add(first)
	// Reserve half the pool for exploration, so broad genre heads or a single
	// large related album cannot turn every refill into the same short playlist.
	visible, args := viewer.itemVisibilitySQL("item.id")
	eligible := ` AND ` + visible + ` AND ` + currentSonicEligibleSQL + `
 AND EXISTS(SELECT 1 FROM catalog_item_availability available WHERE available.entity_id=item.id AND available.available=1 AND available.retired=0)`
	head := max(1, recommendationPoolLimit/(2*max(1, len(viewer.Libraries))))
	for _, library := range viewer.Libraries {
		ids, err := radioPoolKeys(s.read().Query(`SELECT pid(item.public_id) FROM catalog_entities item INDEXED BY catalog_entities_library
   CROSS JOIN catalog_libraries library ON library.id=item.library_id
   CROSS JOIN libraries configured ON configured.id=library.library_id AND configured.kind='music'
   WHERE item.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND item.kind=7 AND item.id>?`+eligible+`
   ORDER BY item.id LIMIT ?`, append(append([]any{library, pivot}, args...), head)...))
		if err != nil {
			return nil, err
		}
		add(ids)
		if left := head - len(ids); left > 0 {
			ids, err = radioPoolKeys(s.read().Query(`SELECT pid(item.public_id) FROM catalog_entities item INDEXED BY catalog_entities_library
   CROSS JOIN catalog_libraries library ON library.id=item.library_id
   CROSS JOIN libraries configured ON configured.id=library.library_id AND configured.kind='music'
    WHERE item.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND item.kind=7 AND item.id<=?`+eligible+`
    ORDER BY item.id LIMIT ?`, append(append([]any{library, pivot}, args...), left)...))
			if err != nil {
				return nil, err
			}
			add(ids)
		}
	}

	// Postings are works, so expand the bounded album heads through the album
	// index; never try a facet-only walk across every library's postings.
	genreHead := max(1, recPostingsHead/max(1, len(viewer.Libraries)))
	for _, library := range viewer.Libraries {
		for _, genre := range genres {
			ids, err := radioPoolKeys(s.read().Query(`SELECT pid(item.public_id) FROM
   (SELECT entity_id FROM catalog_rec_postings WHERE facet=? AND library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) ORDER BY quality DESC,entity_id DESC LIMIT ?) work
   CROSS JOIN catalog_songs song INDEXED BY catalog_songs_album_entity ON song.album_id=work.entity_id
   CROSS JOIN catalog_entities item ON item.id=song.entity_id WHERE item.retired=0 LIMIT ?`, genre, library, genreHead, genreHead))
			if err != nil {
				return nil, err
			}
			add(ids)
		}
	}
	ids, err := radioPoolKeys(s.read().Query(`SELECT pid(item.public_id) FROM json_each(?) work
  CROSS JOIN catalog_entities album ON album.public_id=pid_blob(work.value)
  CROSS JOIN catalog_songs song INDEXED BY catalog_songs_album_entity ON song.album_id=album.id
  CROSS JOIN catalog_entities item ON item.id=song.entity_id WHERE item.retired=0 LIMIT ?`, idsJSON(related), recPostingsHead))
	if err != nil {
		return nil, err
	}
	add(ids)
	return pool, nil
}

func radioPoolKeys(rows *sql.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
