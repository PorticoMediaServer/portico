package catalog

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/facettext"
)

// searchWalk is an ordered index a sorted search reads in its own order: the
// key it orders by (as the ranked path computes it), the index's own order
// and bound column, the row identity (the entity id, the tie break of both
// paths) and the library and kind scope.
type searchWalk struct {
	from, scope, kind, key, order, bound, id string
	numeric                                  bool
}

// searchWalks are the sorts a search can answer from an ordered index rather
// than by ranking every match: title from the title index, release year and
// date added from the browse rows' own indexes.
var searchWalks = map[string]searchWalk{
	"title": {from: `catalog_entities t INDEXED BY catalog_entities_title`, scope: "t.library_id", kind: "t.kind", key: "t.sort_key", order: "t.sort_key", bound: "t.sort_key", id: "t.id"},
	"releaseYear": {from: `catalog_browse_rows wb INDEXED BY catalog_browse_year`, scope: "wb.library_id", kind: "wb.kind",
		key: "wb.year", order: "wb.year", bound: "wb.year", id: "wb.entity_id", numeric: true},
	"dateAdded": {from: `catalog_browse_rows wb INDEXED BY catalog_browse_added`, scope: "wb.library_id", kind: "wb.kind",
		key: "COALESCE(wb.added_text,'')", order: "COALESCE(wb.added_text,'')", bound: "COALESCE(wb.added_text,'')", id: "wb.entity_id"},
}

// walkOrd is the order value a walk's row carries, as the ranked path
// computes it. The window selects the index's own column: a computed column
// stops SQLite merging a serving view's two indexed runs.
func walkOrd(walk searchWalk) string {
	if walk.numeric {
		return "CAST(w.k AS REAL)"
	}
	return "w.k"
}

// titleWalkWindow is how many titles of one library a title-sorted search
// reads in title order before it gives up and ranks the full-text matches.
var titleWalkWindow = 4096

// searchTitleWalk answers a broad title-sorted search from the title index:
// each authorized library's titles are read in title order, at most
// titleWalkWindow of them, and each is tested against the typed words'
// prefix postings. A word that matches a large share of the library fills the
// page within a few dozen titles, where ranking every full-text match cost
// the whole match set. ok is false when a library's window held neither a
// full page nor its last title, or when the words are rare enough that the
// full-text matches are cheaper; the caller then ranks the matches.
//
// The result is a JSON list of [entity id, public id, sort key] rows, the
// union of each library's first `limit` matches after the cursor, which holds
// the overall first `limit`.
func (s *Service) searchTitleWalk(ctx context.Context, r SearchRequest, kind int, sortID, where string, filterArgs []any, limit int, scopeHash string, g searchGroupDefinition, rev ContentRevision) (string, bool, error) {
	walk, ok := searchWalks[sortID]
	if !ok {
		return "", false, nil
	}
	terms, indexed := facettext.IndexedSearchTerms(r.Q)
	if !indexed || len(terms) == 0 || len(r.Libraries) == 0 || len(r.Libraries) > 64 {
		return "", false, nil
	}
	// Rare words: their matches are fewer than the window would read, so the
	// rarest word's postings are the candidates (ordered by the sort's key).
	raw, _ := json.Marshal(r.Libraries)
	rare, rareCount := int64(0), -1
	for _, term := range terms {
		ids, err := s.prefixTermIDs(ctx, term, kind, r.Libraries)
		if err != nil {
			return "", false, err
		}
		if len(ids) == 0 {
			return "[]", true, nil // a word no title carries: nothing matches
		}
		list, _ := json.Marshal(ids)
		var count int
		if err = s.read().QueryRowContext(ctx, `SELECT COALESCE(sum(total),0) FROM catalog_search_prefix_counts WHERE term_id IN(SELECT value FROM json_each(?)) AND kind=? AND container=-1 AND library_id IN(SELECT id FROM catalog_libraries WHERE retired=0 AND library_id IN(SELECT value FROM json_each(?)))`, string(list), kind, string(raw)).Scan(&count); err != nil {
			return "", false, err
		}
		if count < titleWalkWindow/4 && (rareCount < 0 || count < rareCount) {
			rare, rareCount = ids[0], count
		}
	}
	direction, op := "ASC", ">"
	if r.Direction == "desc" {
		direction, op = "DESC", "<"
	}
	bound, boundArgs := "", []any{}
	if r.Cursor != "" {
		c, err := s.decodeRevisionCursor(r.Cursor, s.groupCursorScope(r, g, scopeHash, sortID), rev)
		if err != nil {
			return "", false, nil // the ranked path reports the cursor
		}
		id, err := strconv.ParseInt(c.ID, 10, 64)
		if err != nil {
			return "", false, nil
		}
		value, err := cursorOrderValue(c.Value, walk.numeric)
		if err != nil {
			return "", false, nil
		}
		bound, boundArgs = ` AND (`+walk.bound+`,`+walk.id+`)`+op+`(?,?)`, []any{value, id}
	}
	if rareCount >= 0 {
		return s.searchPostingsWalk(ctx, r, kind, sortID, walk, rare, terms, where, filterArgs, limit, scopeHash, g, rev)
	}
	probes, probeArgs := []string{}, []any{}
	for _, term := range terms {
		probeArgs = append(probeArgs, term)
		probes = append(probes, `EXISTS(SELECT 1 FROM catalog_search_prefix_rows p INDEXED BY catalog_search_prefix_rows_entity WHERE p.entity_id=e.id AND p.term_id=(SELECT id FROM catalog_search_prefix_terms WHERE prefix=?))`)
	}
	// The window is a bounded run of the ordered index after the cursor, read
	// on its own (a serving view merges its two indexed runs only outside a
	// join); the probes then test those rows, and the page is their first
	// matches in the window's order.
	window := `SELECT ` + walk.id + ` AS id,` + walk.key + ` AS k FROM ` + walk.from + ` WHERE ` + walk.scope + `=(SELECT id FROM catalog_libraries WHERE library_id=?) AND ` + walk.kind + `=?` + bound +
		` ORDER BY ` + walk.order + ` ` + direction + `,` + walk.id + ` ` + direction + ` LIMIT ` + strconv.Itoa(titleWalkWindow)
	query := `WITH w AS MATERIALIZED (` + window + `)
 SELECT e.id,d.entity_pid,` + walkOrd(walk) + ` FROM w CROSS JOIN catalog_entities e ON e.id=w.id
 CROSS JOIN catalog_search_documents d ON d.entity_id=e.id JOIN catalog_libraries l ON l.id=e.library_id
 WHERE ` + strings.Join(probes, " AND ") + ` AND 1` + where + ` ORDER BY w.k ` + direction + `,w.id ` + direction + ` LIMIT ?`
	rows := [][3]any{}
	for _, library := range r.Libraries {
		args := append([]any{library, kind}, boundArgs...)
		args = append(args, probeArgs...)
		args = append(args, filterArgs...)
		args = append(args, limit)
		found, err := s.titleWalkRows(ctx, query, args)
		if err != nil {
			return "", false, err
		}
		if len(found) < limit {
			var read int
			if err = s.read().QueryRowContext(ctx, `SELECT count(*) FROM (`+window+`)`, append([]any{library, kind}, boundArgs...)...).Scan(&read); err != nil {
				return "", false, err
			}
			if read == titleWalkWindow {
				return "", false, nil // the window held neither a page nor the end
			}
		}
		rows = append(rows, found...)
	}
	out, _ := json.Marshal(rows)
	return string(out), true, nil
}

func (s *Service) titleWalkRows(ctx context.Context, query string, args []any) ([][3]any, error) {
	rows, err := s.read().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := [][3]any{}
	for rows.Next() {
		var id int64
		var public string
		var key any
		if err = rows.Scan(&id, &public, &key); err != nil {
			return nil, err
		}
		out = append(out, [3]any{id, public, key})
	}
	return out, rows.Err()
}

// searchPostingsWalk answers a sorted search whose rarest word has fewer
// matches than a walk window from that word's postings: those titles, each
// tested for the other words, ordered by the sort's key. Its cost is the
// rare word's matches, never the full-text match set of the broad words.
func (s *Service) searchPostingsWalk(ctx context.Context, r SearchRequest, kind int, sortID string, walk searchWalk, rare int64, terms []string, where string, filterArgs []any, limit int, scopeHash string, g searchGroupDefinition, rev ContentRevision) (string, bool, error) {
	ord := "e.sort_key"
	join := ""
	if sortID == "releaseYear" || sortID == "dateAdded" {
		ord = catalogOrder(sortID, g.kind, r.Q).expr
	}
	direction, op := "ASC", ">"
	if r.Direction == "desc" {
		direction, op = "DESC", "<"
	}
	raw, _ := json.Marshal(r.Libraries)
	args := []any{rare, kind, string(raw)}
	probes := ""
	for _, term := range terms {
		probes += ` AND EXISTS(SELECT 1 FROM catalog_search_prefix_rows other INDEXED BY catalog_search_prefix_rows_entity WHERE other.entity_id=e.id AND other.term_id=(SELECT id FROM catalog_search_prefix_terms WHERE prefix=?))`
		args = append(args, term)
	}
	args = append(args, filterArgs...)
	bound := ""
	if r.Cursor != "" {
		c, err := s.decodeRevisionCursor(r.Cursor, s.groupCursorScope(r, g, scopeHash, sortID), rev)
		if err != nil {
			return "", false, nil // the ranked path reports the cursor
		}
		id, err := strconv.ParseInt(c.ID, 10, 64)
		if err != nil {
			return "", false, nil
		}
		value, err := cursorOrderValue(c.Value, walk.numeric)
		if err != nil {
			return "", false, nil
		}
		bound = ` AND (` + ord + `,e.id)` + op + `(?,?)`
		args = append(args, value, id)
	}
	args = append(args, limit)
	found, err := s.titleWalkRows(ctx, `SELECT e.id,d.entity_pid,`+ord+` FROM catalog_search_prefix_rows p CROSS JOIN catalog_entities e ON e.id=p.entity_id
 CROSS JOIN catalog_search_documents d ON d.entity_id=e.id JOIN catalog_libraries l ON l.id=e.library_id`+join+`
 WHERE p.term_id=? AND p.kind=? AND p.library_id IN(SELECT id FROM catalog_libraries WHERE library_id IN(SELECT value FROM json_each(?)))`+probes+` AND 1`+where+bound+`
 ORDER BY `+ord+` `+direction+`,e.id `+direction+` LIMIT ?`, args)
	if err != nil {
		return "", false, err
	}
	out, _ := json.Marshal(found)
	return string(out), true, nil
}
