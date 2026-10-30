package catalog

import (
	"database/sql"
)

const relatedEntryLimit = 12

type RelatedMovieRow struct {
	ID         string `json:"id"`
	Relation   string `json:"relation"`
	Provider   string `json:"provider"`
	EvidenceID string `json:"evidenceId"`
	Heading    string `json:"heading"`
	// HeadingText is Heading as {code, params, fallback} (CON-19).
	HeadingText ServerText     `json:"headingText"`
	Entries     []ContentEntry `json:"entries"`
	// NextCursor continues the row through its row route
	// (get_items_id_recommendations_row) while HasMore.
	NextCursor string `json:"nextCursor,omitempty"`
	HasMore    bool   `json:"hasMore,omitempty"`
}
type RelatedMovies struct {
	Version     int               `json:"version"`
	LibraryID   string            `json:"libraryId"`
	LibraryName string            `json:"libraryName"`
	Rows        []RelatedMovieRow `json:"rows"`
}

// movieRelations are a film's genre rows (its first two genres) and its
// top-billed actor's row, each ordered nearest the film's year first.
func (s *Service) movieRelations(item Item, genres []Genre) ([]relationSpec, error) {
	if err := s.compactProjectionReady(21); err != nil {
		return nil, err
	}
	var kind int
	if e := s.read().QueryRow(`SELECT kind FROM catalog_libraries WHERE library_id=? AND retired=0`, item.LibraryID).Scan(&kind); e != nil {
		return nil, e
	}
	if item.Kind != "movie" || kind != 1 {
		return nil, nil
	}
	out := []relationSpec{}
	for _, genre := range genres {
		if len(out) == 2 {
			break
		}
		// IDs for manual and NFO genres are local tokens, not global facets:
		// the row reads the genre by its folded name.
		out = append(out, relationSpec{id: "genre:" + genre.Provider + ":" + genre.ID, relation: "genre", provider: genre.Provider, evidence: genre.ID, heading: "More " + genre.Name + " movies",
			caching: relationRevision, boundary: true, order: s.nearestYearOrder(item, 1, "name", normalizeFacetText(genre.Name), genre.Provider)})
	}
	var provider, person, name string
	e := s.read().QueryRow(`SELECT c.provider,c.provider_person_id,c.credited_name
 FROM catalog_entities i JOIN catalog_credits c ON c.entity_id=i.id
 JOIN catalog_credit_labels d ON d.id=c.department_id
 WHERE i.public_id=pid_blob(?) AND c.provider_person_id<>'' AND d.label='Acting'
 ORDER BY c.source_ordinal,c.provider,c.provider_person_id LIMIT 1`, item.ID).Scan(&provider, &person, &name)
	if e != nil && e != sql.ErrNoRows {
		return nil, e
	}
	if e == nil {
		out = append(out, relationSpec{id: "person:" + provider + ":" + person, relation: "person", provider: provider, evidence: person, heading: "With " + name,
			caching: relationRevision, boundary: true, order: s.nearestYearOrder(item, 2, provider, person, provider)})
	}
	return out, nil
}
