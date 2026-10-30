package catalog

import (
	"database/sql"
)

// showRecommendationSeed is the episode a show page's rows are seeded by: the
// show's first visible episode. An episode's work is its show, so any visible
// episode seeds the whole show's facet set, and the show itself is excluded
// from the results.
func (s *Service) showRecommendationSeed(viewer Viewer, show string) (seed, title, library string, err error) {
	library, err = s.LibraryForShow(show)
	if err != nil {
		return "", "", "", err
	}
	if !viewer.AllowsLibrary(library) {
		return "", "", "", sql.ErrNoRows
	}
	if err = s.prepareViewer(viewer); err != nil {
		return "", "", "", err
	}
	// Published compact facts only: the show, its episodes and their
	// asset links (the old tables are the projector's input, not a read).
	restriction, bound := ItemRestrictionSQL("item.id", viewer.EffectiveRestrictions())
	args := append([]any{show}, bound...)
	if err = s.read().QueryRow(`SELECT pid(item.public_id) FROM catalog_entities sh JOIN catalog_episodes ep ON ep.show_id=sh.id JOIN catalog_entities item ON item.id=ep.entity_id
  WHERE sh.public_id=pid_blob(?) AND EXISTS(SELECT 1 FROM catalog_asset_links a WHERE a.entity_id=item.id) AND `+restriction+` ORDER BY item.id LIMIT 1`, args...).Scan(&seed); err != nil {
		return "", "", "", err
	}
	err = s.read().QueryRow(`SELECT title FROM catalog_entities WHERE public_id=pid_blob(?)`, show).Scan(&title)
	return seed, title, library, err
}

// ShowRecommendationRows publishes the library-local rows for a show page
// (Spec — Page Content §2): the same engine as movies (genres, people,
// keywords), seeded by showRecommendationSeed. Each row continues through
// ShowRecommendationRow.
func (s *Service) ShowRecommendationRows(viewer Viewer, show string, limit int) ([]HomeRow, ContentRevision, error) {
	seed, title, library, err := s.showRecommendationSeed(viewer, show)
	if err != nil {
		return nil, ContentRevision{}, err
	}
	before, err := s.ContentRevision(library, viewer.Profile)
	if err != nil {
		return nil, before, err
	}
	loaded, err := s.Get(viewer.Profile, seed)
	if err != nil {
		return nil, before, err
	}
	genres, err := s.itemGenreList(seed)
	if err != nil {
		return nil, before, err
	}
	if limit <= 0 || limit > itemRecommendationRowLimit {
		limit = itemRecommendationRowLimit
	}
	specs, err := s.itemRelationSpecs(viewer, loaded, genres, false, false)
	if err != nil {
		return nil, before, err
	}
	owner := relationOwner{kind: "show", id: show, library: library, itemKind: "show"}
	rows := []HomeRow{}
	for _, spec := range specs {
		if len(rows) == itemRecommendationRows {
			break
		}
		if spec.relation == "more_like" {
			spec.heading = "More like " + title
		}
		row, err := s.relationPage(viewer, owner, spec, "", limit)
		if err != nil {
			return nil, before, err
		}
		if len(row.Entries) == 0 {
			continue
		}
		row.Priority = len(rows) * 10
		rows = append(rows, row)
	}
	after, err := s.ContentRevision(library, viewer.Profile)
	if err != nil {
		return nil, before, err
	}
	if before != after {
		return nil, before, ErrStaleContinuation
	}
	return rows, before, nil
}
