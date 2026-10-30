package compactcatalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
)

type movieCategoryKey struct {
	library, kind int
	value         string
	desired       bool
}

func categoryLess(a, b movieCategoryKey) bool {
	if a.library != b.library {
		return a.library < b.library
	}
	if a.kind != b.kind {
		return a.kind < b.kind
	}
	return a.value < b.value
}

// Per-item category statements that must stay index seeks.
const (
	categoryMemberCount  = `SELECT count(*) FROM catalog_movie_category_members INDEXED BY catalog_movie_category_member_id WHERE item_id=? AND library_id=? AND kind=? AND value=?`
	categoryMemberDelete = `DELETE FROM catalog_movie_category_members INDEXED BY catalog_movie_category_member_id WHERE item_id=? AND library_id=? AND kind=? AND value=?`
)

// deriveCategories reconciles one movie's category edges (decade 0, genre 1,
// studio 2), at most limit categories per step. Old edges survive the movie's
// deletion so their counts and poster samples can be reversed without
// scanning a library. The cursor restarts when the movie is queued again.
func deriveCategories(ctx context.Context, tx *sql.Tx, k Key, limit int) (bool, error) {
	if limit < 1 || limit > 500 {
		return false, fmt.Errorf("invalid category fanout limit")
	}
	var prior int64
	var after movieCategoryKey
	cursorErr := tx.QueryRowContext(ctx, `SELECT sequence,after_library,after_kind,after_value FROM catalog_movie_category_cursors WHERE item_id=?`, k.ID).Scan(&prior, &after.library, &after.kind, &after.value)
	if cursorErr != nil && cursorErr != sql.ErrNoRows {
		return false, cursorErr
	}
	if cursorErr == sql.ErrNoRows || prior != k.Revision {
		after = movieCategoryKey{kind: -1}
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_movie_category_cursors(item_id,sequence,projection_version,after_library,after_kind,after_value) VALUES(?,?,1,0,-1,'') ON CONFLICT(item_id) DO UPDATE SET sequence=excluded.sequence,after_library=0,after_kind=-1,after_value=''`, k.ID, k.Revision); err != nil {
			return false, err
		}
	}
	old, err := readCategoryKeys(ctx, tx, `SELECT library_id,kind,value FROM catalog_movie_category_members INDEXED BY catalog_movie_category_member_id WHERE item_id=? AND (library_id,kind,value)>(?,?,?) GROUP BY library_id,kind,value ORDER BY library_id,kind,value LIMIT ?`, k.ID, after, limit)
	if err != nil {
		return false, err
	}
	current, err := currentCategoryKeys(ctx, tx, k.ID, after, limit)
	if err != nil {
		return false, err
	}
	all := append(old, current...)
	sort.Slice(all, func(i, j int) bool { return categoryLess(all[i], all[j]) })
	keys := make([]movieCategoryKey, 0, limit)
	for _, candidate := range all {
		if len(keys) > 0 && !categoryLess(keys[len(keys)-1], candidate) && !categoryLess(candidate, keys[len(keys)-1]) {
			keys[len(keys)-1].desired = keys[len(keys)-1].desired || candidate.desired
			continue
		}
		keys = append(keys, candidate)
		if len(keys) == limit {
			break
		}
	}
	for _, category := range keys {
		var priorCount int
		if err := tx.QueryRowContext(ctx, categoryMemberCount, k.ID, category.library, category.kind, category.value).Scan(&priorCount); err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, categoryMemberDelete, k.ID, category.library, category.kind, category.value); err != nil {
			return false, err
		}
		var desiredCount int
		if category.desired {
			result, err := tx.ExecContext(ctx, `INSERT INTO catalog_movie_category_members(library_id,kind,value,item_id,title,poster_url)
 SELECT ?,?,?,e.id,e.title,COALESCE(d.poster_url,'') FROM catalog_entities e LEFT JOIN catalog_item_details d ON d.entity_id=e.id
 WHERE e.id=? AND e.kind=1 AND e.retired=0 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements g WHERE g.item_id=e.id)`, category.library, category.kind, category.value, k.ID)
			if err != nil {
				return false, err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return false, err
			}
			desiredCount = int(n)
		}
		if err := updateCategorySummary(ctx, tx, category, desiredCount-priorCount); err != nil {
			return false, err
		}
	}
	if len(keys) < limit {
		_, err := tx.ExecContext(ctx, `DELETE FROM catalog_movie_category_cursors WHERE item_id=?`, k.ID)
		return true, err
	}
	last := keys[len(keys)-1]
	_, err = tx.ExecContext(ctx, `UPDATE catalog_movie_category_cursors SET after_library=?,after_kind=?,after_value=? WHERE item_id=?`, last.library, last.kind, last.value, k.ID)
	return false, err
}

func init() {
	Register(DomainCategories, Derivation{
		Version: 1,
		Drain: func(ctx context.Context, tx *sql.Tx, keys []Key, limit int) ([]int64, error) {
			var done []int64
			for _, k := range keys {
				finished, err := deriveCategories(ctx, tx, k, limit)
				if err != nil {
					return nil, err
				}
				if finished {
					done = append(done, k.ID)
				}
			}
			return done, nil
		},
		Backfill: func(ctx context.Context, tx *sql.Tx, after int64, limit int) ([]int64, error) {
			return scanIDs(ctx, tx, `SELECT id FROM catalog_entities WHERE kind=1 AND id>? ORDER BY id LIMIT ?`, after, limit)
		},
	})
}

func readCategoryKeys(ctx context.Context, tx *sql.Tx, query string, key int64, after movieCategoryKey, limit int) ([]movieCategoryKey, error) {
	rows, err := tx.QueryContext(ctx, query, key, after.library, after.kind, after.value, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]movieCategoryKey, 0, limit)
	for rows.Next() {
		var k movieCategoryKey
		if err := rows.Scan(&k.library, &k.kind, &k.value); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func currentCategoryKeys(ctx context.Context, tx *sql.Tx, item int64, after movieCategoryKey, limit int) ([]movieCategoryKey, error) {
	var library, year int
	err := tx.QueryRowContext(ctx, `SELECT e.library_id,e.year FROM catalog_entities e
 WHERE e.id=? AND e.kind=1 AND e.retired=0 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements d WHERE d.item_id=e.id)`, item).Scan(&library, &year)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]movieCategoryKey, 0, 3*limit)
	if year >= 1800 && year <= 2199 {
		decade := movieCategoryKey{library: library, kind: 0, value: fmt.Sprint((year / 10) * 10), desired: true}
		if categoryLess(after, decade) {
			out = append(out, decade)
		}
	}
	for kind := 1; kind <= 2; kind++ {
		if library < after.library || (library == after.library && kind < after.kind) {
			continue
		}
		start := ""
		if library == after.library && kind == after.kind {
			start = after.value
		}
		query := `SELECT DISTINCT t.label FROM catalog_entity_terms m JOIN catalog_terms t ON t.id=m.term_id AND t.vocab=1 WHERE m.entity_id=? AND t.label>? ORDER BY t.label LIMIT ?`
		if kind == 2 {
			query = `SELECT DISTINCT x.source_value FROM catalog_item_attribute_edges x JOIN catalog_attribute_terms t ON t.id=x.term_id
 JOIN catalog_attribute_fields f ON f.id=t.field_id AND f.field='studio' WHERE x.item_id=? AND x.source_value>? ORDER BY x.source_value LIMIT ?`
		}
		rows, err := tx.QueryContext(ctx, query, item, start, limit)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var value string
			if err = rows.Scan(&value); err != nil {
				break
			}
			out = append(out, movieCategoryKey{library: library, kind: kind, value: value, desired: true})
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func updateCategorySummary(ctx context.Context, tx *sql.Tx, key movieCategoryKey, delta int) error {
	if delta > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_movie_category_summaries(library_id,kind,value,total,posters_json) VALUES(?,?,?,?, '[]') ON CONFLICT(library_id,kind,value) DO UPDATE SET total=total+excluded.total`, key.library, key.kind, key.value, delta); err != nil {
			return err
		}
	} else if delta < 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_movie_category_summaries WHERE library_id=? AND kind=? AND value=? AND total=?`, key.library, key.kind, key.value, -delta); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_movie_category_summaries SET total=total+? WHERE library_id=? AND kind=? AND value=?`, delta, key.library, key.kind, key.value); err != nil {
			return err
		}
	}
	posters := []string{}
	rows, err := tx.QueryContext(ctx, `SELECT poster_url FROM catalog_movie_category_members INDEXED BY catalog_movie_category_poster WHERE library_id=? AND kind=? AND value=? AND poster_url<>'' ORDER BY title COLLATE NOCASE,item_id LIMIT 4`, key.library, key.kind, key.value)
	if err != nil {
		return err
	}
	for rows.Next() {
		var poster string
		if err := rows.Scan(&poster); err != nil {
			rows.Close()
			return err
		}
		posters = append(posters, poster)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(posters)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_movie_category_summaries SET posters_json=? WHERE library_id=? AND kind=? AND value=?`, string(encoded), key.library, key.kind, key.value)
	return err
}
