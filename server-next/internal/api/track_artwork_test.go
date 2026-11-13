package api

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
)

func TestTracksUseAlbumArtworkAcrossReads(t *testing.T) {
	th := newTheatre(t)
	s, token, ctx := th.s, th.owner.AccessToken, context.Background()
	album, tracks := th.album("Sleeve", 60)
	hash := strings.Repeat("a", 64)
	if _, err := s.db.UploadPicture(ctx, album, db.NewPicture{Role: db.ImagePoster, Hash: hash, Width: 600, Height: 600}); err != nil {
		t.Fatal(err)
	}
	albumURL := get[Item](t, s, token, "/v1/items/"+album).Images["poster"]
	if albumURL != "/v1/images/"+hash {
		t.Fatalf("album image: %q", albumURL)
	}
	if image := get[Item](t, s, token, "/v1/items/"+tracks[0]).Images["poster"]; image != albumURL {
		t.Fatalf("single track image %q, want album %q", image, albumURL)
	}
	// An image of the song itself takes precedence over the album fallback.
	ownURL := "/v1/images/" + strings.Repeat("b", 64)
	if _, err := s.db.UploadPicture(ctx, tracks[0], db.NewPicture{Role: db.ImagePoster, Hash: strings.Repeat("b", 64)}); err != nil {
		t.Fatal(err)
	}
	check := func(item Item) {
		t.Helper()
		want := albumURL
		if item.ID == tracks[0] {
			want = ownURL
		}
		if item.Images["poster"] != want {
			t.Errorf("track %s image %q, want %q", item.ID, item.Images["poster"], want)
		}
	}
	check(get[Item](t, s, token, "/v1/items/"+tracks[0]))
	page := get[ItemPage](t, s, token, "/v1/items?parent="+album+"&limit=200")
	if len(page.Items) != 60 {
		t.Fatalf("album has %d tracks", len(page.Items))
	}
	for _, item := range page.Items {
		check(item)
	}
	var made QueueCreated
	if w := th.queue("POST", "/v1/queues", "", QueueCreate{Source: playqueue.QueueSource{Items: tracks}}, &made); w.Code != 201 {
		t.Fatalf("queue: %d %s", w.Code, w.Body)
	}
	entries := get[QueueEntries](t, s, token, "/v1/queues/"+made.Queue.ID+"/entries?limit=200")
	if len(entries.Items) != 60 {
		t.Fatalf("queue has %d tracks", len(entries.Items))
	}
	for _, entry := range append(made.Window, entries.Items...) {
		check(Item{ID: entry.ItemID, Images: entry.Images})
	}
	s.db.StartSearch(filepath.Join(t.TempDir(), "search.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := s.db.SearchCaughtUp(ctx); err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, group := range get[SearchResults](t, s, token, "/v1/search?q=Sleeve&kind=track&limit=40").Groups {
		if group.Kind == "track" {
			for _, item := range group.Items {
				check(item)
				found++
			}
		}
	}
	if found == 0 {
		t.Fatal("no track search rows")
	}
	if pictures, err := s.db.Pictures(ctx, tracks[1], db.ImagePoster); err != nil || len(pictures) != 0 {
		t.Fatalf("a borrowed album image was stored on the track: %+v, %v", pictures, err)
	}
	if item := get[Item](t, s, token, "/v1/items/"+th.song); len(item.Images) != 0 {
		t.Fatalf("a track without an album borrowed another title's image: %+v", item.Images)
	}
}

func TestTracksDoNotBorrowArtworkFromAHiddenAlbum(t *testing.T) {
	th := newTheatre(t)
	s, token, ctx := th.s, th.owner.AccessToken, context.Background()
	album, tracks := th.album("Hidden sleeve", 1)
	if _, err := s.db.UploadPicture(ctx, album, db.NewPicture{Role: db.ImagePoster, Hash: strings.Repeat("c", 64)}); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []struct{ id, rating string }{{tracks[0], "TV-G"}, {album, "NC-17"}} {
		locks, err := s.db.MetadataLocks(ctx, edit.id)
		if err == nil {
			_, err = s.db.EditMetadata(ctx, edit.id, locks.Revision, db.MetadataEdit{ContentRating: &edit.rating})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	var profile Profile
	call(t, s, "POST", "/v1/profiles", token, ProfileCreate{Name: "Child"}, &profile)
	age := 13
	call(t, s, "PUT", "/v1/profiles/"+profile.ID+"/restrictions", token, Restrictions{AllLibraries: true, MaxRatingAge: &age}, nil)
	var child Session
	call(t, s, "POST", "/v1/profiles/"+profile.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &child)
	if w := call(t, s, "GET", "/v1/items/"+album, child.AccessToken, nil, nil); w.Code != 404 {
		t.Fatalf("album should be hidden: %d %s", w.Code, w.Body)
	}
	if item := get[Item](t, s, child.AccessToken, "/v1/items/"+tracks[0]); len(item.Images) != 0 {
		t.Fatalf("visible track leaked its hidden album's image: %+v", item.Images)
	}
	page := get[ItemPage](t, s, child.AccessToken, "/v1/items?library="+th.libs[db.LibraryMusic].PublicID+"&kind=track")
	seen := false
	for _, item := range page.Items {
		if item.ID == tracks[0] {
			seen = true
			if len(item.Images) != 0 {
				t.Fatalf("track list leaked hidden album artwork: %+v", item.Images)
			}
		}
	}
	if !seen {
		t.Fatal("the lower-rated track should remain visible")
	}
}
