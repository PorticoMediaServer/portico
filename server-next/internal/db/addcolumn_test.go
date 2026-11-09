package db

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestAddColumnCost is a measurement, not a check: the load workflow runs it against a seeded
// database to learn what adding a column to items costs with the table as it is and with it
// declared STRICT. It answers whether the large tables can be STRICT. Without
// PORTICO_LOAD_DB it is skipped.
func TestAddColumnCost(t *testing.T) {
	source := os.Getenv("PORTICO_LOAD_DB")
	if source == "" {
		t.Skip("set PORTICO_LOAD_DB to a seeded database to measure")
	}
	ctx := context.Background()
	result := map[string]any{}
	for _, strict := range []bool{false, true} {
		conn, err := open(filepath.Join(t.TempDir(), "items.db"), "rwc", 1, writeCacheKiB)
		if err != nil {
			t.Fatal(err)
		}
		exec := func(query string, args ...any) time.Duration {
			start := time.Now()
			if _, err := conn.ExecContext(ctx, query, args...); err != nil {
				t.Fatalf("%s: %v", query, err)
			}
			return time.Since(start)
		}
		// Only items is copied, so its references to other tables are not enforced here.
		exec(`PRAGMA foreign_keys = OFF`)
		exec(`ATTACH ? AS source`, source)
		var ddl string
		if err := conn.QueryRowContext(ctx, `SELECT sql FROM source.sqlite_master WHERE name = 'items'`).Scan(&ddl); err != nil {
			t.Fatal(err)
		}
		name := "plain"
		if strict {
			ddl, name = ddl+" STRICT", "strict"
		}
		exec(ddl)
		exec(`INSERT INTO items SELECT * FROM source.items`)
		exec(`DETACH source`)
		var rows int
		if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM items`).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		nullable := exec(`ALTER TABLE items ADD COLUMN trial_text TEXT`)
		withDefault := exec(`ALTER TABLE items ADD COLUMN trial_number INTEGER NOT NULL DEFAULT 0`)
		t.Logf("items %s, %d rows: ADD COLUMN nullable %s, NOT NULL DEFAULT %s", name, rows, nullable, withDefault)
		result[name] = map[string]any{"rows": rows, "addNullableColumnMs": float64(nullable.Microseconds()) / 1000,
			"addDefaultColumnMs": float64(withDefault.Microseconds()) / 1000}
		conn.Close()
	}
	if out := os.Getenv("PORTICO_LOAD_OUT"); out != "" {
		raw, _ := json.MarshalIndent(result, "", "  ")
		if err := os.WriteFile(out, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
