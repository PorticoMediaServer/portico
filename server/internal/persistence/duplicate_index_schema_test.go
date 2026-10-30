package persistence

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// An index whose key columns, order, collation and partial predicate match
// another index on the same table (including a primary key or UNIQUE
// constraint's automatic index) only costs storage and a write per row. S5
// found two; this guard reads the built schema so the next one fails here.
func TestBaselineHasNoDuplicateIndexes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "duplicate-index-guard.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, duplicate := range duplicateIndexes(t, db) {
		t.Error(duplicate)
	}
}

// The guard itself must see a duplicate of a primary key's automatic index and
// of an explicit index, and must not confuse different orders, collations,
// directions or partial predicates with one.
func TestDuplicateIndexGuardFindsDuplicates(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "guard.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE t(a TEXT, b TEXT, c TEXT, PRIMARY KEY(a,b)) STRICT`,
		`CREATE INDEX t_pk_again ON t(a,b)`,
		`CREATE INDEX t_ab_desc ON t(a,b DESC)`,
		`CREATE INDEX t_ba ON t(b,a)`,
		`CREATE INDEX t_ab_nocase ON t(a COLLATE NOCASE,b)`,
		`CREATE INDEX t_c ON t(c)`,
		`CREATE INDEX t_c_again ON t(c)`,
		`CREATE INDEX t_c_partial ON t(c) WHERE a IS NOT NULL`,
		`CREATE INDEX t_lower ON t(lower(c))`,
		`CREATE INDEX t_upper ON t(upper(c))`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(statement, err)
		}
	}
	got := duplicateIndexes(t, db)
	want := []string{"t: t_c and t_c_again have the same key", "t: sqlite_autoindex_t_1 and t_pk_again have the same key"}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("duplicates:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

var duplicateIndexWhere = regexp.MustCompile(`(?is)\)\s*WHERE\s+(.*)$`)

func duplicateIndexes(t *testing.T, db *sql.DB) []string {
	t.Helper()
	tables := querySchemaStrings(t, db, `SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	var out []string
	for _, table := range tables {
		rows, err := db.Query(`SELECT name FROM pragma_index_list(?) ORDER BY name`, table)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for rows.Next() {
			var name string
			if err = rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			names = append(names, name)
		}
		rows.Close()
		seen := map[string]string{}
		for _, name := range names {
			key := indexKey(t, db, name)
			if previous, ok := seen[key]; ok {
				out = append(out, fmt.Sprintf("%s: %s and %s have the same key", table, previous, name))
				continue
			}
			seen[key] = name
		}
	}
	return out
}

// indexKey is the index's key columns with their direction and collation, plus
// its partial predicate. An expression column has no name; its whole column
// list is then part of the key.
func indexKey(t *testing.T, db *sql.DB, index string) string {
	t.Helper()
	rows, err := db.Query(`SELECT cid,coalesce(name,''),desc,coll FROM pragma_index_xinfo(?) WHERE key=1 ORDER BY seqno`, index)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var parts []string
	expression := false
	for rows.Next() {
		var cid, desc int
		var name, collation string
		if err = rows.Scan(&cid, &name, &desc, &collation); err != nil {
			t.Fatal(err)
		}
		expression = expression || cid == -2
		parts = append(parts, fmt.Sprintf("%s/%d/%s", name, desc, strings.ToUpper(collation)))
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	var definition sql.NullString
	if err = db.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='index' AND name=?`, index).Scan(&definition); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	text := strings.Join(strings.Fields(definition.String), " ")
	key := strings.Join(parts, ",")
	if expression {
		if open := strings.Index(text, "("); open >= 0 {
			key += " expr " + text[open:]
		}
	}
	if match := duplicateIndexWhere.FindStringSubmatch(text); match != nil {
		key += " where " + strings.ToLower(match[1])
	}
	return key
}

func querySchemaStrings(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		out = append(out, value)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
