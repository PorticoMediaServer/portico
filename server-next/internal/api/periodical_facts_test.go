package api

import (
	"net/url"
	"path/filepath"
	"testing"
)

// A magazine's archive head (phone-read 24): the run names its publisher from its issues' own
// files and how often it comes out from their dates, and each issue its volume and number from
// its name.
func TestAMagazineArchiveNamesItsPublisherFrequencyAndIssues(t *testing.T) {
	b := newBookShelf(t)
	token := b.owner.AccessToken
	for n, date := range []string{"1929-10", "1929-11", "1929-12"} {
		number := []string{"7", "8", "9"}[n]
		zipFile(t, filepath.Join(b.books, "Magazines", "Amazing Stories", "Amazing Stories Vol. 4, No. "+number+".epub"),
			[2]string{"mimetype", "application/epub+zip"},
			[2]string{"META-INF/container.xml", `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
			<rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
			[2]string{"OEBPS/content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0">
			<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Amazing Stories ` + date + `</dc:title><dc:type>Magazine</dc:type>
			<dc:date>` + date + `</dc:date><dc:publisher>Experimenter Publishing</dc:publisher><meta property="dcterms:modified">2026-11-12T00:00:00Z</meta></metadata>
			<manifest><item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c1"/></spine></package>`},
			[2]string{"OEBPS/c1.xhtml", chapterOne})
	}
	b.scan()
	var books string
	for _, lib := range get[Libraries](t, b.s, token, "/v1/libraries").Items {
		if lib.Kind == "books" {
			books = lib.ID
		}
	}
	runs := get[ItemPage](t, b.s, token, "/v1/items?"+url.Values{"library": {books}, "kind": {"series"}, "publication": {"magazine"}}.Encode())
	if len(runs.Items) != 1 {
		t.Fatalf("magazine runs: %+v", runs.Items)
	}
	run := get[Item](t, b.s, token, "/v1/items/"+runs.Items[0].ID)
	if run.Publisher != "Experimenter Publishing" || run.Frequency != "monthly" {
		t.Fatalf("archive head: publisher %q, frequency %q", run.Publisher, run.Frequency)
	}
	issues := get[ItemPage](t, b.s, token, "/v1/items?"+url.Values{"series": {run.ID}, "sort": {"release"}, "order": {"desc"}}.Encode())
	if len(issues.Items) != 3 || issues.Items[0].Index == nil || *issues.Items[0].Index != 9 ||
		issues.Items[0].ParentIndex == nil || *issues.Items[0].ParentIndex != 4 {
		t.Fatalf("newest issue's volume and number: %+v", issues.Items)
	}
}
