package api

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestOwnerLibrariesHaveFourStableSelectedCovers(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	ctx := context.Background()
	films, err := s.db.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.CreateLibrary(ctx, "Empty", db.LibraryBooks, "en", []string{t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	batch := db.ImportBatch{LibraryID: films.ID, PathID: films.Paths[0].ID}
	for n := 0; n < 7; n++ {
		role := db.ImagePoster
		if n%2 == 0 {
			role = db.ImageCover
		}
		images := []db.ImportImage{{Role: role, Source: db.ImageFromFile, FileHash: strings.Repeat(fmt.Sprint(n), 64)}}
		if n == 6 {
			images[0].Role = db.ImageBackdrop // The newest title has no poster or cover.
		}
		images = append(images, db.ImportImage{Role: role, Source: db.ImageFromFile, FileHash: strings.Repeat("f", 64)})
		batch.Items = append(batch.Items, db.ImportItem{Kind: db.KindMovie, Title: fmt.Sprint(n), AddedAt: int64(n + 1),
			Keys: []db.ItemKey{{Kind: db.KeyName, Key: fmt.Sprint(n)}}, Images: images})
	}
	// The newest title is backdrop-only; every other title's second candidate is unselected.
	batch.Items[6].Images = batch.Items[6].Images[:1]
	if _, err := s.db.Import(ctx, batch); err != nil {
		t.Fatal(err)
	}
	want := []string{}
	for _, n := range []int{5, 4, 3, 2} {
		want = append(want, "/v1/images/"+strings.Repeat(fmt.Sprint(n), 64))
	}
	for range 2 {
		for _, library := range get[AdminLibraryPage](t, s, owner, "/v1/admin/libraries").Items {
			if library.ID == films.PublicID {
				if !reflect.DeepEqual(library.LibraryCoverURLs, want) {
					t.Fatalf("covers: %v, want %v", library.LibraryCoverURLs, want)
				}
			} else if library.LibraryCoverURLs == nil || len(library.LibraryCoverURLs) != 0 {
				t.Fatalf("empty library must return []: %+v", library.LibraryCoverURLs)
			}
		}
	}
}
