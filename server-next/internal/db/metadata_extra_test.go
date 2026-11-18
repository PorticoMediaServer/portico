package db

import (
	"path/filepath"
	"testing"
)

// One provider answer can say several facts kept in a title's extra (its original language,
// whether a show is still running, its episode order): all of them are kept. SQLite keeps only
// the last of several assignments to one column, so they must be one.
func TestAnAnswersExtraFactsAreAllKept(t *testing.T) {
	d, err := Open(t.Context(), filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	lib, err := d.CreateLibrary(t.Context(), "Shows", LibraryShows, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Import(t.Context(), ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1,
		Items: []ImportItem{{Kind: KindShow, Title: "Show", Keys: []ItemKey{{Kind: KeyName, Key: "show"}}}}}); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := d.read.QueryRow(`SELECT id FROM items WHERE kind = ?`, KindShow).Scan(&id); err != nil {
		t.Fatal(err)
	}
	running := true
	if err := d.ApplyDetails(t.Context(), User, id, ItemDetails{Title: "Show", OriginalLanguage: "ja", Continuing: &running,
		Numbering: &EpisodeNumbering{Order: "dvd", Offset: 2}}); err != nil {
		t.Fatal(err)
	}
	var language, order string
	var continuing, offset int
	if err := d.read.QueryRow(`SELECT ifnull(extra ->> '$.originalLanguage', ''), ifnull(extra ->> '$.continuing', 0),
		ifnull(extra ->> '$.episodeOrder', ''), ifnull(extra ->> '$.episodeOffset', 0) FROM items WHERE id = ?`, id).
		Scan(&language, &continuing, &order, &offset); err != nil {
		t.Fatal(err)
	}
	if language != "ja" || continuing != 1 || order != "dvd" || offset != 2 {
		t.Fatalf("kept language %q, continuing %d, order %q, offset %d; want ja, 1, dvd, 2", language, continuing, order, offset)
	}
}
