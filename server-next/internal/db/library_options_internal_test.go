package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

// Saving a library's options changes the keys those options own and leaves the keys other
// areas keep in the same document: the region metadata reads, what automatic collections
// remember. A choice put back to its default is taken out again.
func TestSavingOptionsKeepsOtherAreasKeys(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	lib, err := d.CreateLibrary(ctx, "Films", LibraryMovies, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	// What other areas have put there.
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE libraries SET options = ? WHERE id = ?`,
			`{"region":"CA","removedCollections":["col_1"],"groupSeries":true,"allowDelete":true,"depth":"complete"}`, lib.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	document := func() map[string]any {
		t.Helper()
		var raw string
		if err := d.read.QueryRowContext(ctx, `SELECT options FROM libraries WHERE id = ?`, lib.ID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		out := map[string]any{}
		if err := json.Unmarshal([]byte(raw), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	check := func(when string, wantTrash string) {
		t.Helper()
		doc := document()
		if doc["region"] != "CA" || doc["groupSeries"] != true || len(doc["removedCollections"].([]any)) != 1 {
			t.Fatalf("%s: another area's keys were lost: %v", when, doc)
		}
		if _, still := doc["allowDelete"]; still || doc["depth"] != DepthFiles || doc["trash"].(map[string]any)["mode"] != wantTrash {
			t.Fatalf("%s: the owner's choices were not saved as given: %v", when, doc)
		}
		if src, err := d.LibrarySource(ctx, lib.ID); err != nil || src.Region != "CA" {
			t.Fatalf("%s: metadata reads region %q, %v", when, src.Region, err)
		}
	}
	// Deleting turned off again, another depth, another trash rule.
	if err := d.SetLibraryOptions(ctx, lib.ID, LibraryOptions{Depth: DepthFiles, Trash: TrashRule{Mode: TrashManual}}); err != nil {
		t.Fatal(err)
	}
	check("SetLibraryOptions", TrashManual)
	options := LibraryOptions{Depth: DepthFiles, Trash: TrashRule{Mode: TrashAfterScan}}
	if _, _, err := d.EditLibrary(ctx, lib.PublicID, LibraryEdit{Options: &options}); err != nil {
		t.Fatal(err)
	}
	check("EditLibrary", TrashAfterScan)
}
