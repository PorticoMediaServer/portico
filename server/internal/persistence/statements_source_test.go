package persistence

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionStatementSourceKeepsCompleteQueries(t *testing.T) {
	root := t.TempDir()
	// The projection is only used to build queries; a qualified column without
	// its FROM clause is not independently executable. A similar-looking query
	// passed directly to the database must still be checked, even without FROM.
	source := "package fixture\n" + `
const projection = "SELECT a.username"
const source = " FROM accounts a"
const complete = (projection + source)
const reused = "SELECT a.id"
const combined = reused + source
func queries(db interface{}) {
 db.Query(complete)
 db.Query(combined)
 db.Query(reused)
 db.Query("SELECT 1")
 db.Query("SELECT " + "username FROM accounts")
 db.Query("SELECT missing_column FROM accounts")
 db.Query("SELECT a.missing_column FROM accounts a")
 db.Query("SELECT v.id")
 db.Query("SELECT " + dynamicProjection)
}
`
	if err := os.WriteFile(filepath.Join(root, "queries.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	statements, _ := productionStatementsIn(t, root)
	want := map[string]bool{
		"SELECT a.username FROM accounts a":       false,
		"SELECT a.id FROM accounts a":             false,
		"SELECT a.id":                             false,
		"SELECT 1":                                false,
		"SELECT username FROM accounts":           false,
		"SELECT missing_column FROM accounts":     false,
		"SELECT a.missing_column FROM accounts a": false,
		"SELECT v.id":                             false,
	}
	for _, statement := range statements {
		query := strings.Join(strings.Fields(statement.text), " ")
		if _, ok := want[query]; !ok {
			t.Errorf("collected an incomplete SQL fragment: %q at %s", query, statement.where)
			continue
		}
		want[query] = true
	}
	for query, found := range want {
		if !found {
			t.Errorf("did not collect complete query %q", query)
		}
	}
}

func TestProductionStatementSourceRetainsConstantsUsedByOtherFiles(t *testing.T) {
	root := t.TempDir()
	for name, source := range map[string]string{
		"projection.go": "package fixture\nconst projection = \"SELECT v.id\"\nconst query = projection + \" FROM accounts v\"\n",
		"query.go":      "package fixture\nfunc query(db interface{}) { db.Query(projection) }\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	statements, _ := productionStatementsIn(t, root)
	for _, statement := range statements {
		if statement.text == "SELECT v.id" {
			return
		}
	}
	t.Fatal("constant used directly by another file escaped the guard")
}

func TestProductionStatementGuardRejectsBadSQLFromSource(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	root := t.TempDir()
	source := "package fixture\n" + `
const projection = "SELECT a.missing_column"
const query = projection + " FROM accounts a"
func queries(db interface{}) {
 db.Query(query)
 db.Query("SELECT missing_column FROM accounts")
 db.Query("SELECT v.id")
 db.Query("SELECT * FROM missing_table")
 db.Query("SELECT missing_function()")
 db.Query("SELECT username FROM accounts")
}
`
	if err := os.WriteFile(filepath.Join(root, "queries.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := Open(filepath.Join(t.TempDir(), "negative.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	statements, transient := productionStatementsIn(t, root)
	rejected, prepared := 0, 0
	for _, statement := range statements {
		ok, err := prepareProductionStatement(db, nil, statement, transient)
		if strings.Contains(statement.text, "missing_") || statement.text == "SELECT v.id" {
			if ok || err == nil {
				t.Errorf("bad production SQL escaped the guard: %s (%v)", statement.text, err)
			}
			rejected++
		} else if !ok || err != nil {
			t.Errorf("valid production SQL was rejected: %s (%v)", statement.text, err)
		} else {
			prepared++
		}
	}
	if rejected != 5 || prepared != 1 {
		t.Fatalf("checked %d invalid and %d valid statements; want 5 and 1", rejected, prepared)
	}
	// Preparing a negative statement must never execute it or alter the schema.
	if err := VerifySchemaObjects(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}
