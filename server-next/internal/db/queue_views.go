package db

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
)

// containerView is a view whose cards hold playable titles, rather than being playable.
func containerView(q ItemQuery) bool {
	if q.homeRecent || q.homeIDs != nil {
		return true
	}
	for _, kind := range q.Kinds {
		if kind == KindShow || kind == KindSeason || kind == KindArtist || kind == KindAlbum {
			return true
		}
	}
	return false
}

// containerCursor keeps both positions: the card in the view and the child within it.
// Keeping the ordinary view cursor also keeps a random view's chosen order across refills.
type containerCursor struct {
	Holder int64    `json:"holder"`
	Kind   ItemKind `json:"kind"`
	Next   string   `json:"next"`
	Child  string   `json:"child"`
}

func (c containerCursor) encode() string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func (d *DB) containerViewItems(ctx context.Context, v Viewer, q ItemQuery, limit int) (items []int64, next string, err error) {
	var at containerCursor
	if q.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		if err != nil || json.Unmarshal(raw, &at) != nil || at.Holder == 0 && at.Next == "" {
			return nil, "", ErrCursorExpired
		}
	}
	q.WithTotal, q.Limit, q.Cursor = false, 1, at.Next
	for len(items) < limit {
		if at.Holder == 0 {
			page, err := d.queueCard(ctx, v, q)
			if err != nil || len(page.Items) == 0 {
				return items, "", err
			}
			card := page.Items[0]
			at = containerCursor{Holder: card.ID, Kind: card.Kind, Next: page.NextCursor}
		}
		children, childNext, err := d.queueChildren(ctx, v, at, limit-len(items))
		if err != nil {
			return nil, "", err
		}
		items = append(items, children...)
		at.Child = childNext
		if childNext != "" {
			return items, at.encode(), nil
		}
		if at.Next == "" {
			return items, "", nil
		}
		q.Cursor = at.Next
		if len(items) == limit {
			return items, (containerCursor{Next: at.Next}).encode(), nil
		}
		at = containerCursor{Next: at.Next}
	}
	return items, "", nil
}

func (d *DB) queueChildren(ctx context.Context, v Viewer, at containerCursor, limit int) ([]int64, string, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, "", err
	}
	if at.Kind.Playable() {
		items, err := ids(ctx, tx, `SELECT i.id FROM items i WHERE i.id = @holder AND `+acc.visible("i"),
			append(v.args(), sql.Named("holder", at.Holder))...)
		return items, "", err
	}
	w := newQuery(v)
	w.from = `items i LEFT JOIN items p ON p.id = i.parent_id`
	w.add(`i.kind IN `, playableKindsSQL, ` AND (i.root_id = `, w.arg(at.Holder), ` OR i.parent_id = `, w.arg(at.Holder), `)`)
	w.add(` AND `, acc.visible("i"), ` AND EXISTS (SELECT 1 FROM items holder WHERE holder.id = `, w.arg(at.Holder), ` AND `, acc.visible("holder"), `)`)
	spec := queueChildOrder(at.Kind)
	after, err := decodeCursor(at.Child, SortOrder, false)
	if err != nil || after != nil && !after.fits(spec) {
		return nil, "", ErrCursorExpired
	}
	var page ItemPage
	if err := listRun(ctx, tx, acc, w, spec, ItemQuery{Limit: limit}, after, false, &page); err != nil {
		return nil, "", err
	}
	next := ""
	if len(page.Items) > limit {
		last := page.Items[limit-1]
		next = (listCursor{Sort: SortOrder, ID: last.ID, Values: last.sortValues}).encode()
		page.Items = page.Items[:limit]
	}
	items := make([]int64, len(page.Items))
	for n, item := range page.Items {
		items[n] = item.ID
	}
	return items, next, nil
}

func queueChildOrder(kind ItemKind) sortSpec {
	spec := sortSpecs[SortOrder]
	if kind == KindArtist {
		spec.columns = append([]string{"ifnull(p.year, 0)", "ifnull(p.sort_title, '')", "ifnull(p.id, 0)"}, spec.columns...)
	}
	return spec
}

// queueLeaves narrows playable leaves to the matching cards. The same statement serves a
// count and shuffle, so both apply the view's conditions to containers and limits to leaves.
func queueLeaves(ctx context.Context, tx *sql.Tx, v Viewer, q ItemQuery, acc access) (*query, error) {
	w := newQuery(v)
	q.Sort = SortTitle
	found, err := q.filters(ctx, tx, w, acc)
	if err != nil {
		return nil, err
	}
	match := `SELECT i.id FROM ` + w.from + ` WHERE ` + w.pageWhere()
	if !found {
		match = `SELECT id FROM items WHERE 0`
	}
	w.sql.Reset()
	w.from = `(` + match + `) card CROSS JOIN items i ON i.id = card.id OR i.parent_id = card.id OR i.root_id = card.id`
	w.add(`i.kind IN `, playableKindsSQL, ` AND `, acc.visible("i"))
	// The match already substituted its list-specific placeholders.
	w.labels, w.holders = "", ""
	return w, nil
}

// ViewPlayableCount counts entries, rather than the containers shown by the view.
func (d *DB) ViewPlayableCount(ctx context.Context, v Viewer, q ItemQuery) (int, error) {
	if !containerView(q) {
		q.WithTotal, q.Limit, q.Cursor = true, 1, ""
		page, err := d.Items(ctx, v, q)
		return page.Total, err
	}
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return 0, err
	}
	w, err := queueLeaves(ctx, tx, v, q, acc)
	if err != nil {
		return 0, err
	}
	var count int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM `+w.from+` WHERE `+w.pageWhere(), w.args...).Scan(&count)
	return count, err
}

// containerViewCursorAt resumes the matching container at the leaf that is playing.
func (d *DB) containerViewCursorAt(ctx context.Context, tx *sql.Tx, v Viewer, q ItemQuery, leaf Item) (string, error) {
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return "", err
	}
	w := newQuery(v)
	if found, err := q.filters(ctx, tx, w, acc); err != nil || !found {
		return "", err
	}
	var at containerCursor
	err = tx.QueryRowContext(ctx, `SELECT i.id, i.kind FROM `+w.from+` WHERE `+w.pageWhere()+` AND i.id IN `+
		intList([]int64{leaf.ID, leaf.ParentID, leaf.RootID})+` LIMIT 1`, w.args...).Scan(&at.Holder, &at.Kind)
	if err != nil {
		return "", notFound(err)
	}
	if q.homeIDs != nil && q.Sort == SortOrder {
		index := slices.Index(q.homeIDs, at.Holder)
		at.Next = (listCursor{Sort: SortOrder, Desc: q.Desc, ID: at.Holder, Values: []any{int64(index)}}).encode()
	} else if at.Next, err = cursorAt(ctx, tx, v, q, at.Holder); err != nil {
		return "", err
	}
	if !at.Kind.Playable() {
		spec := queueChildOrder(at.Kind)
		child := listCursor{Sort: SortOrder, ID: leaf.ID, Values: make([]any, len(spec.columns))}
		dest := make([]any, len(child.Values))
		for n := range dest {
			dest[n] = &child.Values[n]
		}
		err = tx.QueryRowContext(ctx, `SELECT `+strings.Join(spec.columns, ", ")+` FROM items i LEFT JOIN items p ON p.id = i.parent_id WHERE i.id = @leaf`,
			append(v.args(), sql.Named("leaf", leaf.ID))...).Scan(dest...)
		if err != nil {
			return "", err
		}
		at.Child = child.encode()
	} else {
		at.Holder = 0
	}
	return at.encode(), nil
}
