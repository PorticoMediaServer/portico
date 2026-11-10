package db

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
)

// Facets a list can be counted by.
const (
	FacetGenre      = "genre"
	FacetStudio     = "studio"
	FacetNetwork    = "network"
	FacetLabel      = "label"
	FacetDecade     = "decade"
	FacetRating     = "contentRating"
	FacetResolution = "resolution"
)

// FacetValue is one choice of a filter and how many items of the list it would leave.
type FacetValue struct {
	Value string // what to send back as the filter: a tag's public id, "1990", "PG-13", "4k"
	Label string
	Count int
}

var facetTagKinds = map[string]string{FacetGenre: TagGenre, FacetStudio: TagStudio, FacetNetwork: TagNetwork, FacetLabel: TagLabel}

// Facets counts the choices of one filter over the list q describes (its sort, cursor and
// limit are ignored). Unless the list is a whole library and kind it reads every row of that
// list, so a client asks for it when someone opens the filter, not with every page.
func (d *DB) Facets(ctx context.Context, v Viewer, q ItemQuery, facet string) ([]FacetValue, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	// A facet's own filter does not narrow it: with "Drama" chosen, the genre list still
	// shows what choosing "Comedy" instead would give.
	// A tag that is all that says which list this is stays: a genre's own page is still that
	// genre's list.
	placed := q.Library != "" || q.Parent != "" || q.Person != "" || q.Favorite != nil && *q.Favorite || q.InList != nil && *q.InList
	switch {
	case facet == FacetDecade:
		q.YearFrom, q.YearTo = 0, 0
	case facet == FacetResolution:
		q.Resolution = ""
	case facet == FacetGenre && placed:
		q.Genre = ""
	}
	if kind, ok := facetTagKinds[facet]; ok && q.Tag != "" && placed {
		var same bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM tags WHERE public_id = ? AND kind = ?)`, q.Tag, kind).Scan(&same); err != nil {
			return nil, err
		}
		if same {
			q.Tag = ""
		}
	}
	w := newQuery(v)
	if found, err := q.filters(ctx, tx, w, acc); err != nil || !found {
		return nil, err
	}
	from, where := w.countFrom, w.countWhere(true)
	var query string
	switch facet {
	case FacetGenre, FacetStudio, FacetNetwork, FacetLabel:
		// A tag counts once for an item that carries it or whose title does. Something under
		// a title counts for the title's tags unless it has the tag itself.
		under := len(q.Kinds) == 0 || slices.ContainsFunc(q.Kinds, func(k ItemKind) bool { return !k.TopLevel() })
		// byRow counts row by row: each row of the list costs a look in the tag table's key.
		// CROSS JOIN makes SQLite read the list and look each row's tags up, not read every
		// tag in the database and look each item up.
		byRow := func(sign, where string) string {
			counts := fmt.Sprintf(`SELECT it.tag_id AS tag, %scount(*) AS n FROM %s CROSS JOIN item_tags it ON it.item_id = i.id WHERE %s GROUP BY 1`, sign, from, where)
			if under {
				counts += fmt.Sprintf(` UNION ALL SELECT it.tag_id, %scount(*) FROM %s CROSS JOIN item_tags it ON it.item_id = i.root_id WHERE %s
					AND NOT EXISTS (SELECT 1 FROM item_tags o WHERE o.item_id = i.id AND o.tag_id = it.tag_id) GROUP BY 1`, sign, from, where)
			}
			return counts
		}
		counts := byRow("", where)
		if w.whole {
			// A whole library and kind reads no row of the list. For each tag of the facet's
			// kind, the items that carry it are counted from item_tags' own index, and what
			// sits under a title that carries it from the index of a title's descendants.
			// Both indexes hold each item's rating, so a rating limit is checked there too.
			// What a blocked label hides is few: those rows are counted one by one and
			// taken away.
			own := fmt.Sprintf(`(SELECT count(*) FROM item_tags it WHERE it.library_id = %d AND it.kind IN %s AND it.tag_id = t.id%s)`,
				w.library, w.kinds, acc.rated("it", ""))
			n := own
			if under {
				// What is under a title that carries the tag counts too, unless it carries the
				// tag itself and was counted already. When nothing of these kinds carries the
				// tag itself, as with episodes, that needs no look at each one: the run of
				// each title's descendants in the index is simply counted.
				below := fmt.Sprintf(`SELECT count(*) FROM item_tags h CROSS JOIN items d ON d.root_id = h.item_id
					WHERE h.tag_id = t.id AND h.library_id = %d AND d.kind IN %s%s`, w.library, w.kinds, acc.rated("d", ""))
				tagged := fmt.Sprintf(`EXISTS (SELECT 1 FROM item_tags it WHERE it.library_id = %d AND it.kind IN %s AND it.tag_id = t.id)`, w.library, w.kinds)
				n = fmt.Sprintf(`%s + CASE WHEN %s THEN (%s AND NOT EXISTS (SELECT 1 FROM item_tags o WHERE o.item_id = d.id AND o.tag_id = h.tag_id))
					ELSE (%s) END`, own, tagged, below, below)
			}
			counts = fmt.Sprintf(`SELECT t.id AS tag, %s AS n FROM tags t WHERE t.kind = '%s'`, n, facetTagKinds[facet])
			if len(acc.blockedTags) > 0 {
				counts += ` UNION ALL ` + byRow("-", `i.id IN (`+acc.blockedIDs(w.library, w.kinds)+`)`+acc.rated("i", ""))
			}
		}
		// The tags are named only after counting, and at most 200 are returned. MATERIALIZED
		// has the counting done once; left to itself SQLite folds it into the outer statement
		// and counts again for the HAVING and for the ORDER BY.
		query = fmt.Sprintf(`WITH c(tag, n) AS MATERIALIZED (%s) SELECT t.public_id, t.name, sum(c.n) FROM c
			JOIN tags t ON t.id = c.tag AND t.kind = '%s'
			GROUP BY t.id HAVING sum(c.n) > 0 ORDER BY 3 DESC, t.norm_name LIMIT 200`, counts, facetTagKinds[facet])
	case FacetDecade:
		query = fmt.Sprintf(`SELECT i.year / 10 * 10, (i.year / 10 * 10) || 's', count(*) FROM %s
			WHERE %s AND i.year IS NOT NULL GROUP BY 1 ORDER BY 1 DESC`, from, where)
	case FacetRating:
		query = fmt.Sprintf(`SELECT i.content_rating, i.content_rating, count(*) FROM %s
			WHERE %s AND i.content_rating IS NOT NULL GROUP BY 1 ORDER BY 3 DESC, 1`, from, where)
	case FacetResolution:
		query = fmt.Sprintf(`SELECT CASE class WHEN 4 THEN '4k' WHEN 3 THEN '1080p' WHEN 2 THEN '720p' ELSE 'sd' END, '', count(*) FROM (
			SELECT i.id, max(%s) AS class FROM %s JOIN media m ON m.item_id = i.id AND m.width IS NOT NULL WHERE %s GROUP BY i.id)
			GROUP BY class ORDER BY class DESC`, resolutionSQL, from, where)
	default:
		return nil, invalid("unknown facet %q", facet)
	}
	return queryAll(ctx, tx, query, w.args, func(f *FacetValue) []any { return []any{&f.Value, &f.Label, &f.Count} })
}

// Related lists the items tied to one item by a relation, such as the films of a collection or
// the extras of a film, in the relation's own order. Only items the viewer may see are listed.
func (d *DB) Related(ctx context.Context, v Viewer, itemID string) (map[RelationKind][]Item, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	var id int64
	args := append(v.args(), sql.Named("id", itemID))
	err = tx.QueryRowContext(ctx, `SELECT i.id FROM items i WHERE i.public_id = ? AND `+acc.visible("i"), itemID).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.kind, `+itemColumns+` FROM item_relations r CROSS JOIN items i ON i.id = r.related_item_id
		LEFT JOIN user_item_state s ON s.profile_id = @viewer_profile AND s.item_id = i.id
		WHERE r.item_id = (SELECT id FROM items WHERE public_id = @id) AND `+acc.visible("i")+` ORDER BY r.kind, r.position, i.id LIMIT 500`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var kinds []RelationKind
	var items []Item
	for rows.Next() {
		var kind RelationKind
		var it Item
		dest := append([]any{&kind}, it.dest()...)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		kinds, items = append(kinds, kind), append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := loadImages(ctx, tx, items); err != nil {
		return nil, err
	}
	out := map[RelationKind][]Item{}
	for n, it := range items {
		out[kinds[n]] = append(out[kinds[n]], it)
	}
	return out, nil
}
