package api

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote/remotetest"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// A book on a remote source reads as one on this server's disk does: an EPUB's parts, a PDF by
// range, a comic's pages, and the saved file. A large page is a few requests to the source, not
// one for every few kilobytes; a source that says "slow down" is waited for; and a file that
// changed at the source answers "book changed".
func TestBooksOnARemoteSource(t *testing.T) {
	b := newBookShelf(t)
	ctx := context.Background()
	made := t.TempDir()
	file := func(name string, write func(path string)) string {
		path := filepath.Join(made, name)
		write(path)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	tale := file("tale.epub", func(p string) { epubFile(t, p, chapterOne) })
	big := make([]byte, 300<<10) // a page that does not compress, as a scanned page does not
	rand.Read(big)
	comic := file("far.cbz", func(p string) {
		zipFile(t, p, [2]string{"01.png", pngPicture(t, 64, 96)}, [2]string{"02.jpg", string(big)})
	})
	dav := remotetest.NewDAV(t, map[string]string{"Books/Remote Tale.epub": tale, "Books/Remote Manual.pdf": "%PDF-1.4 remote\n%%EOF\n",
		"Comics/Far Away.cbz": comic})

	var source RemoteSource
	if w := call(t, b.s, "POST", "/v1/admin/remote-sources", b.owner.AccessToken, RemoteSourceCreate{Kind: "webdav", Name: "NAS",
		Limits: &RemoteLimits{Connections: 8, PerSecond: 1000}, WebDAV: &WebDAVSettings{Address: dav.URL, User: dav.User, Password: dav.Password}}, &source); w.Code != 200 {
		t.Fatalf("adding the source answered %d %s", w.Code, w.Body)
	}
	row, err := b.s.db.RemoteSource(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, lib := range []struct{ name, kind, folder string }{{"Remote books", db.LibraryBooks, "Books"}, {"Remote comics", db.LibraryComics, "Comics"}} {
		// Made with its folder, as the owner makes one: a library always has a folder.
		made, err := b.s.db.NewLibrary(ctx, db.NewLibrary{Name: lib.name, Kind: lib.kind, Language: "en",
			RemoteFolders: []db.RemoteFolder{{SourceID: row.ID, Path: lib.folder}}})
		if err != nil {
			t.Fatal(err)
		}
		libraries, err := b.s.db.AllLibraries(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range libraries {
			if l.ID == made.ID {
				made = l
			}
		}
		if stats, err := (&scan.Scanner{DB: b.s.db, Remote: b.s.remote.Open}).Scan(ctx, made); err != nil || stats.ItemsNew == 0 {
			t.Fatalf("scanning %s: %+v, %v", lib.name, stats, err)
		}
	}
	token := b.owner.AccessToken

	book := get[BookAnswer](t, b.s, token, "/v1/items/"+b.find(db.KindBook, "Remote Tale")+"/book")
	if book.Unreadable != nil || len(book.Contents) != 1 || book.Contents[0].Title != "Chapter One" {
		t.Fatalf("a remote EPUB: %+v", book)
	}
	if r := b.fetch(book.MediaURL + "/parts/OEBPS/text/chapter 1.xhtml"); r.StatusCode != 200 || body(r) != chapterOne {
		t.Fatalf("a remote chapter answered %d", r.StatusCode)
	}
	if r := b.fetch(book.SaveURL); r.StatusCode != 200 || body(r) != tale {
		t.Fatalf("saving a remote EPUB answered %d", r.StatusCode)
	}
	pdf := get[BookAnswer](t, b.s, token, "/v1/items/"+b.find(db.KindBook, "Remote Manual")+"/book")
	if r := b.fetch(pdf.MediaURL+"/source", "Range", "bytes=0-4"); r.StatusCode != 206 || body(r) != "%PDF-" {
		t.Fatalf("a remote PDF by range answered %d", r.StatusCode)
	}

	far := get[BookAnswer](t, b.s, token, "/v1/items/"+b.find(db.KindComic, "Far Away")+"/book")
	if far.Unreadable != nil || len(far.Pages) != 2 {
		t.Fatalf("a remote comic: %+v", far)
	}
	before := dav.Reads.Load()
	if r := b.fetch(far.MediaURL + "/pages/1"); r.StatusCode != 200 || body(r) != string(big) {
		t.Fatalf("a large remote page answered %d", r.StatusCode)
	}
	// The end of the archive, its list, and the page: a handful of reads, where reading a few
	// kilobytes at a time would be about a hundred.
	if reads := dav.Reads.Load() - before; reads > 6 {
		t.Fatalf("a 300 KB page took %d requests to the source", reads)
	}
	dav.SlowDowns.Store(1)
	if r := b.fetch(far.MediaURL + "/pages/0?w=16"); r.StatusCode != 200 {
		t.Fatalf("a page asked for while the source said slow down answered %d", r.StatusCode)
	}

	// A network that blinks while a page is read is "try again", not "damaged"; and so it is
	// for the book itself while its list of pages is read. Asked again, both read.
	dav.Breaks.Store(1)
	if r := b.fetch(far.MediaURL + "/pages/1"); r.StatusCode != 503 || !strings.Contains(body(r), `"source_unreachable"`) || r.Header.Get("Retry-After") == "" {
		t.Fatalf("a page whose source blinked answered %d %s", r.StatusCode, body(r))
	}
	if r := b.fetch(far.MediaURL + "/pages/1"); r.StatusCode != 200 {
		t.Fatalf("the page asked for again answered %d", r.StatusCode)
	}
	dav.Breaks.Store(1)
	again := get[BookAnswer](t, b.s, token, "/v1/items/"+b.find(db.KindComic, "Far Away")+"/book")
	if again.Unreadable == nil || again.Unreadable.Code != "source_unreachable" {
		t.Fatalf("a comic whose source blinked while it opened: %+v", again.Unreadable)
	}
	if again = get[BookAnswer](t, b.s, token, "/v1/items/"+b.find(db.KindComic, "Far Away")+"/book"); again.Unreadable != nil {
		t.Fatalf("the comic opened again: %+v", again.Unreadable)
	}

	dav.Put("Books/Remote Tale.epub", strings.Replace(tale, "cold", "warm", 1)+"and more")
	if r := b.fetch(book.MediaURL + "/parts/OEBPS/text/chapter 1.xhtml"); r.StatusCode != 409 {
		t.Fatalf("a file changed at the source answered %d", r.StatusCode)
	}
}
