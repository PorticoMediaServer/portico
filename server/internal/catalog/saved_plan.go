package catalog

import "github.com/PorticoMediaServer/portico/server/internal/requestinput"

func normalizeSavedRequest(r ContentRequest) (ContentRequest, string, string, error) {
	if r.Shape != "" && r.Shape != "posters" && r.Shape != "squares" {
		return r, "", "", ErrCursor
	}
	if r.View != "watchlist" && r.View != "favorites" {
		return r, "", "", requestinput.New("invalid Saved view")
	}
	if r.Sort == "" {
		r.Sort = "title"
	}
	if r.Direction == "" {
		r.Direction = "asc"
	}
	if r.Filter == "" {
		r.Filter = "all"
	}
	if r.Limit == 0 {
		r.Limit = 40
	}
	order, sortOK := savedSorts[r.Sort]
	filter, filterOK := savedFilters[r.Filter]
	if !sortOK || !filterOK || (r.Direction != "asc" && r.Direction != "desc") || r.Limit < 1 || r.Limit > 100 || r.Q != "" || r.Category != "" || r.EntityID != "" {
		return r, "", "", requestinput.New("invalid Saved query")
	}
	return r, order, filter, nil
}

// Both ordinary Saved and the dynamic-reference union use this exact leaf
// predicate. It does not materialize a group as an entity or expand its flags.
func savedLeafBase(r ContentRequest, libraries []string, flag string, readingScope savedReadingScope) (string, []any) {
	base := ` FROM personal_items p CROSS JOIN catalog_entities i ON i.id=p.item_id LEFT JOIN catalog_item_details detail ON detail.entity_id=i.id WHERE p.profile_id=? AND p.` + flag + `=1 AND i.retired=0 AND i.library_id IN(SELECT id FROM catalog_libraries WHERE library_id IN(SELECT value FROM json_each(?)) AND retired=0)`
	args := []any{r.Profile, idsJSON(libraries)}
	visibility, visibleArgs := r.Viewer.itemVisibilitySQL("i.id")
	containers, containerArgs := r.Viewer.entityVisibilitySQL("i.id", "(SELECT library_id FROM catalog_libraries WHERE id=i.library_id)")
	base += ` AND ((i.kind IN(1,4,7,9) AND ` + visibility + `) OR (i.kind IN(2,5,6,8) AND ` + containers + `) OR (i.kind IN(20,21) AND ` + visibility + ` AND ` + readingScope.predicate + `))`
	args = append(append(append(args, visibleArgs...), containerArgs...), visibleArgs...)
	return base, append(args, readingScope.args...)
}

func savedShapePredicate(shape string) string {
	switch shape {
	case "posters":
		return ` AND i.kind IN(1,2,3,4,20,21)`
	case "squares":
		return ` AND i.kind IN(5,6,7,8,9,13)`
	default:
		return ""
	}
}
