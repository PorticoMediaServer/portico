package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// localMatches admits candidates only through a collection or credits. Cast matches use
// the first three billed actors on each title; director/writer credits use their known roles.
func localMatches(seed int64) string {
	return fmt.Sprintf(`SELECT DISTINCT c.item_id,s.person_id AS signal,s.role FROM credits s
  CROSS JOIN credits c ON c.person_id=s.person_id AND c.role=s.role
  WHERE s.item_id=%[1]d AND (s.role IN (%[3]d,%[4]d) OR (s.role=%[2]d
   AND s.position IN (SELECT position FROM credits WHERE item_id=%[1]d AND role=%[2]d ORDER BY position LIMIT 3)
   AND c.position IN (SELECT position FROM credits WHERE item_id=c.item_id AND role=%[2]d ORDER BY position LIMIT 3)))
 UNION ALL SELECT c.related_item_id,s.item_id,0 FROM item_relations s
  CROSS JOIN item_relations c ON c.item_id=s.item_id AND c.kind=s.kind
  WHERE s.related_item_id=%[1]d AND s.kind=%[5]d`, seed, RoleActor, RoleDirector, RoleWriter, RelationCollectionMember)
}

// Broad facts only score candidates already qualified by a narrow signal.
func localTagScore(seed int64) string {
	return fmt.Sprintf(`(SELECT count(*) FROM item_tags seed CROSS JOIN tags tag ON tag.id=seed.tag_id
  CROSS JOIN item_tags candidate ON candidate.item_id=i.id AND candidate.tag_id=seed.tag_id
  WHERE seed.item_id=%d AND tag.kind IN ('genre','studio','network'))`, seed)
}

func (d *DB) localSimilarPage(ctx context.Context, v Viewer, itemID, cursor string, limit int) (ItemPage, error) {
	page := ItemPage{Items: []Item{}}
	after, err := decodeCursor(cursor, SortOrder, true)
	if err != nil {
		return page, err
	}
	if after != nil && (after.Nulls || !after.fits(sortSpec{columns: []string{"score"}})) {
		return page, ErrCursorExpired
	}
	if limit <= 0 {
		limit = 15
	}
	limit = min(limit, maxPageSize)
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return page, err
	}
	var seed int64
	var kind ItemKind
	err = tx.QueryRowContext(ctx, `SELECT i.id,i.kind FROM items i WHERE i.public_id=? AND `+acc.visible("i"), itemID).Scan(&seed, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return page, ErrNotFound
	}
	if err != nil || (kind != KindMovie && kind != KindShow) {
		return page, err
	}
	w := newQuery(v)
	w.add(`WITH ranked AS (SELECT i.id AS item_id,count(*)+`, localTagScore(seed), ` AS score FROM (`, localMatches(seed), `) matches
  CROSS JOIN items i ON i.id=matches.item_id WHERE i.id<>`, itoa(seed), ` AND i.kind IN (`, itoa(int64(KindMovie)), `,`, itoa(int64(KindShow)), `)
  AND `, acc.visible("i"), ` AND NOT EXISTS(SELECT 1 FROM user_item_state own WHERE own.profile_id=@viewer_profile
   AND own.item_id=i.id AND (own.played=1 OR own.hidden=1)) GROUP BY i.id)
  SELECT `, itemColumns(acc), `,r.score FROM ranked r JOIN items i ON i.id=r.item_id`, stateJoin, `1`)
	if after != nil {
		w.add(` AND (r.score,i.id)<(`, w.arg(after.Values[0]), `,`, w.arg(after.ID), `)`)
	}
	w.add(` ORDER BY r.score DESC,i.id DESC LIMIT `, fmt.Sprint(limit+1))
	rows, err := tx.QueryContext(ctx, w.sql.String(), w.args...)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var it Item
		var score int64
		if err := scanItem(rows, &it, &score); err != nil {
			rows.Close()
			return page, err
		}
		it.sortValues = []any{score}
		page.Items = append(page.Items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = (listCursor{Sort: SortOrder, Desc: true, Values: last.sortValues, ID: last.ID}).encode()
	}
	// Copies of a title in two libraries score alike and so sit together: one is kept. The
	// cursor is the page's last row read, so the next page goes on from the same place.
	page.Items = oneCopyEach(page.Items, map[string]bool{})
	if err := loadImages(ctx, tx, page.Items); err != nil {
		return page, err
	}
	return page, loadLeafCounts(ctx, tx, v, acc, page.Items)
}

// Whether enhanced data defines a ranking is distinct from whether its titles are owned or
// visible here. An existing enhanced ranking must end without local padding.
func (d *DB) hasSimilarTitles(ctx context.Context, v Viewer, itemID string) (bool, error) {
	td := d.titleData.Load()
	if td == nil {
		return false, nil
	}
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return false, err
	}
	var id int64
	var kind ItemKind
	err = tx.QueryRowContext(ctx, `SELECT i.id,i.kind FROM items i WHERE i.public_id=? AND `+acc.visible("i"), itemID).Scan(&id, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil || (kind != KindMovie && kind != KindShow) {
		return false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT provider,external_id FROM item_external_ids WHERE item_id=?`, id)
	if err != nil {
		return false, err
	}
	ids := map[string]string{}
	for rows.Next() {
		var provider, external string
		if err := rows.Scan(&provider, &external); err != nil {
			rows.Close()
			return false, err
		}
		ids[provider] = external
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	qid, err := titleDataWork(ctx, td, kind, ids)
	if err != nil {
		return false, d.unlessSwapped(td, err)
	}
	var found bool
	err = td.conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM work_similar WHERE qid=?)`, qid).Scan(&found)
	return found, d.unlessSwapped(td, err)
}
