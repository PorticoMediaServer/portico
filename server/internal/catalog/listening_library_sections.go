package catalog

import (
	"encoding/json"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// listeningKey continues a library-wide listening section after the last row
// it served, in the maintained browse title order.
type listeningKey struct {
	Key string `json:"k"`
	ID  int64  `json:"id"`
}

// libraryListeningSection serves a library-wide listening list (a music
// library's artists, releases or songs; an audiobook library's books). A
// library can hold millions of songs, so nothing here reads the whole
// library: the page walks the maintained browse rows by key in title order
// (sort key, then id), checking each row's membership and visibility as it
// goes, and the total comes from maintained counts (or, for a title search or
// a restriction class still being built, an exact count).
func (s *Service) libraryListeningSection(r ContentRequest, spec listeningSection, keyed string, start int) (ContentSection, error) {
	sec := contentSection(spec.id, spec.layout, spec.title, []ContentEntry{}, 0, "")
	sec.Start = start
	if err := s.compactProjectionReady(18); err != nil {
		return sec, err
	}
	kindID, err := compactcatalog.ParseKind(spec.browseKind)
	if err != nil {
		return sec, err
	}
	visibility, visibleArgs := listeningSectionVisibility(spec.kind, r.Viewer)
	visibility = strings.ReplaceAll(visibility, "projected.entity_id", "e.entity_id")
	where := `e.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND e.kind=? AND ` + visibility
	args := append([]any{r.Library, int(kindID)}, visibleArgs...)
	if r.Q != "" {
		where += ` AND e.title LIKE ? ESCAPE '\'`
		args = append(args, searchPrefix(r.Q))
	}
	total, _, err := s.libraryListeningTotal(r, spec.browseKind, int(kindID), where, args)
	if err != nil {
		return sec, err
	}
	sec.TotalCount = total
	// A restricted viewer with a published class walks the class's own rows
	// in title order (the browse row's visibility is still rechecked, for the
	// rows walked); otherwise the library's browse rows.
	walk, walkArgs, col := `catalog_browse_rows e INDEXED BY catalog_browse_title WHERE `, []any{}, "e"
	if restrictions := r.Viewer.EffectiveRestrictions(); restrictions.Active() {
		key, generation, ready, classErr := s.publishedVisibilityClass(r.Library, restrictions)
		if classErr != nil {
			return sec, classErr
		}
		if ready {
			walk = `compact_visibility_rows v INDEXED BY compact_visibility_page CROSS JOIN catalog_browse_rows e ON e.entity_id=v.entity_id
			 WHERE v.class_id=(SELECT id FROM compact_visibility_classes WHERE class_key=?) AND v.generation=? AND v.library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND v.kind=? AND `
			walkArgs, col = []any{key, generation, r.Library, int(kindID)}, "v"
		}
	}
	direction, op := "ASC", ">"
	if r.Direction == "desc" {
		direction, op = "DESC", "<"
	}
	order := col + `.sort_key COLLATE NOCASE ` + direction + `,` + col + `.entity_id ` + direction
	pageArgs, offset := append(walkArgs, args...), start
	query := `SELECT e.entity_id,e.sort_key FROM ` + walk + where + ` ORDER BY ` + order + ` LIMIT ? OFFSET ?`
	if keyed != "" {
		var k listeningKey
		if json.Unmarshal([]byte(keyed), &k) != nil || k.ID < 1 {
			return sec, ErrCursor
		}
		// Two limited seeks, the key's later ids and then later keys, so a
		// deep page does not test every earlier title against the cursor.
		arm := `SELECT e.entity_id AS id,e.sort_key AS sort_key,` + col + `.sort_key AS order_key FROM ` + walk + where
		query = `SELECT id,sort_key FROM (SELECT * FROM (` + arm + ` AND ` + col + `.sort_key COLLATE NOCASE=? AND ` + col + `.entity_id` + op + `? ORDER BY ` + col + `.entity_id ` + direction + ` LIMIT ?)
 UNION ALL SELECT * FROM (` + arm + ` AND ` + col + `.sort_key COLLATE NOCASE` + op + `? ORDER BY ` + order + ` LIMIT ?)) ORDER BY order_key COLLATE NOCASE ` + direction + `,id ` + direction + ` LIMIT ? OFFSET ?`
		bound := append(append([]any{}, pageArgs...), k.Key, k.ID, r.Limit+1)
		pageArgs = append(append(bound, pageArgs...), k.Key, r.Limit+1)
		offset = 0
	}
	pageArgs = append(pageArgs, r.Limit+1, offset)
	rows, err := s.read().Query(query, pageArgs...)
	if err != nil {
		return sec, err
	}
	var ids []int64
	var keys []string
	for rows.Next() {
		var id int64
		var key string
		if err = rows.Scan(&id, &key); err != nil {
			rows.Close()
			return sec, err
		}
		ids, keys = append(ids, id), append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return sec, err
	}
	more := len(ids) > r.Limit
	if more {
		ids, keys = ids[:r.Limit], keys[:r.Limit]
	}
	if len(ids) == 0 {
		return sec, nil
	}
	// The section's own projection supplies each row's display fields, for
	// exactly this page's ids.
	rawIDs, _ := json.Marshal(ids)
	hydrate := append(append([]any{}, spec.args...), string(rawIDs))
	found, err := s.read().Query(`SELECT id,title,subtitle,n,entity_id FROM (`+spec.query+`) projected WHERE projected.entity_id IN(SELECT value FROM json_each(?))`, hydrate...)
	if err != nil {
		return sec, err
	}
	byID := map[int64]ContentEntry{}
	for found.Next() {
		var v ContentEntry
		var n int
		var entityID int64
		if err = found.Scan(&v.ID, &v.Title, &v.Subtitle, &n, &entityID); err != nil {
			found.Close()
			return sec, err
		}
		v.Kind = spec.kind
		v.LibraryID = r.Library
		v.Navigation = &ContentNavigation{View: spec.view, EntityID: v.ID}
		if n > 0 {
			v.Count = &n
		}
		byID[entityID] = v
	}
	err = found.Err()
	found.Close()
	if err != nil {
		return sec, err
	}
	for _, id := range ids {
		if v, ok := byID[id]; ok {
			sec.Entries = append(sec.Entries, v)
		}
	}
	if more {
		raw, _ := json.Marshal(listeningKey{Key: keys[len(keys)-1], ID: ids[len(ids)-1]})
		sec.NextCursor = string(raw)
	}
	return sec, s.enrichListeningEntries(r, spec, &sec)
}

// libraryListeningTotal answers a library-wide section's size without
// counting the library: maintained browse totals for an unrestricted viewer,
// the published restriction class's totals for a restricted one, and an exact
// count for a title search (or while a new class is being built).
func (s *Service) libraryListeningTotal(r ContentRequest, kind string, kindID int, where string, args []any) (int, bool, error) {
	restrictions := r.Viewer.EffectiveRestrictions()
	if r.Q == "" && !restrictions.Active() {
		summary, err := s.browseSummarise(r.Library, []string{kind}, restrictions)
		if err != nil {
			return 0, false, err
		}
		if summary.exact {
			return summary.total, false, nil
		}
	}
	if r.Q == "" && restrictions.Active() {
		key, generation, ready, err := s.publishedVisibilityClass(r.Library, restrictions)
		if err != nil {
			return 0, false, err
		}
		if ready {
			_, classID, err := s.currentCategoryClass(r.Library, key, generation)
			if err != nil {
				return 0, false, err
			}
			var total int
			err = s.read().QueryRow(`SELECT COALESCE(sum(total),0) FROM compact_visibility_counts WHERE class_id=? AND generation=? AND library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND kind=?`, classID, generation, r.Library, kindID).Scan(&total)
			return total, false, err
		}
	}
	var total int
	err := s.read().QueryRow(`SELECT count(*) FROM catalog_browse_rows e INDEXED BY catalog_browse_title WHERE `+where, args...).Scan(&total)
	return total, false, err
}
