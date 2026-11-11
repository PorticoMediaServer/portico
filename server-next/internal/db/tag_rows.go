package db

// DELETE THIS FILE when the tag-row work (pull request #2121) is on the same branch. It
// defines both methods for real there, in inserts.go, and the two definitions cannot live
// together: the build fails until this file is gone. Nothing else needs to change. Every
// caller in the metadata write path already calls them where that work needs them called.
//
// Until then a tag row is only the item and the tag, and nothing copies a rating.

// tagItem gives an item a tag.
func (b *batchTx) tagItem(itemID, tagID int64) error {
	_, err := b.exec(`INSERT INTO item_tags (item_id, tag_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, itemID, tagID)
	return err
}

// ratingChanged must be called, in the same transaction, by whatever has just changed the
// rating_age of an item or of the items under it.
func (b *batchTx) ratingChanged(itemID int64) error { return nil }
