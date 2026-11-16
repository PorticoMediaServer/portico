package db

import (
	"context"
	"database/sql"
)

// HomeCollection names a nonempty library collection the profile may see.
type HomeCollection struct{ ID, Title, Library string }

func (d *DB) HomeCollections(ctx context.Context, v Viewer, library, collection string) ([]HomeCollection, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	return queryAll(ctx, tx, `SELECT i.public_id,i.title,l.public_id FROM libraries l CROSS JOIN items i ON i.library_id=l.id
  WHERE i.kind=`+itoa(int64(KindCollection))+` AND (?='' OR l.public_id=?) AND (?='' OR i.public_id=?) AND `+acc.visible("i")+`
  AND EXISTS(SELECT 1 FROM item_relations r JOIN items member ON member.id=r.related_item_id
   WHERE r.item_id=i.id AND r.kind=`+itoa(int64(RelationCollectionMember))+` AND `+acc.visible("member")+`)
  ORDER BY i.sort_title,i.id`, []any{library, library, collection, collection}, func(c *HomeCollection) []any { return []any{&c.ID, &c.Title, &c.Library} })
}
