package catalog

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/identity"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
)

type savedReadingScope struct {
	personalAdmission SavedGroupAdmission
	viewer            Viewer
	libraries         map[string]*readingBrowseScope
	predicate, fence  string
	args              []any
}

// Only libraries with this viewer's actually saved, visible Reading leaves
// participate. Other Saved requests keep their previous cursor domain.
func (s *Service) savedReading(r ContentRequest, libraries []string, flag string) (savedReadingScope, error) {
	out := savedReadingScope{personalAdmission: r.ReadingPersonalAdmission, viewer: r.Viewer, libraries: map[string]*readingBrowseScope{}, predicate: "0"}
	visibility, args := r.Viewer.itemVisibilitySQL("i.id")
	query := `SELECT DISTINCT cl.library_id,cl.id,lib.kind,COALESCE(a.namespace,0),COALESCE(a.input_generation,0),COALESCE(a.ready_generation,0),COALESCE(a.publication_rule_version,0) FROM personal_items p
 JOIN catalog_entities i ON i.id=p.item_id JOIN catalog_libraries cl ON cl.id=i.library_id
 JOIN libraries lib ON lib.id=cl.library_id LEFT JOIN reading_attribution_state a ON a.library_id=cl.id
 WHERE p.profile_id=? AND p.` + flag + `=1 AND i.retired=0 AND cl.retired=0
 AND cl.library_id IN(SELECT value FROM json_each(?))
 AND ((i.kind=20 AND lib.kind='book') OR (i.kind=21 AND lib.kind='comic')) AND ` + visibility + ` ORDER BY cl.library_id`
	parameters := append([]any{r.Profile, idsJSON(libraries)}, args...)
	rows, err := s.read().Query(query, parameters...)
	if err != nil {
		return out, err
	}
	var witnesses []string
	var members []struct {
		Library    int64  `json:"library"`
		Generation int64  `json:"generation"`
		Kind       string `json:"kind"`
		Mobi       bool   `json:"mobi"`
	}
	for rows.Next() {
		var library string
		var scope readingBrowseScope
		var rule int
		if err = rows.Scan(&library, &scope.library, &scope.kind, &scope.generation.Namespace, &scope.generation.Input, &scope.generation.Ready, &rule); err != nil {
			rows.Close()
			return out, err
		}
		if scope.generation.Ready == 0 || scope.generation.Input != scope.generation.Ready || rule != compactcatalog.ReadingProjectionRuleVersion {
			rows.Close()
			return out, ErrReadingProjectionChanged
		}
		scope.mobi = scope.kind == "book" && s.readingMobiConfigured()
		out.libraries[library] = &scope
		members = append(members, struct {
			Library    int64  `json:"library"`
			Generation int64  `json:"generation"`
			Kind       string `json:"kind"`
			Mobi       bool   `json:"mobi"`
		}{scope.library, scope.generation.Ready, scope.kind, scope.mobi})
		witnesses = append(witnesses, library+":"+scope.fence())
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(members) > 0 {
		encodedMembers, err := json.Marshal(members)
		if err != nil {
			return out, err
		}
		out.args = []any{string(encodedMembers)}
		out.predicate = `EXISTS(SELECT 1 FROM json_each(?) admitted
 JOIN reading_attribution_members m ON m.library_id=json_extract(admitted.value,'$.library') AND m.generation=json_extract(admitted.value,'$.generation')
 WHERE m.item_id=i.id AND i.library_id=m.library_id AND EXISTS(SELECT 1 FROM reading_publications publication
 JOIN catalog_asset_links link ON link.entity_id=i.id AND link.asset_id=publication.asset_id
 WHERE publication.item_id=i.id AND publication.asset_id=` + savedReadingPrimaryPublicationSQL("i.id") + `
 AND ((i.kind=20 AND json_extract(admitted.value,'$.kind')='book' AND (publication.format IN('epub','pdf') OR (json_extract(admitted.value,'$.mobi')=1 AND publication.format IN('mobi','azw3'))))
 OR (i.kind=21 AND json_extract(admitted.value,'$.kind')='comic' AND publication.format IN(` + readingcap.ComicFormatsSQL() + `) AND ` + readingcap.LocalComicArchiveSQL("publication") + `))))`
		encoded, err := json.Marshal(witnesses)
		if err != nil {
			return out, err
		}
		out.fence = fmt.Sprintf("reading-saved:%x", sha256.Sum256(encoded))
	}
	return out, nil
}

// The page was already admitted and ordered by Saved's full predicate. Project
// at most that selected page, then reassemble in its original server order.
func (s *Service) savedEntries(profile string, ids []string, scope savedReadingScope) ([]ContentEntry, error) {
	if len(scope.libraries) == 0 {
		return s.homeEngineEntries(profile, ids)
	}
	rows, err := s.read().Query(`SELECT e.id,pid(e.public_id),k.name,e.title,COALESCE(e.year,0),cl.library_id
 FROM json_each(?) selected JOIN catalog_entities e ON e.public_id=pid_blob(selected.value)
 JOIN catalog_kinds k ON k.id=e.kind JOIN catalog_libraries cl ON cl.id=e.library_id`, idsJSON(ids))
	if err != nil {
		return nil, err
	}
	reading := map[string][]browseRow{}
	var ordinary []string
	var readingIDs []int64
	for rows.Next() {
		var row browseRow
		var library string
		if err = rows.Scan(&row.entity, &row.id, &row.kind, &row.title, &row.year, &library); err != nil {
			rows.Close()
			return nil, err
		}
		if row.kind == "ebook" || row.kind == "comic" {
			if scope.libraries[library] == nil {
				rows.Close()
				return nil, ErrReadingProjectionChanged
			}
			reading[library] = append(reading[library], row)
			readingIDs = append(readingIDs, row.entity)
		} else {
			ordinary = append(ordinary, row.id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	states, err := readPersonalBatch(s.Context(), dbwork.Snapshot(s.Context()), profile, readingIDs)
	if err != nil {
		return nil, err
	}
	entries, err := s.homeEngineEntries(profile, ordinary)
	if err != nil {
		return nil, err
	}
	byID := map[string]ContentEntry{}
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	for library, selected := range reading {
		// Re-admit the original selected viewer in this same accepting snapshot.
		// A newly restricted or removed library cannot receive marks read under
		// the earlier ceiling; the caller refreshes the whole saved page.
		authorize := func(tx *sql.Tx) error {
			if scope.personalAdmission == nil {
				return identity.ErrForbidden
			}
			current, err := scope.personalAdmission(s.Context(), tx)
			if err != nil {
				return err
			}
			if current.Profile != profile || current.Fence != scope.viewer.Fence || !current.AllowsLibrary(library) || RestrictionFence(current.EffectiveRestrictions()) != RestrictionFence(scope.viewer.EffectiveRestrictions()) {
				return ErrReadingProjectionChanged
			}
			return nil
		}
		projected, err := scope.libraries[library].project(s, selected, profile, authorize)
		if err != nil {
			return nil, err
		}
		for index, entry := range projected {
			state, ok := states[selected[index].entity]
			if !ok {
				return nil, ErrStaleContinuation
			}
			listed, favorite, read := state.Watchlisted, state.Favorite, state.Watched
			entry.Watchlisted, entry.Favorite, entry.Watched = &listed, &favorite, &read
			byID[entry.ID] = entry
		}
	}
	out := make([]ContentEntry, 0, len(ids))
	for _, id := range ids {
		entry, ok := byID[id]
		if !ok {
			return nil, ErrStaleContinuation
		}
		out = append(out, entry)
	}
	return out, nil
}

// The JSON admission tuple is produced only from the current server's captured
// per-library scope, never user text. Choose primary within that exact kind and
// converter domain so an unavailable original cannot conceal a native version.
func savedReadingPrimaryPublicationSQL(item string) string {
	return `(SELECT candidate.asset_id FROM reading_publications candidate WHERE candidate.item_id=` + item + `
 AND ((json_extract(admitted.value,'$.kind')='book' AND (candidate.format IN('epub','pdf') OR (json_extract(admitted.value,'$.mobi')=1 AND candidate.format IN('mobi','azw3')))) OR (json_extract(admitted.value,'$.kind')='comic' AND candidate.format IN(` + readingcap.ComicFormatsSQL() + `))) AND ` + readingcap.LocalComicArchiveSQL("candidate") + `
 ORDER BY EXISTS(SELECT 1 FROM inventory_item_availability available WHERE available.item_id=candidate.item_id AND available.asset_id=(SELECT token FROM catalog_assets WHERE id=candidate.asset_id) AND available.available=1) DESC,candidate.asset_id LIMIT 1)`
}
