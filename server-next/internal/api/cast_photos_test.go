package api

import (
	"context"
	"testing"
	"time"
)

// Cast photos (definition area 4, rule 47): the lookup records the provider's photo of each
// person it credits, the picture pass fetches them, and the title's credits show them. Turned
// off on Server › Metadata, every photo fetched from the provider is dropped and the pass
// fetches none; turned on again, the pass fetches them at once, without a refresh.
func TestCastPhotos(t *testing.T) {
	ctx := context.Background()
	sh := newShelf(t, "Heat (1995).mkv")
	var films ItemPage
	sh.do("GET", "/v1/items?kind=movie&library="+sh.library, nil, &films, 200)
	heat := films.Items[0].ID
	library, err := sh.s.db.LibraryID(ctx, sh.library)
	if err != nil {
		t.Fatal(err)
	}
	photos := func() (with, all int) {
		t.Helper()
		var credits CreditPage
		sh.do("GET", "/v1/items/"+heat+"/credits?part=cast", nil, &credits, 200)
		for _, c := range credits.Items {
			if c.Image != "" {
				with++
			}
		}
		return with, len(credits.Items)
	}
	if with, all := photos(); with != 0 || all == 0 {
		t.Fatalf("straight after the lookup %d of %d credits have a photo; want none yet", with, all)
	}
	if _, err := sh.s.metadata.FetchPictures(ctx, library, 1000); err != nil {
		t.Fatal(err)
	}
	with, all := photos()
	if with == 0 {
		t.Fatalf("after the picture pass none of the %d credits has a photo", all)
	}
	var page MetadataSettings
	sh.do("GET", "/v1/admin/metadata/settings", nil, &page, 200)
	if !page.CastPhotos {
		t.Fatal("cast photos are off on a new server")
	}

	// Off: the provider's photos are dropped, and the pass fetches none.
	off, on := false, true
	sh.do("PUT", "/v1/admin/metadata/settings", MetadataSettingsChange{CastPhotos: &off}, &page, 200)
	if page.CastPhotos {
		t.Fatal("the page still says cast photos are on")
	}
	waitFor(t, func() bool { n, _ := photos(); return n == 0 }, "the provider's photos to be dropped")
	asked := sh.tmdb.PictureRequests.Load()
	if _, err := sh.s.metadata.FetchPictures(ctx, library, 1000); err != nil {
		t.Fatal(err)
	}
	if n, _ := photos(); n != 0 || sh.tmdb.PictureRequests.Load() != asked {
		t.Fatalf("with cast photos off the pass fetched %d photos (%d requests)", n, sh.tmdb.PictureRequests.Load()-asked)
	}

	// On again: the pass is started by itself and fetches them, with no refresh of the film.
	sh.do("PUT", "/v1/admin/metadata/settings", MetadataSettingsChange{CastPhotos: &on}, &page, 200)
	waitFor(t, func() bool { n, _ := photos(); return n == with }, "the photos to be fetched again")
}

// waitFor waits up to ten seconds for done, for work the server does after it answers.
func waitFor(t *testing.T, done func() bool, what string) {
	t.Helper()
	for range 500 {
		if done() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waited ten seconds for %s", what)
}
