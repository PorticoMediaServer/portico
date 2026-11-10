package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// A library's collections are items of kind collection; the titles each holds are its
// item_relations rows of kind collection_member, item_id the collection and related_item_id
// the title, in position order. The owner and administrators make and change them. A person's
// own collections are lists, not these.

// maxCollectionTitle is the editor's limit for a short field.
const maxCollectionTitle = 300

// lockedTitle and lockedMembers are what an owner's rename and membership change lock on a
// collection, so automatic grouping from the provider keeps the owner's choice.
const (
	lockedTitle   = "title"
	lockedMembers = "members"
)

// CollectionEdit changes a library's collection. Every part is optional and safe to repeat:
// adding a title the collection holds, or removing one it does not, changes nothing.
type CollectionEdit struct {
	Title  *string
	Add    []string // public ids of titles, added at the end in this order
	Remove []string
	// Order puts these members first, in this order; the others follow in their own order.
	Order []string
}

// NewCollection makes a collection in a library.
type NewCollection struct {
	Viewer  Viewer
	Library string // public id
	Title   string
	Items   []string // public ids, in order
	// IdempotencyKey and RequestHash make a repeated request return the collection it made.
	IdempotencyKey string
	RequestHash    string
}

func collectionTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > maxCollectionTitle {
		return "", invalid("title: 1 to %d characters", maxCollectionTitle)
	}
	for _, r := range title {
		if r == utf8.RuneError || r < ' ' {
			return "", invalid("title: has a character that cannot be shown")
		}
	}
	return title, nil
}

// CreateCollection makes a library's collection holding the given titles and returns its
// public id. The viewer must see the library and every title; a title of another library, a
// season or episode, or another collection is refused.
func (d *DB) CreateCollection(ctx context.Context, n NewCollection) (string, error) {
	title, err := collectionTitle(n.Title)
	if err != nil {
		return "", err
	}
	var publicID string
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		if publicID, err = createdBefore(tx, n.Viewer.UserID, n.IdempotencyKey, n.RequestHash); err != nil || publicID != "" {
			return err
		}
		acc, err := loadAccess(ctx, tx, n.Viewer)
		if err != nil {
			return err
		}
		var library int64
		var language sql.NullString
		err = tx.QueryRow(`SELECT id, language FROM libraries l WHERE public_id = ? AND `+acc.libraryVisible("l.id"), n.Library).Scan(&library, &language)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		b := newBatchTx(tx)
		defer b.close()
		now := time.Now()
		publicID = NewID("itm")
		id, err := b.insertItem(itemRow{ImportItem: &ImportItem{Kind: KindCollection, Title: title, Language: language.String},
			libraryID: library, publicID: publicID, addedAt: now.UnixMilli()})
		if err != nil {
			return err
		}
		if _, err := b.exec(`UPDATE items SET locked_fields = ? WHERE id = ?`, lockList(sql.NullString{}, lockedTitle, lockedMembers), id); err != nil {
			return err
		}
		if err := addMembers(ctx, b, acc, library, id, n.Items); err != nil {
			return err
		}
		if err := appendChange(tx, Change{At: now.UnixMilli(), Topic: TopicItem, Op: OpCreated, RefID: id, LibraryID: library}); err != nil {
			return err
		}
		return rememberCreated(tx, n.Viewer.UserID, n.IdempotencyKey, n.RequestHash, publicID, now)
	})
	return publicID, err
}

// EditCollection renames a library's collection, adds and removes titles and puts them in
// order, in one transaction. What it changes is locked against automatic grouping.
func (d *DB) EditCollection(ctx context.Context, v Viewer, collection string, edit CollectionEdit) error {
	var title string
	if edit.Title != nil {
		var err error
		if title, err = collectionTitle(*edit.Title); err != nil {
			return err
		}
	}
	return d.write(ctx, User, func(tx *sql.Tx) error {
		b := newBatchTx(tx)
		defer b.close()
		acc, id, library, locked, err := findCollection(ctx, b, v, collection)
		if err != nil {
			return err
		}
		sets, args := `updated_at = ?, revision = revision + 1`, []any{time.Now().UnixMilli()}
		var locks []string
		if edit.Title != nil {
			var language sql.NullString
			if err := b.queryRow(`SELECT language FROM libraries WHERE id = ?`, library).Scan(&language); err != nil {
				return err
			}
			sets += `, title = ?, sort_title = ?`
			args = append(args, title, sorttext.Key(title, "", language.String))
			locks = append(locks, lockedTitle)
		}
		if len(edit.Add)+len(edit.Remove)+len(edit.Order) > 0 {
			locks = append(locks, lockedMembers)
		}
		sets += `, locked_fields = ?`
		args = append(args, lockList(locked, locks...))
		if _, err := b.exec(`UPDATE items SET `+sets+` WHERE id = ?`, append(args, id)...); err != nil {
			return err
		}
		for _, publicID := range edit.Remove {
			member, err := memberID(ctx, b, acc, publicID)
			if err != nil {
				return err
			}
			if _, err := b.exec(`DELETE FROM item_relations WHERE item_id = ? AND kind = ? AND related_item_id = ?`,
				id, RelationCollectionMember, member); err != nil {
				return err
			}
		}
		if err := addMembers(ctx, b, acc, library, id, edit.Add); err != nil {
			return err
		}
		if len(edit.Order) > 0 {
			if err := orderMembers(ctx, b, acc, id, edit.Order); err != nil {
				return err
			}
		}
		return appendChange(tx, Change{At: time.Now().UnixMilli(), Topic: TopicItem, Op: OpUpdated, RefID: id, LibraryID: library})
	})
}

// DeleteCollection removes a library's collection. The titles it held stay.
func (d *DB) DeleteCollection(ctx context.Context, v Viewer, collection string) error {
	return d.write(ctx, User, func(tx *sql.Tx) error {
		b := newBatchTx(tx)
		defer b.close()
		_, id, library, _, err := findCollection(ctx, b, v, collection)
		if err != nil {
			return err
		}
		return removeItem(b, library, id, time.Now().UnixMilli())
	})
}

// findCollection reads a library's collection the viewer may see, inside the write.
func findCollection(ctx context.Context, b *batchTx, v Viewer, publicID string) (acc access, id, library int64, locked sql.NullString, err error) {
	if acc, err = loadAccess(ctx, b.tx, v); err != nil {
		return
	}
	err = b.tx.QueryRowContext(ctx, `SELECT i.id, i.library_id, i.locked_fields FROM items i WHERE i.public_id = :id AND i.kind = :kind AND `+
		acc.visible("i"), sql.Named("id", publicID), sql.Named("kind", KindCollection)).Scan(&id, &library, &locked)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

// memberID is the id of a title the viewer may see, by its public id.
func memberID(ctx context.Context, b *batchTx, acc access, publicID string) (int64, error) {
	id, _, err := member(ctx, b, acc, publicID)
	return id, err
}

func member(ctx context.Context, b *batchTx, acc access, publicID string) (id, library int64, err error) {
	var kind ItemKind
	var parent sql.NullInt64
	err = b.tx.QueryRowContext(ctx, `SELECT i.id, i.library_id, i.kind, i.parent_id FROM items i WHERE i.public_id = :id AND `+acc.visible("i"),
		sql.Named("id", publicID)).Scan(&id, &library, &kind, &parent)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, 0, ErrNotFound
	case err != nil:
		return 0, 0, err
	case parent.Valid || kind == KindCollection || kind == KindExtra:
		return 0, 0, invalid("a collection holds films, shows and other titles of its library, not a %s", kind)
	}
	return id, library, nil
}

// addMembers adds titles at the end of a collection, in order. One it already holds stays where it is.
func addMembers(ctx context.Context, b *batchTx, acc access, library, collection int64, publicIDs []string) error {
	for _, publicID := range publicIDs {
		id, from, err := member(ctx, b, acc, publicID)
		if err != nil {
			return err
		}
		if from != library {
			return invalid("a collection holds titles of its own library only")
		}
		if _, err := b.exec(`INSERT INTO item_relations (item_id, kind, related_item_id, position)
			SELECT ?1, ?2, ?3, ifnull(max(position), 0) + 1 FROM item_relations WHERE item_id = ?1 AND kind = ?2
			ON CONFLICT DO NOTHING`, collection, RelationCollectionMember, id); err != nil {
			return err
		}
	}
	return nil
}

// orderMembers puts the named members first in the given order and the rest after them, as
// they were. A title the collection does not hold is passed over.
func orderMembers(ctx context.Context, b *batchTx, acc access, collection int64, publicIDs []string) error {
	current, err := queryInts(b.tx, `SELECT related_item_id FROM item_relations WHERE item_id = ? AND kind = ? ORDER BY position, related_item_id`,
		collection, RelationCollectionMember)
	if err != nil {
		return err
	}
	order := make([]int64, 0, len(current))
	for _, publicID := range publicIDs {
		id, err := memberID(ctx, b, acc, publicID)
		if err != nil {
			return err
		}
		if slices.Contains(current, id) && !slices.Contains(order, id) {
			order = append(order, id)
		}
	}
	for _, id := range current {
		if !slices.Contains(order, id) {
			order = append(order, id)
		}
	}
	for n, id := range order {
		if _, err := b.exec(`UPDATE item_relations SET position = ? WHERE item_id = ? AND kind = ? AND related_item_id = ?`,
			n+1, collection, RelationCollectionMember, id); err != nil {
			return err
		}
	}
	return nil
}

// lockList is items.locked_fields with these names added.
func lockList(locked sql.NullString, names ...string) any {
	var list []string
	if locked.Valid {
		json.Unmarshal([]byte(locked.String), &list)
	}
	for _, name := range names {
		if !slices.Contains(list, name) {
			list = append(list, name)
		}
	}
	if len(list) == 0 {
		return nil
	}
	slices.Sort(list)
	raw, _ := json.Marshal(list)
	return string(raw)
}
