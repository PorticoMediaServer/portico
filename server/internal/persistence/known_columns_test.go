package persistence

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The column list is only useful if it is exactly what a fresh install has.
// When it drifts, this writes the new list where it survives the test and says
// so, as the schema object list does.
func TestKnownSchemaColumnsMatchAFreshInstall(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "fresh.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	columns, err := tableColumns(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) < 500 {
		t.Fatalf("a fresh install reported only %d columns", len(columns))
	}
	actual := make([]string, 0, len(columns))
	for column := range columns {
		actual = append(actual, column)
	}
	sort.Strings(actual)
	known := parseSchemaObjects(knownSchemaColumns)
	missing, extra := 0, 0
	for _, column := range actual {
		if !known[column] {
			missing++
		}
	}
	for column := range known {
		if !columns[column] {
			extra++
		}
	}
	if missing == 0 && extra == 0 {
		// A fresh install must also pass the check the list is for.
		if err = VerifySchemaObjects(context.Background(), db); err != nil {
			t.Fatalf("a fresh install fails its own schema check: %v", err)
		}
		return
	}
	// Not t.TempDir: the point of this file is to still be there after the test
	// fails, so regenerating the list is a copy rather than a chore.
	directory, mkErr := os.MkdirTemp("", "portico-schema-")
	if mkErr != nil {
		t.Fatal(mkErr)
	}
	generated := filepath.Join(directory, "known_columns.txt")
	_ = os.WriteFile(generated, []byte(strings.Join(actual, "\n")), 0600)
	t.Fatalf(`the columns this build installs no longer match the generated list
(%d columns this install creates that the list does not have, %d the list has that it does not).

Replace knownSchemaColumns in known_columns.go with the contents of:

	%s
`, missing, extra, generated)
}
