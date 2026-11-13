package db

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// TagPage is one page of the tags a viewer may see, by kind and then name.
type TagPage struct {
	Tags       []Tag
	NextCursor string
}

// tagCursor is the last tag sent: its kind and its name as compared, which together are unique.
type tagCursor struct {
	Kind string `json:"k"`
	Name string `json:"n"`
}

// VisibleTags lists the Tags in use on the server, by kind and then name: with all, every one
// (for the owner and admins, who set the Tags other profiles are kept from); otherwise only
// those on at least one title the viewer may see, and never a Tag the viewer is kept from.
// kind, when set, lists one kind.
//
// The list walks the tags table in its (kind, norm_name) order and stops when the page is
// full. For each Tag it looks, through item_tags_tag, for one title that carries it and that
// the viewer may see: the first row answers for a Tag the viewer sees, and a Tag only on
// titles the viewer may not see costs a look at each of them. So the owner, admins and a
// profile that sees everything pay one look per Tag listed, whatever the library's size; a
// limited profile also pays for every Tag it skips. A narrow one that sees fewer Tags than a
// page holds walks every Tag and nearly every tag row on each call (188 ms for a profile
// allowed only the book library, at 100,000 titles), a cost that grows with tag rows. That is
// accepted while only the owner's screen reads this route. If a screen for limited profiles
// reads it, or a load run at 400,000 passes 300 ms, start a profile limited to a few
// libraries from item_tags_library instead.
func (d *DB) VisibleTags(ctx context.Context, v Viewer, all bool, kind, cursor string, limit int) (TagPage, error) {
	return d.VisibleTagsMatching(ctx, v, all, kind, cursor, limit, nil)
}

// VisibleTagsMatching also reads the names of selected identities without walking unrelated tags.
func (d *DB) VisibleTagsMatching(ctx context.Context, v Viewer, all bool, kind, cursor string, limit int, only []string) (TagPage, error) {
	var page TagPage
	after := tagCursor{}
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(raw, &after) != nil || after.Kind == "" {
			return page, invalid("the cursor does not belong to this list")
		}
	}
	if kind != "" && !tagKinds[kind] {
		return page, invalid("there is no kind of tag %q", kind)
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
	in := `SELECT 1 FROM item_tags it WHERE it.tag_id = t.id`
	notBlocked := ""
	if !all {
		acc, err := loadAccess(ctx, tx, v)
		if err != nil {
			return page, err
		}
		in += ` AND ` + acc.libraryVisible("it.library_id") + acc.rated("it", "")
		if len(acc.blockedTags) > 0 {
			// The title itself is read only when Tags are blocked: neither it nor what it
			// sits under may carry one.
			in = `SELECT 1 FROM item_tags it CROSS JOIN items i ON i.id = it.item_id WHERE it.tag_id = t.id AND ` +
				acc.libraryVisible("it.library_id") + acc.rated("it", "") + acc.unblocked("i")
			notBlocked = ` AND t.id NOT IN ` + intList(acc.blockedTags)
		}
	}
	where := `EXISTS (` + in + `)` + notBlocked
	if only != nil {
		where += ` AND t.public_id IN (SELECT value FROM json_each(@only))`
	}
	onlyJSON, _ := json.Marshal(only)
	if kind != "" {
		where += ` AND t.kind = @kind`
	}
	if cursor != "" {
		where += ` AND (t.kind, t.norm_name) > (@k, @n)`
	}
	type row struct {
		Tag
		norm string
	}
	rows, err := queryAll(ctx, tx, `SELECT t.public_id, t.kind, t.name, t.norm_name FROM tags t WHERE `+where+`
		ORDER BY t.kind, t.norm_name LIMIT @limit`,
		[]any{sql.Named("kind", kind), sql.Named("k", after.Kind), sql.Named("n", after.Name), sql.Named("limit", limit+1), sql.Named("only", string(onlyJSON))},
		func(r *row) []any { return []any{&r.PublicID, &r.Kind, &r.Name, &r.norm} })
	if err != nil {
		return page, err
	}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		raw, _ := json.Marshal(tagCursor{Kind: last.Kind, Name: last.norm})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	page.Tags = make([]Tag, len(rows))
	for n, r := range rows {
		page.Tags[n] = r.Tag
	}
	return page, nil
}

// normTag folds a tag name so "Sci-Fi", "sci fi" and "Science Fiction" are one genre.
func normTag(name string) string {
	folded := strings.Join(strings.FieldsFunc(sorttext.Fold(name), func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == ',' || r == ' ' || r == '\t'
	}), " ")
	if folded == "sci fi" || folded == "scifi" {
		return "science fiction"
	}
	return folded
}
