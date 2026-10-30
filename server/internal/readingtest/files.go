// Package readingtest supplies original, lawful publications for reader tests.
// It is imported by tests only; it is not a production content/data producer.
package readingtest

import (
	"archive/zip"
	"bytes"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
)

func EPUB() []byte { return epub(true) }

// EPUBWithoutCreator is the same lawful publication without an embedded
// creator, so a test can exercise folder-derived attribution alone.
func EPUBWithoutCreator() []byte { return epub(false) }

func epub(creator bool) []byte {
	metadata := `<dc:identifier id="bookid">urn:uuid:382df65f-89d0-496e-ab18-6670e4d27ad4</dc:identifier><dc:title>Original publication</dc:title>`
	if creator {
		metadata += `<dc:creator>Portico</dc:creator>`
	}
	metadata += `<dc:language>en</dc:language><meta property="dcterms:modified">2026-10-23T00:00:00Z</meta>`
	return archive([][2]string{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0"><rootfiles><rootfile full-path="EPUB/package.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"EPUB/package.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">` + metadata + `</metadata><manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/></manifest><spine page-progression-direction="ltr"><itemref idref="chapter"/></spine></package>`},
		{"EPUB/nav.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="en"><head><title>Contents</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml">Chapter</a></li></ol></nav></body></html>`},
		{"EPUB/chapter.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml" lang="en"><head><title>Original chapter</title></head><body><h1 id="chapter">Original chapter</h1><p>This original text is a small publication used to verify reading, source authorization and saved locations.</p></body></html>`},
	})
}
func CBZ() []byte {
	var picture bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 128, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 128; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 80, 255})
		}
	}
	if err := png.Encode(&picture, img); err != nil {
		panic(err)
	}
	return archive([][2]string{{"page1.png", picture.String()}, {"page2.png", picture.String()}, {"ComicInfo.xml", `<ComicInfo><Title>Original comic</Title><Writer>Portico</Writer><Manga>YesAndRightToLeft</Manga><LanguageISO>en</LanguageISO></ComicInfo>`}})
}
func PDF() []byte {
	var b bytes.Buffer
	fmt.Fprint(&b, "%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	text := "BT /F1 12 Tf 20 100 Td (Original Portico reading fixture.) Tj ET\n"
	objects := []string{`<< /Type /Catalog /Pages 2 0 R >>`, `<< /Type /Pages /Kids [3 0 R] /Count 1 >>`, `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>`, `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(text), text)}
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&b, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return b.Bytes()
}
func archive(entries [][2]string) []byte {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e[0], Method: zip.Deflate}
		var w interface{ Write([]byte) (int, error) }
		var err error
		if e[0] == "mimetype" {
			h.Method = zip.Store
			h.CRC32 = crc32.ChecksumIEEE([]byte(e[1]))
			h.CompressedSize64 = uint64(len(e[1]))
			h.UncompressedSize64 = uint64(len(e[1]))
			w, err = z.CreateRaw(h)
		} else {
			w, err = z.CreateHeader(h)
		}
		if err != nil {
			panic(err)
		}
		if _, err = w.Write([]byte(e[1])); err != nil {
			panic(err)
		}
	}
	if err := z.Close(); err != nil {
		panic(err)
	}
	return b.Bytes()
}
