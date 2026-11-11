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

// An NFO file can change a title's rating after the title was made. What a profile with a
// rating limit is offered follows in the same write: the genre's count stays the size of the
// genre's list, and both lose the title, where another film of the genre is still offered.
func TestGenreChoicesFollowARatingChangedByAnNFOFile(t *testing.T) {
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	lib, err := d.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{"/films"})
	if err != nil {
		t.Fatal(err)
	}
	limit := 13
	limited, err := d.CreateSeedProfile(ctx, db.SeedProfile{Name: "limited-by-rating", MaxRatingAge: &limit})
	if err != nil {
		t.Fatal(err)
	}
	file := db.FileRef{Name: "Heat.mkv"}
	age := 8
	item := db.ImportItem{Kind: db.KindMovie, Title: "Heat", Keys: []db.ItemKey{db.FileKey(lib.Paths[0].ID, "Heat.mkv", 0)},
		ContentRating: "PG", RatingAge: &age, Tags: []db.TagName{{Kind: db.TagGenre, Name: "Crime"}},
		LocalFields: []string{db.FieldContentRating, db.FieldGenres}, Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: file}}}}}
	// A second film of the same genre keeps its rating throughout.
	other := db.FileRef{Name: "Thief.mkv"}
	second := db.ImportItem{Kind: db.KindMovie, Title: "Thief", Keys: []db.ItemKey{db.FileKey(lib.Paths[0].ID, "Thief.mkv", 0)},
		ContentRating: "PG", RatingAge: &age, Tags: []db.TagName{{Kind: db.TagGenre, Name: "Crime"}},
		Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: other}}}}}
	batch := db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}},
		Files: []db.ImportFile{{FileRef: file, Role: db.FileMedia, Size: 1, ModifiedNS: 1}, {FileRef: other, Role: db.FileMedia, Size: 2, ModifiedNS: 2}},
		Items: []db.ImportItem{item, second}}
	if _, err := d.Import(ctx, batch); err != nil {
		t.Fatal(err)
	}
	// offered is the count the genre filter gives for Crime and the size of the list.
	offered := func() (count, listed int) {
		t.Helper()
		q := db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{db.KindMovie}}
		choices, err := d.Facets(ctx, limited, q, db.FacetGenre)
		if err != nil {
			t.Fatal(err)
		}
		for _, choice := range choices {
			count += choice.Count
		}
		page, err := d.Items(ctx, limited, q)
		if err != nil {
			t.Fatal(err)
		}
		return count, len(page.Items)
	}
	if count, listed := offered(); count != 2 || listed != 2 {
		t.Fatalf("before the file changed: the genre says %d, the list holds %d", count, listed)
	}
	// The file now rates it for adults and says nothing of its genres.
	adult := 18
	item.Reread, item.ContentRating, item.RatingAge, item.Tags = true, "NC-17", &adult, nil
	item.LocalFields = []string{db.FieldContentRating}
	batch.Scan, batch.Files, batch.Items = 2, nil, []db.ImportItem{item}
	if _, err := d.Import(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if count, listed := offered(); count != 1 || listed != 1 {
		t.Fatalf("after the file rated one of the two for adults: the genre says %d, the list holds %d", count, listed)
	}
}
