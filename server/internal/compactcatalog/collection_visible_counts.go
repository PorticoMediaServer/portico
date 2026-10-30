package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
)

// StepCollectionVisibleCounts is one bounded derived unit inside the ordinary
// supervised projector transaction. It never walks an entire collection or an
// item's every collection in one transaction. Triggers only enqueue keys;
// this worker keysets up to 100 contributions and persists its cursor.
func StepCollectionVisibleCounts(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	if limit < 1 || limit > 500 {
		return 0, fmt.Errorf("invalid collection visible count limit")
	}
	if limit > 100 {
		limit = 100
	}
	var turn int
	if err := tx.QueryRowContext(ctx, `SELECT turn FROM catalog_collection_visible_state WHERE id=1`).Scan(&turn); err != nil {
		return 0, err
	}
	steps := [3]func(context.Context, *sql.Tx, int) (int, error){stepCollectionVisiblePair, stepCollectionVisibleItem, stepCollectionVisibleBackfill}
	for i := range steps {
		kind := (turn + i) % len(steps)
		n, err := steps[kind](ctx, tx, limit)
		if err != nil {
			return 0, err
		}
		if n > 0 {
			_, err = tx.ExecContext(ctx, `UPDATE catalog_collection_visible_state SET turn=? WHERE id=1`, (kind+1)%len(steps))
			return n, err
		}
	}
	return 0, nil
}

type collectionVisibleEdge struct{ class, generation, collection, item int64 }

func stepCollectionVisiblePair(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	var collection, item, afterClass, afterGeneration int64
	err := tx.QueryRowContext(ctx, `SELECT collection_id,item_id,class_cursor,generation_cursor FROM catalog_collection_visible_pair_jobs ORDER BY collection_id,item_id LIMIT 1`).Scan(&collection, &item, &afterClass, &afterGeneration)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT class_id,generation FROM (
 SELECT class_id,generation FROM compact_visibility_rows WHERE entity_id=? AND kind=1 AND (class_id,generation)>(?,?)
 UNION SELECT class_id,generation FROM catalog_collection_visible_edges WHERE collection_id=? AND item_id=? AND (class_id,generation)>(?,?)
 ) ORDER BY class_id,generation LIMIT ?`, item, afterClass, afterGeneration, collection, item, afterClass, afterGeneration, limit)
	if err != nil {
		return 0, err
	}
	keys := []collectionVisibleEdge{}
	for rows.Next() {
		var edge collectionVisibleEdge
		if err = rows.Scan(&edge.class, &edge.generation); err != nil {
			break
		}
		edge.collection, edge.item = collection, item
		keys = append(keys, edge)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, edge := range keys {
		if err = reconcileCollectionVisibleEdge(ctx, tx, edge); err != nil {
			return 0, err
		}
	}
	if len(keys) < limit {
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_collection_visible_pair_jobs WHERE collection_id=? AND item_id=?`, collection, item)
	} else {
		last := keys[len(keys)-1]
		_, err = tx.ExecContext(ctx, `UPDATE catalog_collection_visible_pair_jobs SET class_cursor=?,generation_cursor=? WHERE collection_id=? AND item_id=?`, last.class, last.generation, collection, item)
	}
	if err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		return 1, nil
	}
	return len(keys), nil
}

func stepCollectionVisibleItem(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	var class, generation, item, afterCollection int64
	err := tx.QueryRowContext(ctx, `SELECT class_id,generation,item_id,collection_cursor FROM catalog_collection_visible_item_jobs ORDER BY class_id,generation,item_id LIMIT 1`).Scan(&class, &generation, &item, &afterCollection)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT collection_id FROM (
 SELECT collection_id FROM catalog_collection_members WHERE item_id=? AND collection_id>?
 UNION SELECT collection_id FROM catalog_collection_visible_edges WHERE class_id=? AND generation=? AND item_id=? AND collection_id>?
 ) ORDER BY collection_id LIMIT ?`, item, afterCollection, class, generation, item, afterCollection, limit)
	if err != nil {
		return 0, err
	}
	keys := []collectionVisibleEdge{}
	for rows.Next() {
		var edge collectionVisibleEdge
		if err = rows.Scan(&edge.collection); err != nil {
			break
		}
		edge.class, edge.generation, edge.item = class, generation, item
		keys = append(keys, edge)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, edge := range keys {
		if err = reconcileCollectionVisibleEdge(ctx, tx, edge); err != nil {
			return 0, err
		}
	}
	if len(keys) < limit {
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_collection_visible_item_jobs WHERE class_id=? AND generation=? AND item_id=?`, class, generation, item)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_collection_visible_item_jobs SET collection_cursor=? WHERE class_id=? AND generation=? AND item_id=?`, keys[len(keys)-1].collection, class, generation, item)
	}
	if err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		return 1, nil
	}
	return len(keys), nil
}

// Existing class rows predate the queue triggers. Seed one item job per row
// with a persisted keyset cursor; concurrent class changes enqueue themselves.
func stepCollectionVisibleBackfill(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	var class, generation, item int64
	var done bool
	if err := tx.QueryRowContext(ctx, `SELECT class_cursor,generation_cursor,item_cursor,backfill_done FROM catalog_collection_visible_state WHERE id=1`).Scan(&class, &generation, &item, &done); err != nil {
		return 0, err
	}
	if done {
		return 0, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT class_id,generation,entity_id FROM compact_visibility_rows WHERE kind=1 AND (class_id,generation,entity_id)>(?,?,?) ORDER BY class_id,generation,entity_id LIMIT ?`, class, generation, item, limit)
	if err != nil {
		return 0, err
	}
	keys := []collectionVisibleEdge{}
	for rows.Next() {
		var edge collectionVisibleEdge
		if err = rows.Scan(&edge.class, &edge.generation, &edge.item); err != nil {
			break
		}
		keys = append(keys, edge)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_collection_visible_state SET backfill_done=1 WHERE id=1`)
		return 1, err
	}
	for _, edge := range keys {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_collection_visible_item_jobs(class_id,generation,item_id) VALUES(?,?,?) ON CONFLICT(class_id,generation,item_id) DO NOTHING`, edge.class, edge.generation, edge.item); err != nil {
			return 0, err
		}
	}
	last := keys[len(keys)-1]
	_, err = tx.ExecContext(ctx, `UPDATE catalog_collection_visible_state SET class_cursor=?,generation_cursor=?,item_cursor=? WHERE id=1`, last.class, last.generation, last.item)
	return len(keys), err
}

func reconcileCollectionVisibleEdge(ctx context.Context, tx *sql.Tx, edge collectionVisibleEdge) error {
	var desired, current bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(
	 SELECT 1 FROM catalog_collection_members m JOIN catalog_collections c ON c.entity_id=m.collection_id
	 JOIN catalog_entities ce ON ce.id=c.entity_id AND ce.retired=0
	 JOIN compact_visibility_rows v ON v.entity_id=m.item_id AND v.class_id=? AND v.generation=? AND v.kind=1 AND v.library_id=c.library_id
	 WHERE m.collection_id=? AND m.item_id=?)`, edge.class, edge.generation, edge.collection, edge.item).Scan(&desired)
	if err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_collection_visible_edges WHERE class_id=? AND generation=? AND collection_id=? AND item_id=?)`, edge.class, edge.generation, edge.collection, edge.item).Scan(&current)
	if err != nil || desired == current {
		return err
	}
	if desired {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_collection_visible_edges(class_id,generation,collection_id,item_id) VALUES(?,?,?,?)`, edge.class, edge.generation, edge.collection, edge.item); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO catalog_collection_visible_counts(class_id,generation,collection_id,total) VALUES(?,?,?,1) ON CONFLICT(class_id,generation,collection_id) DO UPDATE SET total=total+1`, edge.class, edge.generation, edge.collection)
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_collection_visible_edges WHERE class_id=? AND generation=? AND collection_id=? AND item_id=?`, edge.class, edge.generation, edge.collection, edge.item); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE catalog_collection_visible_counts SET total=total-1 WHERE class_id=? AND generation=? AND collection_id=?`, edge.class, edge.generation, edge.collection)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return fmt.Errorf("collection visible count missing for contribution")
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM catalog_collection_visible_counts WHERE class_id=? AND generation=? AND collection_id=? AND total=0`, edge.class, edge.generation, edge.collection)
	return err
}
