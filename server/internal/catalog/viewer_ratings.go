package catalog

// attachViewerRatings sets the viewer's own rating on the item rows of one
// page (NEW-24). It is one indexed read per page, keyed on the page's ids and
// the viewer's profile, never on the library. The rows have already passed the
// viewer's visibility and restriction checks, and the rating is the viewer's
// own personal state, so the pass cannot reveal anything the page did not.
// Containers (shows, albums, artists) carry no personal rating.
func (s *Service) attachViewerRatings(profile string, pages ...[]ContentEntry) error {
	if profile == "" {
		return nil
	}
	ids := []string{}
	for _, entries := range pages {
		for _, entry := range entries {
			if isItemEntity(entry.Kind) {
				ids = append(ids, entry.ID)
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.read().Query(`SELECT pid(ce.public_id),pi.rating FROM personal_items pi JOIN catalog_entities ce ON ce.id=pi.item_id WHERE pi.profile_id=? AND pi.rating IS NOT NULL AND ce.public_id IN(SELECT pid_blob(value) FROM json_each(?))`, profile, idsJSON(ids))
	if err != nil {
		return err
	}
	ratings := map[string]float64{}
	for rows.Next() {
		var id string
		var rating float64
		if err = rows.Scan(&id, &rating); err != nil {
			rows.Close()
			return err
		}
		ratings[id] = rating
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, entries := range pages {
		for index := range entries {
			if rating, ok := ratings[entries[index].ID]; ok && isItemEntity(entries[index].Kind) {
				value := rating
				entries[index].UserRating = &value
			}
		}
	}
	return nil
}
