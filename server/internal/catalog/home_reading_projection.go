package catalog

import (
	"database/sql"
	"sort"
)

// homeReadingPage projects only the selected IDs in the current composition
// snapshot. It returns the Reading subset so callers can retain their own order.
func (s *Service) homeReadingPage(viewer Viewer, ids []string, authorize func(*sql.Tx, string) error) (map[string]ContentEntry, error) {
	out := map[string]ContentEntry{}
	if len(ids) == 0 {
		return out, nil
	}
	visible, args := viewer.itemVisibilitySQL("e.id")
	args = append([]any{idsJSON(ids)}, args...)
	rows, err := s.read().Query(`SELECT e.id,pid(e.public_id),k.name,e.title,COALESCE(e.year,0),cl.library_id,COALESCE(b.sort_key,'') FROM json_each(?) requested CROSS JOIN catalog_entities e ON e.public_id=pid_blob(requested.value) JOIN catalog_kinds k ON k.id=e.kind JOIN catalog_libraries cl ON cl.id=e.library_id LEFT JOIN catalog_browse_rows b ON b.entity_id=e.id WHERE e.kind IN (20,21) AND `+visible, args...)
	if err != nil {
		return nil, err
	}
	groups := map[string][]browseRow{}
	for rows.Next() {
		var row browseRow
		var library string
		if err = rows.Scan(&row.entity, &row.id, &row.kind, &row.title, &row.year, &library, &row.sortKey); err != nil {
			break
		}
		groups[library] = append(groups[library], row)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	libraries := make([]string, 0, len(groups))
	for library := range groups {
		libraries = append(libraries, library)
	}
	sort.Strings(libraries)
	for _, library := range libraries {
		scope, e := s.readingBrowseScope(library)
		if e != nil {
			return nil, e
		}
		if scope == nil {
			return nil, ErrReadingProjectionChanged
		}
		selected := groups[library]
		keys := make([]string, len(selected))
		for i, row := range selected {
			keys[i] = row.id
		}
		var count int
		if e = s.read().QueryRow(`SELECT count(*) FROM json_each(?) requested CROSS JOIN catalog_entities e ON e.public_id=pid_blob(requested.value) WHERE `+scope.eligibleSQL("e.id"), idsJSON(keys)).Scan(&count); e != nil {
			return nil, e
		}
		if count != len(selected) {
			return nil, ErrReadingProjectionChanged
		}
		var personal func(*sql.Tx) error
		if authorize != nil {
			personal = func(tx *sql.Tx) error { return authorize(tx, library) }
		}
		entries, e := scope.project(s, selected, viewer.Profile, personal)
		if e != nil {
			return nil, e
		}
		for _, entry := range entries {
			out[entry.ID] = entry
		}
	}
	return out, nil
}
