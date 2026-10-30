package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
)

type personalVisibilityPair struct {
	profile                   string
	class, generation, entity int64
}

func stepPersonalVisibilityProfile(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	var pair personalVisibilityPair
	var sequence, cursorSequence, afterClass, afterGeneration int64
	err := tx.QueryRowContext(ctx, `SELECT profile_id,entity_id,sequence,cursor_sequence,class_cursor,generation_cursor FROM catalog_personal_visibility_sort_profile_jobs ORDER BY profile_id,entity_id LIMIT 1`).Scan(&pair.profile, &pair.entity, &sequence, &cursorSequence, &afterClass, &afterGeneration)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if sequence != cursorSequence {
		afterClass, afterGeneration = 0, 0
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_personal_visibility_sort_profile_jobs SET cursor_sequence=sequence,class_cursor=0,generation_cursor=0 WHERE profile_id=? AND entity_id=?`, pair.profile, pair.entity); err != nil {
			return 0, err
		}
	}
	rows, err := tx.QueryContext(ctx, `WITH current AS (
	 SELECT class_id,generation FROM compact_visibility_rows INDEXED BY compact_visibility_sort_entity WHERE entity_id=? AND (class_id,generation)>(?,?) ORDER BY class_id,generation LIMIT ?
	), prior AS (
	 SELECT DISTINCT class_id,generation FROM catalog_personal_visibility_sort_edges INDEXED BY catalog_personal_visibility_sort_edge_entity WHERE profile_id=? AND entity_id=? AND (class_id,generation)>(?,?) ORDER BY class_id,generation LIMIT ?
	) SELECT class_id,generation FROM current UNION SELECT class_id,generation FROM prior ORDER BY class_id,generation LIMIT ?`, pair.entity, afterClass, afterGeneration, limit, pair.profile, pair.entity, afterClass, afterGeneration, limit, limit)
	if err != nil {
		return 0, err
	}
	keys := []personalVisibilityPair{}
	for rows.Next() {
		k := pair
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
		if err = reconcilePersonalVisibilityPair(ctx, tx, k); err != nil {
			return 0, err
		}
	}
	if len(keys) < limit {
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_personal_visibility_sort_profile_jobs WHERE profile_id=? AND entity_id=?`, pair.profile, pair.entity)
	} else {
		last := keys[len(keys)-1]
		_, err = tx.ExecContext(ctx, `UPDATE catalog_personal_visibility_sort_profile_jobs SET class_cursor=?,generation_cursor=? WHERE profile_id=? AND entity_id=? AND sequence=?`, last.class, last.generation, pair.profile, pair.entity, sequence)
	}
	if err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		return 1, nil
	}
	return len(keys), nil
}

func stepPersonalVisibilityClass(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	var pair personalVisibilityPair
	var sequence, cursorSequence int64
	var after string
	err := tx.QueryRowContext(ctx, `SELECT class_id,generation,entity_id,sequence,cursor_sequence,profile_cursor FROM catalog_personal_visibility_sort_class_jobs ORDER BY class_id,generation,entity_id LIMIT 1`).Scan(&pair.class, &pair.generation, &pair.entity, &sequence, &cursorSequence, &after)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if sequence != cursorSequence {
		after = ""
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_personal_visibility_sort_class_jobs SET cursor_sequence=sequence,profile_cursor='' WHERE class_id=? AND generation=? AND entity_id=?`, pair.class, pair.generation, pair.entity); err != nil {
			return 0, err
		}
	}
	rows, err := tx.QueryContext(ctx, `WITH current AS (
	 SELECT profile_id FROM catalog_personal_sort_rows INDEXED BY catalog_personal_sort_row_entity WHERE entity_id=? AND profile_id>? ORDER BY profile_id LIMIT ?
	), prior AS (
	 SELECT DISTINCT profile_id FROM catalog_personal_visibility_sort_edges INDEXED BY catalog_personal_visibility_sort_edge_class WHERE class_id=? AND generation=? AND entity_id=? AND profile_id>? ORDER BY profile_id LIMIT ?
	) SELECT profile_id FROM current UNION SELECT profile_id FROM prior ORDER BY profile_id LIMIT ?`, pair.entity, after, limit, pair.class, pair.generation, pair.entity, after, limit, limit)
	if err != nil {
		return 0, err
	}
	keys := []personalVisibilityPair{}
	for rows.Next() {
		k := pair
		if err = rows.Scan(&k.profile); err != nil {
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
		if err = reconcilePersonalVisibilityPair(ctx, tx, k); err != nil {
			return 0, err
		}
	}
	if len(keys) < limit {
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_personal_visibility_sort_class_jobs WHERE class_id=? AND generation=? AND entity_id=?`, pair.class, pair.generation, pair.entity)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_personal_visibility_sort_class_jobs SET profile_cursor=? WHERE class_id=? AND generation=? AND entity_id=? AND sequence=?`, keys[len(keys)-1].profile, pair.class, pair.generation, pair.entity, sequence)
	}
	if err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		return 1, nil
	}
	return len(keys), nil
}

func reconcilePersonalVisibilityPair(ctx context.Context, tx *sql.Tx, pair personalVisibilityPair) error {
	type edge struct {
		library, kind, axis int
		value               string
	}
	rows, err := tx.QueryContext(ctx, `SELECT library_id,kind,axis,value FROM catalog_personal_visibility_sort_edges WHERE profile_id=? AND class_id=? AND generation=? AND entity_id=? ORDER BY axis`, pair.profile, pair.class, pair.generation, pair.entity)
	if err != nil {
		return err
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
		result, err := tx.ExecContext(ctx, `UPDATE catalog_personal_visibility_sort_buckets SET total=total-1 WHERE profile_id=? AND class_id=? AND generation=? AND library_id=? AND kind=? AND axis=? AND value=?`, pair.profile, pair.class, pair.generation, e.library, e.kind, e.axis, e.value)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return fmt.Errorf("missing personal class sort bucket")
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_personal_visibility_sort_edges WHERE profile_id=? AND class_id=? AND generation=? AND entity_id=?`, pair.profile, pair.class, pair.generation, pair.entity); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_personal_visibility_sort_buckets WHERE profile_id=? AND class_id=? AND generation=? AND total=0`, pair.profile, pair.class, pair.generation); err != nil {
		return err
	}
	var library, kind int
	var rating, month, year string
	err = tx.QueryRowContext(ctx, `SELECT p.library_id,p.kind,p.rating_key,p.last_month,p.last_year FROM catalog_personal_sort_rows p JOIN compact_visibility_rows v ON v.entity_id=p.entity_id AND v.library_id=p.library_id AND v.kind=p.kind WHERE p.profile_id=? AND p.entity_id=? AND v.class_id=? AND v.generation=?`, pair.profile, pair.entity, pair.class, pair.generation).Scan(&library, &kind, &rating, &month, &year)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	for offset, value := range [3]string{rating, month, year} {
		axis := offset + 7
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_personal_visibility_sort_edges(profile_id,class_id,generation,entity_id,library_id,kind,axis,value) VALUES(?,?,?,?,?,?,?,?)`, pair.profile, pair.class, pair.generation, pair.entity, library, kind, axis, value); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_personal_visibility_sort_buckets(profile_id,class_id,generation,library_id,kind,axis,value,total) VALUES(?,?,?,?,?,?,?,1) ON CONFLICT(profile_id,class_id,generation,library_id,kind,axis,value) DO UPDATE SET total=total+1`, pair.profile, pair.class, pair.generation, library, kind, axis, value); err != nil {
			return err
		}
	}
	return nil
}
