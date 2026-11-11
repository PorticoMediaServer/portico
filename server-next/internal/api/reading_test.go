package api

import (
	"archive/zip"
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// zipFile writes a zip archive of the given entries, in order.
func zipFile(t *testing.T, path string, entries ...[2]string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e[0])
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(e[1]))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func pngPicture(t *testing.T, width, height int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for n := range img.Pix {
		img.Pix[n] = 200
	}
	img.Set(0, 0, color.Black)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

const chapterOne = `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><body><p>It was a bright cold day.</p></body></html>`

func epubFile(t *testing.T, path, chapter string) {
	zipFile(t, path,
		[2]string{"mimetype", "application/epub+zip"},
		[2]string{"META-INF/container.xml", `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
			<rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		[2]string{"OEBPS/content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0">
			<metadata/><manifest>
			<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
			<item id="c1" href="text/chapter%201.xhtml" media-type="application/xhtml+xml"/>
			<item id="pic" href="images/cover.png" media-type="image/png"/>
			<item id="away" href="../../etc/passwd" media-type="text/plain"/>
			</manifest><spine><itemref idref="c1"/></spine></package>`},
		[2]string{"OEBPS/nav.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><body>
			<nav epub:type="toc"><ol><li><a href="text/chapter%201.xhtml#start">Chapter&nbsp;One</a></li></ol></nav></body></html>`},
		[2]string{"OEBPS/text/chapter 1.xhtml", chapter},
		[2]string{"OEBPS/images/cover.png", pngPicture(t, 4, 4)},
	)
}

// bookShelf is a server with a books library and a comics library made of small real files,
// scanned, and a token for a profile whose rating limit hides every book.
type bookShelf struct {
	*theatre
	books, comics string
	limited       string
}

func newBookShelf(t *testing.T) *bookShelf {
	th := newTheatre(t)
	b := &bookShelf{theatre: th, books: filepath.Join(t.TempDir(), "books"), comics: filepath.Join(t.TempDir(), "comics")}
	epubFile(t, filepath.Join(b.books, "Orwell", "Nineteen.epub"), chapterOne)
	if err := os.WriteFile(filepath.Join(b.books, "Orwell", "Nineteen.pdf"), []byte("%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.books, "Kindle Book.mobi"), []byte("BOOKMOBI and more"), 0o644); err != nil {
		t.Fatal(err)
	}
	page := pngPicture(t, 64, 96)
	zipFile(t, filepath.Join(b.comics, "Saga", "Saga 001.cbz"),
		[2]string{"p10.png", page}, [2]string{"p2.png", page}, [2]string{"p1.png", page},
		[2]string{"__MACOSX/._p1.png", "x"},
		[2]string{"ComicInfo.xml", `<ComicInfo><Manga>YesAndRightToLeft</Manga></ComicInfo>`})
	ctx := context.Background()
	for _, lib := range []struct{ kind, folder string }{{db.LibraryBooks, b.books}, {db.LibraryComics, b.comics}} {
		if _, err := th.s.db.CreateLibrary(ctx, lib.kind, lib.kind, "en", []string{lib.folder}); err != nil {
			t.Fatal(err)
		}
	}
	b.scan()
	started, err := th.s.tokens.CheckAccess(th.owner.AccessToken, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	age := 13
	v, err := th.s.db.CreateSeedProfile(ctx, db.SeedProfile{Name: "child", MaxRatingAge: &age})
	if err != nil {
		t.Fatal(err)
	}
	b.limited, _ = th.s.tokens.Access(auth.Claims{UserID: v.UserID, ProfileID: v.ProfileID, Role: "member", Boot: started.Boot},
		time.Now(), time.Now().Add(time.Hour))
	return b
}

func (b *bookShelf) scan() {
	b.Helper()
	libraries, err := b.s.db.AllLibraries(context.Background())
	if err != nil {
		b.Fatal(err)
	}
	for _, lib := range libraries {
		if lib.Kind == db.LibraryBooks || lib.Kind == db.LibraryComics {
			if _, err := (&scan.Scanner{DB: b.s.db}).Scan(context.Background(), lib); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// find is the id of the one item of a kind with this title.
func (b *bookShelf) find(kind db.ItemKind, title string) string {
	b.Helper()
	libraries, err := b.s.db.AllLibraries(context.Background())
	if err != nil {
		b.Fatal(err)
	}
	for _, lib := range libraries {
		q := db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{kind}, Limit: 100}
		page, err := b.s.db.Items(context.Background(), db.Viewer{UserID: 1, ProfileID: 1}, q)
		if err != nil {
			continue // a library that holds no such kind
		}
		for _, it := range page.Items {
			if it.Title == title {
				return it.PublicID
			}
		}
	}
	b.Fatalf("no %s %q", kind, title)
	return ""
}

func (b *bookShelf) fetch(path string, header ...string) *http.Response {
	b.Helper()
	r := newRequest("GET", strings.ReplaceAll(path, " ", "%20"), "", nil)
	for n := 0; n+1 < len(header); n += 2 {
		r.Header.Set(header[n], header[n+1])
	}
	return serve(b.s, r).Result()
}

func body(r *http.Response) string {
	var buf bytes.Buffer
	buf.ReadFrom(r.Body)
	return buf.String()
}

// A reader opens an EPUB, a PDF and a comic, reads their parts, pages and bytes, and nothing
// outside each book's own lists can be asked for. A book this server cannot open yet says so
// and can still be saved. A profile that may not see a book is told it does not exist.
func TestOpeningBooks(t *testing.T) {
	b := newBookShelf(t)
	token := b.owner.AccessToken
	nineteen := b.find(db.KindBook, "Nineteen")

	book := get[BookAnswer](t, b.s, token, "/v1/items/"+nineteen+"/book")
	if book.Format != "epub" || len(book.Versions) != 2 || book.Unreadable != nil {
		t.Fatalf("an EPUB and a PDF of one book should open the EPUB: %+v", book)
	}
	if !slices.Equal(book.Spine, []string{"OEBPS/text/chapter 1.xhtml"}) || len(book.Contents) != 1 ||
		book.Contents[0] != (BookContents{Title: "Chapter One", Part: "OEBPS/text/chapter 1.xhtml", Fragment: "start"}) {
		t.Fatalf("spine %v, contents %+v", book.Spine, book.Contents)
	}
	for _, p := range book.Parts {
		if strings.Contains(p.Path, "passwd") {
			t.Fatalf("a manifest entry outside the archive was listed: %+v", p)
		}
	}
	if r := b.fetch(book.MediaURL + "/parts/OEBPS/text/chapter 1.xhtml"); r.StatusCode != 200 || body(r) != chapterOne ||
		r.Header.Get("Content-Type") != "application/xhtml+xml" || r.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("a chapter answered %d %q", r.StatusCode, r.Header.Get("Content-Type"))
	}
	// (A path with ".." in it is redirected to its plain form by the router before it gets here.)
	for _, path := range []string{"/parts/META-INF/container.xml", "/parts/OEBPS/../../etc/passwd", "/parts/mimetype", "/pages/0", "/nothing"} {
		if r := b.fetch(book.MediaURL + path); r.StatusCode != 404 && r.StatusCode != 307 {
			t.Fatalf("%s answered %d", path, r.StatusCode)
		}
	}
	// An address that was altered is no address at all.
	altered := []byte(book.MediaURL)
	altered[len(altered)-3] ^= 1
	if r := b.fetch(string(altered) + "/parts/OEBPS/text/chapter 1.xhtml"); r.StatusCode != 404 {
		t.Fatalf("an altered address answered %d", r.StatusCode)
	}
	// The saved file is the book's own bytes, as an attachment.
	if r := b.fetch(book.SaveURL); r.StatusCode != 200 || !strings.HasPrefix(r.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("saving answered %d %q", r.StatusCode, r.Header.Get("Content-Disposition"))
	}

	var pdf BookAnswer
	for _, v := range book.Versions {
		if v.Format == "pdf" {
			pdf = get[BookAnswer](t, b.s, token, "/v1/items/"+nineteen+"/book?version="+v.ID)
		}
	}
	if r := b.fetch(pdf.MediaURL+"/source", "Range", "bytes=0-4"); pdf.Format != "pdf" || r.StatusCode != 206 || body(r) != "%PDF-" {
		t.Fatalf("a PDF read by range answered %d (%+v)", r.StatusCode, pdf)
	}

	mobi := get[BookAnswer](t, b.s, token, "/v1/items/"+b.find(db.KindBook, "Kindle Book")+"/book")
	if mobi.Unreadable == nil || mobi.Unreadable.Message != "This server can't open this kind of book yet." || mobi.MediaURL != "" || mobi.SaveURL == "" {
		t.Fatalf("a MOBI book: %+v", mobi)
	}
	if r := b.fetch(mobi.SaveURL); r.StatusCode != 200 || body(r) != "BOOKMOBI and more" {
		t.Fatalf("saving a MOBI book answered %d", r.StatusCode)
	}

	comic := get[BookAnswer](t, b.s, token, "/v1/items/"+b.find(db.KindIssue, "Saga 001")+"/book")
	if !slices.Equal(comic.Pages, []string{"p1.png", "p2.png", "p10.png"}) || comic.Direction != "rtl" {
		t.Fatalf("comic pages %v, direction %q", comic.Pages, comic.Direction)
	}
	if r := b.fetch(comic.MediaURL + "/pages/2"); r.StatusCode != 200 || r.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("a page answered %d", r.StatusCode)
	}
	r := b.fetch(comic.MediaURL + "/pages/0?w=16")
	config, err := png.DecodeConfig(r.Body)
	if r.StatusCode != 200 || err != nil || config.Width != 16 || config.Height != 24 {
		t.Fatalf("a page asked for at width 16 answered %d, %dx%d (%v)", r.StatusCode, config.Width, config.Height, err)
	}
	if r := b.fetch(comic.MediaURL + "/pages/3"); r.StatusCode != 404 {
		t.Fatalf("a page past the end answered %d", r.StatusCode)
	}
	// Comic and EPUB addresses are not interchangeable: an EPUB has no pages and a comic no parts.
	if r := b.fetch(comic.MediaURL + "/parts/p1.png"); r.StatusCode != 404 {
		t.Fatalf("a comic's part answered %d", r.StatusCode)
	}

	for _, id := range []string{nineteen, b.find(db.KindIssue, "Saga 001")} {
		if w := call(t, b.s, "GET", "/v1/items/"+id+"/book", b.limited, nil, nil); w.Code != 404 {
			t.Fatalf("a hidden book answered %d", w.Code)
		}
	}

	// A file replaced since the address was given is not served from the old address.
	time.Sleep(10 * time.Millisecond)
	epubFile(t, filepath.Join(b.books, "Orwell", "Nineteen.epub"), strings.Replace(chapterOne, "cold", "warm", 1))
	if r := b.fetch(book.MediaURL + "/parts/OEBPS/text/chapter 1.xhtml"); r.StatusCode != 409 {
		t.Fatalf("a replaced file answered %d", r.StatusCode)
	}
	b.scan()
	again := get[BookAnswer](t, b.s, token, "/v1/items/"+nineteen+"/book")
	if again.Fingerprint == book.Fingerprint {
		t.Fatal("a replaced file kept its fingerprint")
	}
	if r := b.fetch(again.MediaURL + "/parts/OEBPS/text/chapter 1.xhtml"); r.StatusCode != 200 || !strings.Contains(body(r), "warm") {
		t.Fatalf("the replaced file answered %d", r.StatusCode)
	}
}

// A comic archive made to cause harm lists only its plain pages, and asking for a page that
// claims an enormous size refuses that page while the rest of the comic reads.
func TestAHostileComicArchive(t *testing.T) {
	b := newBookShelf(t)
	page := pngPicture(t, 8, 8)
	var inner bytes.Buffer
	iz := zip.NewWriter(&inner)
	w, _ := iz.Create("p0.png")
	w.Write([]byte(page))
	iz.Close()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range [][2]string{{"../evil.png", page}, {"/abs.png", page}, {"a/../../b.png", page}, {`c:\d.png`, page},
		{"inner.zip", inner.String()}, {"ok/01.png", page}, {"ok/01.png", "a second entry with the same name"}} {
		w, err := zw.Create(e[0])
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(e[1]))
	}
	w, err := zw.CreateRaw(&zip.FileHeader{Name: "ok/02.png", Method: zip.Store, CompressedSize64: 4, UncompressedSize64: 1 << 40})
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("tiny"))
	zw.Close()
	if err := os.WriteFile(filepath.Join(b.comics, "Hostile.cbz"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	b.scan()
	comic := get[BookAnswer](t, b.s, b.owner.AccessToken, "/v1/items/"+b.find(db.KindComic, "Hostile")+"/book")
	if !slices.Equal(comic.Pages, []string{"ok/01.png", "ok/02.png"}) {
		t.Fatalf("a hostile archive listed %v", comic.Pages)
	}
	if r := b.fetch(comic.MediaURL + "/pages/0"); r.StatusCode != 200 || body(r) != page {
		t.Fatalf("the plain page answered %d", r.StatusCode)
	}
	if r := b.fetch(comic.MediaURL + "/pages/1"); r.StatusCode != 422 || !strings.Contains(body(r), "page_too_large") {
		t.Fatalf("a page claiming a terabyte answered %d", r.StatusCode)
	}
}
