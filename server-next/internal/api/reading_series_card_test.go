package api

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A book or comic library's series names its publication, by the publication filter's own rule (a
// magazine's or newspaper's run of issues names it), so every client opens its card (Home, Discover,
// search, a library tab) as a reading series or a periodical; an audiobook series names none and is
// unchanged on the wire.
func TestAReadingSeriesNamesItsPublication(t *testing.T) {
	b := newBookShelf(t)
	magazine(t, filepath.Join(b.books, "Monthly", "October.epub"), "Monthly October", "2026-10")
	magazine(t, filepath.Join(b.books, "Monthly", "November.epub"), "Monthly November", "2026-11-03")
	for _, day := range []string{"2026-11-01", "2026-11-02"} {
		newspaperIssue(t, filepath.Join(b.books, "Gazette", day+".epub"), "Gazette "+day, day)
		// A comic library's run the scan files as newspapers still opens as a reading series: periodicals
		// live only in book libraries (G6, 22 Oct: "magazines and newspapers go in Books").
		zipFile(t, filepath.Join(b.comics, "Daily", "Daily "+day+".cbz"), [2]string{"p1.png", pngPicture(t, 64, 96)},
			[2]string{"ComicInfo.xml", `<ComicInfo><Format>Newspaper</Format></ComicInfo>`})
	}
	b.scan()
	ctx := context.Background()
	audiobooks, err := b.s.db.CreateLibrary(ctx, "Audiobooks", db.LibraryAudiobooks, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	tides := db.ItemKey{Kind: db.KeyName, Key: "series|tides"}
	if _, err := b.s.db.Import(ctx, db.ImportBatch{LibraryID: audiobooks.ID, PathID: audiobooks.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}},
		Files: []db.ImportFile{{FileRef: db.FileRef{Name: "a.m4b"}, Role: db.FileMedia, Size: 1, ModifiedNS: 1, Container: "mp4", DurationMS: 60_000,
			Streams: []db.Stream{{Kind: db.StreamAudio, Codec: "aac", Channels: 2, SampleRate: 44100}}}},
		Items: []db.ImportItem{
			{Kind: db.KindSeries, Title: "Tides", Keys: []db.ItemKey{tides}},
			{Kind: db.KindAudiobook, Title: "Low Water", Keys: []db.ItemKey{{Kind: db.KeyName, Key: "audiobook|low water"}},
				Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: db.FileRef{Name: "a.m4b"}}}}},
				ExtraOf:  []db.ItemKey{tides}, ExtraKind: db.RelationSeriesMember},
		}}); err != nil {
		t.Fatal(err)
	}
	publication := map[string]string{}
	for _, lib := range get[Libraries](t, b.s, b.owner.AccessToken, "/v1/libraries").Items {
		for _, it := range get[ItemPage](t, b.s, b.owner.AccessToken, "/v1/items?library="+lib.ID+"&kind=series").Items {
			publication[lib.Kind+" "+it.Title] = it.PublicationType
			if one := get[Item](t, b.s, b.owner.AccessToken, "/v1/items/"+it.ID); one.PublicationType != it.PublicationType {
				t.Errorf("%s's own page names %q, its card %q", it.Title, one.PublicationType, it.PublicationType)
			}
		}
	}
	if got := publication[db.LibraryComics+" Saga"]; got != "book" {
		t.Errorf("a comic library's series names publication %q, want book", got)
	}
	if got := publication[db.LibraryBooks+" Monthly"]; got != "magazine" {
		t.Errorf("a magazine's run of issues names publication %q, want magazine (%v)", got, publication)
	}
	if got := publication[db.LibraryBooks+" Gazette"]; got != "newspaper" {
		t.Errorf("a newspaper's run of issues names publication %q, want newspaper (%v)", got, publication)
	}
	if got := publication[db.LibraryComics+" Daily"]; got != "book" {
		t.Errorf("a comic library's run names publication %q, want the ordinary series value book (%v)", got, publication)
	}
	if got, ok := publication[db.LibraryAudiobooks+" Tides"]; !ok || got != "" {
		t.Errorf("an audiobook series names publication %q (listed %v), want none", got, ok)
	}
}

// newspaperIssue writes one issue of a newspaper as an EPUB that says so, in a run named "Gazette".
func newspaperIssue(t *testing.T, path, title, date string) {
	zipFile(t, path,
		[2]string{"mimetype", "application/epub+zip"},
		[2]string{"META-INF/container.xml", `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
			<rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		[2]string{"OEBPS/content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0">
			<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>` + title + `</dc:title><dc:type>Newspaper</dc:type><dc:date>` + date + `</dc:date>
			<meta property="belongs-to-collection" id="c">Gazette</meta><meta property="dcterms:modified">2026-11-12T00:00:00Z</meta></metadata>
			<manifest><item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c1"/></spine></package>`},
		[2]string{"OEBPS/c1.xhtml", chapterOne})
}
