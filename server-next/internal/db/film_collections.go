package db

import (
	"context"
	"database/sql"
)

// FilmCollection is a library collection this viewer may open, with the film's zero-based
// position among its visible members in the collection's own order.
type FilmCollection struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MemberCount int    `json:"memberCount"`
	Position    int    `json:"position"`
}

func filmCollections(ctx context.Context, tx *sql.Tx, v Viewer, acc access, item int64) ([]FilmCollection, error) {
	return queryAll(ctx, tx, `SELECT c.public_id, c.title, count(*),
		sum(CASE WHEN (members.position, m.id) < (target.position, target.related_item_id) THEN 1 ELSE 0 END)
		FROM item_relations target JOIN items c ON c.id = target.item_id
		JOIN item_relations members ON members.item_id = c.id AND members.kind = target.kind
		JOIN items m ON m.id = members.related_item_id
		WHERE target.related_item_id = @film AND target.kind = `+itoa(int64(RelationCollectionMember))+`
		AND c.kind = `+itoa(int64(KindCollection))+` AND `+acc.visible("c")+` AND `+acc.visible("m")+`
		GROUP BY c.id ORDER BY c.sort_title, c.id`, append(v.args(), sql.Named("film", item)),
		func(c *FilmCollection) []any { return []any{&c.ID, &c.Name, &c.MemberCount, &c.Position} })
}
