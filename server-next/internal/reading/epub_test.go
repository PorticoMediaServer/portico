package reading

import (
	"archive/zip"
	"bytes"
	"testing"
)

// Every EPUB 3 names when it was last changed in <meta property="dcterms:modified">…</meta>, with
// an end tag. Read as HTML, <meta> closed itself and that end tag made every such book "damaged".
func TestAnEPUB3WithItsRequiredModifiedDateReads(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range [][2]string{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"OEBPS/content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id">
			<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">urn:uuid:1</dc:identifier><dc:title>A Book</dc:title>
			<meta property="dcterms:modified">2026-11-12T00:00:00Z</meta><meta name="cover" content="c1"/></metadata>
			<manifest><item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c1"/></spine></package>`},
		{"OEBPS/c1.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>Words.</p></body></html>`},
	} {
		w, err := zw.Create(e[0])
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(e[1]))
	}
	zw.Close()
	a, err := openArchive(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	book, err := readEPUB(a)
	if err != nil {
		t.Fatalf("a valid EPUB 3 is refused: %v", err)
	}
	if len(book.spine) != 1 || book.spine[0] != "OEBPS/c1.xhtml" {
		t.Fatalf("spine %v", book.spine)
	}
}
