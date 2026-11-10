package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
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
	// FacetLetter is the letter rail: the first letter of every title in the list, in title
	// order, "#" for digits and symbols, each with how many titles begin with it. A letter's
	// position in the list sorted by title is the sum of the counts before it.
	FacetLetter = "letter"
)

// maxLetters bounds the letter rail of a library in a script with thousands of first characters.
const maxLetters = 500

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
	case FacetLetter:
		return letters(ctx, tx, w)
	case FacetGenre, FacetStudio, FacetNetwork, FacetLabel:
		// A tag counts once for an item that carries it or whose title does. Each list row
		// costs a look in the tag table's key, so this reads in proportion to the list; the
		// tags are named only after counting, and at most 200 are returned.
		// CROSS JOIN makes SQLite read the list and look each row's tags up, not read every
		// tag in the database and look each item up.
		counts := fmt.Sprintf(`SELECT it.tag_id AS tag, count(*) AS n FROM %s CROSS JOIN item_tags it ON it.item_id = i.id WHERE %s GROUP BY 1`, from, where)
		if w.plainKinds != "" && len(acc.blockedTags) == 0 {
			// A whole library and kind: the tags are counted from item_tags' own index.
			counts = fmt.Sprintf(`SELECT it.tag_id AS tag, count(*) AS n FROM item_tags it WHERE it.library_id = %d AND it.kind IN %s GROUP BY 1`,
				w.plainLibrary, w.plainKinds)
		}
		if len(q.Kinds) == 0 || slices.ContainsFunc(q.Kinds, func(k ItemKind) bool { return !k.TopLevel() }) {
			// Something under a title also counts for the title's tags, unless it has the tag itself.
			counts += fmt.Sprintf(` UNION ALL SELECT it.tag_id, count(*) FROM %s CROSS JOIN item_tags it ON it.item_id = i.root_id WHERE %s
				AND NOT EXISTS (SELECT 1 FROM item_tags o WHERE o.item_id = i.id AND o.tag_id = it.tag_id) GROUP BY 1`, from, where)
		}
		query = fmt.Sprintf(`SELECT t.public_id, t.name, sum(c.n) FROM (%s) c JOIN tags t ON t.id = c.tag AND t.kind = '%s'
			GROUP BY t.id ORDER BY 3 DESC, t.norm_name LIMIT 200`, counts, facetTagKinds[facet])
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

// letters reads the letter rail of a list. It never sorts or groups the list: it asks the
// title index for the first title at or after a point, counts the titles that share its first
// character, and moves to the next character, so it reads each index entry once.
func letters(ctx context.Context, tx *sql.Tx, w *query) ([]FacetValue, error) {
	var out []FacetValue
	args := append(append([]any(nil), w.args...), nil, nil)
	from, until := &args[len(args)-2], &args[len(args)-1]
	*from = sql.Named("letter_from", "")
	for len(out) < maxLetters {
		var first string
		err := tx.QueryRowContext(ctx, `SELECT i.sort_title FROM `+w.from+` WHERE `+w.pageWhere()+` AND i.sort_title >= @letter_from
			ORDER BY i.sort_title LIMIT 1`, args[:len(args)-1]...).Scan(&first)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return nil, err
		}
		// The end of this character's titles is the next character. A title with no sort
		// text at all is counted with the symbols.
		label, next := "#", "\x00"
		for _, r := range first {
			label, next = sorttext.Letter(string(r)), string(r+1)
			if r+1 >= 0xD800 && r+1 <= 0xDFFF {
				next = "\uE000"
			}
			break
		}
		*from, *until = sql.Named("letter_from", first), sql.Named("letter_until", next)
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+w.from+` WHERE `+w.pageWhere()+` AND i.sort_title >= @letter_from
			AND i.sort_title < @letter_until`, args...).Scan(&count); err != nil {
			return nil, err
		}
		if n := len(out); n > 0 && out[n-1].Value == label {
			out[n-1].Count += count // digits and symbols are several characters and one "#"
		} else {
			out = append(out, FacetValue{Value: label, Count: count})
		}
		*from = sql.Named("letter_from", next)
	}
	return out, nil
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
	if err := loadLeafCounts(ctx, tx, v, acc, items); err != nil {
		return nil, err
	}
	out := map[RelationKind][]Item{}
	for n, it := range items {
		out[kinds[n]] = append(out[kinds[n]], it)
	}
	return out, nil
}
