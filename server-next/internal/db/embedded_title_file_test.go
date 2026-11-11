package db_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// A title is looked up by its file's name only when the title shown is the one written inside
// the file. One a provider matched, a person typed or locked, or an NFO file gave is looked up
// as it is, however badly its file is named.
func TestOnlyATitleFromInsideItsFileIsLookedUpByTheFileName(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	root := t.TempDir()
	names := []string{"inside", "provider", "locked", "typed", "nfo"}
	for n, name := range names {
		if err := os.WriteFile(filepath.Join(root, "xq7z_"+name+".mkv"), make([]byte, 100+n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lib, err := d.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&scan.Scanner{DB: d, Prober: titled{"Written Inside"}}).Scan(ctx, lib); err != nil {
		t.Fatal(err)
	}
	films, err := d.ToMatch(ctx, lib.ID, []db.ItemKind{db.KindMovie}, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, film := range films {
		ids[film.Title] = film.ID
	}
	for _, set := range []string{
		// Each film as a failed lookup, a match, a person or an NFO file leaves it.
		`UPDATE items SET title = 'Written Inside', refreshed_at = 1 WHERE title = 'xq7z inside'`,
		`UPDATE items SET title = 'Written Inside', match_state = 1 WHERE title = 'xq7z provider'`,
		`UPDATE items SET title = 'Written Inside', locked_fields = '["title"]' WHERE title = 'xq7z locked'`,
		`UPDATE items SET title = 'Written Inside', local_fields = '{"title":"edit"}' WHERE title = 'xq7z typed'`,
		`UPDATE items SET title = 'Heat', year = 1995 WHERE title = 'xq7z nfo'`,
	} {
		if err := d.ExecForTest(ctx, set); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range names {
		id := ids["xq7z "+name]
		if id == 0 {
			t.Fatalf("no film from xq7z_%s.mkv; have %v", name, ids)
		}
		rel, err := d.EmbeddedTitleFile(ctx, id)
		switch {
		case name == "inside" && (err != nil || rel != "xq7z_inside.mkv"):
			t.Errorf("a title from inside its file: %q, %v; want its file", rel, err)
		case name != "inside" && !errors.Is(err, db.ErrNotFound):
			t.Errorf("a title from %s: %q, %v; want looked up as it is", name, rel, err)
		}
	}
	if _, err := d.EmbeddedTitleFile(ctx, 1<<40); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("a title with no file: %v", err)
	}
}
