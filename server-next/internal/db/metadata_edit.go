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
	Edition       *string
	// Lists replace the whole list of one kind and lock it: genres, studios, networks,
	// countries. Labels are the owner's own: no provider or scan changes them, so they are
	// not locked.
	Genres    *[]string
	Studios   *[]string
	Networks  *[]string
	Countries *[]string
	Labels    *[]string
	// Credits replaces the cast and crew in this order and locks them.
	Credits *[]CreditEdit
	Lock    []string // field names to lock without changing them
	Unlock  []string
	// LockAll and UnlockAll lock or unlock every field, list and the match.
	LockAll, UnlockAll bool
}

// CreditEdit is one line of Cast & crew. Person is a person's public id; without one the name
// joins the person of that name, ignoring case and accents, or makes a new one.
type CreditEdit struct {
	Person    string
	Name      string
	Role      string // actor, director, writer, producer, composer, author, narrator, artist, guest, crew
	Character string
}

// maxCredits bounds a title's cast and crew as a person edits it.
const maxCredits = 500

// editLists are the lists an edit can replace, by tag kind, with the field that locks each.
// Labels lock nothing.
var editLists = []struct {
	kind, field string
	value       func(*MetadataEdit) *[]string
}{
	{TagGenre, FieldGenres, func(e *MetadataEdit) *[]string { return e.Genres }},
	{TagStudio, FieldStudios, func(e *MetadataEdit) *[]string { return e.Studios }},
	{TagNetwork, FieldNetworks, func(e *MetadataEdit) *[]string { return e.Networks }},
	{TagCountry, FieldCountries, func(e *MetadataEdit) *[]string { return e.Countries }},
	{TagLabel, "", func(e *MetadataEdit) *[]string { return e.Labels }},
}

// LockableFields are the names a field, list or the match is locked by.
var LockableFields = []string{FieldTitle, FieldSortTitle, FieldOriginalTitle, FieldSummary, FieldTagline, FieldEdition, FieldYear,
	FieldReleaseDate, FieldContentRating, FieldRating, FieldGenres, FieldStudios, FieldNetworks, FieldCountries, FieldCredits, FieldMatch}

// MetadataLocks says which of a title's fields a person locked and which its files gave, and
// the revision an edit must name.
type MetadataLocks struct {
	Revision int64
	Locked   []string
	Local    []string
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
	out.Locked, out.Local = sortedFields(fieldSet(locked)), sortedFields(fieldSet(local))
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
		{FieldContentRating, "content_rating", edit.ContentRating, maxShortField, false},
		{FieldEdition, "edition", edit.Edition, maxShortField, false}}
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
	lists := map[string][]TagName{}
	for _, list := range editLists {
		names := list.value(&edit)
		if names == nil {
			continue
		}
		seen := map[string]bool{}
		tags := []TagName{}
		for _, name := range *names {
			name = strings.TrimSpace(name)
			if name == "" || seen[normTag(name)] {
				continue
			}
			seen[normTag(name)] = true
			if err := checkText(list.kind+"s", name, maxListEntryLen, false); err != nil {
				return 0, err
			}
			tags = append(tags, TagName{Kind: list.kind, Name: name})
		}
		if len(tags) > maxListEntries {
			return 0, invalid("%ss: at most %d entries", list.kind, maxListEntries)
		}
		lists[list.kind] = tags
	}
	var credits []ImportCredit
	var creditPeople []string
	if edit.Credits != nil {
		if len(*edit.Credits) > maxCredits {
			return 0, invalid("%s: at most %d entries", FieldCredits, maxCredits)
		}
		for _, c := range *edit.Credits {
			role, known := creditRoleByName[c.Role]
			name, character := strings.TrimSpace(c.Name), strings.TrimSpace(c.Character)
			switch {
			case !known:
				return 0, invalid("%s: %q is not a role", FieldCredits, c.Role)
			case c.Person == "" && name == "":
				return 0, invalid("%s: each line needs a person or a name", FieldCredits)
			}
			for _, text := range []string{name, character} {
				if err := checkText(FieldCredits, text, maxShortField, false); err != nil {
					return 0, err
				}
			}
			credits = append(credits, ImportCredit{Name: name, Role: role, Character: character})
			creditPeople = append(creditPeople, c.Person)
		}
	}
	known := map[string]bool{}
	for _, field := range LockableFields {
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
		var locked, language sql.NullString
		var root sql.NullInt64
		err := b.queryRow(`SELECT i.id, i.library_id, i.revision, i.title, i.locked_fields, i.root_id, l.language FROM items i
			JOIN libraries l ON l.id = i.library_id WHERE i.public_id = ?`, publicID).Scan(&id, &library, &current, &title, &locked, &root, &language)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if current != revision {
			return ErrStale
		}
		if err := remember(b, id, HistoryFields, now); err != nil {
			return err
		}
		locks := fieldSet(locked)
		sortLocked := locks[FieldSortTitle] // a sort title a person set does not follow a new title
		sets, args := "updated_at = ?, revision = revision + 1", []any{now}
		for _, f := range texts {
			if f.value == nil {
				continue
			}
			locks[f.name] = true
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
			locks[FieldYear] = true
			sets += ", year = ?"
			args = append(args, nz(*edit.Year))
		}
		var age *int
		if edit.ContentRating != nil {
			// The rating's age follows the rating: it decides who may see the title.
			if n, ok := contentrating.Age(*edit.ContentRating); ok {
				age = &n
			}
			sets += ", rating_age = ?"
			args = append(args, age)
		}
		for _, list := range editLists {
			if _, given := lists[list.kind]; given && list.field != "" {
				locks[list.field] = true
			}
		}
		if edit.Credits != nil {
			locks[FieldCredits] = true
		}
		if edit.UnlockAll {
			locks = map[string]bool{}
		}
		if edit.LockAll {
			for _, field := range LockableFields {
				locks[field] = true
			}
		}
		for _, field := range edit.Lock {
			locks[field] = true
		}
		for _, field := range edit.Unlock {
			delete(locks, field)
		}
		sets += ", locked_fields = ?"
		args = append(args, fieldList(sortedFields(locks)))
		if _, err := b.exec(`UPDATE items SET `+sets+` WHERE id = ?`, append(args, id)...); err != nil {
			return err
		}
		if edit.ContentRating != nil && !root.Valid {
			if _, err := b.exec(`UPDATE items SET rating_age = ? WHERE root_id = ? AND content_rating IS NULL`, age, id); err != nil {
				return err
			}
		}
		im := &importer{batchTx: b, now: now, tags: map[TagName]int64{}, people: map[string]int64{}}
		for kind, tags := range lists {
			if _, err := b.exec(`DELETE FROM item_tags WHERE item_id = ? AND tag_id IN (SELECT id FROM tags WHERE kind = ?)`, id, kind); err != nil {
				return err
			}
			for _, tag := range tags {
				tagID, err := im.tagID(tag)
				if err != nil {
					return err
				}
				if _, err := b.exec(`INSERT INTO item_tags (item_id, tag_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, id, tagID); err != nil {
					return err
				}
			}
		}
		if edit.Credits != nil {
			if _, err := b.exec(`DELETE FROM credits WHERE item_id = ?`, id); err != nil {
				return err
			}
			for position, credit := range credits {
				var personID int64
				if creditPeople[position] != "" {
					if err := b.queryRow(`SELECT id, name FROM people WHERE public_id = ?`, creditPeople[position]).Scan(&personID, &credit.Name); errors.Is(err, sql.ErrNoRows) {
						return invalid("%s: no such person", FieldCredits)
					} else if err != nil {
						return err
					}
				} else if personID, err = im.personID(credit); err != nil {
					return err
				}
				if err := b.insertCredit(id, position, personID, credit.Role, credit.Character); err != nil {
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
	Continuing  bool // a show that has not ended
	ExternalIDs []ExternalID
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
	for i := range items {
		items[i].ExternalIDs, err = queryAll(ctx, tx, `SELECT provider, external_id FROM item_external_ids WHERE item_id = ?`,
			[]any{items[i].ID}, func(e *ExternalID) []any { return []any{&e.Provider, &e.ID} })
		if err != nil {
			return nil, err
		}
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

// creditRoleByName reads a role as the editor names it.
var creditRoleByName = func() map[string]CreditRole {
	out := map[string]CreditRole{}
	for role, name := range creditRoleNames {
		out[name] = role
	}
	return out
}()
