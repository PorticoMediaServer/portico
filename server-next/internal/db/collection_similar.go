package db

import (
	"context"
	"database/sql"
	"errors"
)

const collectionSimilarFilms = 12

// collectionSimilarPage is More like this for a collection (desktop-detail f8 draws it under
// the collection's films): its films' own More like this, taken in turn from each film in the
// collection's order so every film is represented, without the collection's own films or a
// title twice. It is one page; ok is false when the item is not a collection.
func (d *DB) collectionSimilarPage(ctx context.Context, v Viewer, itemID, cursor string, limit int) (page ItemPage, ok bool, err error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, false, err
	}
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		tx.Rollback()
		return page, false, err
	}
	var kind ItemKind
	err = tx.QueryRowContext(ctx, `SELECT i.kind FROM items i WHERE i.public_id = ? AND `+acc.visible("i"), itemID).Scan(&kind)
	tx.Rollback()
	if errors.Is(err, sql.ErrNoRows) || err == nil && kind != KindCollection {
		return page, false, nil
	}
	if err != nil {
		return page, true, err
	}
	if cursor != "" {
		return page, true, ErrCursorExpired
	}
	if limit <= 0 {
		limit = 15
	}
	limit = min(limit, maxPageSize)
	members, err := d.Items(ctx, v, ItemQuery{Collection: itemID, Sort: SortOrder, Limit: maxPageSize})
	if err != nil {
		return page, true, err
	}
	seen := map[int64]bool{}
	var lists [][]Item
	copies := map[string]bool{}
	for _, film := range members.Items {
		seen[film.ID] = true
	}
	// Another library's copy of one of the collection's films is not a suggestion either.
	for _, film := range members.Items {
		if key, ok := copyKey(film); ok {
			copies[key] = true
		}
	}
	// A long collection is represented by its first films; each costs one similar-titles read.
	for _, film := range members.Items[:min(len(members.Items), collectionSimilarFilms)] {
		like, err := d.MoreLikeThisPage(ctx, v, film.PublicID, "", limit)
		if err != nil {
			return page, true, err
		}
		lists = append(lists, like.Items)
	}
	page.Items = []Item{}
	for n := 0; len(page.Items) < limit; n++ {
		more := false
		for _, list := range lists {
			if n >= len(list) {
				continue
			}
			more = true
			it := list[n]
			key, isCopy := copyKey(it)
			if seen[it.ID] || isCopy && copies[key] || len(page.Items) == limit {
				continue
			}
			seen[it.ID] = true
			if isCopy {
				copies[key] = true
			}
			page.Items = append(page.Items, it)
		}
		if !more {
			break
		}
	}
	return page, true, nil
}
