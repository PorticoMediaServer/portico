package api

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Finishing a volume offers the next one in its series: by number, then those with no number
// by title. The last volume offers nothing.
func TestTheNextVolume(t *testing.T) {
	b := newBookShelf(t)
	for _, v := range []struct{ name, index string }{{"One", "1"}, {"Two", "2"}, {"Coda", ""}} {
		epubFile(t, filepath.Join(b.books, "Fables", v.name+".epub"), chapterOne)
		index := ""
		if v.index != "" {
			index = fmt.Sprintf(`<meta name="calibre:series_index" content="%s"/>`, v.index)
		}
		opf := fmt.Sprintf(`<package xmlns="http://www.idpf.org/2007/opf"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
			<dc:title>%s</dc:title><meta name="calibre:series" content="Fables"/>%s</metadata></package>`, v.name, index)
		if err := os.WriteFile(filepath.Join(b.books, "Fables", v.name+".opf"), []byte(opf), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	page := pngPicture(t, 8, 8)
	for _, n := range []string{"010", "002"} {
		zipFile(t, filepath.Join(b.comics, "Saga", "Saga "+n+".cbz"), [2]string{"1.png", page})
	}
	b.scan()
	next := func(kind db.ItemKind, title string) string {
		book := get[BookAnswer](t, b.s, b.owner.AccessToken, "/v1/items/"+b.find(kind, title)+"/book")
		if book.Next == nil {
			return ""
		}
		return book.Next.Title
	}
	for _, c := range []struct {
		kind       db.ItemKind
		from, want string
	}{{db.KindBook, "One", "Two"}, {db.KindBook, "Two", "Coda"}, {db.KindBook, "Coda", ""}, {db.KindBook, "Nineteen", ""},
		{db.KindIssue, "Saga 001", "Saga 002"}, {db.KindIssue, "Saga 002", "Saga 010"}, {db.KindIssue, "Saga 010", ""}} {
		if got := next(c.kind, c.from); got != c.want {
			t.Errorf("after %s comes %q, want %q", c.from, got, c.want)
		}
	}
}
