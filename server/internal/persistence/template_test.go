package persistence

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// The template is only useful if a database built from it is indistinguishable
// from one the installers produced.
func TestSchemaTemplateProducesTheSameDatabaseAsAFullInstall(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	directory := t.TempDir()
	installed, err := OpenFresh(filepath.Join(directory, "installed.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer installed.Close()
	UseSchemaTemplate()
	t.Cleanup(func() { templateEnabled.Store(false) })
	copied, err := Open(filepath.Join(directory, "copied.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()

	objects := func(db *sql.DB) []string {
		rows, err := db.Query(`SELECT type||' '||name||' '||COALESCE(sql,'') FROM sqlite_master ORDER BY type,name`)
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
		return out
	}
	before, after := objects(installed), objects(copied)
	if len(before) == 0 {
		t.Fatal("the installed database has no schema objects")
	}
	if len(before) != len(after) {
		t.Fatalf("the template produced %d schema objects, a full install produced %d", len(after), len(before))
	}
	for index := range before {
		if before[index] != after[index] {
			t.Fatalf("schema object %d differs:\n install: %s\ntemplate: %s", index, before[index], after[index])
		}
	}
	// The per-database secret must not be shared between two databases.
	for _, name := range []string{"chapter_cursor_key", "activity_cursor_key", "catalog_cursor_key"} {
		if Get(installed, name) == "" || Get(copied, name) == "" {
			t.Fatal("the cursor key is missing", name)
		}
		if Get(installed, name) == Get(copied, name) {
			t.Fatal("two databases share one cursor-signing key", name)
		}
	}
	// An existing database is never overwritten by the template.
	reopened, err := Open(filepath.Join(directory, "copied.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if Get(reopened, "chapter_cursor_key") != Get(copied, "chapter_cursor_key") {
		t.Fatal("reopening an existing database replaced it with the template")
	}
}
