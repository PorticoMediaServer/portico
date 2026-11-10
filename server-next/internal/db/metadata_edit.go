package db

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server-next/internal/contentrating"
	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// The editor's limits: a short field, a description, and a list.
const (
	maxShortField   = 300
	maxDescription  = 20_000
	maxListEntries  = 64
	maxListEntryLen = 128
)

// MetadataEdit is a person's edit to one title. A nil field is left alone. Every field and
// list the edit sets is locked, so no scan or refresh changes it afterwards; Unlock lets the
// provider's value return at the next refresh.
type MetadataEdit struct {
	Title         *string
	SortTitle     *string
	OriginalTitle *string
	Summary       *string
	Tagline       *string
	Year          *int
	ReleaseDate   *string // YYYY-MM-DD, or "" for none
	ContentRating *string
	Genres        *[]string
	Lock          []string // field names to lock without changing them
	Unlock        []string
}

// MetadataLocks says which of a title's fields a person locked, where each value that is not
// the provider's came from (SourceFile or SourceEdit, by field), and the revision an edit
// must name. Local is the fields whose source is the files, for callers that only ask that.
type MetadataLocks struct {
	Revision int64
	Locked   []string
	Local    []string
	Sources  map[string]string
	Match    MatchState
}

// MetadataLocks returns a title's locks.
func (d *DB) MetadataLocks(ctx context.Context, publicID string) (MetadataLocks, error) {
	var out MetadataLocks
	var locked, local sql.NullString
	err := d.read.QueryRowContext(ctx, `SELECT revision, locked_fields, local_fields, match_state FROM items WHERE public_id = ?`, publicID).
		Scan(&out.Revision, &locked, &local, &out.Match)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	out.Locked, out.Sources = sortedFields(fieldSet(locked)), fieldSources(local)
	out.Local = []string{}
	for field, source := range out.Sources {
		if source == SourceFile {
			out.Local = append(out.Local, field)
		}
	}
	slices.Sort(out.Local)
	return out, err
}

func sortedFields(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for field := range set {
		out = append(out, field)
	}
	slices.Sort(out)
	return out
}

func checkText(field, value string, limit int, lines bool) error {
	if utf8.RuneCountInString(value) > limit {
		return invalid("%s: at most %d characters", field, limit)
	}
	for _, r := range value {
		if r == utf8.RuneError || r < ' ' && !(lines && (r == '\n' || r == '\r' || r == '\t')) {
			return invalid("%s: has a character that cannot be shown", field)
		}
	}
	return nil
}

// EditMetadata stores a person's edit to a title in one transaction and locks what it set.
// revision is the one the editor read: if the title has changed since, nothing is written and
// the answer is ErrStale. A value outside the editor's limits is refused, naming the field.
// It returns the title's new revision.
func (d *DB) EditMetadata(ctx context.Context, publicID string, revision int64, edit MetadataEdit) (int64, error) {
	type textField struct {
		name, column string
		value        *string
		limit        int
		lines        bool
	}
	texts := []textField{{FieldTitle, "title", edit.Title, maxShortField, false}, {FieldSortTitle, "", edit.SortTitle, maxShortField, false},
		{FieldOriginalTitle, "original_title", edit.OriginalTitle, maxShortField, false}, {FieldSummary, "summary", edit.Summary, maxDescription, true},
		{FieldTagline, "tagline", edit.Tagline, maxShortField, false}, {FieldReleaseDate, "release_date", edit.ReleaseDate, 10, false},
		{FieldContentRating, "content_rating", edit.ContentRating, maxShortField, false}}
	for i, f := range texts {
		if f.value == nil {
			continue
		}
		trimmed := strings.TrimSpace(*f.value)
		texts[i].value = &trimmed
		if err := checkText(f.name, trimmed, f.limit, f.lines); err != nil {
			return 0, err
		}
	}
	if edit.Title != nil && strings.TrimSpace(*edit.Title) == "" {
		return 0, invalid("%s: a title cannot be empty", FieldTitle)
	}
	if edit.Year != nil && (*edit.Year < 0 || *edit.Year > 9999) {
		return 0, invalid("%s: 0 to 9999", FieldYear)
	}
	if edit.ReleaseDate != nil && strings.TrimSpace(*edit.ReleaseDate) != "" {
		if _, err := time.Parse("2006-01-02", strings.TrimSpace(*edit.ReleaseDate)); err != nil {
			return 0, invalid("%s: a real date, as YYYY-MM-DD", FieldReleaseDate)
		}
	}
	var genres []TagName
	if edit.Genres != nil {
		seen := map[string]bool{}
		for _, name := range *edit.Genres {
			name = strings.TrimSpace(name)
			if name == "" || seen[normTag(name)] {
				continue
			}
			seen[normTag(name)] = true
			if err := checkText(FieldGenres, name, maxListEntryLen, false); err != nil {
				return 0, err
			}
			genres = append(genres, TagName{Kind: TagGenre, Name: name})
		}
		if len(genres) > maxListEntries {
			return 0, invalid("%s: at most %d entries", FieldGenres, maxListEntries)
		}
	}
	known := map[string]bool{FieldRating: true, FieldCredits: true, FieldMatch: true, FieldYear: true}
	for _, f := range texts {
		known[f.name] = true
	}
	for _, field := range tagFields {
		known[field] = true
	}
	for _, field := range append(append([]string(nil), edit.Lock...), edit.Unlock...) {
		if !known[field] {
			return 0, invalid("%s: not a field that can be locked", field)
		}
	}

	var next int64
	err := d.write(ctx, User, func(tx *sql.Tx) error {
		b := newBatchTx(tx)
		defer b.close()
		now := time.Now().UnixMilli()
		var id, library, current int64
		var title string
		var locked, local, language sql.NullString
		var root sql.NullInt64
		err := b.queryRow(`SELECT i.id, i.library_id, i.revision, i.title, i.locked_fields, i.local_fields, i.root_id, l.language FROM items i
			JOIN libraries l ON l.id = i.library_id WHERE i.public_id = ?`, publicID).Scan(&id, &library, &current, &title, &locked, &local, &root, &language)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if current != revision {
			return ErrStale
		}
		locks := fieldSet(locked)
		sortLocked := locks[FieldSortTitle] // a sort title a person set does not follow a new title
		// What this edit sets is a person's from now on, whatever gave it before. A field that
		// is only locked or unlocked keeps its source.
		sources := fieldSources(local)
		sets, args := "updated_at = ?, revision = revision + 1", []any{now}
		for _, f := range texts {
			if f.value == nil {
				continue
			}
			locks[f.name], sources[f.name] = true, SourceEdit
			if f.column != "" {
				sets += ", " + f.column + " = ?"
				args = append(args, nz(*f.value))
			}
		}
		if edit.Title != nil {
			title = strings.TrimSpace(*edit.Title)
		}
		if edit.SortTitle != nil || edit.Title != nil && !sortLocked {
			explicit := ""
			if edit.SortTitle != nil {
				explicit = strings.TrimSpace(*edit.SortTitle)
			}
			sets += ", sort_title = ?"
			args = append(args, sorttext.Key(title, explicit, language.String))
		}
		if edit.Year != nil {
			locks[FieldYear], sources[FieldYear] = true, SourceEdit
			sets += ", year = ?"
			args = append(args, nz(*edit.Year))
		}
		var age *int
		if edit.ContentRating != nil {
			// The rating's age follows the rating: it decides who may see the title.
			if n, ok := contentrating.Age(*edit.ContentRating); ok {
				age = &n
			}
			if strings.TrimSpace(*edit.ContentRating) == "" && root.Valid {
				// An episode with no rating of its own uses its show's.
				sets += ", rating_age = (SELECT r.rating_age FROM items r WHERE r.id = ?)"
				args = append(args, root.Int64)
			} else {
				sets += ", rating_age = ?"
				args = append(args, age)
			}
		}
		if edit.Genres != nil {
			locks[FieldGenres], sources[FieldGenres] = true, SourceEdit
		}
		for _, field := range edit.Lock {
			locks[field] = true
		}
		for _, field := range edit.Unlock {
			delete(locks, field)
		}
		sets += ", locked_fields = ?, local_fields = ?"
		args = append(args, fieldList(sortedFields(locks)), sourceJSON(sources))
		if _, err := b.exec(`UPDATE items SET `+sets+` WHERE id = ?`, append(args, id)...); err != nil {
			return err
		}
		if edit.ContentRating != nil && !root.Valid {
			if _, err := b.exec(`UPDATE items SET rating_age = ? WHERE root_id = ? AND content_rating IS NULL`, age, id); err != nil {
				return err
			}
		}
		if edit.Genres != nil {
			if _, err := b.exec(`DELETE FROM item_tags WHERE item_id = ? AND tag_id IN (SELECT id FROM tags WHERE kind = ?)`, id, TagGenre); err != nil {
				return err
			}
			im := &importer{batchTx: b, now: now, tags: map[TagName]int64{}}
			for _, genre := range genres {
				tagID, err := im.tagID(genre)
				if err != nil {
					return err
				}
				if _, err := b.exec(`INSERT INTO item_tags (item_id, tag_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, id, tagID); err != nil {
					return err
				}
			}
		}
		next = current + 1
		return appendChange(tx, Change{At: now, Topic: TopicItem, Op: OpUpdated, RefID: id, LibraryID: library})
	})
	return next, err
}

// RefreshItem is a matched title due to have its provider asked again.
type RefreshItem struct {
	ID          int64
	Kind        ItemKind
	RefreshedAt int64
	AddedAt     int64
	Continuing  bool         // a show that has not ended
	ExternalIDs []ExternalID // filled by RefreshTarget; ToRefresh leaves it for ExternalIDs to answer
}

// ToRefresh lists a library's matched top-level titles that were last refreshed at or before the
// given time, oldest first, a page at a time: pass the last line of one page to get the next.
// It reads along the items_to_refresh index.
func (d *DB) ToRefresh(ctx context.Context, libraryID, before int64, after *RefreshItem, limit int) ([]RefreshItem, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	where, args := `library_id = ? AND match_state IN (1, 2) AND parent_id IS NULL AND refreshed_at <= ?`, []any{libraryID, before}
	if after != nil {
		where += ` AND (refreshed_at, id) > (?, ?)`
		args = append(args, after.RefreshedAt, after.ID)
	}
	items, err := queryAll(ctx, tx, `SELECT id, kind, refreshed_at, added_at, ifnull(extra ->> '$.continuing', 0) FROM items WHERE `+where+`
		ORDER BY refreshed_at, id LIMIT ?`, append(args, limit),
		func(r *RefreshItem) []any { return []any{&r.ID, &r.Kind, &r.RefreshedAt, &r.AddedAt, &r.Continuing} })
	if err != nil {
		return nil, err
	}
	return items, nil
}

// RefreshTarget returns one title for a refresh a person asked for.
func (d *DB) RefreshTarget(ctx context.Context, publicID string) (RefreshItem, int64, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return RefreshItem{}, 0, err
	}
	defer tx.Rollback()
	var r RefreshItem
	var library int64
	err = tx.QueryRowContext(ctx, `SELECT id, kind, ifnull(refreshed_at, 0), added_at, library_id FROM items WHERE public_id = ?`, publicID).
		Scan(&r.ID, &r.Kind, &r.RefreshedAt, &r.AddedAt, &library)
	if errors.Is(err, sql.ErrNoRows) {
		return r, 0, ErrNotFound
	}
	if err != nil {
		return r, 0, err
	}
	r.ExternalIDs, err = queryAll(ctx, tx, `SELECT provider, external_id FROM item_external_ids WHERE item_id = ?`, []any{r.ID},
		func(e *ExternalID) []any { return []any{&e.Provider, &e.ID} })
	return r, library, err
}

// MarkRefreshed records that a provider was asked about a title and had nothing to say (it no
// longer knows the title), so it is not asked again until the title is next due.
func (d *DB) MarkRefreshed(ctx context.Context, p Priority, itemID int64) error {
	return d.write(ctx, p, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE items SET refreshed_at = ? WHERE id = ?`, time.Now().UnixMilli(), itemID)
		return err
	})
}

// ExternalIDs returns a title's ids at providers.
func (d *DB) ExternalIDs(ctx context.Context, itemID int64) ([]ExternalID, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return queryAll(ctx, tx, `SELECT provider, external_id FROM item_external_ids WHERE item_id = ?`, []any{itemID},
		func(e *ExternalID) []any { return []any{&e.Provider, &e.ID} })
}
