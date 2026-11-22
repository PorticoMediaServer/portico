package api

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

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

	// The owner sets a volume's number (reading rule 42): "One" becomes volume 3, so it now
	// follows "Two"; a rescan of its changed file, whose own metadata still says 1, keeps it.
	one := b.find(db.KindBook, "One")
	var doc MetadataDocument
	if w := call(t, b.s, "GET", "/v1/admin/items/"+one+"/metadata", b.owner.AccessToken, nil, &doc); w.Code != 200 || doc.Fields["number"].Value != float64(1) {
		t.Fatalf("the editor's number for One: %d %+v", w.Code, doc.Fields["number"])
	}
	three := 3
	r := newRequest("PATCH", "/v1/admin/items/"+one+"/metadata", b.owner.AccessToken, MetadataPatch{Number: &three})
	r.Header.Set("If-Match", `"`+doc.Revision+`"`)
	w := httptest.NewRecorder()
	b.s.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("setting One's number answered %d: %s", w.Code, w.Body)
	}
	opf := `<package xmlns="http://www.idpf.org/2007/opf"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
			<dc:title>One</dc:title><meta name="calibre:series" content="Fables"/><meta name="calibre:series_index" content="1"/><meta name="x" content="changed"/></metadata></package>`
	if err := os.WriteFile(filepath.Join(b.books, "Fables", "One.opf"), []byte(opf), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	for _, f := range []string{"One.opf", "One.epub"} {
		if err := os.Chtimes(filepath.Join(b.books, "Fables", f), future, future); err != nil {
			t.Fatal(err)
		}
	}
	b.scan()
	if got := next(db.KindBook, "Two"); got != "One" {
		t.Errorf("after Two comes %q, want One (the owner's volume 3)", got)
	}
	call(t, b.s, "GET", "/v1/admin/items/"+one+"/metadata", b.owner.AccessToken, nil, &doc)
	if f := doc.Fields["number"]; f.Value != float64(3) || !f.Locked {
		t.Errorf("One's number after a rescan: %+v", f)
	}
}
