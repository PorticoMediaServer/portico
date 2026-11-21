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

func TestLibrarySummaryOnlyCountsAndShowsPermittedTitles(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	ctx := context.Background()
	films, err := s.db.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	books, err := s.db.CreateLibrary(ctx, "Books", db.LibraryBooks, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.BeginScan(ctx, books.ID); err != nil {
		t.Fatal(err)
	}
	allowed, hidden := strings.Repeat("a", 64), strings.Repeat("b", 64)
	batch := db.ImportBatch{LibraryID: films.ID, PathID: films.Paths[0].ID, Items: []db.ImportItem{
		{Kind: db.KindMovie, Title: "Allowed", AddedAt: 1, RatingAge: new(8),
			Keys:   []db.ItemKey{{Kind: db.KeyName, Key: "allowed"}},
			Images: []db.ImportImage{{Role: db.ImagePoster, Source: db.ImageFromFile, FileHash: allowed}}},
		{Kind: db.KindMovie, Title: "Hidden", AddedAt: 2, RatingAge: new(18),
			Keys:   []db.ItemKey{{Kind: db.KeyName, Key: "hidden"}},
			Images: []db.ImportImage{{Role: db.ImagePoster, Source: db.ImageFromFile, FileHash: hidden}}},
	}}
	if _, err := s.db.Import(ctx, batch); err != nil {
		t.Fatal(err)
	}
	var profile Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Child"}, &profile)
	call(t, s, "PUT", "/v1/profiles/"+profile.ID+"/restrictions", owner, Restrictions{AllLibraries: true, MaxRatingAge: new(8)}, nil)
	var child Session
	call(t, s, "POST", "/v1/profiles/"+profile.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &child)

	summaryFor := func(token, id string) *LibrarySummary {
		t.Helper()
		for _, library := range get[Libraries](t, s, token, "/v1/libraries").Items {
			if library.ID == id && library.Summary != nil {
				return library.Summary
			}
		}
		t.Fatalf("library %q has no summary", id)
		return nil
	}
	expectFilms := func(token string, count int, covers ...string) {
		t.Helper()
		summary := summaryFor(token, films.PublicID)
		if !reflect.DeepEqual(summary.Counts, []db.LibrarySummaryCount{{Kind: "movie", Count: count}}) ||
			!reflect.DeepEqual(summary.CoverURLs, covers) || summary.LatestArtwork != covers[0] || summary.Empty || !summary.EmptyKnown {
			t.Fatalf("film summary: %+v", summary)
		}
	}
	expectFilms(owner, 2, "/v1/images/"+hidden, "/v1/images/"+allowed)
	expectFilms(child.AccessToken, 1, "/v1/images/"+allowed)
	empty := summaryFor(owner, books.PublicID)
	if !empty.FirstScan || !empty.Empty || !empty.EmptyKnown || empty.CoverURLs == nil || len(empty.CoverURLs) != 0 || empty.LatestArtwork != "" {
		t.Fatalf("empty first scan summary: %+v", empty)
	}
	if err := s.db.EndScan(ctx, books.ID, nil); err != nil {
		t.Fatal(err)
	}
	if summaryFor(owner, books.PublicID).FirstScan {
		t.Fatal("the completed first scan still appears active")
	}
}
