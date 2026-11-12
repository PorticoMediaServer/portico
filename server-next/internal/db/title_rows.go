package db

import (
	"context"
	"database/sql"
	"errors"
	"slices"
)

// A film's or show's page shows, after More like this, rows of other titles: one for each of
// its first three credited actors ("Starring"), one for its creator ("From its creator": a
// film's first credited director, a show's first credited writer, as there is no creator
// role), and one for its first genre as its page lists them. Each row is the start of the one
// list it opens to (a person's titles, or the genre's titles of that kind in that library),
// so it holds only what the profile may see and is in that list's order; the title itself is
// never in its own rows, and a row with nothing in it is left out.

// TitleRow is one of those rows: Relation "cast", "creator" or "genre", with the person or the
// genre it is of.
type TitleRow struct {
	Relation   string
	PersonID   string
	PersonName string
	TagID      string
	TagName    string
	Items      []Item
}

// titleRowSize is how many titles a row holds.
const titleRowSize = 20

// TitleRows reads the rows of a title's page, in the order the page draws them: cast, creator,
// genre. A title that is not a film or a show has none.
func (d *DB) TitleRows(ctx context.Context, v Viewer, itemID string) ([]TitleRow, error) {
	var rows []TitleRow
	var kind ItemKind
	var library string
	err := func() error {
		tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return err
		}
		defer tx.Rollback()
		acc, err := loadAccess(ctx, tx, v)
		if err != nil {
			return err
		}
		var id int64
		err = tx.QueryRowContext(ctx, `SELECT i.id, i.kind, l.public_id FROM items i JOIN libraries l ON l.id = i.library_id
			WHERE i.public_id = ? AND `+acc.visible("i"), itemID).Scan(&id, &kind, &library)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil || (kind != KindMovie && kind != KindShow) {
			return err
		}
		creator := RoleDirector
		if kind == KindShow {
			creator = RoleWriter
		}
		lead := func(relation string, role CreditRole, n int) error {
			people, err := queryAll(ctx, tx, `SELECT p.public_id, p.name FROM credits c JOIN people p ON p.id = c.person_id
				WHERE c.item_id = ? AND c.role = ? ORDER BY c.position LIMIT ?`, []any{id, role, n},
				func(r *TitleRow) []any { return []any{&r.PersonID, &r.PersonName} })
			for _, p := range people {
				// A creator who is also one of the leads already has a row.
				if !slices.ContainsFunc(rows, func(r TitleRow) bool { return r.PersonID == p.PersonID }) {
					p.Relation = relation
					rows = append(rows, p)
				}
			}
			return err
		}
		if err := lead("cast", RoleActor, 3); err != nil {
			return err
		}
		if err := lead("creator", creator, 1); err != nil {
			return err
		}
		var genre TitleRow
		err = tx.QueryRowContext(ctx, `SELECT t.public_id, t.name FROM item_tags it JOIN tags t ON t.id = it.tag_id
			WHERE it.item_id = ? AND t.kind = 'genre' ORDER BY t.name LIMIT 1`, id).Scan(&genre.TagID, &genre.TagName)
		if err == nil {
			genre.Relation = "genre"
			rows = append(rows, genre)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return nil
	}()
	if err != nil {
		return nil, err
	}
	// Each row is read as the first page of its list, one more than it shows, less the title.
	out := rows[:0]
	for _, row := range rows {
		q := ItemQuery{Person: row.PersonID, Kinds: []ItemKind{KindMovie, KindShow}, Limit: titleRowSize + 1}
		if row.Relation == "genre" {
			// The genre's best rated titles of the same kind first.
			q = ItemQuery{Library: library, Kinds: []ItemKind{kind}, Tag: row.TagID, Sort: SortCommunityRating, Desc: true, Limit: titleRowSize + 1}
		}
		page, err := d.Items(ctx, v, q)
		if err != nil {
			return nil, err
		}
		for _, it := range page.Items {
			if it.PublicID != itemID && len(row.Items) < titleRowSize {
				row.Items = append(row.Items, it)
			}
		}
		if len(row.Items) > 0 {
			out = append(out, row)
		}
	}
	return out, nil
}
