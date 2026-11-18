package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
)

// "Some of what plays" in a Library Channel preview draws each title with its own poster (definition 11
// rule 106): every sampled film carries its chosen pictures, as a title does everywhere else.
func TestAChannelPreviewSampleCarriesEachTitlesPoster(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	ctx := context.Background()
	lib, err := s.db.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	hashes := map[string]string{}
	var films []db.ImportItem
	for n := 1; n <= 8; n++ {
		title, hash := fmt.Sprintf("Film %d", n), fmt.Sprintf("%064d", n)
		hashes[title] = hash
		films = append(films, db.ImportItem{Kind: db.KindMovie, Keys: []db.ItemKey{{Kind: db.KeyName, Key: title}}, Title: title, DurationMS: 5_400_000,
			Images: []db.ImportImage{{Role: db.ImagePoster, Source: db.ImageFromFile, FileHash: hash}}})
	}
	if _, err := s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}}, Items: films}); err != nil {
		t.Fatal(err)
	}
	b := librarychannels.LibraryChannelBuilder{LibraryChannelSelection: librarychannels.LibraryChannelSelection{Libraries: []string{lib.PublicID}, Kinds: []string{"movie"}}}
	var preview LibraryChannelPreview
	if w := call(t, s, "POST", "/v1/admin/library-channels/preview", owner, LibraryChannelPreviewAsk{TimeZone: "UTC", Builder: b}, &preview); w.Code != 200 {
		t.Fatalf("preview answered %d: %s", w.Code, w.Body)
	}
	if len(preview.Sample) == 0 {
		t.Fatal("the preview sampled nothing")
	}
	for _, title := range preview.Sample {
		if want := "/v1/images/" + hashes[title.Title]; title.Images["poster"] != want {
			t.Fatalf("%s: poster %q, want %q", title.Title, title.Images["poster"], want)
		}
	}
	for _, row := range preview.Today {
		if row.Title != nil && row.Title.Images != nil {
			t.Fatalf("today's schedule carries pictures it does not draw: %+v", row.Title)
		}
	}
}
