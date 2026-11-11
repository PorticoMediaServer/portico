package api

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Reading rule 17 (design/definition/10-reading.md): a book in progress is in the library's
// "Continue reading" row. A comics library is a reading library too: its landing must not say
// "Continue Watching" over comics, nor draw them as landscape video cards. An audiobooks
// library's row is "Continue Listening" (the lead's ruling, 11 Nov).
func TestAReadingLibrarysLandingSaysContinueReading(t *testing.T) {
	th := newTheatre(t)
	ctx := context.Background()
	for kind, want := range map[string]struct {
		item  db.ItemKind
		title string
	}{db.LibraryBooks: {db.KindBook, "Continue reading"}, db.LibraryComics: {db.KindComic, "Continue reading"},
		db.LibraryAudiobooks: {db.KindAudiobook, "Continue Listening"}} {
		item := want.item
		lib, err := th.s.db.CreateLibrary(ctx, kind, kind, "en", []string{"/media/" + kind})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := th.s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}},
			Files: []db.ImportFile{{FileRef: db.FileRef{Name: "one.file"}, Role: db.FileMedia, Size: 1 << 20}},
			Items: []db.ImportItem{{Kind: item, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: "one"}}, Title: "One",
				Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: db.FileRef{Name: "one.file"}}}}}}}}); err != nil {
			t.Fatal(err)
		}
		page, err := th.s.db.Items(ctx, db.Viewer{UserID: 1, ProfileID: 1}, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{item}, Sort: db.SortTitle})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("%s: %d items, %v", kind, len(page.Items), err)
		}
		at := int64(60_000)
		if _, _, err := th.s.db.SetState(ctx, db.Viewer{UserID: 1, ProfileID: 1}, page.Items[0].PublicID, db.StateEdit{PositionMS: &at}); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range get[Home](t, th.s, th.owner.AccessToken, "/v1/libraries/"+lib.PublicID+"/landing").Rows {
			if row.ID != "continue" {
				continue
			}
			found = true
			if row.Title != want.title || row.Shape != "poster" {
				t.Errorf("%s landing: the row of titles in progress is %q, drawn as %q; want %q as posters", kind, row.Title, row.Shape, want.title)
			}
		}
		if !found {
			t.Errorf("%s landing has no row for the title in progress", kind)
		}
	}
}
