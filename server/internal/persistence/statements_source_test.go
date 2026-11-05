package persistence

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
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

func TestBaselineStatementSourceExcludesOnlyStore(t *testing.T) {
	root := t.TempDir()
	inside := []string{"internal/store/query.go", "internal/store/writer/query.go"}
	outside := []string{"queries.go", "internal/store.go", "internal/storefront/query.go", "internal/stores/query.go", "internal/storage/query.go", "internal/other/query.go", "other/internal/store/query.go"}
	write := func(relative, query, extra string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		source := "package fixture\nfunc query(db interface{}) { db.Query(" + strconv.Quote(query) + ") }\n" + extra
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, relative := range inside {
		write(relative, "SELECT * FROM new_store_only_table", "func setup() { RegisterScalarFunction(\"store_only_fn\", nil) }\n")
	}
	for _, relative := range outside {
		write(relative, "SELECT username FROM accounts", "")
	}
	write("internal/storefront/bad.go", "SELECT missing_store_boundary_column FROM accounts", "")
	write("internal/stores/unregistered.go", "SELECT store_only_fn()", "")

	statements, transient := productionStatementsMatching(t, root, baselineStatementSource)
	if transient["function:store_only_fn"] {
		t.Fatal("a new-store registration exempted a legacy statement")
	}
	db, err := Open(filepath.Join(t.TempDir(), "boundary.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seen := map[string]bool{}
	prepared, rejected := 0, 0
	for _, statement := range statements {
		relative, _, _ := strings.Cut(filepath.ToSlash(statement.where), ":")
		seen[relative] = true
		ok, err := prepareProductionStatement(db, nil, statement, transient)
		if relative == "internal/storefront/bad.go" || relative == "internal/stores/unregistered.go" {
			if ok || err == nil {
				t.Errorf("bad SQL just outside store escaped: %s (%v)", relative, err)
			}
			rejected++
		} else if !ok || err != nil {
			t.Errorf("non-store SQL was not prepared: %s (%v)", relative, err)
		} else {
			prepared++
		}
	}
	for _, relative := range inside {
		if seen[relative] || baselineStatementSource(filepath.FromSlash(relative)) {
			t.Errorf("store source entered old baseline check: %s", relative)
		}
	}
	for _, relative := range outside {
		if !seen[relative] || !baselineStatementSource(filepath.FromSlash(relative)) {
			t.Errorf("non-store source escaped old baseline check: %s", relative)
		}
	}
	if prepared != len(outside) || rejected != 2 {
		t.Fatalf("prepared/rejected = %d/%d, want %d/2", prepared, rejected, len(outside))
	}
	// The exemption belongs only to the baseline preparation test. Other
	// statement/plan censuses retain the original complete source inventory.
	all, allTransient := productionStatementsIn(t, root)
	if len(all) != len(statements)+len(inside) || !allTransient["function:store_only_fn"] {
		t.Fatal("unfiltered collector lost new-store source")
	}
}
