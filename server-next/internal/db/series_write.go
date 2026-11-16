package db

// A book's series are what its file says. A volume whose file stops naming a series leaves it
// and loses its number in it, and a series that holds nothing any more goes.

// rereadSeries brings a book read again up to date with the series its file names: the series
// the file named before are left (the one it names now is joined again after this, by relate),
// and its number is the one the file gives now, none when it names no series. A title under a
// show or a series takes its number with its place instead (rename).
func (im *importer) rereadSeries(id int64, it *ImportItem) error {
	series, err := im.queryInts(`SELECT item_id FROM item_relations WHERE related_item_id = ? AND kind = ?`, id, RelationSeriesMember)
	if err != nil {
		return err
	}
	if _, err := im.exec(`DELETE FROM item_relations WHERE related_item_id = ? AND kind = ?`, id, RelationSeriesMember); err != nil {
		return err
	}
	if err := im.collectionsRated(series...); err != nil {
		return err
	}
	_, err = im.exec(`UPDATE items SET index_number = ? WHERE id = ? AND parent_id IS NULL AND index_number IS NOT ?`,
		it.IndexNumber, id, it.IndexNumber)
	return err
}

// removeEmptySeries removes the library's series that hold nothing any more: no volume is in
// one and no issue is under it. A series is made with its first volume or issue, in the same
// batch, so one left empty is one whose last volume's file stopped naming it (reread) or went.
func (im *importer) removeEmptySeries() error {
	empty, err := im.queryInts(`SELECT id FROM items i WHERE i.library_id = ? AND i.kind = ? AND i.child_count = 0
		AND NOT EXISTS (SELECT 1 FROM item_relations r WHERE r.item_id = i.id)`, im.batch.LibraryID, KindSeries)
	if err != nil {
		return err
	}
	for _, id := range empty {
		if err := removeItem(im.batchTx, im.batch.LibraryID, id, im.now); err != nil {
			return err
		}
		for key, item := range im.items {
			if item.id == id {
				delete(im.items, key)
			}
		}
	}
	return nil
}
