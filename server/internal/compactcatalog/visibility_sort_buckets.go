package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
)

type visibilitySortPair struct{ class, generation, entity int64 }

// StepVisibilitySortBuckets reconciles at most limit class/entity pairs. The
// old fixed seven-axis ledger survives source deletion until it is reversed.
func StepVisibilitySortBuckets(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	if limit < 1 || limit > 500 {
		return 0, fmt.Errorf("invalid visibility sort bucket limit")
	}
	if limit > 100 {
		limit = 100
	}
	var turn int
	if err := tx.QueryRowContext(ctx, `SELECT turn FROM catalog_visibility_sort_state WHERE id=1`).Scan(&turn); err != nil {
		return 0, err
	}
	steps := [3]func(context.Context, *sql.Tx, int) (int, error){stepVisibilitySortPair, stepVisibilitySortEntity, stepVisibilitySortBackfill}
	for i := range steps {
		kind := (turn + i) % len(steps)
		n, err := steps[kind](ctx, tx, limit)
		if err != nil {
			return 0, err
		}
		if n > 0 {
			_, err = tx.ExecContext(ctx, `UPDATE catalog_visibility_sort_state SET turn=? WHERE id=1`, (kind+1)%len(steps))
			return n, err
		}
	}
	return 0, nil
}

func stepVisibilitySortPair(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT class_id,generation,entity_id FROM catalog_visibility_sort_pair_jobs ORDER BY class_id,generation,entity_id LIMIT ?`, limit)
	if err != nil {
		return 0, err
	}
	keys := []visibilitySortPair{}
	for rows.Next() {
		var k visibilitySortPair
		if err = rows.Scan(&k.class, &k.generation, &k.entity); err != nil {
			break
		}
		keys = append(keys, k)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, k := range keys {
		if err = reconcileVisibilitySortPair(ctx, tx, k); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_visibility_sort_pair_jobs WHERE class_id=? AND generation=? AND entity_id=?`, k.class, k.generation, k.entity); err != nil {
			return 0, err
		}
	}
	return len(keys), nil
}

func stepVisibilitySortEntity(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	var entity, sequence, cursorSequence, afterClass, afterGeneration int64
	err := tx.QueryRowContext(ctx, `SELECT entity_id,sequence,cursor_sequence,class_cursor,generation_cursor FROM catalog_visibility_sort_entity_jobs ORDER BY entity_id LIMIT 1`).Scan(&entity, &sequence, &cursorSequence, &afterClass, &afterGeneration)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if sequence != cursorSequence {
		afterClass, afterGeneration = 0, 0
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_visibility_sort_entity_jobs SET cursor_sequence=sequence,class_cursor=0,generation_cursor=0 WHERE entity_id=?`, entity); err != nil {
			return 0, err
		}
	}
	rows, err := tx.QueryContext(ctx, `WITH current AS (
	 SELECT class_id,generation FROM compact_visibility_rows INDEXED BY compact_visibility_sort_entity WHERE entity_id=? AND (class_id,generation)>(?,?) ORDER BY class_id,generation LIMIT ?
	), prior AS (
	 SELECT DISTINCT class_id,generation FROM catalog_visibility_sort_edges INDEXED BY catalog_visibility_sort_edge_entity WHERE entity_id=? AND (class_id,generation)>(?,?) ORDER BY class_id,generation LIMIT ?
	) SELECT class_id,generation FROM current UNION SELECT class_id,generation FROM prior ORDER BY class_id,generation LIMIT ?`, entity, afterClass, afterGeneration, limit, entity, afterClass, afterGeneration, limit, limit)
	if err != nil {
		return 0, err
	}
	keys := []visibilitySortPair{}
	for rows.Next() {
		k := visibilitySortPair{entity: entity}
		if err = rows.Scan(&k.class, &k.generation); err != nil {
			break
		}
		keys = append(keys, k)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, k := range keys {
		if err = reconcileVisibilitySortPair(ctx, tx, k); err != nil {
			return 0, err
		}
	}
	if len(keys) < limit {
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_visibility_sort_entity_jobs WHERE entity_id=?`, entity)
	} else {
		last := keys[len(keys)-1]
		_, err = tx.ExecContext(ctx, `UPDATE catalog_visibility_sort_entity_jobs SET class_cursor=?,generation_cursor=? WHERE entity_id=? AND sequence=?`, last.class, last.generation, entity, sequence)
	}
	if err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		return 1, nil
	}
	return len(keys), nil
}

func stepVisibilitySortBackfill(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	var class, generation, entity int64
	var done bool
	if err := tx.QueryRowContext(ctx, `SELECT class_cursor,generation_cursor,entity_cursor,backfill_done FROM catalog_visibility_sort_state WHERE id=1`).Scan(&class, &generation, &entity, &done); err != nil {
		return 0, err
	}
	if done {
		return 0, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT class_id,generation,entity_id FROM compact_visibility_rows WHERE (class_id,generation,entity_id)>(?,?,?) ORDER BY class_id,generation,entity_id LIMIT ?`, class, generation, entity, limit)
	if err != nil {
		return 0, err
	}
	keys := []visibilitySortPair{}
	for rows.Next() {
		var k visibilitySortPair
		if err = rows.Scan(&k.class, &k.generation, &k.entity); err != nil {
			break
		}
		keys = append(keys, k)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_visibility_sort_state SET backfill_done=1 WHERE id=1`)
		return 1, err
	}
	for _, k := range keys {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_visibility_sort_pair_jobs(class_id,generation,entity_id) VALUES(?,?,?) ON CONFLICT(class_id,generation,entity_id) DO NOTHING`, k.class, k.generation, k.entity); err != nil {
			return 0, err
		}
	}
	last := keys[len(keys)-1]
	_, err = tx.ExecContext(ctx, `UPDATE catalog_visibility_sort_state SET class_cursor=?,generation_cursor=?,entity_cursor=? WHERE id=1`, last.class, last.generation, last.entity)
	return len(keys), err
}

func reconcileVisibilitySortPair(ctx context.Context, tx *sql.Tx, pair visibilitySortPair) error {
	rows, err := tx.QueryContext(ctx, `SELECT library_id,kind,axis,value FROM catalog_visibility_sort_edges WHERE class_id=? AND generation=? AND entity_id=? ORDER BY axis`, pair.class, pair.generation, pair.entity)
	if err != nil {
		return err
	}
	type edge struct {
		library, kind, axis int
		value               string
	}
	old := []edge{}
	for rows.Next() {
		var e edge
		if err = rows.Scan(&e.library, &e.kind, &e.axis, &e.value); err != nil {
			break
		}
		old = append(old, e)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, e := range old {
		result, err := tx.ExecContext(ctx, `UPDATE catalog_visibility_sort_buckets SET total=total-1 WHERE class_id=? AND generation=? AND library_id=? AND kind=? AND axis=? AND value=?`, pair.class, pair.generation, e.library, e.kind, e.axis, e.value)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return fmt.Errorf("missing visibility sort bucket for contribution")
		}
		// Only the bucket just decremented can have emptied.
		if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_visibility_sort_buckets WHERE class_id=? AND generation=? AND library_id=? AND kind=? AND axis=? AND value=? AND total=0`, pair.class, pair.generation, e.library, e.kind, e.axis, e.value); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_visibility_sort_edges WHERE class_id=? AND generation=? AND entity_id=?`, pair.class, pair.generation, pair.entity); err != nil {
		return err
	}
	var library, kind, year int
	var head, rating, duration string
	var added sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT v.library_id,v.kind,b.head,b.year,b.added_text,c.rating_bucket,c.duration_bucket
	 FROM compact_visibility_rows v JOIN catalog_browse_rows b ON b.entity_id=v.entity_id AND b.library_id=v.library_id AND b.kind=v.kind
	 JOIN catalog_browse_counted_rows c ON c.entity_id=b.entity_id
	 WHERE v.class_id=? AND v.generation=? AND v.entity_id=?`, pair.class, pair.generation, pair.entity).Scan(&library, &kind, &head, &year, &added, &rating, &duration)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	values := browseStaticAxes(browseRow{head: head, year: year, added: added, ratingBucket: rating, durationBucket: duration})
	for axis, value := range values {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_visibility_sort_edges(class_id,generation,entity_id,library_id,kind,axis,value) VALUES(?,?,?,?,?,?,?)`, pair.class, pair.generation, pair.entity, library, kind, axis, value); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_visibility_sort_buckets(class_id,generation,library_id,kind,axis,value,total) VALUES(?,?,?,?,?,?,1)
		 ON CONFLICT(class_id,generation,library_id,kind,axis,value) DO UPDATE SET total=total+1`, pair.class, pair.generation, library, kind, axis, value); err != nil {
			return err
		}
	}
	return nil
}
