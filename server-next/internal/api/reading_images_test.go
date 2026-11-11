package api

import (
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A comic made of a folder of pictures reads page by page from the scanner's own list of its
// pages, with a cover first, a page made smaller on request, nothing to save, and an address
// that says "book changed" once a page is replaced.
func TestAComicMadeOfPictures(t *testing.T) {
	b := newBookShelf(t)
	folder := filepath.Join(b.comics, "Saga", "Saga 003")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, width := range map[string]int{"10.png": 64, "2.png": 48, "cover.png": 32} {
		if err := os.WriteFile(filepath.Join(folder, name), []byte(pngPicture(t, width, 96)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b.scan()
	issue := "/v1/items/" + b.find(db.KindIssue, "Saga 003") + "/book"
	comic := get[BookAnswer](t, b.s, b.owner.AccessToken, issue)
	if comic.Format != "images" || !slices.Equal(comic.Pages, []string{"cover.png", "2.png", "10.png"}) || comic.SaveURL != "" || comic.Unreadable != nil {
		t.Fatalf("a folder comic: %+v", comic)
	}
	for n, width := range []int{32, 48, 64} {
		r := b.fetch(comic.MediaURL + "/pages/" + string(rune('0'+n)))
		config, err := png.DecodeConfig(r.Body)
		if r.StatusCode != 200 || err != nil || config.Width != width {
			t.Fatalf("page %d answered %d, %d wide (%v)", n, r.StatusCode, config.Width, err)
		}
	}
	r := b.fetch(comic.MediaURL + "/pages/2?w=16")
	if config, err := png.DecodeConfig(r.Body); r.StatusCode != 200 || err != nil || config.Width != 16 {
		t.Fatalf("a page asked for at width 16 answered %d (%v)", r.StatusCode, err)
	}
	for _, path := range []string{"/pages/3", "/pages/-1", "/source?save=1", "/parts/2.png"} {
		if r := b.fetch(comic.MediaURL + path); r.StatusCode != 404 {
			t.Fatalf("%s answered %d", path, r.StatusCode)
		}
	}

	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(folder, "2.png"), []byte(pngPicture(t, 40, 96)), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := b.fetch(comic.MediaURL + "/pages/1"); r.StatusCode != 409 {
		t.Fatalf("a replaced page answered %d before a scan", r.StatusCode)
	}
	b.scan()
	if r := b.fetch(comic.MediaURL + "/pages/0"); r.StatusCode != 409 {
		t.Fatalf("an address from before the scan answered %d", r.StatusCode)
	}
	again := get[BookAnswer](t, b.s, b.owner.AccessToken, issue)
	r = b.fetch(again.MediaURL + "/pages/1")
	if config, err := png.DecodeConfig(r.Body); r.StatusCode != 200 || err != nil || config.Width != 40 {
		t.Fatalf("the replaced page answered %d (%v)", r.StatusCode, err)
	}
}
