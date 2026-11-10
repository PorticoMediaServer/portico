package db

import (
	"database/sql"
	"errors"
	"fmt"
	"path"
	"strconv"

	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// FolderKey is the key that says a folder is a title's. See KeyFolder.
func FolderKey(pathID int64, rel string) ItemKey {
	return ItemKey{Kind: KeyFolder, Key: strconv.FormatInt(pathID, 10) + "/" + rel}
}

// FileKey is the key that says a file plays a title; n is 0, or the place of the episode or
// track among several in one file. See KeyFile.
func FileKey(pathID int64, rel string, n int) ItemKey {
	key := strconv.FormatInt(pathID, 10) + "/" + rel
	if n > 0 {
		key += "#" + strconv.Itoa(n)
	}
	return ItemKey{Kind: KeyFile, Key: key}
}

// ImportMove says a stored file is now at another place: the same size and modified time
// where its old place has nothing. The file keeps its row, everything read from it and its title.
type ImportMove struct {
	FileID int64
	To     FileRef
}

func (im *importer) moveFile(move ImportMove) error {
	dir, err := im.dirID(move.To.Dir)
	if err != nil {
		return err
	}
	var oldPath int64
	var oldDir, oldName string
	err = im.queryRow(`SELECT d.library_path_id, d.rel_path, f.name FROM files f JOIN directories d ON d.id = f.directory_id WHERE f.id = ?`,
		move.FileID).Scan(&oldPath, &oldDir, &oldName)
	if err != nil {
		return err
	}
	if _, err := im.exec(`UPDATE files SET directory_id = ?, name = ?, seen_scan = ?, missing_since_at = NULL WHERE id = ?`,
		dir, move.To.Name, im.batch.Scan, move.FileID); err != nil {
		return err
	}
	// The old place no longer plays the title: a different file put there later is a new title.
	old := FileKey(oldPath, path.Join(oldDir, oldName), 0).Key
	if _, err := im.exec(`DELETE FROM item_keys WHERE library_id = ? AND kind = ? AND (key = ? OR key > ? AND key < ?)`,
		im.batch.LibraryID, KeyFile, old, old+"#", old+"$"); err != nil {
		return err
	}
	delete(im.files, move.To)
	f, err := im.file(move.To)
	if err != nil {
		return err
	}
	f.touched, f.moved = true, true
	im.changedFiles = append(im.changedFiles, f.id)
	return nil
}

// goneFile handles a file its folder no longer lists. A file that plays a title is kept and
// marked missing, so the title can wait in the trash and come back. Any other file is forgotten.
func (im *importer) goneFile(ref FileRef) error {
	dir, err := im.dirID(ref.Dir)
	if err != nil {
		return err
	}
	return goneFiles(im.batchTx, im.now, &im.changedFiles, `directory_id = ? AND name = ?`, dir, ref.Name)
}

func goneFiles(b *batchTx, now int64, changed *[]int64, where string, args ...any) error {
	rows, err := b.tx.Query(`SELECT id, EXISTS (SELECT 1 FROM media_parts p WHERE p.file_id = files.id) FROM files
		WHERE missing_since_at IS NULL AND `+where, args...)
	if err != nil {
		return err
	}
	var plays, others []int64
	for rows.Next() {
		var id int64
		var played bool
		if err := rows.Scan(&id, &played); err != nil {
			rows.Close()
			return err
		}
		if played {
			plays = append(plays, id)
		} else {
			others = append(others, id)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range plays {
		if _, err := b.exec(`UPDATE files SET missing_since_at = ? WHERE id = ?`, now, id); err != nil {
			return err
		}
	}
	for _, id := range others {
		// A picture that was this file goes with it, and the next best is shown.
		shown, err := shownPictures(b, id)
		if err != nil {
			return err
		}
		if _, err := b.exec(`DELETE FROM files WHERE id = ?`, id); err != nil {
			return err
		}
		for _, picture := range shown {
			if err := selectPicture(b, picture.item, picture.role); err != nil {
				return err
			}
		}
	}
	*changed = append(*changed, plays...)
	return nil
}

// errNothingToReread: an item that only re-reads local details names a parent that is not stored.
var errNothingToReread = errors.New("nothing to read again")

// place finds an item's parent and turns its keys into the ones stored: a ChildKey becomes a
// name key under the parent.
func (im *importer) place(it *ImportItem) (*importedItem, []ItemKey, error) {
	var parent *importedItem
	switch {
	case it.ParentRow > 0:
		if it.ParentRow > len(im.rows) {
			return nil, nil, invalid("item %q names a parent that comes after it in the batch", it.Title)
		}
		parent = im.rows[it.ParentRow-1]
	case it.Parent != nil:
		found, err := im.itemByKey(*it.Parent)
		if err != nil {
			return nil, nil, err
		}
		if found == nil && it.Reread && len(it.Versions) == 0 {
			return nil, nil, errNothingToReread
		}
		if found == nil {
			return nil, nil, invalid("item %q names a parent that is not in this batch or an earlier one", it.Title)
		}
		parent = found
	}
	keys := it.Keys
	if it.ChildKey != "" {
		if parent == nil {
			return nil, nil, invalid("item %q has a place under a parent and no parent", it.Title)
		}
		keys = append(append([]ItemKey(nil), keys...), ItemKey{Kind: KeyName, Key: fmt.Sprintf("%d/%s", parent.id, it.ChildKey)})
	}
	return parent, keys, nil
}

// movedItem returns the stored item a moved file brings with it, renamed to what the new
// names say, or nil when the item's files did not move.
func (im *importer) movedItem(it *ImportItem, parent *importedItem) (*importedItem, error) {
	var id int64
	switch {
	case it.Follow != nil:
		f := im.files[*it.Follow]
		if f == nil || !f.moved {
			return nil, nil
		}
		// The container of this kind above the item the file plays.
		err := im.queryRow(`SELECT c.id FROM media_parts p JOIN media m ON m.id = p.media_id JOIN items i ON i.id = m.item_id
			JOIN items c ON c.id IN (i.parent_id, i.root_id) WHERE p.file_id = ? AND c.kind = ? LIMIT 1`, f.id, it.Kind).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if _, err := im.exec(`DELETE FROM item_keys WHERE item_id = ? AND kind = ?`, id, KeyFolder); err != nil {
			return nil, err
		}
	default:
		var err error
		if id, err = im.movedFrom(it); err != nil || id == 0 {
			return nil, err
		}
	}
	return im.rename(id, it, parent)
}

// movedFrom is the item a moved file played before this batch, or 0 when the item's file did
// not move. One file can play several items (two episodes): the one with this number.
func (im *importer) movedFrom(it *ImportItem) (int64, error) {
	if len(it.Versions) == 0 || len(it.Versions[0].Parts) == 0 {
		return 0, nil
	}
	part := it.Versions[0].Parts[0]
	f := im.files[part.File]
	if f == nil || !f.moved {
		return 0, nil
	}
	var id int64
	err := im.queryRow(`SELECT i.id FROM media_parts p JOIN media m ON m.id = p.media_id JOIN items i ON i.id = m.item_id
		WHERE p.file_id = ? AND p.position = 0 AND p.start_ms IS ? AND i.kind = ?
		ORDER BY i.index_number IS ? DESC, i.id LIMIT 1`, f.id, part.StartMS, it.Kind, it.IndexNumber).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// pinnedItem returns the item a moved file stays with because a person put it there: a title
// whose match the owner chose, or one that has no name or id key of its own, which is what a
// version split off by hand is.
func (im *importer) pinnedItem(it *ImportItem) (*importedItem, error) {
	id, err := im.movedFrom(it)
	if err != nil || id == 0 {
		return nil, err
	}
	item := &importedItem{id: id}
	var root, parent, rating sql.NullInt64
	var pinned bool
	err = im.queryRow(`SELECT root_id, parent_id, rating_age, match_state = ? OR NOT EXISTS (SELECT 1 FROM item_keys
		WHERE item_id = items.id AND kind IN (?, ?, ?)) FROM items WHERE id = ?`, MatchChosen, KeyName, KeyProvider, KeyEmbedded, id).
		Scan(&root, &parent, &rating, &pinned)
	if err != nil || !pinned {
		return nil, err
	}
	item.rootID, item.parentID = root.Int64, parent.Int64
	if rating.Valid {
		age := int(rating.Int64)
		item.ratingAge = &age
	}
	return item, nil
}

func fileKeys(keys []ItemKey) []ItemKey {
	var out []ItemKey
	for _, key := range keys {
		if key.Kind == KeyFile {
			out = append(out, key)
		}
	}
	return out
}

// rename gives a stored item the names its files now have. Its details are only rewritten
// while nobody has matched or edited it; its place under a parent always follows the files.
func (im *importer) rename(id int64, it *ImportItem, parent *importedItem) (*importedItem, error) {
	item := &importedItem{id: id}
	var root, oldParent, rating, locked sql.NullInt64
	var match MatchState
	err := im.queryRow(`SELECT root_id, parent_id, rating_age, match_state, locked_fields IS NOT NULL FROM items WHERE id = ?`, id).
		Scan(&root, &oldParent, &rating, &match, &locked)
	if err != nil {
		return nil, err
	}
	item.rootID, item.parentID = root.Int64, oldParent.Int64
	if rating.Valid {
		age := int(rating.Int64)
		item.ratingAge = &age
	}
	if match == MatchNone && locked.Int64 == 0 {
		if _, err := im.exec(`UPDATE items SET title = ?, sort_title = ?, year = ?, updated_at = ? WHERE id = ?`,
			it.Title, sorttext.Key(it.Title, it.SortTitle, it.Language), nz(it.Year), im.now, id); err != nil {
			return nil, err
		}
	}
	if parent != nil && (parent.id != item.parentID || it.IndexNumber != nil) {
		newRoot := parent.rootID
		if newRoot == 0 {
			newRoot = parent.id
		}
		if _, err := im.exec(`UPDATE items SET parent_id = ?, root_id = ?, index_number = ?, parent_index = ?, updated_at = ? WHERE id = ?`,
			parent.id, newRoot, it.IndexNumber, it.ParentIndex, im.now, id); err != nil {
			return nil, err
		}
		if parent.id != item.parentID {
			leaf := 0
			if it.Kind.Playable() {
				leaf = 1
			}
			for holder, by := range map[int64]int{item.parentID: -1, parent.id: 1} {
				im.count(holder).children += by
				im.count(holder).leaves += by * leaf
			}
			if item.rootID != item.parentID {
				im.count(item.rootID).leaves -= leaf
			}
			if newRoot != parent.id {
				im.count(newRoot).leaves += leaf
			}
			im.unsettled = append(im.unsettled, item.parentID)
			item.parentID, item.rootID = parent.id, newRoot
		}
	}
	return item, appendChange(im.tx, Change{At: im.now, Topic: TopicItem, Op: OpUpdated, RefID: id, LibraryID: im.batch.LibraryID})
}

// relate ties an extra to the title it belongs to.
func (im *importer) relate(id int64, it *ImportItem) error {
	if len(it.ExtraOf) == 0 {
		return nil
	}
	owner, err := im.findItem(it.ExtraOf)
	if err != nil || owner == nil {
		return err
	}
	if err := checkCode("item_relations.kind", relationKindNames, it.ExtraKind); err != nil {
		return err
	}
	_, err = im.exec(`INSERT INTO item_relations (item_id, kind, related_item_id) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, owner.id, it.ExtraKind, id)
	return err
}

// ensureParts rewrites a stored version's parts when the files that make it up changed: the
// second half of a split film turned up.
func (im *importer) ensureParts(mediaID int64, v *ImportVersion, facts versionFacts) error {
	rows, err := im.tx.Query(`SELECT file_id FROM media_parts WHERE media_id = ? ORDER BY position`, mediaID)
	if err != nil {
		return err
	}
	var stored []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		stored = append(stored, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	same := len(stored) == len(facts.fileIDs)
	for i := 0; same && i < len(stored); i++ {
		same = stored[i] == facts.fileIDs[i]
	}
	if same {
		return nil
	}
	if _, err := im.exec(`DELETE FROM media_parts WHERE media_id = ?`, mediaID); err != nil {
		return err
	}
	for i, part := range v.Parts {
		if _, err := im.exec(`INSERT INTO media_parts (media_id, position, file_id, start_ms, end_ms) VALUES (?, ?, ?, ?, ?)`,
			mediaID, i, facts.fileIDs[i], part.StartMS, part.EndMS); err != nil {
			return err
		}
	}
	_, err = im.exec(`UPDATE media SET size = ?, duration_ms = ? WHERE id = ?`, nz(facts.size), nz(facts.durationMS), mediaID)
	return err
}

// settle works out again what is available, for every item whose files came, went or moved.
func (im *importer) settle() error {
	items := append([]int64(nil), im.unsettled...)
	for _, file := range im.changedFiles {
		rows, err := im.tx.Query(`SELECT DISTINCT m.item_id FROM media_parts p JOIN media m ON m.id = p.media_id WHERE p.file_id = ?`, file)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			items = append(items, id)
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	for _, id := range items {
		if err := settleItem(im.batchTx, im.batch.LibraryID, id, im.now); err != nil {
			return err
		}
	}
	return nil
}
