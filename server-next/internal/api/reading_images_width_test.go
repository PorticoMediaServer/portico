package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A reader that asks for its screen width gets a page that is already narrower than that
// whole and unchanged, as it does from a CBZ. (The size check reads the page's header first.)
func TestAPictureComicPageNarrowerThanAskedIsSentWhole(t *testing.T) {
	b := newBookShelf(t)
	folder := filepath.Join(b.comics, "Saga", "Saga 004")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	pages := map[string]string{"cover.png": pngPicture(t, 32, 96), "1.png": pngPicture(t, 48, 96)}
	for name, bytes := range pages {
		if err := os.WriteFile(filepath.Join(folder, name), []byte(bytes), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b.scan()
	comic := get[BookAnswer](t, b.s, b.owner.AccessToken, "/v1/items/"+b.find(db.KindIssue, "Saga 004")+"/book")
	r := b.fetch(comic.MediaURL + "/pages/1?w=1000")
	if got := body(r); r.StatusCode != 200 || got != pages["1.png"] {
		t.Fatalf("a page narrower than asked answered %d with %d of %d bytes", r.StatusCode, len(got), len(pages["1.png"]))
	}
}
