package scan_test

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/remote"
)

// zipped is a real archive of the given entries, in order.
func zipped(t *testing.T, entries ...[2]string) []byte {
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
	return buf.Bytes()
}

// epub is a small real EPUB whose package document has the given metadata.
func epub(t *testing.T, version, metadata string) []byte {
	return zipped(t, [2]string{"mimetype", "application/epub+zip"},
		[2]string{"META-INF/container.xml", `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
			<rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		[2]string{"OEBPS/content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="` + version + `" unique-identifier="id">
			<metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">` + metadata + `</metadata>
			<manifest><item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c1"/></spine></package>`},
		[2]string{"OEBPS/c1.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>Words.</p></body></html>`})
}

func creditNames(d db.ItemDetail) []string {
	var out []string
	for _, c := range d.Credits {
		out = append(out, c.Name)
	}
	return out
}

// A book's details come from its own file (10-reading rule 8): an EPUB 3's and an EPUB 2's
// package document, whatever the file is called. A Calibre sidecar beside the book is the
// owner's and wins over the file.
func TestABookIsReadFromItsOwnPackageDocument(t *testing.T) {
	f := newFixture(t, db.LibraryBooks)
	f.put("scan0001.epub", epub(t, "3.0", `<dc:identifier id="id">urn:isbn:9780000000001</dc:identifier><dc:title>The Quiet Harbour</dc:title>
		<dc:creator id="a1">Jane Doe</dc:creator><meta refines="#a1" property="role" scheme="marc:relators">aut</meta>
		<dc:date>2020-05-01</dc:date><dc:description>Boats at dusk.</dc:description><dc:language>en</dc:language>
		<meta property="belongs-to-collection" id="c">Harbour Tales</meta><meta refines="#c" property="group-position">2</meta>
		<meta property="dcterms:modified">2026-11-12T00:00:00Z</meta>`))
	f.put("old/book.epub", epub(t, "2.0", `<dc:title>A Second Tide</dc:title><dc:creator opf:role="aut">Sam Roe</dc:creator><dc:date>1999</dc:date>
		<meta name="calibre:series" content="Tides"/><meta name="calibre:series_index" content="3"/>`))
	f.put("Owner/kept.epub", epub(t, "3.0", `<dc:title>What the file says</dc:title><dc:creator>File Author</dc:creator>
		<meta property="dcterms:modified">2026-11-12T00:00:00Z</meta>`))
	f.text("Owner/metadata.opf", `<package xmlns:dc="http://purl.org/dc/elements/1.1/"><metadata><dc:title>What the owner says</dc:title>
		<dc:creator>Owner Author</dc:creator></metadata></package>`)
	f.scan()
	books := f.titles(db.KindBook)
	harbour, tide, kept := books["The Quiet Harbour"], books["A Second Tide"], books["What the owner says"]
	if harbour.PublicID == "" || tide.PublicID == "" || kept.PublicID == "" {
		t.Fatalf("books %v", keys(books))
	}
	if d := f.detail(harbour.PublicID); d.Year != 2020 || d.Summary != "Boats at dusk." || len(d.ExternalIDs) != 1 ||
		len(creditNames(d)) != 1 || creditNames(d)[0] != "Jane Doe" || d.IndexNumber == nil || *d.IndexNumber != 2 {
		t.Fatalf("EPUB 3: year %d, summary %q, ids %v, credits %v, index %v", d.Year, d.Summary, d.ExternalIDs, creditNames(d), d.IndexNumber)
	}
	if d := f.detail(tide.PublicID); d.Year != 1999 || len(creditNames(d)) != 1 || creditNames(d)[0] != "Sam Roe" || d.IndexNumber == nil || *d.IndexNumber != 3 {
		t.Fatalf("EPUB 2: year %d, credits %v, index %v", d.Year, creditNames(d), d.IndexNumber)
	}
	if names := creditNames(f.detail(kept.PublicID)); len(names) != 1 || names[0] != "Owner Author" {
		t.Fatalf("the sidecar's author should win: %v", names)
	}
	series := f.titles(db.KindSeries)
	if series["Harbour Tales"].PublicID == "" || series["Tides"].PublicID == "" {
		t.Fatalf("series %v", keys(series))
	}
}

// A comic's details come from its ComicInfo.xml: the issue's own title, writers, year, summary
// and genres. Without one, the file's name is the title as before.
func TestAComicIsReadFromItsComicInfo(t *testing.T) {
	f := newFixture(t, db.LibraryComics)
	page := zipped(t, [2]string{"001.png", "not really a picture"})
	f.put("Tiny Hero/Tiny Hero 001.cbz", zipped(t, [2]string{"001.png", "page"}, [2]string{"ComicInfo.xml", `<?xml version="1.0"?>
		<ComicInfo><Title>The First Light</Title><Series>Tiny Hero</Series><Number>1</Number><Writer>Ann Lee, Bo Park</Writer>
		<Year>2019</Year><Month>3</Month><Summary>It begins.</Summary><Genre>Adventure</Genre></ComicInfo>`}))
	f.put("Tiny Hero/Tiny Hero 002.cbz", page)
	f.scan()
	issues := f.children(f.titles(db.KindSeries)["Tiny Hero"].PublicID)
	first, second := issues[1], issues[2]
	if first.Title != "The First Light" || second.Title != "Tiny Hero 002" {
		t.Fatalf("issues: %q and %q", first.Title, second.Title)
	}
	d := f.detail(first.PublicID)
	if d.Year != 2019 || d.Summary != "It begins." || len(d.Tags) != 1 || len(creditNames(d)) != 2 || creditNames(d)[1] != "Bo Park" {
		t.Fatalf("issue 1: year %d, summary %q, tags %v, credits %v", d.Year, d.Summary, d.Tags, creditNames(d))
	}
}

// A long book's package document lists thousands of parts after its metadata; its details are
// still read.
func TestALongBooksDetailsAreRead(t *testing.T) {
	f := newFixture(t, db.LibraryBooks)
	var manifest bytes.Buffer
	for n := 0; n < 3000; n++ {
		fmt.Fprintf(&manifest, `<item id="p%d" href="p%d.xhtml" media-type="application/xhtml+xml"/>`, n, n)
	}
	f.put("long.epub", zipped(t, [2]string{"mimetype", "application/epub+zip"},
		[2]string{"META-INF/container.xml", `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles>
			<rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		[2]string{"content.opf", `<package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
			<dc:title>A Very Long Book</dc:title><dc:creator>Long Writer</dc:creator></metadata><manifest>` + manifest.String() +
			`</manifest><spine><itemref idref="p0"/></spine></package>`}))
	f.scan()
	long := f.titles(db.KindBook)["A Very Long Book"]
	if long.PublicID == "" || len(creditNames(f.detail(long.PublicID))) != 1 {
		t.Fatalf("books %v", keys(f.titles(db.KindBook)))
	}
}

// A book whose file is rewritten with new details is brought up to date on the next scan.
func TestABookWhoseFileChangesIsReadAgain(t *testing.T) {
	f := newFixture(t, db.LibraryBooks)
	f.put("draft.epub", epub(t, "3.0", `<dc:title>Working Title</dc:title><dc:creator>First Writer</dc:creator>`))
	f.scan()
	before := f.titles(db.KindBook)["Working Title"]
	f.put("draft.epub", epub(t, "3.0", `<dc:title>Final Title</dc:title><dc:creator>Second Writer</dc:creator><dc:date>2024</dc:date>`))
	f.scan()
	books := f.titles(db.KindBook)
	after := books["Final Title"]
	if len(books) != 1 || after.PublicID != before.PublicID {
		t.Fatalf("books %v: the same book should be brought up to date", keys(books))
	}
	if d := f.detail(after.PublicID); d.Year != 2024 || len(creditNames(d)) != 1 || creditNames(d)[0] != "Second Writer" {
		t.Fatalf("after the change: year %d, credits %v", d.Year, creditNames(d))
	}
}

// Scan all files reads every file again, changed or not (03-scanning rule 27): a book scanned
// when its own details were not read takes them from its file, and is the same book.
func TestScanAllFilesReadsABooksOwnDetailsAgain(t *testing.T) {
	f := newFixture(t, db.LibraryBooks)
	f.put("plain.epub", epub(t, "3.0", `<dc:title>Its Own Title</dc:title><dc:creator>Its Author</dc:creator><dc:date>2019</dc:date>`))
	libraries, err := f.d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// First scanned for names only, as a library was before books' own details were read.
	if err := f.d.SetLibraryOptions(ctx, libraries[0].ID, db.LibraryOptions{Depth: db.DepthFiles}); err != nil {
		t.Fatal(err)
	}
	f.scan()
	before := f.titles(db.KindBook)["plain"]
	if err := f.d.SetLibraryOptions(ctx, libraries[0].ID, db.LibraryOptions{Depth: db.DepthBasic}); err != nil {
		t.Fatal(err)
	}
	f.scan()
	if _, still := f.titles(db.KindBook)["plain"]; !still {
		t.Fatalf("an ordinary scan read an unchanged file: %v", keys(f.titles(db.KindBook)))
	}
	f.s.AllFiles = true
	f.scan()
	after := f.titles(db.KindBook)["Its Own Title"]
	if after.PublicID == "" || after.PublicID != before.PublicID {
		t.Fatalf("after Scan all files: %v (want the same book under its own title)", keys(f.titles(db.KindBook)))
	}
	if d := f.detail(after.PublicID); d.Year != 2019 || len(creditNames(d)) != 1 || creditNames(d)[0] != "Its Author" {
		t.Fatalf("after Scan all files: year %d, credits %v", d.Year, creditNames(d))
	}
}

// A lone dated file and a folder ending in Magazine do not classify a book; explicit fields do.
func TestPeriodicalNamesFillMissingFileFields(t *testing.T) {
	f := newFixture(t, db.LibraryBooks)
	f.put("Wired Magazine/Wired Magazine 2026-11.epub", epub(t, "3.0", `<dc:title>Wired Magazine November</dc:title>`))
	f.put("Mixed/mixed.epub", epub(t, "3.0", `<dc:title>Mixed</dc:title><dc:type>magazine</dc:type><dc:type>newspaper</dc:type>`))
	f.put("Real/real.epub", epub(t, "3.0", `<dc:title>Real Issue</dc:title><dc:type>Magazine</dc:type>`))
	f.scan()
	f.titles(db.KindBook) // loads the library the query names
	magazine := db.PublicationMagazine
	page, err := f.d.Items(ctx, f.viewer, db.ItemQuery{Library: f.lib.PublicID, Kinds: []db.ItemKind{db.KindBook}, Publication: &magazine, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, it := range page.Items {
		got[it.Title]++
	}
	if len(got) != 1 || got["Real Issue"] != 1 {
		t.Fatalf("magazines: %v (want only the explicit issue)", got)
	}
}

// Reading all remote chapters is Complete-depth work, not an ordinary metadata scan.
func TestRemoteEPUBWordCountsRequireCompleteDepth(t *testing.T) {
	for _, depth := range []string{db.DepthBasic, db.DepthComplete} {
		f := newFixture(t, db.LibraryBooks)
		f.put("book.epub", epub(t, "3.0", `<dc:title>Counted</dc:title>`))
		store := &storage{root: f.root}
		f.s.Remote = func(context.Context, int64) (remote.Storage, time.Duration, error) { return store, time.Hour, nil }
		libraries, err := f.d.AllLibraries(ctx)
		if err != nil {
			t.Fatal(err)
		}
		library := libraries[0]
		library.Options.Depth = depth
		f.lib = library
		library.Paths[0].RemoteID, library.Paths[0].Path = 1, ""
		if _, err := f.s.Scan(ctx, library); err != nil {
			t.Fatal(err)
		}
		count := f.titles(db.KindBook)["Counted"].WordCount
		if depth == db.DepthBasic && count != nil {
			t.Fatal("Basic read the remote book's text")
		}
		if depth == db.DepthComplete && (count == nil || *count != 1) {
			t.Fatal("Complete did not count the remote book's words")
		}
	}
}

// Common date/issue shapes group a publication without a provider or extra file reads.
func TestPeriodicalDatesAndGroupsFromNamesKeepOwnerLocks(t *testing.T) {
	f := newFixture(t, db.LibraryBooks)
	cases := []struct {
		file, title, date string
		publication       int
	}{
		{"Amazing Stories 1926-04.epub", "Amazing Stories 1926-04", "1926-04", db.PublicationMagazine},
		{"Amazing Stories 1926-05.epub", "Amazing Stories 1926-05", "1926-05", db.PublicationMagazine},
		{"The Strand Magazine/The Strand Magazine 1891-07.epub", "The Strand Magazine 1891-07", "1891-07", db.PublicationMagazine},
		{"Times/Times 1918-10-21.epub", "Times 1918-10-21", "1918-10-21", db.PublicationNewspaper},
		{"Times/Times 19181022.epub", "Times 19181022", "1918-10-22", db.PublicationNewspaper},
		{"The Strand Magazine/The Strand Magazine August 1891.epub", "The Strand Magazine August 1891", "1891-08", db.PublicationMagazine},
		{"Newspapers/Daily/21 Oct 1918/edition.epub", "edition", "1918-10-21", db.PublicationNewspaper},
		{"Magazines/Wired/Issue 12.epub", "Issue 12", "", db.PublicationMagazine},
		{"Magazines/Wired/0013.epub", "0013", "", db.PublicationMagazine},
		{"Wired/Wired #14.epub", "Wired #14", "", db.PublicationBook},
		{"Aleksandr Solzhenitsyn/August 1914/August 1914.epub", "August 1914", "", db.PublicationBook},
		{"David Halberstam/October 1964.epub", "October 1964", "", db.PublicationBook},
		{"James S A Corey/The Expanse/The Expanse #3.epub", "The Expanse #3", "", db.PublicationBook},
		{"Discworld/Discworld 03.epub", "Discworld 03", "", db.PublicationBook},
		{"Ambiguous/Ambiguous 03-04-2026.epub", "Ambiguous 03-04-2026", "", db.PublicationBook},
		{"Invalid/Invalid 2026-02-30.epub", "Invalid 2026-02-30", "", db.PublicationBook},
	}
	for _, c := range cases {
		f.put(c.file, epub(t, "3.0", ""))
	}
	f.write("Unsorted/Wired 2026-11.pdf")
	f.scan()
	books := f.titles(db.KindBook)
	for _, c := range cases {
		it := books[c.title]
		if it.PublicID == "" || it.Publication != c.publication || it.ReleaseDate != c.date {
			t.Fatalf("%s: publication %d, date %q; want %d, %q", c.file, it.Publication, it.ReleaseDate, c.publication, c.date)
		}
	}
	series := f.titles(db.KindSeries)
	for name, count := range map[string]int{"Amazing Stories": 2, "The Strand Magazine": 2, "Times": 2, "Daily": 1, "Wired": 2} {
		group := series[name]
		if group.PublicID == "" {
			t.Fatalf("no publication group %s", name)
		}
		page, err := f.d.Items(ctx, f.viewer, db.ItemQuery{Library: f.lib.PublicID, Series: group.PublicID, Limit: 10})
		if err != nil || len(page.Items) != count {
			t.Fatalf("%s group: %d issues, %v", name, len(page.Items), err)
		}
	}
	// A PDF already scanned as a book learns its publication when moved into that folder.
	oldPDF := books["Wired 2026-11"]
	f.move("Unsorted/Wired 2026-11.pdf", "Magazines/Wired/Wired 2026-11.pdf")
	f.scan()
	movedPDF := f.titles(db.KindBook)["Wired 2026-11"]
	if movedPDF.PublicID != oldPDF.PublicID || movedPDF.Publication != db.PublicationMagazine || movedPDF.ReleaseDate != "2026-11" {
		t.Fatalf("the moved PDF did not learn its issue facts: %+v", movedPDF)
	}
	wired := books["Issue 12"]
	if wired.IndexNumber == nil || *wired.IndexNumber != 12 {
		t.Fatal("numbered issue lost its number")
	}
	kept := books["Times 1918-10-21"]
	locks, err := f.d.MetadataLocks(ctx, kept.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	book, date := "book", "1999-01-01"
	if _, err := f.d.EditMetadata(ctx, kept.PublicID, locks.Revision, db.MetadataEdit{PublicationType: &book, ReleaseDate: &date}); err != nil {
		t.Fatal(err)
	}
	f.put("Times/Times 1918-10-21.epub", epub(t, "3.0", `<dc:type>newspaper</dc:type><dc:date>2000-01-02</dc:date>`))
	f.scan()
	after := f.detail(kept.PublicID)
	if after.Publication != db.PublicationBook || after.ReleaseDate != date {
		t.Fatalf("scan overwrote owner locks: publication %d, date %q", after.Publication, after.ReleaseDate)
	}
}
