package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// LibrarySource is where a library's details come from.
type LibrarySource struct {
	ID       int64
	Kind     string
	Provider string // "" is the default for the kind; "local" is local metadata only
	Language string
	Region   string
	Lyrics   string // a music library's lyrics source: "" for its files only, or LyricsLRCLIB
}

// ProviderLocal is the provider of a library that uses local metadata only.
const ProviderLocal = "local"

// LibrarySource returns how one library gets its details.
func (d *DB) LibrarySource(ctx context.Context, libraryID int64) (LibrarySource, error) {
	src := LibrarySource{ID: libraryID}
	var options sql.NullString
	err := d.read.QueryRowContext(ctx, `SELECT kind, ifnull(metadata_provider, ''), ifnull(language, ''), options FROM libraries WHERE id = ?`,
		libraryID).Scan(&src.Kind, &src.Provider, &src.Language, &options)
	if errors.Is(err, sql.ErrNoRows) {
		return src, ErrNotFound
	}
	var region struct {
		Region string `json:"region"`
		Lyrics string `json:"lyrics"`
	}
	if options.Valid {
		json.Unmarshal([]byte(options.String), &region)
	}
	src.Region, src.Lyrics = region.Region, region.Lyrics
	return src, err
}

// MatchItem is a top-level title no provider has been asked about yet.
type MatchItem struct {
	ID          int64
	Kind        ItemKind
	Title       string
	Year        int
	ExternalIDs []ExternalID // ids its names or an NFO file gave
}

// ToMatch lists a library's top-level titles of the given kinds that no provider has been
// asked about, a page at a time in id order: pass the last id of one page to get the next.
func (d *DB) ToMatch(ctx context.Context, libraryID int64, kinds []ItemKind, after int64, limit int) ([]MatchItem, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	list := make([]int64, len(kinds))
	for i, kind := range kinds {
		list[i] = int64(kind)
	}
	// The condition is written as the items_to_match index has it, so the read is the index.
	items, err := queryAll(ctx, tx, `SELECT id, kind, title, ifnull(year, 0) FROM items WHERE library_id = ? AND match_state = 0
		AND parent_id IS NULL AND id > ? AND kind IN `+intList(list)+` ORDER BY id LIMIT ?`, []any{libraryID, after, limit},
		func(m *MatchItem) []any { return []any{&m.ID, &m.Kind, &m.Title, &m.Year} })
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

// ChildNumbers lists the numbers of the containers directly under a title: a show's seasons.
func (d *DB) ChildNumbers(ctx context.Context, itemID int64) ([]int, error) {
	rows, err := d.read.QueryContext(ctx, `SELECT index_number FROM items WHERE parent_id = ? AND index_number IS NOT NULL
		AND kind IN (?, ?) ORDER BY index_number`, itemID, KindSeason, KindAlbum)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ItemDetails is what a provider said about one title. An empty field or list means the
// provider did not say, and what the title has is kept.
type ItemDetails struct {
	// Match is MatchFound for a match the server made and MatchChosen for one a person
	// picked. Zero leaves the match as it is: a refresh.
	Match MatchState
	// ExternalIDs are the title's ids at the provider and elsewhere. ProviderKey is the key
	// the title is found by from now on ("tmdb:movie:603"), or "".
	ExternalIDs     []ExternalID
	ProviderKey     string
	Title           string
	OriginalTitle   string
	Summary         string
	Tagline         string
	Year            int
	ReleaseDate     string
	ContentRating   string
	RatingAge       *int // the age ContentRating stands for; nil when it is not known
	CommunityRating float64
	Tags            []TagName // genres, studios, networks and countries; each kind given replaces that list
	Credits         []ImportCredit
	// Index is, for a child, its number under its parent: an episode's number.
	Index int
}

// ApplyDetails stores what a provider said about a title, in one transaction. It is the one
// place that decides what a provider may change: never a field or list a person locked, never
// one the title's own files gave, never labels or anyone's state, and nothing is blanked
// because the provider did not say. Background work passes Background.
func (d *DB) ApplyDetails(ctx context.Context, p Priority, itemID int64, details ItemDetails) error {
	return d.write(ctx, p, func(tx *sql.Tx) error {
		b := newBatchTx(tx)
		defer b.close()
		return applyDetails(b, itemID, &details, time.Now().UnixMilli())
	})
}

// ApplyChildren stores what a provider said about the children of a title in one group: the
// episodes of one season of a show. A child the library does not have is passed over.
func (d *DB) ApplyChildren(ctx context.Context, p Priority, itemID int64, kind ItemKind, group int, children []ItemDetails) error {
	return d.write(ctx, p, func(tx *sql.Tx) error {
		b := newBatchTx(tx)
		defer b.close()
		now := time.Now().UnixMilli()
		for i := range children {
			var id int64
			err := b.queryRow(`SELECT id FROM items WHERE root_id = ? AND kind = ? AND parent_index = ? AND index_number = ? ORDER BY id LIMIT 1`,
				itemID, kind, group, children[i].Index).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if err := applyDetails(b, id, &children[i], now); err != nil {
				return err
			}
		}
		return nil
	})
}

func applyDetails(b *batchTx, itemID int64, d *ItemDetails, now int64) error {
	var library int64
	var kind ItemKind
	var locked, local, language sql.NullString
	var root sql.NullInt64
	err := b.queryRow(`SELECT i.library_id, i.kind, i.root_id, i.locked_fields, i.local_fields, l.language FROM items i
		JOIN libraries l ON l.id = i.library_id WHERE i.id = ?`, itemID).Scan(&library, &kind, &root, &locked, &local, &language)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	sources := fieldSources(local)
	keep := keptFields(locked, sources)
	sets, args := "refreshed_at = ?, updated_at = ?, revision = revision + 1", []any{now, now}
	// A field a person edited and later unlocked is the provider's again from this answer on.
	unlocked := false
	for field, source := range sources {
		if source == SourceEdit && !keep[field] {
			delete(sources, field)
			unlocked = true
		}
	}
	if unlocked {
		sets += ", local_fields = ?"
		args = append(args, sourceJSON(sources))
	}
	set := func(field, column string, value any, said bool) {
		if said && !keep[field] {
			sets += ", " + column + " = ?"
			args = append(args, value)
		}
	}
	set(FieldTitle, "title", d.Title, d.Title != "")
	if !keep[FieldTitle] {
		set(FieldSortTitle, "sort_title", sorttext.Key(d.Title, "", language.String), d.Title != "")
	}
	set(FieldOriginalTitle, "original_title", d.OriginalTitle, d.OriginalTitle != "")
	set(FieldSummary, "summary", d.Summary, d.Summary != "")
	set(FieldTagline, "tagline", d.Tagline, d.Tagline != "")
	set(FieldYear, "year", d.Year, d.Year != 0)
	set(FieldReleaseDate, "release_date", d.ReleaseDate, d.ReleaseDate != "")
	set(FieldContentRating, "content_rating", d.ContentRating, d.ContentRating != "")
	set(FieldContentRating, "rating_age", d.RatingAge, d.ContentRating != "")
	set(FieldRating, "community_rating", d.CommunityRating, d.CommunityRating != 0)
	if d.Match != MatchNone && !keep[FieldMatch] {
		// A match a person chose is never made the server's own again.
		sets += ", match_state = CASE WHEN match_state = ? AND ? = ? THEN match_state ELSE ? END"
		args = append(args, MatchChosen, d.Match, MatchFound, d.Match)
	}
	if _, err := b.exec(`UPDATE items SET `+sets+` WHERE id = ?`, append(args, itemID)...); err != nil {
		return err
	}
	// An episode with no rating of its own uses its show's.
	if d.ContentRating != "" && !keep[FieldContentRating] && !root.Valid {
		if _, err := b.exec(`UPDATE items SET rating_age = ? WHERE root_id = ? AND content_rating IS NULL`, d.RatingAge, itemID); err != nil {
			return err
		}
	}
	for _, ext := range d.ExternalIDs {
		if _, err := b.exec(`INSERT INTO item_external_ids (item_id, provider, external_id) VALUES (?, ?, ?)
			ON CONFLICT (item_id, provider) DO UPDATE SET external_id = excluded.external_id`, itemID, ext.Provider, ext.ID); err != nil {
			return err
		}
	}
	if d.ProviderKey != "" && !keep[FieldMatch] {
		if _, err := b.exec(`INSERT INTO item_keys (library_id, kind, key, item_id) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`,
			library, KeyProvider, d.ProviderKey, itemID); err != nil {
			return err
		}
	}
	// The importer's lookups of tags and people by name, so a provider's genre or person is
	// the same row a scan would make.
	im := &importer{batchTx: b, now: now, tags: map[TagName]int64{}, people: map[string]int64{}}
	given := map[string]bool{}
	for _, tag := range d.Tags {
		field, known := tagFields[tag.Kind]
		if !known || keep[field] {
			continue
		}
		if !given[tag.Kind] {
			given[tag.Kind] = true
			if _, err := b.exec(`DELETE FROM item_tags WHERE item_id = ? AND tag_id IN (SELECT id FROM tags WHERE kind = ?)`, itemID, tag.Kind); err != nil {
				return err
			}
		}
		tagID, err := im.tagID(tag)
		if err != nil {
			return err
		}
		if _, err := b.exec(`INSERT INTO item_tags (item_id, tag_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, itemID, tagID); err != nil {
			return err
		}
	}
	if len(d.Credits) > 0 && !keep[FieldCredits] {
		if _, err := b.exec(`DELETE FROM credits WHERE item_id = ?`, itemID); err != nil {
			return err
		}
		for position, credit := range d.Credits {
			personID, err := im.personID(credit)
			if err != nil {
				return err
			}
			if err := b.insertCredit(itemID, position, personID, credit.Role, credit.Character); err != nil {
				return err
			}
		}
	}
	return appendChange(b.tx, Change{At: now, Topic: TopicItem, Op: OpUpdated, RefID: itemID, LibraryID: library})
}

// SetUnmatched records that a provider was asked about a title and gave no confident answer:
// the title joins its library's Unmatched items list with the reason and the closest matches
// (JSON, kept on the title so the list is read without asking the provider again).
func (d *DB) SetUnmatched(ctx context.Context, p Priority, itemID int64, reason, candidates string) error {
	return d.write(ctx, p, func(tx *sql.Tx) error {
		if candidates == "" {
			candidates = "[]"
		}
		_, err := tx.Exec(`UPDATE items SET match_state = ?, refreshed_at = ?,
			extra = json_set(ifnull(extra, '{}'), '$.unmatched', json_object('reason', ?, 'matches', json(?)))
			WHERE id = ? AND match_state = ?`, MatchNeedsAttention, time.Now().UnixMilli(), reason, candidates, itemID, MatchNone)
		return err
	})
}

// UnmatchedItem is one line of a library's Unmatched items list.
type UnmatchedItem struct {
	PublicID string
	Kind     ItemKind
	Title    string
	Year     int
	Reason   string
	Matches  string // the closest matches as JSON, as SetUnmatched was given them

	id        int64
	sortTitle string
}

// Unmatched lists the titles of a library that a provider was asked about without a confident
// answer, by title. after is the last line of the page before, or nil for the first page. It
// reads the items_unmatched index and nothing else, however large the library.
func (d *DB) Unmatched(ctx context.Context, libraryID int64, after *UnmatchedItem, limit int) ([]UnmatchedItem, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// "Kept as it is" titles share the index and are passed over.
	where, args := `library_id = ? AND match_state IN (3, 4) AND match_state = 3`, []any{libraryID}
	if after != nil {
		where += ` AND (sort_title, id) > (?, ?)`
		args = append(args, after.sortTitle, after.id)
	}
	return queryAll(ctx, tx, `SELECT id, sort_title, public_id, kind, title, ifnull(year, 0), ifnull(extra ->> '$.unmatched.reason', ''),
		ifnull(extra ->> '$.unmatched.matches', '[]') FROM items WHERE `+where+` ORDER BY sort_title, id LIMIT ?`, append(args, limit),
		func(u *UnmatchedItem) []any {
			return []any{&u.id, &u.sortTitle, &u.PublicID, &u.Kind, &u.Title, &u.Year, &u.Reason, &u.Matches}
		})
}
