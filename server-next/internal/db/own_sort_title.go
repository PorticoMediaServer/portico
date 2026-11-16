package db

// ownSortTitle keeps the sort title a person or an NFO file gave a title, as written, beside
// the sort key made from it (sort_title), so the editor can show it; "" when the sort key is
// made from the title alone.
func (b *batchTx) ownSortTitle(itemID int64, sortTitle string) error {
	_, err := b.exec(`UPDATE items SET extra = CASE WHEN ?1 = '' THEN nullif(json_remove(ifnull(extra, '{}'), '$.sortTitle'), '{}')
		ELSE json_set(ifnull(extra, '{}'), '$.sortTitle', ?1) END WHERE id = ?2`, sortTitle, itemID)
	return err
}
