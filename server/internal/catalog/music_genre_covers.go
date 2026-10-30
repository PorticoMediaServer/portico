package catalog

// A returned genre gets at most four real album covers from a bounded sample
// of its own currently visible, available tracks. Missing art stays missing.
func (s *Service) musicGenreCovers(viewer Viewer, library, genre string) ([]string, error) {
	visible, bind := viewer.itemVisibilitySQL("item.id")
	query := `WITH sample AS MATERIALIZED (
 SELECT song.album_id,m.entity_id song_id FROM catalog_facet_members m INDEXED BY catalog_facet_group_recent
 JOIN catalog_entities item ON item.id=m.entity_id JOIN catalog_songs song ON song.entity_id=item.id
 WHERE m.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND m.field='genre' AND m.value=?
 AND ` + visible + ` AND EXISTS(SELECT 1 FROM catalog_item_availability av WHERE av.entity_id=item.id AND av.available=1 AND av.retired=0)
 ORDER BY m.recent_key DESC,m.entity_id DESC LIMIT 64)
 SELECT pid(album.public_id) FROM sample JOIN catalog_entities album ON album.id=sample.album_id
 JOIN catalog_item_details details ON details.entity_id=album.id
 WHERE album.retired=0 AND album.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND details.poster_url<>'' GROUP BY album.id ORDER BY min(sample.song_id) LIMIT 4`
	args := append([]any{library, genre}, bind...)
	rows, err := s.read().Query(query, append(args, library)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	covers := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		covers = append(covers, "/v1/items/"+id+"/art/poster")
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return covers, describeItemArtIn(s, covers, func(u *string) *string { return u })
}
