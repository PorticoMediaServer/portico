package db

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
)

// The genres of the films and shows a profile may see, across every library it may see, for
// Search before anything is typed. Each is found, and given its cover, by looking at no more
// than genreCandidates of its titles in index order, so the read costs the same whatever the
// size of the library. Counting is what grows with the library, so it is asked for apart.
const (
	maxGenres       = 200
	genreCandidates = 50
)

// Genre is one genre the viewer may see: its public id and name, its cover (the backdrop of
// the first of its titles the viewer may see that has one: the same title on every read until
// the library changes), and with counts how many films and shows the viewer may see carry it.
type Genre struct {
	PublicID, Name string
	CoverHash      string
	Count          int
}

// Genres lists the genres of the films and shows the viewer may see, by name, at most 200.
func (d *DB) Genres(ctx context.Context, v Viewer, counts bool) ([]Genre, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	// Every library the viewer may see, whatever its kind: the titles are picked by their own
	// kind below, so a library of any kind that holds films or shows (anime does) is taken in,
	// and one that holds none costs one empty look in the index.
	libraries, err := ids(ctx, tx, `SELECT l.id FROM libraries l WHERE `+acc.libraryVisible("l.id"))
	if err != nil || len(libraries) == 0 {
		return nil, err
	}
	in, kinds := intList(libraries), fmt.Sprintf("(%d, %d)", KindMovie, KindShow)
	// The first candidates of a genre, in the order of item_tags_library: by library, kind,
	// rating and item. With a rating limit the index skips what is above it.
	candidates := fmt.Sprintf(`SELECT x.item_id FROM item_tags x INDEXED BY item_tags_library WHERE x.library_id IN %s AND x.kind IN %s
		AND x.tag_id = t.id%s ORDER BY x.library_id, x.kind, x.rating_age, x.item_id LIMIT %d`, in, kinds, acc.rated("x", ""), genreCandidates)
	genres, err := queryAll(ctx, tx, fmt.Sprintf(`SELECT t.id, t.public_id, t.name, ifnull((SELECT im.file_hash FROM (%s) c
			CROSS JOIN items i ON i.id = c.item_id CROSS JOIN images im ON im.item_id = i.id AND im.role = %d AND im.selected = 1
			WHERE im.file_hash IS NOT NULL AND %s LIMIT 1), '') FROM tags t WHERE t.kind = '%s'
		AND EXISTS (SELECT 1 FROM (%s) c CROSS JOIN items i ON i.id = c.item_id WHERE %s) ORDER BY t.norm_name LIMIT %d`,
		candidates, ImageBackdrop, acc.visible("i"), TagGenre, candidates, acc.visible("i"), maxGenres), v.args(),
		func(g *struct {
			id int64
			Genre
		}) []any {
			return []any{&g.id, &g.PublicID, &g.Name, &g.CoverHash}
		})
	if err != nil || len(genres) == 0 {
		return nil, err
	}
	out := make([]Genre, len(genres))
	place := map[int64]int{}
	list := make([]int64, len(genres))
	for n, g := range genres {
		out[n], place[g.id], list[n] = g.Genre, n, g.id
	}
	if !counts {
		return out, nil
	}
	// Counted from the index: the films and shows within the rating limit, less those a
	// blocked label hides and the shows left with nothing under them the viewer may see.
	n := fmt.Sprintf(`SELECT t.id, (SELECT count(*) FROM item_tags it WHERE it.tag_id = t.id AND it.library_id IN %s AND it.kind IN %s%s)
		FROM tags t WHERE t.id IN %s`, in, kinds, acc.rated("it", ""), intList(list))
	// What is taken away is read from item_tags alone, by its key: the row carries the item's
	// library, kind and rating. Each "+" keeps SQLite from reading every film's and show's tags
	// through another index instead.
	less := func(items []int64) string {
		return fmt.Sprintf(` UNION ALL SELECT it.tag_id, -count(*) FROM item_tags it WHERE it.item_id IN %s AND +it.tag_id IN %s
			AND +it.library_id IN %s AND +it.kind IN %s%s GROUP BY 1`, intList(items), intList(list), in, kinds, acc.rated("it", "+"))
	}
	// Both sets are only what the profile is kept from, so they are small. Each is read once and
	// named by id: inside the count, SQLite would work them out again.
	var blocked []int64
	if len(acc.blockedTags) > 0 {
		if blocked, err = ids(ctx, tx, acc.blockedIDs(0, kinds), v.args()...); err != nil {
			return nil, err
		}
		if len(blocked) > 0 {
			n += less(blocked)
		}
	}
	// Each such show once: one a blocked label covers was taken away just above.
	if set := acc.emptiedSet(0, fmt.Sprintf("(%d)", KindShow)); set != "" {
		emptied, err := ids(ctx, tx, set, v.args()...)
		if err != nil {
			return nil, err
		}
		emptied = slices.DeleteFunc(emptied, func(id int64) bool { return slices.Contains(blocked, id) })
		if len(emptied) > 0 {
			n += less(emptied)
		}
	}
	type count struct {
		tag int64
		n   int
	}
	sums, err := queryAll(ctx, tx, `WITH c(tag, n) AS MATERIALIZED (`+n+`) SELECT tag, sum(n) FROM c GROUP BY tag`, v.args(),
		func(c *count) []any { return []any{&c.tag, &c.n} })
	for _, c := range sums {
		out[place[c.tag]].Count = c.n
	}
	return out, err
}
