package db_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// An NFO file that changes after a person chose the title's match brings its new details and
// leaves the match alone: the id it carries does not replace the chosen one.
func TestAChangedNFOFileDoesNotReplaceAChosenMatch(t *testing.T) {
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	lib, err := d.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{"/films"})
	if err != nil {
		t.Fatal(err)
	}
	viewers, err := seed.Profiles(ctx, d, 1)
	if err != nil {
		t.Fatal(err)
	}
	file := db.FileRef{Name: "Heat.mkv"}
	item := db.ImportItem{Kind: db.KindMovie, Title: "Heat", Keys: []db.ItemKey{db.FileKey(lib.Paths[0].ID, "Heat.mkv", 0)},
		ExternalIDs: []db.ExternalID{{Provider: "tmdb", ID: "949"}}, Summary: "First.", LocalFields: []string{db.FieldSummary},
		Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: file}}}}}
	batch := db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}},
		Files: []db.ImportFile{{FileRef: file, Role: db.FileMedia, Size: 1, ModifiedNS: 1}}, Items: []db.ImportItem{item}}
	if _, err := d.Import(ctx, batch); err != nil {
		t.Fatal(err)
	}
	page, err := d.Items(ctx, viewers[0], db.ItemQuery{Library: lib.PublicID, Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("items: %d, %v", len(page.Items), err)
	}
	id := page.Items[0].PublicID
	if err := db.ChooseMatch(d, id); err != nil {
		t.Fatal(err)
	}

	item.Reread, item.Summary, item.ExternalIDs = true, "Second.", []db.ExternalID{{Provider: "tmdb", ID: "15389"}}
	batch.Scan, batch.Files, batch.Items = 2, nil, []db.ImportItem{item}
	if _, err := d.Import(ctx, batch); err != nil {
		t.Fatal(err)
	}
	detail, err := d.Item(ctx, viewers[0], id)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Summary != "Second." {
		t.Errorf("the changed file's details were not read: %q", detail.Summary)
	}
	if !slices.Equal(detail.ExternalIDs, []db.ExternalID{{Provider: "tmdb", ID: "949"}}) {
		t.Errorf("a changed NFO file replaced the match a person chose: %+v", detail.ExternalIDs)
	}
}
