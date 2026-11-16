package reading

import (
	"bytes"
	"encoding/xml"
	"errors"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// openMobi opens a book in testdata/mobi (written by generate.go there) as the reader does.
func openMobi(t *testing.T, name string) (*archive, *epub) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "mobi", name))
	if err != nil {
		t.Fatal(err)
	}
	a, err := ebookArchive(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	book, err := readEPUB(a)
	if err != nil {
		t.Fatal(err)
	}
	return a, book
}

// part reads one part, which must be XML the app's reader can parse as it is.
func part(t *testing.T, a *archive, name string) string {
	t.Helper()
	raw, err := a.read(name, maxPartSize)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	d := xml.NewDecoder(bytes.NewReader(raw))
	for {
		if _, err := d.Token(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("%s is not XML: %v\n%s", name, err, raw)
		}
	}
	return string(raw)
}

// An AZW3 reads as the EPUB its KF8 text is rebuilt into: each file put back together from its
// skeleton and fragments, a link to a place going to the id there, its picture, style sheet,
// contents and cover all named in the book.
func TestAnAZW3ReadsAsTheEPUBItIsRebuiltInto(t *testing.T) {
	a, book := openMobi(t, "kf8.azw3")
	if !slices.Equal(book.spine, []string{"part0000.xhtml", "part0001.xhtml"}) {
		t.Fatalf("spine %v", book.spine)
	}
	one, two := part(t, a, "part0000.xhtml"), part(t, a, "part0001.xhtml")
	for _, want := range []string{`<body aid="0"><p>One. <a href="part0001.xhtml#two">To two</a> <img src="image00001.png"`, `href="flow0001.css"`} {
		if !strings.Contains(one, want) {
			t.Fatalf("part one lacks %s:\n%s", want, one)
		}
	}
	if !strings.Contains(two, `<body aid="0"><h1 id="two">Two</h1><p>The end.</p></body>`) {
		t.Fatalf("part two:\n%s", two)
	}
	if css, err := a.read("flow0001.css", maxPartSize); err != nil || string(css) != "p { margin: 0 }" {
		t.Fatalf("style sheet %q, %v", css, err)
	}
	if picture, err := a.read("image00001.png", maxPartSize); err != nil {
		t.Fatal(err)
	} else if _, err := png.Decode(bytes.NewReader(picture)); err != nil {
		t.Fatalf("picture: %v", err)
	}
	if !slices.Equal(book.contents, []Contents{{Title: "One", Part: "part0000.xhtml"}, {Title: "Two", Part: "part0001.xhtml", Fragment: "two"}}) {
		t.Fatalf("contents %+v", book.contents)
	}
	raw, _ := os.ReadFile(filepath.Join("testdata", "mobi", "kf8.azw3"))
	pkg, err := EPUBPackage(bytes.NewReader(raw), int64(len(raw)))
	if err != nil || !strings.Contains(string(pkg), `<dc:title>A Tiny KF8 Book</dc:title>`) || !strings.Contains(string(pkg), `<dc:creator opf:role="aut">Bo Example</dc:creator>`) {
		t.Fatalf("package %s, %v", pkg, err)
	}
	if !strings.Contains(string(pkg), `<meta name="cover" content="cover"/>`) || !strings.Contains(string(pkg), `<item id="cover" href="image00001.png"`) {
		t.Fatalf("the cover is not named: %s", pkg)
	}
}

// An older MOBI's one HTML text, PalmDOC-compressed in Windows-1252, is split at its page break
// into XHTML parts. A link to a place goes to an anchor put there, and its picture and contents
// are the book's. A book locked with DRM says so and is not opened.
func TestAnOlderMOBIIsSplitAtItsPageBreaksIntoXHTML(t *testing.T) {
	a, book := openMobi(t, "older.mobi")
	if !slices.Equal(book.spine, []string{"part0000.xhtml", "part0001.xhtml"}) {
		t.Fatalf("spine %v", book.spine)
	}
	one, two := part(t, a, "part0000.xhtml"), part(t, a, "part0001.xhtml")
	for _, want := range []string{"Café by the river.", "abcabcabcabcabc", `<img src="image00001.png"/>`} {
		if !strings.Contains(one, want) {
			t.Fatalf("part one lacks %s:\n%s", want, one)
		}
	}
	link := strings.Index(one, `<a href="part0001.xhtml#filepos`)
	if link < 0 {
		t.Fatalf("part one has no link to part two:\n%s", one)
	}
	id := one[link+len(`<a href="part0001.xhtml#`):]
	id = id[:strings.IndexByte(id, '"')]
	if !strings.Contains(two, `<span id="`+id+`"></span><p>Chapter Two</p>`) || strings.Contains(two, "mbp:") {
		t.Fatalf("part two has no anchor %s where the link points:\n%s", id, two)
	}
	if len(book.contents) != 2 || book.contents[1] != (Contents{Title: "Two", Part: "part0001.xhtml", Fragment: id}) {
		t.Fatalf("contents %+v", book.contents)
	}
	raw, _ := os.ReadFile(filepath.Join("testdata", "mobi", "locked.mobi"))
	if _, err := ebookArchive(bytes.NewReader(raw), int64(len(raw))); !errors.Is(err, ErrLocked) {
		t.Fatalf("a locked book: %v", err)
	}
}
