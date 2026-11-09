package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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
// limit are ignored). It reads every row of that list, so a client asks for it when someone
// opens the filter, not with every page.
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
	w := newQuery(v)
	if found, err := q.filters(ctx, tx, w, acc); err != nil || !found {
		return nil, err
	}
	from, where := w.countFrom, w.countWhere()
	var query string
	switch facet {
	case FacetGenre, FacetStudio, FacetNetwork, FacetLabel:
		// A tag counts once for an item that carries it or whose title does. The item's own
		// tags and its title's are read separately, each through the tag table's key.
		pairs := `SELECT i.id AS item, it.tag_id AS tag FROM %[1]s JOIN item_tags it ON it.item_id = %[3]s WHERE %[2]s`
		query = fmt.Sprintf(`SELECT t.public_id, t.name, count(*) FROM (`+pairs+` UNION `+pairs+`) pair
			JOIN tags t ON t.id = pair.tag AND t.kind = '%[4]s' GROUP BY t.id ORDER BY 3 DESC, t.norm_name LIMIT 200`,
			from, where, "{id}", facetTagKinds[facet])
		query = strings.Replace(strings.Replace(query, "{id}", "i.id", 1), "{id}", "i.root_id", 1)
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
