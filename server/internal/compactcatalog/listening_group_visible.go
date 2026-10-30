package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// StepBookGroupVisibleCounts maintains distinct visible books per group. Each
// call visits at most limit class/file or class/group/book contributions, with
// durable cursors for both potentially large dimensions.
func StepBookGroupVisibleCounts(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	if limit < 1 || limit > 500 {
		return 0, fmt.Errorf("invalid book group visible limit")
	}
	if limit > 100 {
		limit = 100
	}
	var turn int
	if err := tx.QueryRowContext(ctx, `SELECT turn FROM catalog_book_group_visible_state WHERE id=1`).Scan(&turn); err != nil {
		return 0, err
	}
	steps := [3]func(context.Context, *sql.Tx, int) (int, error){stepBookVisibleFile, stepBookGroupVisiblePair, stepBookVisibleBackfill}
	for i := 0; i < len(steps); i++ {
		kind := (turn + i) % len(steps)
		n, err := steps[kind](ctx, tx, limit)
		if err != nil {
			return 0, err
		}
		if n > 0 {
			_, err = tx.ExecContext(ctx, `UPDATE catalog_book_group_visible_state SET turn=? WHERE id=1`, (kind+1)%len(steps))
			return n, err
		}
	}
	return 0, nil
}

type bookVisibleKey struct{ class, generation, book int64 }

func stepBookVisibleFile(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	var file, afterClass, afterGeneration int64
	err := tx.QueryRowContext(ctx, `SELECT file_id,class_cursor,generation_cursor FROM catalog_book_visible_file_jobs ORDER BY file_id LIMIT 1`).Scan(&file, &afterClass, &afterGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT class_id,generation FROM (
  SELECT class_id,generation FROM compact_visibility_rows INDEXED BY compact_visibility_sort_entity WHERE entity_id=? AND kind=9 AND (class_id,generation)>(?,?)
  UNION SELECT class_id,generation FROM catalog_book_visible_file_edges INDEXED BY catalog_book_visible_file_edges_file WHERE file_id=? AND (class_id,generation)>(?,?)
 ) ORDER BY class_id,generation LIMIT ?`, file, afterClass, afterGeneration, file, afterClass, afterGeneration, limit)
	if err != nil {
		return 0, err
	}
	keys := []bookVisibleKey{}
	for rows.Next() {
		var k bookVisibleKey
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
		if err = reconcileBookVisibleFile(ctx, tx, k, file); err != nil {
			return 0, err
		}
	}
	if len(keys) < limit {
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_book_visible_file_jobs WHERE file_id=?`, file)
	} else {
		last := keys[len(keys)-1]
		_, err = tx.ExecContext(ctx, `UPDATE catalog_book_visible_file_jobs SET class_cursor=?,generation_cursor=? WHERE file_id=?`, last.class, last.generation, file)
	}
	if err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		return 1, nil
	}
	return len(keys), nil
}
func reconcileBookVisibleFile(ctx context.Context, tx *sql.Tx, k bookVisibleKey, file int64) error {
	var oldBook, newBook int64
	err := tx.QueryRowContext(ctx, `SELECT book_id FROM catalog_book_visible_file_edges WHERE class_id=? AND generation=? AND file_id=?`, k.class, k.generation, file).Scan(&oldBook)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	err = tx.QueryRowContext(ctx, `SELECT f.book_id FROM compact_visibility_rows v JOIN catalog_book_files f ON f.entity_id=v.entity_id JOIN catalog_entities b ON b.id=f.book_id AND b.retired=0 WHERE v.class_id=? AND v.generation=? AND v.entity_id=? AND v.kind=9 AND v.library_id=b.library_id`, k.class, k.generation, file).Scan(&newBook)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if oldBook == newBook {
		return nil
	}
	if oldBook != 0 {
		if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_book_visible_file_edges WHERE class_id=? AND generation=? AND file_id=?`, k.class, k.generation, file); err != nil {
			return err
		}
		var count int64
		if err = tx.QueryRowContext(ctx, `SELECT total FROM catalog_book_visible_counts WHERE class_id=? AND generation=? AND book_id=?`, k.class, k.generation, oldBook).Scan(&count); err != nil {
			return err
		}
		if count == 1 {
			_, err = tx.ExecContext(ctx, `DELETE FROM catalog_book_visible_counts WHERE class_id=? AND generation=? AND book_id=?`, k.class, k.generation, oldBook)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE catalog_book_visible_counts SET total=total-1 WHERE class_id=? AND generation=? AND book_id=?`, k.class, k.generation, oldBook)
		}
		if err != nil {
			return err
		}
		if count == 1 {
			if err = queueBookGroupVisiblePairs(ctx, tx, oldBook); err != nil {
				return err
			}
		}
	}
	if newBook != 0 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_book_visible_file_edges(class_id,generation,file_id,book_id) VALUES(?,?,?,?)`, k.class, k.generation, file, newBook); err != nil {
			return err
		}
		var had bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_book_visible_counts WHERE class_id=? AND generation=? AND book_id=?)`, k.class, k.generation, newBook).Scan(&had); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO catalog_book_visible_counts(class_id,generation,book_id,total) VALUES(?,?,?,1) ON CONFLICT(class_id,generation,book_id) DO UPDATE SET total=total+1`, k.class, k.generation, newBook)
		if err != nil {
			return err
		}
		if !had {
			return queueBookGroupVisiblePairs(ctx, tx, newBook)
		}
	}
	return nil
}
func queueBookGroupVisiblePairs(ctx context.Context, tx *sql.Tx, book int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO catalog_book_group_visible_pair_jobs(group_id,book_id)
 SELECT group_id,book_id FROM catalog_book_group_members WHERE book_id=?
 ON CONFLICT(group_id,book_id) DO UPDATE SET class_cursor=0,generation_cursor=0`, book)
	return err
}
func stepBookGroupVisiblePair(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	var group, book, afterClass, afterGeneration int64
	err := tx.QueryRowContext(ctx, `SELECT group_id,book_id,class_cursor,generation_cursor FROM catalog_book_group_visible_pair_jobs ORDER BY group_id,book_id LIMIT 1`).Scan(&group, &book, &afterClass, &afterGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT class_id,generation FROM (
 SELECT class_id,generation FROM catalog_book_visible_counts INDEXED BY catalog_book_visible_counts_book WHERE book_id=? AND (class_id,generation)>(?,?)
 UNION SELECT class_id,generation FROM catalog_book_group_visible_edges INDEXED BY catalog_book_group_visible_edges_pair WHERE group_id=? AND book_id=? AND (class_id,generation)>(?,?)
 ) ORDER BY class_id,generation LIMIT ?`, book, afterClass, afterGeneration, group, book, afterClass, afterGeneration, limit)
	if err != nil {
		return 0, err
	}
	keys := []bookVisibleKey{}
	for rows.Next() {
		var k bookVisibleKey
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
		if err = reconcileBookGroupVisible(ctx, tx, k, group, book); err != nil {
			return 0, err
		}
	}
	if len(keys) < limit {
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_book_group_visible_pair_jobs WHERE group_id=? AND book_id=?`, group, book)
	} else {
		last := keys[len(keys)-1]
		_, err = tx.ExecContext(ctx, `UPDATE catalog_book_group_visible_pair_jobs SET class_cursor=?,generation_cursor=? WHERE group_id=? AND book_id=?`, last.class, last.generation, group, book)
	}
	if err != nil {
		return 0, err
	}
	if len(keys) == 0 {
		return 1, nil
	}
	return len(keys), nil
}
func reconcileBookGroupVisible(ctx context.Context, tx *sql.Tx, k bookVisibleKey, group, book int64) error {
	var desired, current bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_book_group_members m JOIN catalog_book_groups g ON g.id=m.group_id AND g.retired=0 JOIN catalog_book_visible_counts c ON c.book_id=m.book_id AND c.class_id=? AND c.generation=? WHERE m.group_id=? AND m.book_id=? AND g.library_id=(SELECT library_id FROM catalog_book_context WHERE book_id=m.book_id))`, k.class, k.generation, group, book).Scan(&desired)
	if err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_book_group_visible_edges WHERE class_id=? AND generation=? AND group_id=? AND book_id=?)`, k.class, k.generation, group, book).Scan(&current); err != nil {
		return err
	}
	if desired == current {
		return nil
	}
	if desired {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_book_group_visible_edges(class_id,generation,group_id,book_id) VALUES(?,?,?,?)`, k.class, k.generation, group, book); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO catalog_book_group_visible_counts(class_id,generation,group_id,total) VALUES(?,?,?,1) ON CONFLICT(class_id,generation,group_id) DO UPDATE SET total=total+1`, k.class, k.generation, group)
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM catalog_book_group_visible_edges WHERE class_id=? AND generation=? AND group_id=? AND book_id=?`, k.class, k.generation, group, book); err != nil {
		return err
	}
	var count int64
	if err = tx.QueryRowContext(ctx, `SELECT total FROM catalog_book_group_visible_counts WHERE class_id=? AND generation=? AND group_id=?`, k.class, k.generation, group).Scan(&count); err != nil {
		return err
	}
	if count == 1 {
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_book_group_visible_counts WHERE class_id=? AND generation=? AND group_id=?`, k.class, k.generation, group)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_book_group_visible_counts SET total=total-1 WHERE class_id=? AND generation=? AND group_id=?`, k.class, k.generation, group)
	}
	return err
}
func stepBookVisibleBackfill(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	var cursor int64
	var done bool
	if err := tx.QueryRowContext(ctx, `SELECT file_cursor,backfill_done FROM catalog_book_group_visible_state WHERE id=1`).Scan(&cursor, &done); err != nil {
		return 0, err
	}
	if done {
		return 0, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT entity_id FROM catalog_book_files WHERE entity_id>? ORDER BY entity_id LIMIT ?`, cursor, limit)
	if err != nil {
		return 0, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_book_group_visible_state SET backfill_done=1 WHERE id=1`)
		return 1, err
	}
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, `INSERT INTO catalog_book_visible_file_jobs(file_id) VALUES(?) ON CONFLICT(file_id) DO NOTHING`, id); err != nil {
			return 0, err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_book_group_visible_state SET file_cursor=? WHERE id=1`, ids[len(ids)-1])
	return len(ids), err
}
