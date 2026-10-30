package catalog

import (
	"database/sql"
	"sort"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Project only the already selected personal-collection page. Reading uses its
// canonical ready publication projector, never timed-media playback facts.
func (s *Service) resourceReadingEntries(viewer Viewer, ids []string, entries map[string]ContentEntry, admit SavedGroupAdmission) error {
	rows, err := s.read().Query(`SELECT e.id,pid(e.public_id),k.name,e.title,COALESCE(e.year,0),cl.library_id FROM json_each(?) requested JOIN catalog_entities e ON e.public_id=pid_blob(requested.value) JOIN catalog_kinds k ON k.id=e.kind JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.kind IN(20,21) AND e.retired=0 AND cl.retired=0`, idsJSON(ids))
	if err != nil {
		return err
	}
	groups := map[string][]browseRow{}
	for rows.Next() {
		var row browseRow
		var library string
		if err = rows.Scan(&row.entity, &row.id, &row.kind, &row.title, &row.year, &library); err != nil {
			rows.Close()
			return err
		}
		groups[library] = append(groups[library], row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(groups) == 0 {
		return err
	}
	if admit == nil {
		return identity.ErrForbidden
	}
	libraries := make([]string, 0, len(groups))
	for library := range groups {
		libraries = append(libraries, library)
	}
	sort.Strings(libraries)
	for _, library := range libraries {
		authorize := func(tx *sql.Tx) error {
			current, err := admit(s.Context(), tx)
			if err != nil {
				return err
			}
			if current.Profile != viewer.Profile || current.Fence != viewer.Fence || !current.AllowsLibrary(library) || RestrictionFence(current.EffectiveRestrictions()) != RestrictionFence(viewer.EffectiveRestrictions()) {
				return ErrReadingProjectionChanged
			}
			return nil
		}
		if err = authorize(dbwork.Snapshot(s.Context())); err != nil {
			return err
		}
		scope, err := s.readingBrowseScope(library)
		if err != nil {
			return err
		}
		if scope == nil {
			return ErrReadingProjectionChanged
		}
		selected := groups[library]
		keys := make([]string, len(selected))
		for i, row := range selected {
			keys[i] = row.id
		}
		var eligible int
		if err = s.read().QueryRow(`SELECT count(*) FROM json_each(?) requested JOIN catalog_entities e ON e.public_id=pid_blob(requested.value) WHERE `+scope.eligibleSQL("e.id"), idsJSON(keys)).Scan(&eligible); err != nil {
			return err
		}
		if eligible != len(selected) {
			return ErrReadingProjectionChanged
		}
		projected, err := scope.project(s, selected, viewer.Profile, authorize)
		if err != nil {
			return err
		}
		for _, entry := range projected {
			entries[entry.ID] = entry
		}
	}
	return nil
}
