package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

// What a History entry says was done (metadata_history.kind).
type HistoryKind int

const (
	HistoryFields  HistoryKind = 1 // fields, lists or locks edited
	HistoryArtwork HistoryKind = 2 // a picture chosen, uploaded or removed
	HistoryMatch   HistoryKind = 3 // a match chosen
	HistoryUndo    HistoryKind = 4 // the title put back to before an earlier change
)

var historyKindNames = map[HistoryKind]string{HistoryFields: "fields_edited", HistoryArtwork: "artwork_chosen",
	HistoryMatch: "match_changed", HistoryUndo: "undone"}

func (k HistoryKind) String() string { return historyKindNames[k] }

// historyKept is how many changes people made a title's History keeps.
const historyKept = 20

// snapshot is a title's details, lists, locks, chosen pictures and match as a person's change
// found them: what "Undo to before this" puts back. Labels are in it: they are the owner's
// own and an undo of an edit to them puts them back too. Everyone's state is not.
type snapshot struct {
	Columns     map[string]any `json:"columns"`
	ExternalIDs []ExternalID   `json:"externalIds"`
	Keys        []string       `json:"providerKeys"`
	Tags        []TagName      `json:"tags"`
	Credits     []snapCredit   `json:"credits"`
	Pictures    []int64        `json:"lockedPictures"` // images rows locked as the role's choice
}

type snapCredit struct {
	Person    int64      `json:"person"`
	Role      CreditRole `json:"role"`
	Character string     `json:"character,omitempty"`
}

// snapshotColumns are the items columns a person's change can alter.
var snapshotColumns = []string{"title", "sort_title", "original_title", "summary", "tagline", "edition", "year", "release_date",
	"content_rating", "rating_age", "community_rating", "locked_fields", "local_fields", "match_state"}

// snapshotTags are the tag kinds a snapshot keeps: everything but moods, which no person edits.
var snapshotTags = []string{TagGenre, TagStudio, TagNetwork, TagCountry, TagLabel}

func takeSnapshot(b *batchTx, itemID int64) (snapshot, error) {
	s := snapshot{Columns: map[string]any{}}
	values := make([]any, len(snapshotColumns))
	ptrs := make([]any, len(values))
	for i := range values {
		ptrs[i] = &values[i]
	}
	query := `SELECT `
	for i, column := range snapshotColumns {
		if i > 0 {
			query += ", "
		}
		query += column
	}
	if err := b.queryRow(query+` FROM items WHERE id = ?`, itemID).Scan(ptrs...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s, ErrNotFound
		}
		return s, err
	}
	for i, column := range snapshotColumns {
		if raw, ok := values[i].([]byte); ok {
			values[i] = string(raw)
		}
		s.Columns[column] = values[i]
	}
	var err error
	ctx := context.Background()
	if s.ExternalIDs, err = queryAll(ctx, b.tx, `SELECT provider, external_id FROM item_external_ids WHERE item_id = ? ORDER BY provider`,
		[]any{itemID}, func(e *ExternalID) []any { return []any{&e.Provider, &e.ID} }); err != nil {
		return s, err
	}
	if s.Keys, err = queryAll(ctx, b.tx, `SELECT key FROM item_keys WHERE item_id = ? AND kind = ? ORDER BY key`, []any{itemID, KeyProvider},
		func(k *string) []any { return []any{k} }); err != nil {
		return s, err
	}
	if s.Tags, err = queryAll(ctx, b.tx, `SELECT t.kind, t.name FROM item_tags it JOIN tags t ON t.id = it.tag_id WHERE it.item_id = ?
		AND t.kind IN ('genre', 'studio', 'network', 'country', 'label') ORDER BY t.kind, t.id`, []any{itemID},
		func(t *TagName) []any { return []any{&t.Kind, &t.Name} }); err != nil {
		return s, err
	}
	if s.Credits, err = queryAll(ctx, b.tx, `SELECT person_id, role, ifnull(character, '') FROM credits WHERE item_id = ? ORDER BY position`,
		[]any{itemID}, func(c *snapCredit) []any { return []any{&c.Person, &c.Role, &c.Character} }); err != nil {
		return s, err
	}
	s.Pictures, err = queryInts(b.tx, `SELECT id FROM images WHERE item_id = ? AND locked = 1 ORDER BY id`, itemID)
	return s, err
}

// remember records, inside a person's change, the title as it was before it, and keeps only
// the newest entries.
func remember(b *batchTx, itemID int64, kind HistoryKind, now int64) error {
	s, err := takeSnapshot(b, itemID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if _, err := b.exec(`INSERT INTO metadata_history (item_id, at, kind, before) VALUES (?, ?, ?, ?)`, itemID, now, kind, string(raw)); err != nil {
		return err
	}
	_, err = b.exec(`DELETE FROM metadata_history WHERE item_id = ?1 AND id < (SELECT min(id) FROM
		(SELECT id FROM metadata_history WHERE item_id = ?1 ORDER BY id DESC LIMIT ?2))`, itemID, historyKept)
	return err
}

// HistoryEntry is one line of a title's History.
type HistoryEntry struct {
	ID   string
	At   int64
	Kind HistoryKind
}

// MetadataHistory lists the changes people made to a title, newest first.
func (d *DB) MetadataHistory(ctx context.Context, publicID string) ([]HistoryEntry, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM items WHERE public_id = ?`, publicID).Scan(&id); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	return queryAll(ctx, tx, `SELECT id, at, kind FROM metadata_history WHERE item_id = ? ORDER BY id DESC`, []any{id},
		func(h *HistoryEntry) []any { return []any{&h.ID, &h.At, &h.Kind} })
}

// UndoMetadata puts a title's details, lists, locks, chosen pictures and match back to how they
// were before one of its History entries, in one transaction, and adds the undo to History.
// What the title's files gave since is left as it is. It returns the title's new revision.
func (d *DB) UndoMetadata(ctx context.Context, publicID, entryID string) (int64, error) {
	entry, err := strconv.ParseInt(entryID, 10, 64)
	if err != nil {
		return 0, ErrNotFound
	}
	var revision int64
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		b := newBatchTx(tx)
		defer b.close()
		var id, library int64
		var root sql.NullInt64
		var raw string
		err := b.queryRow(`SELECT i.id, i.library_id, i.root_id, h.before FROM metadata_history h JOIN items i ON i.id = h.item_id
			WHERE i.public_id = ? AND h.id = ?`, publicID, entry).Scan(&id, &library, &root, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		var s snapshot
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			return err
		}
		now := time.Now().UnixMilli()
		if err := remember(b, id, HistoryUndo, now); err != nil {
			return err
		}
		sets, args := "updated_at = ?, revision = revision + 1", []any{now}
		for _, column := range snapshotColumns {
			if value, ok := s.Columns[column]; ok {
				sets += ", " + column + " = ?"
				args = append(args, value)
			}
		}
		if _, err := b.exec(`UPDATE items SET `+sets+` WHERE id = ?`, append(args, id)...); err != nil {
			return err
		}
		// Episodes with no rating of their own follow their show's again.
		if !root.Valid {
			if _, err := b.exec(`UPDATE items SET rating_age = (SELECT rating_age FROM items WHERE id = ?1) WHERE root_id = ?1
				AND content_rating IS NULL`, id); err != nil {
				return err
			}
		}
		steps := []string{`DELETE FROM item_external_ids WHERE item_id = ?`, `DELETE FROM item_keys WHERE item_id = ? AND kind = ` + strconv.Itoa(int(KeyProvider)),
			`DELETE FROM item_tags WHERE item_id = ? AND tag_id IN (SELECT id FROM tags WHERE kind IN ('genre', 'studio', 'network', 'country', 'label'))`,
			`DELETE FROM credits WHERE item_id = ?`, `UPDATE images SET locked = 0 WHERE item_id = ? AND locked = 1`}
		for _, step := range steps {
			if _, err := b.exec(step, id); err != nil {
				return err
			}
		}
		for _, ext := range s.ExternalIDs {
			if _, err := b.exec(`INSERT INTO item_external_ids (item_id, provider, external_id) VALUES (?, ?, ?)`, id, ext.Provider, ext.ID); err != nil {
				return err
			}
		}
		for _, key := range s.Keys {
			// Another title that has taken the id since keeps it: the server never merges by itself.
			if _, err := b.exec(`INSERT INTO item_keys (library_id, kind, key, item_id) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`,
				library, KeyProvider, key, id); err != nil {
				return err
			}
		}
		im := &importer{batchTx: b, now: now, tags: map[TagName]int64{}, people: map[string]int64{}}
		for _, tag := range s.Tags {
			tagID, err := im.tagID(tag)
			if err != nil {
				return err
			}
			if err := b.tagItem(id, tagID); err != nil {
				return err
			}
		}
		// The rating may be put back, on the title and on what under it has none of its own:
		// the copies of it on the tag rows follow, after the tags are written.
		if err := b.ratingChanged(id); err != nil {
			return err
		}
		position := 0
		for _, c := range s.Credits {
			var exists bool
			if err := b.queryRow(`SELECT EXISTS (SELECT 1 FROM people WHERE id = ?)`, c.Person).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				continue // a person no title holds any more has gone
			}
			if err := b.insertCredit(id, position, c.Person, c.Role, c.Character); err != nil {
				return err
			}
			position++
		}
		for _, picture := range s.Pictures {
			if _, err := b.exec(`UPDATE images SET locked = 1 WHERE id = ? AND item_id = ?`, picture, id); err != nil {
				return err
			}
		}
		// The chosen pictures are shown again, and every other role shows its best.
		if _, err := b.exec(`UPDATE images SET selected = locked WHERE item_id = ?1 AND role IN (SELECT role FROM images WHERE item_id = ?1
			AND locked = 1)`, id); err != nil {
			return err
		}
		roles, err := queryInts(b.tx, `SELECT DISTINCT role FROM images WHERE item_id = ?`, id)
		if err != nil {
			return err
		}
		for _, role := range roles {
			if err := selectPicture(b, id, ImageRole(role)); err != nil {
				return err
			}
		}
		if err := b.queryRow(`SELECT revision FROM items WHERE id = ?`, id).Scan(&revision); err != nil {
			return err
		}
		return appendChange(tx, Change{At: now, Topic: TopicItem, Op: OpUpdated, RefID: id, LibraryID: library})
	})
	return revision, err
}
