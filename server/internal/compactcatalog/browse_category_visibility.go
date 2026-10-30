package compactcatalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

type categoryVisibilityEdge struct {
	class, generation, library, kind, item int64
	value                                  string
}

// StepCategoryVisibility reconciles one bounded slice of category/class
// contributions. Source triggers only enqueue keys; no trigger scans classes
// or an item's categories.
func StepCategoryVisibility(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	if limit < 1 || limit > 500 {
		return 0, fmt.Errorf("invalid category visibility limit")
	}
	if limit > 100 {
		limit = 100
	}
	var turn int
	if err := tx.QueryRowContext(ctx, `SELECT turn FROM catalog_movie_category_visible_state WHERE id=1`).Scan(&turn); err != nil {
		return 0, err
	}
	steps := [3]func(context.Context, *sql.Tx, int) (int, error){stepCategoryVisibilityPair, stepCategoryVisibilityItem, stepCategoryVisibilityBackfill}
	for i := range steps {
		kind := (turn + i) % len(steps)
		n, err := steps[kind](ctx, tx, limit)
		if err != nil {
			return 0, err
		}
		if n > 0 {
			_, err = tx.ExecContext(ctx, `UPDATE catalog_movie_category_visible_state SET turn=? WHERE id=1`, (kind+1)%len(steps))
			return n, err
		}
	}
	return 0, nil
}

func stepCategoryVisibilityPair(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	var edge categoryVisibilityEdge
	var sequence, cursorSequence, afterClass, afterGeneration int64
	err := tx.QueryRowContext(ctx, `SELECT library_id,kind,value,item_id,sequence,cursor_sequence,class_cursor,generation_cursor FROM catalog_movie_category_visible_pair_jobs ORDER BY library_id,kind,value,item_id LIMIT 1`).Scan(&edge.library, &edge.kind, &edge.value, &edge.item, &sequence, &cursorSequence, &afterClass, &afterGeneration)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if sequence != cursorSequence {
		afterClass, afterGeneration = 0, 0
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_movie_category_visible_pair_jobs SET cursor_sequence=sequence,class_cursor=0,generation_cursor=0 WHERE library_id=? AND kind=? AND value=? AND item_id=?`, edge.library, edge.kind, edge.value, edge.item); err != nil {
			return 0, err
		}
	}
	rows, err := tx.QueryContext(ctx, `WITH current AS (
	 SELECT class_id,generation FROM compact_visibility_rows INDEXED BY compact_visibility_rows_movie_item WHERE entity_id=? AND kind=1 AND (class_id,generation)>(?,?) ORDER BY class_id,generation LIMIT ?
	), prior AS (
	 SELECT class_id,generation FROM catalog_movie_category_visible_edges INDEXED BY catalog_movie_category_visible_pair WHERE library_id=? AND kind=? AND value=? AND item_id=? AND (class_id,generation)>(?,?) ORDER BY class_id,generation LIMIT ?
	) SELECT class_id,generation FROM current UNION SELECT class_id,generation FROM prior ORDER BY class_id,generation LIMIT ?`, edge.item, afterClass, afterGeneration, limit, edge.library, edge.kind, edge.value, edge.item, afterClass, afterGeneration, limit, limit)
	if err != nil {
		return 0, err
	}
	keys := []categoryVisibilityEdge{}
	for rows.Next() {
		k := edge
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
		if err = reconcileCategoryVisibility(ctx, tx, k); err != nil {
			return 0, err
		}
	}
	if len(keys) < limit {
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_movie_category_visible_pair_jobs WHERE library_id=? AND kind=? AND value=? AND item_id=?`, edge.library, edge.kind, edge.value, edge.item)
	} else {
		last := keys[len(keys)-1]
		_, err = tx.ExecContext(ctx, `UPDATE catalog_movie_category_visible_pair_jobs SET class_cursor=?,generation_cursor=? WHERE library_id=? AND kind=? AND value=? AND item_id=? AND sequence=?`, last.class, last.generation, edge.library, edge.kind, edge.value, edge.item, sequence)
	}
	if err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		return 1, nil
	}
	return len(keys), nil
}

func stepCategoryVisibilityItem(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	var edge categoryVisibilityEdge
	var sequence, cursorSequence, afterLibrary, afterKind int64
	var afterValue string
	err := tx.QueryRowContext(ctx, `SELECT class_id,generation,item_id,sequence,cursor_sequence,library_cursor,kind_cursor,value_cursor FROM catalog_movie_category_visible_item_jobs ORDER BY class_id,generation,item_id LIMIT 1`).Scan(&edge.class, &edge.generation, &edge.item, &sequence, &cursorSequence, &afterLibrary, &afterKind, &afterValue)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if sequence != cursorSequence {
		afterLibrary, afterKind, afterValue = 0, -1, ""
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_movie_category_visible_item_jobs SET cursor_sequence=sequence,library_cursor=0,kind_cursor=-1,value_cursor='' WHERE class_id=? AND generation=? AND item_id=?`, edge.class, edge.generation, edge.item); err != nil {
			return 0, err
		}
	}
	rows, err := tx.QueryContext(ctx, `WITH current AS (
	 SELECT library_id,kind,value FROM catalog_movie_category_members INDEXED BY catalog_movie_category_member_id WHERE item_id=? AND (library_id,kind,value)>(?,?,?) ORDER BY library_id,kind,value LIMIT ?
	), prior AS (
	 SELECT library_id,kind,value FROM catalog_movie_category_visible_edges INDEXED BY catalog_movie_category_visible_item WHERE class_id=? AND generation=? AND item_id=? AND (library_id,kind,value)>(?,?,?) ORDER BY library_id,kind,value LIMIT ?
	) SELECT library_id,kind,value FROM current UNION SELECT library_id,kind,value FROM prior ORDER BY library_id,kind,value LIMIT ?`, edge.item, afterLibrary, afterKind, afterValue, limit, edge.class, edge.generation, edge.item, afterLibrary, afterKind, afterValue, limit, limit)
	if err != nil {
		return 0, err
	}
	keys := []categoryVisibilityEdge{}
	for rows.Next() {
		k := edge
		if err = rows.Scan(&k.library, &k.kind, &k.value); err != nil {
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
		if err = reconcileCategoryVisibility(ctx, tx, k); err != nil {
			return 0, err
		}
	}
	if len(keys) < limit {
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_movie_category_visible_item_jobs WHERE class_id=? AND generation=? AND item_id=?`, edge.class, edge.generation, edge.item)
	} else {
		last := keys[len(keys)-1]
		_, err = tx.ExecContext(ctx, `UPDATE catalog_movie_category_visible_item_jobs SET library_cursor=?,kind_cursor=?,value_cursor=? WHERE class_id=? AND generation=? AND item_id=? AND sequence=?`, last.library, last.kind, last.value, edge.class, edge.generation, edge.item, sequence)
	}
	if err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		return 1, nil
	}
	return len(keys), nil
}

func stepCategoryVisibilityBackfill(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	var class, generation, item int64
	var done bool
	if err := tx.QueryRowContext(ctx, `SELECT class_cursor,generation_cursor,item_cursor,backfill_done FROM catalog_movie_category_visible_state WHERE id=1`).Scan(&class, &generation, &item, &done); err != nil {
		return 0, err
	}
	if done {
		return 0, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT class_id,generation,entity_id FROM compact_visibility_rows INDEXED BY compact_visibility_rows_movie_backfill WHERE kind=1 AND (class_id,generation,entity_id)>(?,?,?) ORDER BY class_id,generation,entity_id LIMIT ?`, class, generation, item, limit)
	if err != nil {
		return 0, err
	}
	keys := []categoryVisibilityEdge{}
	for rows.Next() {
		var k categoryVisibilityEdge
		if err = rows.Scan(&k.class, &k.generation, &k.item); err != nil {
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
		_, err = tx.ExecContext(ctx, `UPDATE catalog_movie_category_visible_state SET backfill_done=1 WHERE id=1`)
		return 1, err
	}
	for _, k := range keys {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_movie_category_visible_item_jobs(class_id,generation,item_id) VALUES(?,?,?) ON CONFLICT(class_id,generation,item_id) DO UPDATE SET sequence=sequence+1`, k.class, k.generation, k.item); err != nil {
			return 0, err
		}
	}
	last := keys[len(keys)-1]
	_, err = tx.ExecContext(ctx, `UPDATE catalog_movie_category_visible_state SET class_cursor=?,generation_cursor=?,item_cursor=? WHERE id=1`, last.class, last.generation, last.item)
	return len(keys), err
}

func reconcileCategoryVisibility(ctx context.Context, tx *sql.Tx, edge categoryVisibilityEdge) error {
	var title, poster string
	err := tx.QueryRowContext(ctx, `SELECT m.title,m.poster_url FROM catalog_movie_category_members m
	 JOIN compact_visibility_rows v ON v.entity_id=m.item_id AND v.class_id=? AND v.generation=? AND v.kind=1 AND v.library_id=m.library_id
	 WHERE m.library_id=? AND m.kind=? AND m.value=? AND m.item_id=?`, edge.class, edge.generation, edge.library, edge.kind, edge.value, edge.item).Scan(&title, &poster)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	desired := err == nil
	var current bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_movie_category_visible_edges WHERE class_id=? AND generation=? AND library_id=? AND kind=? AND value=? AND item_id=?)`, edge.class, edge.generation, edge.library, edge.kind, edge.value, edge.item).Scan(&current); err != nil {
		return err
	}
	delta := 0
	if desired {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_movie_category_visible_edges(class_id,generation,library_id,kind,value,item_id,title,poster_url) VALUES(?,?,?,?,?,?,?,?)
		 ON CONFLICT(class_id,generation,library_id,kind,value,item_id) DO UPDATE SET title=excluded.title,poster_url=excluded.poster_url`, edge.class, edge.generation, edge.library, edge.kind, edge.value, edge.item, title, poster); err != nil {
			return err
		}
		if !current {
			delta = 1
		}
	} else if current {
		if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_movie_category_visible_edges WHERE class_id=? AND generation=? AND library_id=? AND kind=? AND value=? AND item_id=?`, edge.class, edge.generation, edge.library, edge.kind, edge.value, edge.item); err != nil {
			return err
		}
		delta = -1
	}
	if !desired && !current {
		return nil
	}
	if delta > 0 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_movie_category_visible_summaries(class_id,generation,library_id,kind,value,total,posters_json) VALUES(?,?,?,?,?,1,'[]') ON CONFLICT(class_id,generation,library_id,kind,value) DO UPDATE SET total=total+1`, edge.class, edge.generation, edge.library, edge.kind, edge.value); err != nil {
			return err
		}
	} else if delta < 0 {
		if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_movie_category_visible_summaries WHERE class_id=? AND generation=? AND library_id=? AND kind=? AND value=? AND total=1`, edge.class, edge.generation, edge.library, edge.kind, edge.value); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_movie_category_visible_summaries SET total=total-1 WHERE class_id=? AND generation=? AND library_id=? AND kind=? AND value=?`, edge.class, edge.generation, edge.library, edge.kind, edge.value); err != nil {
			return err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT poster_url FROM catalog_movie_category_visible_edges INDEXED BY catalog_movie_category_visible_poster
	 WHERE class_id=? AND generation=? AND library_id=? AND kind=? AND value=? AND poster_url<>'' ORDER BY title COLLATE NOCASE,item_id LIMIT 4`, edge.class, edge.generation, edge.library, edge.kind, edge.value)
	if err != nil {
		return err
	}
	posters := []string{}
	for rows.Next() {
		var poster string
		if err = rows.Scan(&poster); err != nil {
			break
		}
		posters = append(posters, poster)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	encoded, _ := json.Marshal(posters)
	_, err = tx.ExecContext(ctx, `UPDATE catalog_movie_category_visible_summaries SET posters_json=? WHERE class_id=? AND generation=? AND library_id=? AND kind=? AND value=?`, string(encoded), edge.class, edge.generation, edge.library, edge.kind, edge.value)
	return err
}
