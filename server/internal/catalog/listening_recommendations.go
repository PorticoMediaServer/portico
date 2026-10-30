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
			for _, c := range all {
				if containsString(c.Facets, facet) {
					out.public = append(out.public, c.ID)
				}
			}
			return out, nil
		}
		out = append(out, relationSpec{id: relation + ":local:" + value, relation: relation, provider: "local", evidence: value, heading: heading,
			caching: relationContinuation, boundary: true, order: order})
	}
	return out, nil
}
