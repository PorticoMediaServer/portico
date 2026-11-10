package db

import (
	"context"
	"database/sql"
	"errors"
)

// What a queue can be built from. Each function returns the playable titles the viewer may
// see, by their internal ids, in the order they play.

// PlayableUnder returns the titles under a show, season, album, artist or audiobook in their
// own order, or the title itself when it is playable. More than a queue holds is ErrQueueFull.
func (d *DB) PlayableUnder(ctx context.Context, v Viewer, holderPublicID string) ([]int64, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	var holder int64
	var kind ItemKind
	err = tx.QueryRowContext(ctx, `SELECT i.id, i.kind FROM items i WHERE i.public_id = @id AND `+acc.visible("i"),
		append(v.args(), sql.Named("id", holderPublicID))...).Scan(&holder, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if kind.Playable() {
		return []int64{holder}, nil
	}
	if kind == KindCollection {
		// A collection's films in its own order. A show in a collection is not played from it.
		out, err := ids(ctx, tx, `SELECT c.id FROM item_relations r CROSS JOIN items c ON c.id = r.related_item_id
			WHERE r.item_id = @holder AND r.kind = `+itoa(int64(RelationCollectionMember))+` AND c.kind IN `+playableKindsSQL+` AND `+acc.visible("c")+`
			ORDER BY r.position, c.id LIMIT `+itoa(int64(QueueMax)+1), append(v.args(), sql.Named("holder", holder))...)
		if err == nil && len(out) > QueueMax {
			err = ErrQueueFull
		}
		return out, err
	}
	// An artist's songs play album by album, oldest first; everything else by season or
	// disc number and then episode or track number.
	order := `ifnull(c.parent_index, 0), ifnull(c.index_number, 0), c.sort_title, c.id`
	if kind == KindArtist {
		order = `ifnull(p.year, 0), ifnull(p.sort_title, ''), ifnull(p.id, 0), ` + order
	}
	out, err := ids(ctx, tx, `SELECT c.id FROM items c LEFT JOIN items p ON p.id = c.parent_id
		WHERE (c.root_id = @holder OR c.parent_id = @holder) AND c.kind IN `+playableKindsSQL+` AND `+acc.visible("c")+`
		ORDER BY `+order+` LIMIT `+itoa(int64(QueueMax)+1),
		append(v.args(), sql.Named("holder", holder))...)
	if err == nil && len(out) > QueueMax {
		err = ErrQueueFull
	}
	return out, err
}

// PlayableItems returns those of the named titles the viewer may see and can play, in the
// order they were named.
func (d *DB) PlayableItems(ctx context.Context, v Viewer, publicIDs []string) ([]int64, error) {
	if len(publicIDs) == 0 {
		return nil, nil
	}
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	find, err := tx.PrepareContext(ctx, `SELECT i.id FROM items i WHERE i.public_id = @id AND i.kind IN `+playableKindsSQL+` AND `+acc.visible("i"))
	if err != nil {
		return nil, err
	}
	defer find.Close()
	var out []int64
	for _, publicID := range publicIDs {
		var id int64
		err := find.QueryRowContext(ctx, append(v.args(), sql.Named("id", publicID))...).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

// ListItems returns what a playlist or collection holds, in its own order: one the profile
// made, one shared with it, or a library's collection. An entry that is not itself playable
// (a show in a collection) is left out. after and limit page through a long list by entry.
func (d *DB) ListItems(ctx context.Context, v Viewer, listPublicID string, after int64, limit int) (items []int64, last int64, err error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, 0, err
	}
	var list int64
	err = tx.QueryRowContext(ctx, `SELECT l.id FROM lists l WHERE l.public_id = ?1 AND l.kind IN ('playlist', 'collection') AND
		(l.owner_profile_id = ?2 OR EXISTS (SELECT 1 FROM list_shares s WHERE s.list_id = l.id AND s.profile_id = ?2)
		 OR (l.owner_profile_id IS NULL AND `+acc.libraryVisible("l.library_id")+`))`, listPublicID, v.ProfileID).Scan(&list)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT e.position, i.id FROM list_entries e JOIN items i ON i.id = e.item_id
		WHERE e.list_id = @list AND e.position > @after AND i.kind IN `+playableKindsSQL+` AND `+acc.visible("i")+`
		ORDER BY e.position LIMIT @limit`, append(v.args(), sql.Named("list", list), sql.Named("after", after), sql.Named("limit", limit))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	last = after
	for rows.Next() {
		var id int64
		if err := rows.Scan(&last, &id); err != nil {
			return nil, 0, err
		}
		items = append(items, id)
	}
	return items, last, rows.Err()
}

// ViewItems returns the next titles of a view (a library with its filters and sort) after
// the cursor, and the cursor to go on from, which is empty at the end.
func (d *DB) ViewItems(ctx context.Context, v Viewer, q ItemQuery, limit int) (items []int64, next string, err error) {
	q.WithTotal = false
	for len(items) < limit {
		q.Limit = min(limit-len(items), maxPageSize)
		page, err := d.Items(ctx, v, q)
		if err != nil {
			return nil, "", err
		}
		for _, item := range page.Items {
			if item.Kind.Playable() {
				items = append(items, item.ID)
			}
		}
		if q.Cursor = page.NextCursor; q.Cursor == "" {
			break
		}
	}
	return items, q.Cursor, nil
}

// ViewCursorAt returns the cursor that makes ViewItems carry on just after one title of a
// view, and that title's id: where a person pressed Play in a list.
func (d *DB) ViewCursorAt(ctx context.Context, v Viewer, q ItemQuery, itemPublicID string) (cursor string, item int64, err error) {
	detail, err := d.Item(ctx, v, itemPublicID)
	if err != nil {
		return "", 0, err
	}
	if q.Sort == "" {
		q.Sort = SortTitle
	}
	at := listCursor{Sort: q.Sort, Desc: q.Desc, ID: detail.ID}
	if values := detail.Item.sortValues(q.Sort); values[0] == nil {
		at.Nulls = true
	} else {
		at.Values = values
	}
	return at.encode(), detail.ID, nil
}

// shufflePrime is the modulus of the shuffle order: a prime above any item id a library has.
const shufflePrime = 2147483647

// ShuffledItems returns the next titles of a view in an order the seed decides: every title
// exactly once before any comes again. after is the place the last call ended at (0 to
// begin), and last the place to go on from. The order is each id multiplied by a number the
// seed gives, modulo a prime: a different walk through the library for every seed, read
// without building the whole list.
func (d *DB) ShuffledItems(ctx context.Context, v Viewer, q ItemQuery, seed, after int64, limit int) (items []int64, last int64, err error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, 0, err
	}
	where := newQuery(v)
	q.Sort = SortTitle
	if len(q.Kinds) == 0 {
		q.Kinds = []ItemKind{KindMovie, KindEpisode, KindTrack, KindAudiobook}
	}
	if found, err := q.filters(ctx, tx, where, acc); err != nil || !found {
		return nil, after, err
	}
	// The multiplier is odd and large, so neighbours in the library land far apart.
	multiplier := (uint64(seed)*0x9e3779b97f4a7c15>>33)%(shufflePrime-2) + 2
	key := `((i.id % ` + itoa(shufflePrime) + `) * ` + itoa(int64(multiplier)) + ` % ` + itoa(shufflePrime) + `)`
	rows, err := tx.QueryContext(ctx, `SELECT `+key+`, i.id FROM `+where.from+` WHERE `+where.pageWhere()+` AND `+key+` > `+where.arg(after)+`
		ORDER BY 1 LIMIT `+itoa(int64(limit)), where.args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	last = after
	for rows.Next() {
		var id int64
		if err := rows.Scan(&last, &id); err != nil {
			return nil, 0, err
		}
		items = append(items, id)
	}
	return items, last, rows.Err()
}

// FirstUnwatched returns the index of the first of the items the profile has not watched:
// where a show resumes. When all are watched it is 0.
func (d *DB) FirstUnwatched(ctx context.Context, v Viewer, items []int64) (int, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT item_id FROM user_item_state WHERE profile_id = ? AND played = 1 AND item_id IN `+intList(items), v.ProfileID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	watched := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		watched[id] = true
	}
	for index, id := range items {
		if !watched[id] {
			return index, rows.Err()
		}
	}
	return 0, rows.Err()
}

// ItemPublicID returns the wire id of an item.
func (d *DB) ItemPublicID(ctx context.Context, id int64) (string, error) {
	var public string
	err := d.read.QueryRowContext(ctx, `SELECT public_id FROM items WHERE id = ?`, id).Scan(&public)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return public, err
}

// RadioSeed returns what a radio from a song, album or artist plays from: its library and
// a genre it carries itself or through its album or artist. genre is empty when it has none.
func (d *DB) RadioSeed(ctx context.Context, v Viewer, itemPublicID string) (library, genre string, err error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return "", "", err
	}
	var id, parent, root int64
	err = tx.QueryRowContext(ctx, `SELECT i.id, ifnull(i.parent_id, 0), ifnull(i.root_id, 0), l.public_id FROM items i
		JOIN libraries l ON l.id = i.library_id WHERE i.public_id = @id AND i.kind IN (5, 6, 7) AND `+acc.visible("i"),
		append(v.args(), sql.Named("id", itemPublicID))...).Scan(&id, &parent, &root, &library)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	err = tx.QueryRowContext(ctx, `SELECT t.public_id FROM item_tags it JOIN tags t ON t.id = it.tag_id
		WHERE t.kind = 'genre' AND it.item_id IN (?, ?, ?) ORDER BY it.item_id = ?1 DESC, t.id LIMIT 1`, id, parent, root).Scan(&genre)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return library, genre, err
}
