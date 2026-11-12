package db

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// PeopleRoles are the roles a library's people can be listed by: the ones a library tab is
// made of. A library's actors are reached through its titles and the filters, not listed.
var PeopleRoles = map[string]CreditRole{"author": RoleAuthor, "narrator": RoleNarrator}

// PeoplePage is one page of a library's people, by name.
type PeoplePage struct {
	People     []Person
	NextCursor string
	Total      int
}

type peopleCursor struct {
	Name string `json:"n"`
	ID   int64  `json:"i"`
}

// People lists the people who have one role on the titles of one library that the viewer may
// see: an audiobook library's authors, its narrators. They come by name, a page at a time,
// each with how many of the library's titles the viewer may see credit them in that role. A
// person all of whose titles the viewer may not see is not listed, and not counted.
//
// The read goes through the library's titles and their credits, not through everyone the
// server knows of, so it costs in proportion to the library and not to the cast of every film.
func (d *DB) People(ctx context.Context, v Viewer, library, role, cursor string, limit int, withTotal bool) (PeoplePage, error) {
	var page PeoplePage
	code, ok := PeopleRoles[role]
	if !ok {
		return page, invalid("A library's people are listed by author or narrator.")
	}
	var after peopleCursor
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(raw, &after) != nil {
			return page, ErrCursorExpired
		}
	}
	if limit <= 0 {
		limit = defaultPageSize
	}
	limit = min(limit, maxPageSize)
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return page, err
	}
	var libraryID int64
	if err := tx.QueryRowContext(ctx, `SELECT l.id FROM libraries l WHERE l.public_id = ? AND `+acc.libraryVisible("l.id"), library).Scan(&libraryID); err != nil {
		return page, notFound(err)
	}
	// CROSS JOIN: read the library's titles and look each one's credits up by its key.
	credited := fmt.Sprintf(`FROM items i CROSS JOIN credits c ON c.item_id = i.id AND c.role = %d WHERE i.library_id = %d AND %s`,
		code, libraryID, acc.visible("i"))
	args := append(v.args(), sql.Named("after_name", after.Name), sql.Named("after_id", after.ID), sql.Named("limit", limit+1))
	type row struct {
		Person
		id    int64
		sort  string
		total int
	}
	// The total is counted from the same pass over the library, only when it is asked for.
	total := "0"
	if withTotal {
		total = "(SELECT count(*) FROM who)"
	}
	rows, err := queryAll(ctx, tx, `WITH who(person, titles) AS MATERIALIZED (SELECT c.person_id, count(DISTINCT i.id) `+credited+` GROUP BY 1)
		SELECT p.id, p.public_id, p.name, p.sort_name, who.titles, `+total+` FROM who JOIN people p ON p.id = who.person
		WHERE (p.sort_name, p.id) > (@after_name, @after_id) ORDER BY p.sort_name, p.id LIMIT @limit`, args,
		func(r *row) []any { return []any{&r.id, &r.PublicID, &r.Name, &r.sort, &r.Titles, &r.total} })
	if err != nil {
		return page, err
	}
	if len(rows) > 0 {
		page.Total = rows[0].total
	}
	if len(rows) > limit {
		rows = rows[:limit]
		raw, _ := json.Marshal(peopleCursor{Name: rows[limit-1].sort, ID: rows[limit-1].id})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	page.People = make([]Person, len(rows))
	place := map[int64]int{}
	list := make([]int64, len(rows))
	for n, r := range rows {
		page.People[n], place[r.id], list[n] = r.Person, n, r.id
	}
	if len(list) > 0 {
		type picture struct {
			person int64
			Image
		}
		pictures, err := queryAll(ctx, tx, `SELECT person_id, id, role, ifnull(file_hash, ''), ifnull(width, 0), ifnull(height, 0), ifnull(placeholder, '')
			FROM images WHERE person_id IN `+intList(list)+` AND selected = 1`, nil,
			func(i *picture) []any {
				return []any{&i.person, &i.ID, &i.Role, &i.FileHash, &i.Width, &i.Height, &i.Placeholder}
			})
		if err != nil {
			return page, err
		}
		for _, p := range pictures {
			page.People[place[p.person]].Images = append(page.People[place[p.person]].Images, p.Image)
		}
	}
	return page, nil
}
