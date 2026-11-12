package api

import (
	"net/url"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// magazine is a small real EPUB whose own package says it is a magazine issue of a run.
func magazine(t *testing.T, path, title, date string) {
	zipFile(t, path,
		[2]string{"mimetype", "application/epub+zip"},
		[2]string{"META-INF/container.xml", `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
			<rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		[2]string{"OEBPS/content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0">
			<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>` + title + `</dc:title><dc:type>Magazine</dc:type><dc:date>` + date + `</dc:date>
			<meta property="belongs-to-collection" id="c">Monthly</meta><meta property="dcterms:modified">2026-11-12T00:00:00Z</meta></metadata>
			<manifest><item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c1"/></spine></package>`},
		[2]string{"OEBPS/c1.xhtml", chapterOne})
}

// A Books or Comics library's magazines and newspapers: a title is one only when its own file
// says so; an issue's date is its own, or for a periodical a date in its name. The library lists
// a Magazines or Newspapers view only when the profile may see such an issue; the list filters by
// publication, a run's issues come by date with their years and days counted, and the library's
// Discover has New issues.
func TestMagazinesAndNewspapers(t *testing.T) {
	b := newBookShelf(t)
	token := b.owner.AccessToken
	magazine(t, filepath.Join(b.books, "Monthly", "October.epub"), "Monthly October", "2026-10")
	magazine(t, filepath.Join(b.books, "Monthly", "November.epub"), "Monthly November", "2026-11-03")
	page := pngPicture(t, 64, 96)
	for _, day := range []string{"2026-11-01", "2026-11-02"} {
		zipFile(t, filepath.Join(b.comics, "Daily", "Daily "+day+".cbz"), [2]string{"p1.png", page},
			[2]string{"ComicInfo.xml", `<ComicInfo><Format>Newspaper</Format></ComicInfo>`})
	}
	b.scan()
	var books, comics string
	for _, lib := range get[Libraries](t, b.s, token, "/v1/libraries").Items {
		switch lib.Kind {
		case db.LibraryBooks:
			books = lib.ID
		case db.LibraryComics:
			comics = lib.ID
		}
	}
	views := func(token, id string) map[string]bool {
		out := map[string]bool{}
		for _, v := range get[Library](t, b.s, token, "/v1/libraries/"+id).Views {
			out[v.Kind] = true
		}
		return out
	}
	if v := views(token, books); !v["magazine"] || v["newspaper"] {
		t.Fatalf("books library views %v", v)
	}
	if v := views(token, comics); !v["newspaper"] || v["magazine"] {
		t.Fatalf("comics library views %v", v)
	}
	if v := views(b.limited, books); v["magazine"] {
		t.Fatal("a profile that may see no issue is offered the Magazines tab")
	}

	runs := get[ItemPage](t, b.s, token, "/v1/items?"+url.Values{"library": {books}, "kind": {"series"}, "publication": {"magazine"}}.Encode())
	if len(runs.Items) != 1 || runs.Items[0].Title != "Monthly" {
		t.Fatalf("magazine runs: %+v", runs.Items)
	}
	issues := get[ItemPage](t, b.s, token, "/v1/items?"+url.Values{"series": {runs.Items[0].ID}, "sort": {"release"}, "order": {"desc"}}.Encode())
	if len(issues.Items) != 2 || issues.Items[0].ReleaseDate != "2026-11-03" || issues.Items[1].ReleaseDate != "2026-10" {
		t.Fatalf("issues newest first: %+v", issues.Items)
	}
	if issues.Items[0].WordCount == nil || *issues.Items[0].WordCount == 0 {
		t.Fatal("the scan did not store the EPUB word count")
	}
	if issues.Items[0].PublicationType != "magazine" {
		t.Fatal("the issue does not say it is a magazine")
	}
	if plain := get[ItemPage](t, b.s, token, "/v1/items?"+url.Values{"library": {books}, "kind": {"book"}, "publication": {"book"}}.Encode()); len(plain.Items) == 0 {
		t.Fatal("a book that says nothing about itself is a book")
	}
	years := get[Facets](t, b.s, token, "/v1/facets?"+url.Values{"series": {runs.Items[0].ID}, "facet": {"issueYear"}}.Encode())
	if len(years.Values) != 1 || years.Values[0].Value != "2026" || years.Values[0].Count != 2 {
		t.Fatalf("issue years: %+v", years.Values)
	}

	daily := get[ItemPage](t, b.s, token, "/v1/items?"+url.Values{"library": {comics}, "kind": {"issue"}, "publication": {"newspaper"}, "sort": {"release"}, "order": {"asc"}}.Encode())
	if len(daily.Items) != 2 || daily.Items[0].ReleaseDate != "2026-11-01" {
		t.Fatalf("a newspaper's editions take their dates from their names: %+v", daily.Items)
	}
	days := get[Facets](t, b.s, token, "/v1/facets?"+url.Values{"library": {comics}, "kind": {"issue"}, "publication": {"newspaper"},
		"released": {"2026-11-01..2026-11-30"}, "facet": {"issueDay"}}.Encode())
	if len(days.Values) != 2 || days.Values[0].Value != "2026-11-02" {
		t.Fatalf("a month's days: %+v", days.Values)
	}

	landing := get[Home](t, b.s, token, "/v1/libraries/"+books+"/landing")
	var newIssues []Item
	for _, row := range landing.Rows {
		if row.ID == "new_issues" {
			newIssues = row.Items
		}
	}
	if len(newIssues) != 2 || newIssues[0].Title != "Monthly November" {
		t.Fatalf("New issues: %+v", newIssues)
	}
}
