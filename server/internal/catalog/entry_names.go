package catalog

import "database/sql"

// nameEntryMakers says who made each album and book on a page of entries: an
// album card always names its artist and a book card its author, wherever the
// card appears (a library, Home, search). An audiobook file names its book's
// author, so Home's hero can say who wrote what is being listened to. One query
// for the page, each row a unique public id seek.
func (s *Service) nameEntryMakers(entries []ContentEntry) error {
	ids := []string{}
	for _, entry := range entries {
		if entry.Kind == "album" && entry.Artist == nil || (entry.Kind == "book" || entry.Kind == "audiobook_file") && entry.Author == "" {
			ids = append(ids, entry.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.read().Query(`SELECT pid(e.public_id),
 COALESCE((SELECT pid(ar.public_id) FROM catalog_albums a JOIN catalog_entities ar ON ar.id=a.artist_id WHERE a.entity_id=e.id),''),
 COALESCE((SELECT ar.title FROM catalog_albums a JOIN catalog_entities ar ON ar.id=a.artist_id WHERE a.entity_id=e.id),''),
 COALESCE((SELECT b.author FROM catalog_books b WHERE b.entity_id=e.id),(SELECT b.author FROM catalog_book_files f JOIN catalog_books b ON b.entity_id=f.book_id WHERE f.entity_id=e.id),'')
 FROM (SELECT DISTINCT value FROM json_each(?)) requested CROSS JOIN catalog_entities e ON e.public_id=pid_blob(requested.value)`, idsJSON(ids))
	if err != nil {
		return err
	}
	defer rows.Close()
	type maker struct {
		artist ContentEntityRef
		author string
	}
	found := map[string]maker{}
	for rows.Next() {
		var id string
		var m maker
		var artistID, artistName, author sql.NullString
		if err = rows.Scan(&id, &artistID, &artistName, &author); err != nil {
			return err
		}
		m.artist = ContentEntityRef{ID: artistID.String, Name: artistName.String}
		m.author = author.String
		found[id] = m
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for index := range entries {
		m, ok := found[entries[index].ID]
		if !ok {
			continue
		}
		if entries[index].Kind == "album" && entries[index].Artist == nil && m.artist.Name != "" {
			artist := m.artist
			entries[index].Artist = &artist
		}
		if (entries[index].Kind == "book" || entries[index].Kind == "audiobook_file") && m.author != "" {
			entries[index].Author = m.author
		}
	}
	return nil
}
