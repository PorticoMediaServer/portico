package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestUpgradeCost is a measurement, not a check. It rehearses what a release after the first
// could do to a large library's database, and times each step: adding a column to the largest
// tables, as they are and declared STRICT; rebuilding a table (the only way to change a
// column's type or constraint, or to make a table STRICT); adding an index; a new table; and a
// backfill in small batches. The numbers say which steps can run while the server starts and
// which must be background work, and so which tables must be right before the first release.
//
// It changes the database it is given, so it runs only when PORTICO_UPGRADE_DB names a copy
// made for it. PORTICO_UPGRADE_OUT, if set, receives the numbers as JSON.
func TestUpgradeCost(t *testing.T) {
	path := os.Getenv("PORTICO_UPGRADE_DB")
	if path == "" {
		t.Skip("set PORTICO_UPGRADE_DB to a seeded database it may change")
	}
	ctx := context.Background()
	conn, err := open(path, "rw", 1, writeCacheKiB)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	result := map[string]any{}
	timed := func(name string, statements ...string) float64 {
		t.Helper()
		start := time.Now()
		for _, s := range statements {
			if _, err := conn.ExecContext(ctx, s); err != nil {
				conn.ExecContext(ctx, `ROLLBACK`) // a failed rebuild leaves its transaction open
				t.Logf("%s: %s: %v", name, s, err)
				result[name] = "failed: " + err.Error()
				return -1
			}
		}
		ms := float64(time.Since(start).Microseconds()) / 1000
		t.Logf("%-55s %10.1f ms", name, ms)
		result[name] = ms
		return ms
	}
	// A rebuild drops a table others refer to; references are checked once at the end.
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}

	// The largest tables by rows.
	var tables []string
	rows, err := conn.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		rows.Scan(&name)
		tables = append(tables, name)
	}
	rows.Close()
	counts := map[string]int64{}
	for _, name := range tables {
		var n int64
		conn.QueryRowContext(ctx, `SELECT count(*) FROM `+name).Scan(&n)
		counts[name] = n
	}
	slices.SortFunc(tables, func(a, b string) int { return int(counts[b] - counts[a]) })
	largest := tables[:6]
	sizes := map[string]int64{}
	for _, name := range largest {
		sizes[name] = counts[name]
	}
	result["rows"] = sizes
	t.Logf("largest tables: %v", sizes)

	addColumns := func(table, as string) {
		suffix := strings.ReplaceAll(as, " ", "_")
		timed(table+" "+as+": ADD COLUMN nullable", `ALTER TABLE `+table+` ADD COLUMN trial_text_`+suffix+` TEXT`)
		timed(table+" "+as+": ADD COLUMN NOT NULL DEFAULT", `ALTER TABLE `+table+` ADD COLUMN trial_number_`+suffix+` INTEGER NOT NULL DEFAULT 0`)
		timed(table+" "+as+": ADD COLUMN with CHECK", `ALTER TABLE `+table+` ADD COLUMN trial_flag_`+suffix+
			` INTEGER NOT NULL DEFAULT 0 CHECK (trial_flag_`+suffix+` IN (0, 1))`)
	}
	for _, table := range largest {
		declared := tableSQL(t, conn, table)
		addColumns(table, "as declared")
		timed(table+": rebuild as declared", rebuild(t, conn, table, false)...)
		if strings.Contains(declared[strings.LastIndex(declared, ")"):], "STRICT") {
			continue
		}
		if timed(table+": rebuild as STRICT", rebuild(t, conn, table, true)...) >= 0 {
			addColumns(table, "STRICT")
		}
	}

	// The plan's "next release": an index on items and on credits, a new table, a backfill.
	timed("CREATE TABLE (new, empty)", `CREATE TABLE trial_new (id INTEGER NOT NULL PRIMARY KEY, item_id INTEGER NOT NULL REFERENCES items(id), note TEXT) STRICT`)
	timed("items: ADD COLUMN for the backfill", `ALTER TABLE items ADD COLUMN trial_backfill INTEGER`)
	backfill(t, conn, result)
	timed("items: CREATE INDEX on the backfilled column", `CREATE INDEX trial_items_backfill ON items (trial_backfill)`)
	timed("credits: CREATE INDEX (role, person_id)", `CREATE INDEX trial_credits_role ON credits (role, person_id)`)
	timed("whole database: PRAGMA foreign_key_check", `PRAGMA foreign_key_check`)

	if out := os.Getenv("PORTICO_UPGRADE_OUT"); out != "" {
		raw, _ := json.MarshalIndent(result, "", "  ")
		if err := os.WriteFile(out, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func tableSQL(t *testing.T, conn *sql.DB, table string) string {
	var ddl string
	if err := conn.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	return ddl
}

// rebuild is SQLite's documented way to change a table beyond ADD COLUMN: copy it into a new
// table, drop the old one, rename, and make its indexes again, all in one transaction.
func rebuild(t *testing.T, conn *sql.DB, table string, strict bool) []string {
	ddl := tableSQL(t, conn, table)
	// After one rebuild SQLite stores the name quoted, so everything before the columns is replaced.
	ddl = "CREATE TABLE " + table + "_rebuilt " + ddl[strings.Index(ddl, "("):]
	if strict {
		if strings.HasSuffix(ddl, "WITHOUT ROWID") {
			ddl = strings.TrimSuffix(ddl, "WITHOUT ROWID") + "STRICT, WITHOUT ROWID"
		} else {
			ddl += " STRICT"
		}
	}
	steps := []string{`BEGIN IMMEDIATE`, ddl, `INSERT INTO ` + table + `_rebuilt SELECT * FROM ` + table, `DROP TABLE ` + table,
		`ALTER TABLE ` + table + `_rebuilt RENAME TO ` + table}
	rows, err := conn.Query(`SELECT sql FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND sql IS NOT NULL`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var index string
		rows.Scan(&index)
		steps = append(steps, index)
	}
	return append(steps, `COMMIT`)
}

// backfill fills a new column of items in batches of 2,000 rows, one transaction each, as
// background work would. The longest batch is how long a person's write would wait.
func backfill(t *testing.T, conn *sql.DB, result map[string]any) {
	var last int64
	conn.QueryRow(`SELECT max(id) FROM items`).Scan(&last)
	const batch = 2000
	var longest time.Duration
	var times []time.Duration
	start := time.Now()
	for from := int64(0); from <= last; from += batch {
		began := time.Now()
		if _, err := conn.Exec(`UPDATE items SET trial_backfill = length(title) + year WHERE id > ? AND id <= ?`, from, from+batch); err != nil {
			t.Fatal(err)
		}
		took := time.Since(began)
		times = append(times, took)
		longest = max(longest, took)
	}
	slices.Sort(times)
	p99 := times[len(times)*99/100]
	total := time.Since(start)
	t.Logf("%-55s %10.1f ms (%d batches, p99 %.1f ms, longest %.1f ms)", "items: backfill in batches of 2,000",
		float64(total.Microseconds())/1000, len(times), float64(p99.Microseconds())/1000, float64(longest.Microseconds())/1000)
	result["items: backfill in batches of 2,000"] = map[string]any{"totalMs": total.Milliseconds(), "batches": len(times),
		"p99BatchMs": float64(p99.Microseconds()) / 1000, "longestBatchMs": float64(longest.Microseconds()) / 1000}
}
