package api

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func (sh *shelf) posters(id string) []ArtworkPicture {
	sh.t.Helper()
	var page Page[ArtworkPicture]
	sh.do("GET", "/v1/admin/items/"+id+"/artwork?role=poster", nil, &page, 200)
	return page.Items
}

func (sh *shelf) uploadPoster(token, id string, data []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/v1/admin/items/"+id+"/artwork?role=poster", bytes.NewReader(data))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()
	sh.s.ServeHTTP(w, r)
	return w
}

// A matched film gets the provider's poster, served from the server's own copy; Find more
// brings the others; the owner uses one, uploads one, removes the upload and falls back, and
// undoes a choice from History. Only a real picture is taken, and a limited admin is told nothing.
func TestArtwork(t *testing.T) {
	sh := newShelf(t, "Heat (1995).mkv")
	var films ItemPage
	sh.do("GET", "/v1/items?kind=movie&library="+sh.library, nil, &films, 200)
	heat := films.Items[0].ID
	item := sh.detail(heat)
	if item.Images["poster"] == "" || item.Images["backdrop"] == "" || item.Images["logo"] == "" {
		t.Fatalf("a matched film's pictures: %v", item.Images)
	}
	if w := call(t, sh.s, "GET", item.Images["poster"], "", nil, nil); w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("the stored poster answered %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	if got := sh.posters(heat); len(got) != 1 || got[0].Source != "provider" || !got[0].Selected || got[0].Locked {
		t.Fatalf("posters after matching: %+v", got)
	}
	var found Page[ArtworkPicture]
	sh.do("POST", "/v1/admin/items/"+heat+"/artwork/find?role=poster", nil, &found, 200)
	if len(found.Items) != 2 {
		t.Fatalf("Find more: %+v", found.Items)
	}
	other := found.Items[1].ID
	sh.do("PUT", "/v1/admin/items/"+heat+"/artwork/poster", ArtworkChoice{ImageID: other}, &found, 200)
	if found.Items[0].ID != other || !found.Items[0].Selected || !found.Items[0].Locked {
		t.Fatalf("after Use this: %+v", found.Items)
	}

	var picture bytes.Buffer
	png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 30, 45)))
	w := sh.uploadPoster(sh.owner, heat, picture.Bytes())
	if w.Code != 201 {
		t.Fatalf("upload answered %d: %s", w.Code, w.Body)
	}
	if got := sh.posters(heat); got[0].Source != "upload" || !got[0].Locked || got[0].Width != 30 || sh.detail(heat).Images["poster"] != got[0].URL {
		t.Fatalf("after upload: %+v", got)
	}
	if w := sh.uploadPoster(sh.owner, heat, []byte("GIF89a not really a picture")); w.Code != 415 {
		t.Fatalf("a file that is not a picture was answered %d", w.Code)
	}
	if w := sh.uploadPoster(sh.owner, heat, make([]byte, maxPictureUpload+1)); w.Code != 413 {
		t.Fatalf("a picture over 10 MB was answered %d", w.Code)
	}
	upload, uploadURL := sh.posters(heat)[0].ID, sh.posters(heat)[0].URL
	sh.do("DELETE", "/v1/admin/items/"+heat+"/artwork/"+upload, nil, nil, 204)
	if got := sh.posters(heat); len(got) != 2 || got[0].Source != "provider" || got[0].Locked {
		t.Fatalf("after removing the upload: %+v", got)
	}
	sh.do("DELETE", "/v1/admin/items/"+heat+"/artwork/"+other, nil, nil, 409) // a provider's picture stays to choose from

	// Undo of the removal puts the upload back as the choice: its file was kept for History.
	var history Page[MetadataHistoryEntry]
	sh.do("GET", "/v1/admin/items/"+heat+"/metadata/history", nil, &history, 200)
	if len(history.Items) != 3 || history.Items[0].Kind != "artwork_chosen" {
		t.Fatalf("history: %+v", history.Items)
	}
	sh.do("POST", "/v1/admin/items/"+heat+"/metadata/history/"+history.Items[0].ID+"/undo", nil, nil, 200)
	if got := sh.posters(heat); got[0].Source != "upload" || !got[0].Locked || got[0].URL != uploadURL || sh.detail(heat).Images["poster"] != uploadURL {
		t.Fatalf("after undoing the removal: %+v", got)
	}
	// Undo to before the upload: the choice before it comes back.
	sh.do("GET", "/v1/admin/items/"+heat+"/metadata/history", nil, &history, 200)
	sh.do("POST", "/v1/admin/items/"+heat+"/metadata/history/"+history.Items[2].ID+"/undo", nil, nil, 200)
	if got := sh.posters(heat); got[0].ID != other || !got[0].Locked {
		t.Fatalf("after undoing the upload: %+v", got)
	}

	// The daily tidy-up's sweep takes a removed upload's file only once no History entry names
	// it, and never a picture in use. A picture stored in the last hour is kept, so the clock
	// is moved on for each sweep.
	for _, p := range sh.posters(heat) {
		if p.Source == "upload" {
			sh.do("DELETE", "/v1/admin/items/"+heat+"/artwork/"+p.ID, nil, nil, 204)
		}
	}
	sweep := func() int {
		t.Helper()
		sh.s.setClock(func() time.Time { return time.Now().Add(2 * time.Hour) })
		defer sh.s.setClock(time.Now)
		removed, err := sh.s.SweepPictures(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return removed
	}
	if removed, err := sh.s.SweepPictures(context.Background()); err != nil || removed != 0 {
		t.Fatalf("a sweep within the hour removed %d: %v", removed, err)
	}
	if removed := sweep(); removed != 0 {
		t.Fatalf("the sweep removed %d pictures while History names the upload", removed)
	}
	for range 20 { // History keeps a title's last 20 changes
		sh.do("PUT", "/v1/admin/items/"+heat+"/artwork/backdrop", ArtworkChoice{Unlock: true}, nil, 200)
	}
	if removed := sweep(); removed != 1 {
		t.Fatalf("the sweep removed %d pictures, want the one upload no History entry names", removed)
	}
	if w := call(t, sh.s, "GET", uploadURL, "", nil, nil); w.Code != 404 {
		t.Fatalf("the removed upload's picture is still served: %d", w.Code)
	}
	if w := call(t, sh.s, "GET", sh.detail(heat).Images["poster"], "", nil, nil); w.Code != 200 {
		t.Fatalf("the poster in use was swept: %d", w.Code)
	}

	// A GIF is taken by its first frame and stored at the large size, 1920 on its longest edge.
	var animated bytes.Buffer
	gif.EncodeAll(&animated, &gif.GIF{Image: []*image.Paletted{image.NewPaletted(image.Rect(0, 0, 1200, 2400), color.Palette{color.Black, color.White})},
		Delay: []int{0}})
	var gifPoster ArtworkPicture
	if w := sh.uploadPoster(sh.owner, heat, animated.Bytes()); w.Code != 201 || jsonInto(w, &gifPoster) != nil || gifPoster.Width != 960 || gifPoster.Height != 1920 {
		t.Fatalf("a large GIF upload: %d %s", w.Code, w.Body)
	}
	if got := sh.posters(heat); got[0].ID != gifPoster.ID || got[0].Source != "upload" {
		t.Fatalf("after the GIF upload: %+v", got)
	}

	if w := sh.uploadPoster(sh.limitedAdmin, heat, picture.Bytes()); w.Code != http.StatusNotFound {
		t.Fatalf("an admin limited to 13 and under uploaded to an R-rated film: %d", w.Code)
	}
}

func (sh *shelf) detail(id string) Item {
	sh.t.Helper()
	var item Item
	sh.do("GET", "/v1/items/"+id, nil, &item, 200)
	return item
}
