package db

import "strconv"

// A library's collection carries, as its own rating, the lowest rating among its films. So
// the one visibility condition, and every total and list read from the rating index, show a
// rating-limited profile a collection while it holds a film that profile may see, and never
// one that holds none: no read needs to look inside a collection. A collection of unrated
// films is unrated. Opened, a collection lists only the films the profile may see.
//
// collectionsRated must be called, in the same transaction, by whatever adds a film to a
// collection or takes one out; ratingChanged calls it for the collections of a film whose
// rating changed.

// collectionsRated stores each of these collections' rating again from its films, and the
// copies of it on its tag rows.
func (b *batchTx) collectionsRated(collections ...int64) error {
	for _, id := range collections {
		res, err := b.exec(`UPDATE items SET rating_age = (SELECT min(m.rating_age) FROM item_relations r JOIN items m ON m.id = r.related_item_id
			WHERE r.item_id = items.id AND r.kind = `+strconv.Itoa(int(RelationCollectionMember))+`)
			WHERE id = ? AND kind = `+strconv.Itoa(int(KindCollection)), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		if _, err := b.exec(`UPDATE item_tags SET rating_age = (SELECT rating_age FROM items WHERE id = ?1) WHERE item_id = ?1`, id); err != nil {
			return err
		}
	}
	return nil
}
