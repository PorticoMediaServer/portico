package mggroupg

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestINTGroupGSelectedMetadata011(t *testing.T) {
	f := native(t, candidate)
	var m struct{ Objects []struct{ Name, Type string } }
	if err := json.Unmarshal(manifest, &m); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, o := range m.Objects {
		name := o.Name
		if o.Type == "index" {
			if err := f.c.QueryRowContext(t.Context(), "SELECT tbl_name FROM sqlite_schema WHERE type='index' AND name=?", name).Scan(&name); err != nil {
				t.Fatal(err)
			}
		}
		names[name] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	slices.Sort(sorted)
	out := map[string]any{"source_scope": "selected G tables and index targets only; no full FK probe sweep", "tables": map[string]any{}}
	for _, name := range sorted {
		var create string
		if err := f.c.QueryRowContext(t.Context(), "SELECT sql FROM sqlite_schema WHERE type='table' AND name=?", name).Scan(&create); err != nil {
			t.Fatal(err)
		}
		rows, err := f.c.QueryContext(t.Context(), "PRAGMA main.index_list("+name+")")
		if err != nil {
			t.Fatal(err)
		}
		var indexes []map[string]any
		for rows.Next() {
			var seq, unique, partial int
			var index, origin string
			if err := rows.Scan(&seq, &index, &unique, &origin, &partial); err != nil {
				t.Fatal(err)
			}
			indexes = append(indexes, map[string]any{"name": index, "unique": unique != 0, "origin": origin, "partial": partial != 0})
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		for _, index := range indexes {
			rows, err = f.c.QueryContext(t.Context(), "PRAGMA main.index_xinfo("+index["name"].(string)+")")
			if err != nil {
				t.Fatal(err)
			}
			var keys []map[string]any
			for rows.Next() {
				var seq, cid, desc, key int
				var col, coll sql.NullString
				if err := rows.Scan(&seq, &cid, &col, &desc, &coll, &key); err != nil {
					t.Fatal(err)
				}
				if key == 1 {
					var column any
					if col.Valid {
						column = col.String
					}
					keys = append(keys, map[string]any{"column": column, "collation": coll.String, "descending": desc != 0})
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			index["keys"] = keys
			var indexSQL sql.NullString
			if err := f.c.QueryRowContext(t.Context(), "SELECT sql FROM sqlite_schema WHERE type='index' AND name=?", index["name"]).Scan(&indexSQL); err != nil && !(err == sql.ErrNoRows && index["origin"] == "pk") {
				t.Fatal(err)
			}
			if indexSQL.Valid {
				index["sql"] = indexSQL.String
			} else {
				index["sql"] = nil
			}
		}
		out["tables"].(map[string]any)[name] = map[string]any{"sql": create, "strict": strings.HasSuffix(create, "STRICT"), "complete_release_indexes": indexes}
	}
	var count int
	if err := f.c.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_schema").Scan(&count); err != nil {
		t.Fatal(err)
	}
	out["sqlite_schema_rows"] = count
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR")
	if dir == "" {
		t.Fatal("runner evidence directory required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "G-SELECTED-METADATA.json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("selected tables=%d native sqlite_schema rows=%d", len(sorted), count)
}
