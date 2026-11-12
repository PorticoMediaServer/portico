package db

import "strconv"

// A library's collection holds its films, and a series of books or audiobooks its volumes, by
// a relation, where a show holds its episodes as children. Both are holders all the same
// (browse rule 2): a profile sees one only while it holds a title the profile may see, and
// opening one it may not see answers "not found".
//
// So the holder's own rating is the most lenient of its titles': the lowest rating, or 0 when
// some title is rated and another is not (an unrated title is the most lenient to a profile
// that allows unrated titles), and none when no title is rated. The one visibility condition,
// and every total and list read from the rating index, then never leave out a holder some
// profile could find a title in. Where its titles are rated or labelled differently from it,
// the holder is flagged as able to be left empty (may_empty, see holder_flags.go), and
// access.filled and access.emptiedSet look inside it through item_relations, as they look
// under a show. Opened, a collection lists only the films the profile may see. What is said
// of collections below is true of those series too.
//
// collectionsRated must be called, in the same transaction, by whatever adds a film to a
// collection or takes one out; ratingChanged calls it for the collections of a film whose
// rating changed, and labelsChanged flags them through flagHolders.

// heldByRelation are the relations by which a collection or a series holds its titles.
var heldByRelation = "(" + strconv.Itoa(int(RelationCollectionMember)) + ", " + strconv.Itoa(int(RelationSeriesMember)) + ")"

// relationHolderKinds are the kinds of item that can hold titles by those relations.
var relationHolderKinds = "(" + strconv.Itoa(int(KindSeries)) + ", " + strconv.Itoa(int(KindCollection)) + ")"

// collectionsRated stores each of these collections' rating again from its films, and the
// copies of it on its tag rows, and flags it when a film now differs from it.
func (b *batchTx) collectionsRated(collections ...int64) error {
	for _, id := range collections {
		res, err := b.exec(`UPDATE items SET rating_age = (SELECT CASE WHEN count(m.rating_age) = 0 THEN NULL
				WHEN count(m.rating_age) < count(*) THEN 0 ELSE min(m.rating_age) END
			FROM item_relations r JOIN items m ON m.id = r.related_item_id
			WHERE r.item_id = items.id AND r.kind IN `+heldByRelation+`)
			WHERE id = ? AND kind IN `+relationHolderKinds, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		if _, err := b.exec(`UPDATE item_tags SET rating_age = (SELECT rating_age FROM items WHERE id = ?1) WHERE item_id = ?1`, id); err != nil {
			return err
		}
		if _, err := b.exec(`UPDATE items SET may_empty = 1 WHERE id = ? AND may_empty = 0 AND `+differsBelow, id); err != nil {
			return err
		}
	}
	return nil
}
