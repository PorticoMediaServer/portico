package reading

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"
)

// The fixtures are original, minimal publications authored for these tests.
// They contain actual EPUB package/spine/XHTML and PNG image bytes, no copies
// of third-party books and no fake playback endpoints or archive body mocks.
func fixtureZIP(t *testing.T, entries [][2]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, e := range entries {
		header := &zip.FileHeader{Name: e[0], Method: zip.Deflate}
		if e[0] == "mimetype" {
			header.Method = zip.Store
		}
		var w io.Writer
		var err error
		if e[0] == "mimetype" {
			header.CRC32 = crc32.ChecksumIEEE([]byte(e[1]))
			header.CompressedSize64 = uint64(len(e[1]))
			header.UncompressedSize64 = uint64(len(e[1]))
			w, err = z.CreateRaw(header)
		} else {
			w, err = z.CreateHeader(header)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(e[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func fixtureEPUB(t *testing.T, body string) []byte {
	return fixtureZIP(t, [][2]string{{"mimetype", "application/epub+zip"}, {"META-INF/container.xml", `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`}, {"OEBPS/content.opf", `<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">urn:uuid:test</dc:identifier><dc:title>Original reading fixture</dc:title><dc:creator>Portico tests</dc:creator><dc:language>en</dc:language><meta property="dcterms:modified">2026-10-23T00:00:00Z</meta></metadata><manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="c" href="chapter.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c"/></spine></package>`}, {"OEBPS/nav.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title></head><body><nav epub:type="toc"><ol><li><a href="chapter.xhtml">Chapter</a></li></ol></nav></body></html>`}, {"OEBPS/chapter.xhtml", body}})
}
func TestEPUBActualAssetsAndActiveContentBoundary(t *testing.T) {
	good := `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title></head><body><h1 id="start">Original text</h1><p>Lawful fixture.</p><a href="#start">Return</a></body></html>`
	for _, tc := range []struct {
		name, body string
		allowed    bool
	}{{"safe", good, true}, {"script", `<html><script>alert(1)</script></html>`, false}, {"external", `<html><img src="https://origin.invalid/private"/></html>`, false}, {"event", `<html><body onload="alert(1)"/></html>`, false}, {"css", `<html><style>p{background:u\72l(https://origin.invalid)}</style></html>`, false}, {"DTD", `<!DOCTYPE html [<!ENTITY x SYSTEM "file:///etc/passwd">]><html>&x;</html>`, false}, {"refresh", `<html><meta http-equiv="refresh" content="0;url=https://origin.invalid"/></html>`, false}} {
		t.Run(tc.name, func(t *testing.T) {
			raw := fixtureEPUB(t, tc.body)
			e, err := OpenEPUB(context.Background(), bytes.NewReader(raw), int64(len(raw)))
			if err != nil {
				t.Fatal(err)
			}
			if e.Metadata.Title != "Original reading fixture" || len(e.Spine) != 1 {
				t.Fatal(e.Metadata, e.Spine)
			}
			got, mime, err := e.Asset(context.Background(), e.Spine[0])
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v err=%v", tc.allowed, err)
			}
			if tc.allowed && (string(got) != good || mime != "application/xhtml+xml") {
				t.Fatal("asset bytes changed")
			}
			if _, _, err = e.Asset(context.Background(), "../secret"); err == nil {
				t.Fatal("traversal allowed")
			}
		})
	}
}
func fixturePNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 200, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.String()
}
func TestComicActualPagesNaturalOrderAndBounds(t *testing.T) {
	png := fixturePNG(t, 64, 128)
	raw := fixtureZIP(t, [][2]string{{"page10.png", png}, {"page2.png", png}, {"page1.png", png}, {"ComicInfo.xml", `<ComicInfo><Title>Original comic</Title><Manga>YesAndRightToLeft</Manga></ComicInfo>`}})
	c, err := OpenComic(context.Background(), bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if c.Metadata.Direction != "rtl" || c.Names[0] != "page1.png" || c.Names[1] != "page2.png" {
		t.Fatal(c.Names, c.Metadata)
	}
	p, err := c.Page(context.Background(), 1, 32)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "page2.png" || p.Image.Width != 32 || p.Image.Height != 64 || p.Image.MIME != "image/png" {
		t.Fatal(p)
	}
	for _, i := range []int{-1, 3} {
		if _, err = c.Page(context.Background(), i, 0); err == nil {
			t.Fatal("page boundary")
		}
	}
	if _, err = c.Page(context.Background(), 0, 2049); err == nil {
		t.Fatal("width boundary")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.Page(ctx, 0, 32); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestArchiveTraversalDuplicateEncryptionBombAndDirectoryBounds(t *testing.T) {
	for _, name := range []string{"../escape.png", "/absolute.png", "C:drive.png", "a\\b.png", "a/%2e%2e.png", "./a.png"} {
		raw := fixtureZIP(t, [][2]string{{name, "x"}})
		if _, err := openArchive(bytes.NewReader(raw), int64(len(raw))); err == nil {
			t.Fatal(name)
		}
	}
	raw := fixtureZIP(t, [][2]string{{"a.png", "x"}, {"a.png", "y"}})
	if _, err := openArchive(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("duplicate")
	}
	raw = fixtureZIP(t, [][2]string{{"a.png", string(bytes.Repeat([]byte{'x'}, 2<<20))}})
	if _, err := openArchive(bytes.NewReader(raw), int64(len(raw))); !errors.Is(err, ErrLimit) {
		t.Fatal("bomb", err)
	}
	raw = fixtureZIP(t, [][2]string{{"a.png", "x"}})
	binary.LittleEndian.PutUint16(raw[len(raw)-12:], MaxEntries+1)
	if _, err := openArchive(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("entry limit")
	}
	raw = fixtureZIP(t, [][2]string{{"a.png", "x"}})
	for i := 0; i < len(raw)-46; i++ {
		if binary.LittleEndian.Uint32(raw[i:]) == 0x02014b50 {
			binary.LittleEndian.PutUint16(raw[i+8:], 1)
			break
		}
	}
	if _, err := openArchive(bytes.NewReader(raw), int64(len(raw))); err == nil {
		t.Fatal("encrypted")
	}
}
func TestCorruptImageAndCRCFailClosed(t *testing.T) {
	raw := fixtureZIP(t, [][2]string{{"page.png", "<svg onload='attack()'/>"}})
	c, err := OpenComic(context.Background(), bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Page(context.Background(), 0, 0); err == nil {
		t.Fatal("spoofed image")
	}
	good := fixturePNG(t, 2, 2)
	raw = fixtureZIP(t, [][2]string{{"page.png", good}})
	for i := 0; i < len(raw)-46; i++ {
		if binary.LittleEndian.Uint32(raw[i:]) == 0x02014b50 {
			binary.LittleEndian.PutUint32(raw[i+16:], 123)
			break
		}
	}
	c, err = OpenComic(context.Background(), bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Page(context.Background(), 0, 0); err == nil {
		t.Fatal("corrupt CRC")
	}
}
func TestArchiveDoesNotReadWholeSource(t *testing.T) {
	raw := fixtureEPUB(t, `<html><body><p>Original fixture</p></body></html>`)
	counted := &countReader{ReaderAt: bytes.NewReader(raw)}
	e, err := OpenEPUB(context.Background(), counted, int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Resources) != 5 {
		t.Fatal(e.Resources)
	}
	if counted.max > 65557 {
		t.Fatalf("unbounded acquisition %d", counted.max)
	}
}

type countReader struct {
	io.ReaderAt
	max int
}

func (r *countReader) ReadAt(b []byte, o int64) (int, error) {
	r.max = max(r.max, len(b))
	return r.ReaderAt.ReadAt(b, o)
}

type interruptedArchiveReader struct {
	io.ReaderAt
	err error
}

func (r *interruptedArchiveReader) ReadAt(b []byte, offset int64) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	return r.ReaderAt.ReadAt(b, offset)
}

func TestArchiveMemberSourceFailureIsNotMalformedContent(t *testing.T) {
	raw := fixtureZIP(t, [][2]string{{"original.txt", "Original authored member bytes."}})
	r := &interruptedArchiveReader{ReaderAt: bytes.NewReader(raw)}
	a, err := openArchive(r, int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	interrupted := errors.New("temporary source refusal")
	r.err = interrupted
	if _, err = a.read(context.Background(), "original.txt", 1024); !errors.Is(err, interrupted) || errors.Is(err, ErrInvalid) {
		t.Fatal("source failure was replaced by a format error", err)
	}
	r.err = nil
	if body, err := a.read(context.Background(), "original.txt", 1024); err != nil || string(body) != "Original authored member bytes." {
		t.Fatal("temporary source failure poisoned the archive", string(body), err)
	}
}

func TestEPUBMimetypeSourceFailureIsNotMalformedContent(t *testing.T) {
	raw := fixtureEPUB(t, `<html><body>Original authored text.</body></html>`)
	r := &interruptedArchiveReader{ReaderAt: bytes.NewReader(raw)}
	a, err := openArchive(r, int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	interrupted := errors.New("temporary source refusal")
	r.err = interrupted
	if _, err = openEPUBArchive(context.Background(), a); !errors.Is(err, interrupted) || errors.Is(err, ErrInvalid) {
		t.Fatal("mimetype source refusal was replaced by a format error", err)
	}
	r.err = nil
	if _, err = openEPUBArchive(context.Background(), a); err != nil {
		t.Fatal("temporary refusal poisoned the EPUB", err)
	}
}
