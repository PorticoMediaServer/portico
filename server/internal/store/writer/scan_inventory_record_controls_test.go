package writer

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// The command and original reference are frozen before enlarging preserved
// bytes. Use the complete native driver unchanged, including its real registry,
// opening, keeper, governor, recipe and memory partition.
func TestN1OtherExistingRecordCapacityBeforeEffects(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"jobs_error", `UPDATE jobs SET error=? WHERE id=?`},
		{"source_name", `UPDATE library_sources SET name=? WHERE id=(SELECT source_id FROM inventory_runs WHERE job_id=?)`},
		{"directory_revision", `UPDATE inventory_directories SET revision=? WHERE job_id=? AND relative_path='.'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, preparation, command, original := n1ComposedDriverPrepared(t)
			db := d.opening.executor.db
			changed, err := db.ExecContext(t.Context(), tc.query, strings.Repeat("x", 128<<10), original.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if n, err := changed.RowsAffected(); err != nil || n != 1 {
				t.Fatal("control must enlarge exactly one existing row", n, err)
			}
			// Snapshot every native table after injecting the preserved bytes. This
			// catches partial fresh writes as well as mutations to any touched row.
			snapshot := func() map[string][][]any {
				t.Helper()
				names, err := db.QueryContext(t.Context(), `SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
				if err != nil {
					t.Fatal(err)
				}
				var tables []string
				for names.Next() {
					var name string
					if err := names.Scan(&name); err != nil {
						t.Fatal(err)
					}
					tables = append(tables, name)
				}
				if err := errors.Join(names.Err(), names.Close()); err != nil {
					t.Fatal(err)
				}
				out := make(map[string][][]any)
				for _, table := range tables {
					rows, err := db.QueryContext(t.Context(), `SELECT * FROM "`+strings.ReplaceAll(table, `"`, `""`)+`"`)
					if err != nil {
						t.Fatal(err)
					}
					columns, err := rows.Columns()
					if err != nil {
						t.Fatal(err)
					}
					for rows.Next() {
						values := make([]any, len(columns))
						dest := make([]any, len(columns))
						for i := range values {
							dest[i] = &values[i]
						}
						if err := rows.Scan(dest...); err != nil {
							t.Fatal(err)
						}
						out[table] = append(out[table], values)
					}
					if err := errors.Join(rows.Err(), rows.Close()); err != nil {
						t.Fatal(err)
					}
				}
				return out
			}
			before := snapshot()
			got, err := preparation.Accept(t.Context(), command)
			var refusal *Refusal
			if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || !reflect.DeepEqual(got, InventoryResult{}) {
				t.Fatalf("preserved row must refuse capacity with a full zero result: result=%+v err=%v", got, err)
			}
			if after := snapshot(); !reflect.DeepEqual(before, after) {
				t.Fatal("capacity refusal changed native tables")
			}
			if reference, err := InventoryAcceptReference(command); err != nil || reference != original {
				t.Fatal("prepared original changed", reference, err)
			}
		})
	}
}
