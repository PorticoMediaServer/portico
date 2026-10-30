package persistence

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// These indexes are on maintenance cursors, rather than immutable identity
// columns. Measure both SQL row effects and the extra index B-tree operations
// for one actual background walk advance; total_changes alone omits index work.
func TestProfilePurgeCursorIndexAdvanceCost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "cursor-cost.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cases := []struct{ table, index, columns, seed, where string }{
		{"catalog_personal_sort_entity_jobs", "p2_62_purge_sort_entity_cursor", "profile_cursor,entity_id", "INSERT INTO catalog_personal_sort_entity_jobs(entity_id) VALUES(1)", "entity_id=1"},
		{"catalog_personal_sort_member_jobs", "p2_62_purge_sort_member_cursor", "profile_cursor,item_id", "INSERT INTO catalog_personal_sort_member_jobs(item_id) VALUES(1)", "item_id=1"},
		{"catalog_personal_visibility_sort_class_jobs", "p2_62_purge_visibility_class_cursor", "profile_cursor,class_id,generation,entity_id", "INSERT INTO catalog_personal_visibility_sort_class_jobs(class_id,generation,entity_id) VALUES(1,1,1)", "class_id=1 AND generation=1 AND entity_id=1"},
	}
	for _, c := range cases {
		t.Run(c.table, func(t *testing.T) {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for _, q := range []string{"DROP INDEX " + c.index, c.seed} {
				if _, err = tx.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			q := "UPDATE " + c.table + " SET profile_cursor=? WHERE " + c.where
			before := cursorIndexOps(t, tx, q)
			var rowEffects [2]int64
			for i := range rowEffects {
				if i == 1 {
					if _, err = tx.Exec("CREATE INDEX " + c.index + " ON " + c.table + "(" + c.columns + ")"); err != nil {
						t.Fatal(err)
					}
				}
				var start, end int64
				if err = tx.QueryRow(`SELECT total_changes()`).Scan(&start); err != nil {
					t.Fatal(err)
				}
				if _, err = tx.Exec(q, []string{"old-owner", "next-owner"}[i]); err != nil {
					t.Fatal(err)
				}
				if err = tx.QueryRow(`SELECT total_changes()`).Scan(&end); err != nil {
					t.Fatal(err)
				}
				rowEffects[i] = end - start
			}
			after := cursorIndexOps(t, tx, q)
			if rowEffects != [2]int64{1, 1} || after[0]-before[0] != 1 || after[1]-before[1] != 1 {
				t.Fatalf("unexpected per-advance cost: row effects=%v index ops before=%v after=%v", rowEffects, before, after)
			}
			t.Logf("per advance: SQL row effects before=%d after=%d; IdxDelete before=%d after=%d; IdxInsert before=%d after=%d (one extra index delete + insert)", rowEffects[0], rowEffects[1], before[0], after[0], before[1], after[1])
		})
	}
}
func cursorIndexOps(t *testing.T, tx *sql.Tx, q string) (out [2]int) {
	t.Helper()
	rows, err := tx.Query("EXPLAIN "+q, "probe-owner")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var addr, p1, p2, p3, p5 int
		var op string
		var p4, comment any
		if err = rows.Scan(&addr, &op, &p1, &p2, &p3, &p4, &p5, &comment); err != nil {
			t.Fatal(err)
		}
		if op == "IdxDelete" {
			out[0]++
		}
		if op == "IdxInsert" {
			out[1]++
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return
}
