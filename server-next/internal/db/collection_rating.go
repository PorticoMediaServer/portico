package db

import "strconv"

// A library's collection carries, as its own rating, the lowest rating among its films, and a
// series of books or audiobooks the lowest among its volumes: both hold their titles by a
// relation, where a show holds its episodes as children (see holder_flags.go for those). So
// the one visibility condition, and every total and list read from the rating index, show a
// rating-limited profile a collection while it holds a film that profile may see, and never
// one that holds none: no read needs to look inside a collection. A collection of unrated
// films is unrated. Opened, a collection lists only the films the profile may see. What is
// said of collections below is true of those series too.
//
// collectionsRated must be called, in the same transaction, by whatever adds a film to a
// collection or takes one out; ratingChanged calls it for the collections of a film whose
// rating changed.

// heldByRelation are the relations by which a collection or a series holds its titles.
var heldByRelation = "(" + strconv.Itoa(int(RelationCollectionMember)) + ", " + strconv.Itoa(int(RelationSeriesMember)) + ")"

// collectionsRated stores each of these collections' rating again from its films, and the
// copies of it on its tag rows.
func (b *batchTx) collectionsRated(collections ...int64) error {
	for _, id := range collections {
		res, err := b.exec(`UPDATE items SET rating_age = (SELECT min(m.rating_age) FROM item_relations r JOIN items m ON m.id = r.related_item_id
			WHERE r.item_id = items.id AND r.kind IN `+heldByRelation+`)
			WHERE id = ? AND kind IN (`+strconv.Itoa(int(KindCollection))+`, `+strconv.Itoa(int(KindSeries))+`)`, id)
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
