package db

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"
)

// The title data's provider codes each film or show id is looked up under, in the order tried:
// an item's TMDB id is a film id for a film and a TV id for a show.
var titleDataCodes = map[ItemKind][]struct {
	code     int
	provider string // item_external_ids.provider
}{
	KindMovie: {{1, "tmdb"}, {4, "tvdb"}, {5, "anilist"}, {6, "imdb"}},
	KindShow:  {{2, "tmdb"}, {3, "tvdb"}, {5, "anilist"}, {6, "imdb"}},
}

// A title gets the title data's tags that are a defining feature of it or central to it
// (strength 2 or 3; the data set's own guidance is that 1 is a soft hint) and that make a row
// (the vocabulary gives them a row label). Nothing else reads the others.
const titleDataTags = `SELECT v.tag FROM work_tags w CROSS JOIN vocabulary v ON v.id = w.tag
	WHERE w.qid = ? AND w.strength >= 2 AND v.retired_in IS NULL AND v.row_label IS NOT NULL ORDER BY v.tag`

// titleDataWork finds the title data's work for a film or show by the ids its providers gave
// it, or 0. Matching is by id only, never by name.
func titleDataWork(ctx context.Context, td *titleData, kind ItemKind, ids map[string]string) (int64, error) {
	for _, c := range titleDataCodes[kind] {
		id, ok := ids[c.provider]
		if !ok {
			continue
		}
		var value any = id
		if c.provider != "imdb" {
			n, err := strconv.ParseInt(id, 10, 64)
			if err != nil {
				continue
			}
			value = n // the file keeps numeric ids as integers
		}
		var qid int64
		err := td.conn.QueryRowContext(ctx, `SELECT qid FROM work_ids WHERE provider = ? AND provider_id = ? ORDER BY qid LIMIT 1`,
			c.code, value).Scan(&qid)
		if err == nil {
			return qid, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	return 0, nil
}

// ApplyTitleData gives the films and shows after the item id after their mood tags from the
// title data in use, up to limit titles, and answers where to carry on, 0 at the end. Run from
// 0 to the end it covers every film and show: it is run after a new file is installed, and
// again daily for titles matched since. A title whose ids no longer match, or with no file in
// use, loses its mood tags. It writes only what changed, at Background priority, one batch per
// transaction; reading the file happens before the write starts.
func (d *DB) ApplyTitleData(ctx context.Context, after int64, limit int) (int64, error) {
	type title struct {
		id   int64
		kind ItemKind
		ids  map[string]string
		have []string
		want []string
	}
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// Walked by id, not through the parent index, which would read every top-level title of
	// every kind and sort them for each step: one pass reads the table once.
	titles, err := queryAll(ctx, tx, `SELECT id, kind FROM items WHERE id > ? AND kind IN (?, ?) AND +parent_id IS NULL ORDER BY id LIMIT ?`,
		[]any{after, KindMovie, KindShow, limit}, func(t *title) []any { return []any{&t.id, &t.kind} })
	if err != nil || len(titles) == 0 {
		return 0, err
	}
	byID := map[int64]*title{}
	list := make([]int64, len(titles))
	for n := range titles {
		titles[n].ids = map[string]string{}
		byID[titles[n].id], list[n] = &titles[n], titles[n].id
	}
	type pair struct {
		item        int64
		first, more string
	}
	ext, err := queryAll(ctx, tx, `SELECT item_id, provider, external_id FROM item_external_ids WHERE item_id IN `+intList(list),
		nil, func(p *pair) []any { return []any{&p.item, &p.first, &p.more} })
	if err != nil {
		return 0, err
	}
	for _, p := range ext {
		byID[p.item].ids[p.first] = p.more
	}
	have, err := queryAll(ctx, tx, `SELECT it.item_id, t.name, '' FROM item_tags it CROSS JOIN tags t ON t.id = it.tag_id
		WHERE it.item_id IN `+intList(list)+` AND t.kind = 'mood'`, nil, func(p *pair) []any { return []any{&p.item, &p.first, &p.more} })
	if err != nil {
		return 0, err
	}
	tx.Rollback()
	for _, p := range have {
		byID[p.item].have = append(byID[p.item].have, p.first)
	}
	if td := d.titleData.Load(); td != nil {
		for n := range titles {
			t := &titles[n]
			qid, err := titleDataWork(ctx, td, t.kind, t.ids)
			if err != nil {
				return 0, err
			}
			if qid != 0 {
				if t.want, err = td.strings(ctx, titleDataTags, qid); err != nil {
					return 0, err
				}
			}
		}
	}
	var changed []*title
	for n := range titles {
		slices.Sort(titles[n].have)
		if !slices.Equal(titles[n].have, titles[n].want) {
			changed = append(changed, &titles[n])
		}
	}
	next := titles[len(titles)-1].id
	if len(changed) == 0 {
		return next, nil
	}
	return next, d.write(ctx, Background, func(tx *sql.Tx) error {
		tags := map[string]int64{}
		for _, t := range changed {
			if _, err := tx.ExecContext(ctx, `DELETE FROM item_tags WHERE item_id = ? AND tag_id IN (SELECT id FROM tags WHERE kind = 'mood')`,
				t.id); err != nil {
				return err
			}
			for _, name := range t.want {
				id, ok := tags[name]
				if !ok {
					if _, err := tx.ExecContext(ctx, `INSERT INTO tags (public_id, kind, name, norm_name) VALUES (?, 'mood', ?, ?)
						ON CONFLICT (kind, norm_name) DO NOTHING`, NewID("tag"), name, normTag(name)); err != nil {
						return err
					}
					if err := tx.QueryRowContext(ctx, `SELECT id FROM tags WHERE kind = 'mood' AND norm_name = ?`, normTag(name)).Scan(&id); err != nil {
						return err
					}
					tags[name] = id
				}
				// The title may have been deleted since it was read.
				if _, err := tx.ExecContext(ctx, `INSERT INTO item_tags (item_id, tag_id) SELECT ?, ? WHERE EXISTS (SELECT 1 FROM items WHERE id = ?)
					ON CONFLICT DO NOTHING`, t.id, id, t.id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// RemoveUnusedTitleDataTags deletes mood tags no title carries any more: a tag the data set
// retired, or every one once the file is gone. Mood tags are a few hundred rows.
func (d *DB) RemoveUnusedTitleDataTags(ctx context.Context) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM tags WHERE kind = 'mood' AND NOT EXISTS (SELECT 1 FROM item_tags WHERE tag_id = tags.id)`)
		return err
	})
}

// RemoveTitleDataTags takes mood tags off up to limit titles, for when title data is switched
// off, and answers whether any are left.
func (d *DB) RemoveTitleDataTags(ctx context.Context, limit int) (bool, error) {
	var left bool
	err := d.write(ctx, Background, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM item_tags WHERE (item_id, tag_id) IN
			(SELECT it.item_id, it.tag_id FROM tags t CROSS JOIN item_tags it ON it.tag_id = t.id WHERE t.kind = 'mood' LIMIT ?)`, limit); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM tags t CROSS JOIN item_tags it ON it.tag_id = t.id WHERE t.kind = 'mood')`).
			Scan(&left)
	})
	return left, err
}

// strings answers a one-column read of the file.
func (td *titleData) strings(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := td.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
