package catalog

import "strings"

// Listening relations reuse the same permitted work/facet projection as Home.
// A song or part is evidence for its album/book, never a recommendation card.
// Each row is the seed's related ranking narrowed to one of its facets (the
// artist, author, genre or book series), read once for all of them.
func (s *Service) listeningRelations(profile string, item Item) ([]relationSpec, error) {
	r := HomeRequest{Profile: profile, Libraries: []string{item.LibraryID}, Restrictions: s.recRestrictions}
	// Only the seed supplies the headings; candidate facets are scored when
	// a row is read.
	base, args := recBase(r, []string{item.ID})
	args = append(args, item.ID)
	rows, err := s.read().Query(base+` SELECT f.f FROM facets f JOIN members m ON m.work=f.work WHERE m.public_id=pid_blob(?) ORDER BY f.f`, args...)
	if err != nil {
		return nil, err
	}
	facets := []string{}
	for rows.Next() {
		var f string
		if err = rows.Scan(&f); err != nil {
			rows.Close()
			return nil, err
		}
		facets = append(facets, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var candidates []recCandidate
	read := false
	ranked := func() ([]recCandidate, error) {
		if read {
			return candidates, nil
		}
		var err error
		candidates, err = s.recommendationCandidates(r, "related", item.ID)
		read = err == nil
		return candidates, err
	}
	out := []relationSpec{}
	for _, f := range facets {
		prefix, value, _ := strings.Cut(f, ":")
		relation, heading := "", ""
		switch prefix {
		case "a":
			relation = "artist"
			var artist string
			if err = s.read().QueryRow(`SELECT e.title FROM catalog_entities e JOIN catalog_artists a ON a.entity_id=e.id WHERE e.public_id=pid_blob(?) AND e.retired=0`, value).Scan(&artist); err != nil {
				return nil, err
			}
			heading = "More releases by " + artist
		case "b":
			relation = "author"
			heading = "More by " + value
		case "g":
			relation = "genre"
			heading = "More " + value
		case "k":
			relation = "book"
			heading = "More from " + value
		default:
			continue
		}
		facet := f
		order := func(int) (*relationOrdered, error) {
			all, err := ranked()
			if err != nil {
				return nil, err
			}
			out := &relationOrdered{public: []string{}, complete: true}
			seen := map[string]bool{}
			for _, c := range all {
				if containsString(c.Facets, facet) {
					out.public = append(out.public, c.ID)
					seen[c.ID] = true
				}
			}
			// The ranking only holds what scored as related; the row is still
			// this facet's (lead, 2 Nov), so the facet's other titles follow,
			// up to the row size. The row is a boundary row: relationEntries
			// drops anything this viewer can't see.
			if len(out.public) < listeningRowSize {
				more, err := s.listeningFacetFill(item, facet, seen, listeningRowSize-len(out.public))
				if err != nil {
					return nil, err
				}
				out.public = append(out.public, more...)
			}
			return out, nil
		}
		out = append(out, relationSpec{id: relation + ":local:" + value, relation: relation, provider: "local", evidence: value, heading: heading,
			caching: relationContinuation, boundary: true, order: order})
	}
	return out, nil
}

// listeningFacetFill lists up to n more works of one facet (an artist, author,
// genre or series) in item's library, best first, skipping the item's own
// album or book and every id already in the row.
func (s *Service) listeningFacetFill(item Item, facet string, seen map[string]bool, n int) ([]string, error) {
	var library, own int64
	if err := s.read().QueryRow(`SELECT l.id,COALESCE(song.album_id,part.book_id,0) FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id
 LEFT JOIN catalog_songs song ON song.entity_id=e.id LEFT JOIN catalog_book_files part ON part.entity_id=e.id
 WHERE e.public_id=pid_blob(?) AND e.retired=0 AND l.retired=0`, item.ID).Scan(&library, &own); err != nil {
		return nil, err
	}
	works := []int64{}
	if err := s.recPostings(facet, library, 4*listeningRowSize, func(work int64, _ float64) {
		if work != own {
			works = append(works, work)
		}
	}); err != nil || len(works) == 0 {
		return nil, err
	}
	rows, err := s.read().Query(`SELECT pid(e.public_id) FROM json_each(?) j JOIN catalog_entities e ON e.id=j.value WHERE e.retired=0 ORDER BY j.key`, idsJSON64(works))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() && len(out) < n {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, rows.Err()
}
