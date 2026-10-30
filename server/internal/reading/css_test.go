package reading

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
)

func fixturePublisherFont(t *testing.T) []byte {
	t.Helper()
	// Use the actual licensed publisher font already pinned in this repository.
	font, err := os.ReadFile("../../../packages/reader-core/assets/Literata[opsz,wght].ttf")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(font)) != "b41138c9373112f32abb589cc22e8674b06ed4048b0c513be922bdd26f274440" {
		t.Fatal("publisher font differs from the checked font")
	}
	return font
}

func TestEPUBDeclaredLocalCSSPreservesEveryAsset(t *testing.T) {
	font := fixturePublisherFont(t)
	var err error
	css := `@import "../Theme/palette.css" screen;
@import url('../Theme/layout.css') layer(layout) supports(display: grid);
@font-face { font-family: Publisher; src: url('../Fonts/publisher.ttf') format('truetype'); }
@media screen and (min-width: 32em) { p { background-image: url(../Images/diagram.png); } }
@supports (display: grid) { body { display: grid; } }
p::before { content: "The word url and https://example.invalid are plain text here"; }`
	chapter := `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Original chapter</title><link rel="stylesheet" href="../Styles/main.css"/><style>@media screen { p { background: url('../Images/diagram.png#figure'); } }</style></head><body><p style="background-image:url('../Images/diagram.png')">Original text.</p></body></html>`
	entries := [][2]string{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0"><rootfiles><rootfile full-path="EPUB/package.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"EPUB/package.opf", `<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">urn:uuid:4875335a-10c2-4850-aa65-3c5c9ae752e6</dc:identifier><dc:title>Original CSS publication</dc:title><dc:language>en</dc:language><meta property="dcterms:modified">2026-10-23T00:00:00Z</meta></metadata><manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="chapter" href="Text/chapter.xhtml" media-type="application/xhtml+xml"/><item id="css" href="Styles/main.css" media-type="text/css"/><item id="palette" href="Theme/palette.css" media-type="text/css"/><item id="layout" href="Theme/layout.css" media-type="text/css"/><item id="font" href="Fonts/publisher.ttf" media-type="font/ttf"/><item id="diagram" href="Images/diagram.png" media-type="image/png"/></manifest><spine><itemref idref="chapter"/></spine></package>`},
		{"EPUB/nav.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title></head><body><nav epub:type="toc"><ol><li><a href="Text/chapter.xhtml">Chapter</a></li></ol></nav></body></html>`},
		{"EPUB/Text/chapter.xhtml", chapter},
		{"EPUB/Styles/main.css", css},
		{"EPUB/Theme/palette.css", `p { color: #231f20; }`},
		{"EPUB/Theme/layout.css", `body { margin: 1em; }`},
		{"EPUB/Fonts/publisher.ttf", string(font)},
		{"EPUB/Images/diagram.png", fixturePNG(t, 64, 64)},
	}
	raw := fixtureZIP(t, entries)
	for _, sequential := range []bool{false, true} {
		t.Run(fmt.Sprint("sequential=", sequential), func(t *testing.T) {
			var e *EPUB
			if sequential {
				opens := 0
				e, err = OpenSequentialEPUB(context.Background(), sequentialFixture(raw, &opens))
			} else {
				e, err = OpenEPUB(context.Background(), bytes.NewReader(raw), int64(len(raw)))
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(e.Resources) != len(entries) {
				t.Fatal("enumeration omitted original assets", e.Resources)
			}
			for _, entry := range e.Resources {
				original := ""
				for _, source := range entries {
					if source[0] == entry.Path {
						original = source[1]
					}
				}
				got, mime, err := e.Asset(context.Background(), entry.Path)
				if err != nil || mime != entry.MIME || int64(len(got)) != entry.Size || string(got) != original {
					t.Fatalf("asset %s changed or failed: mime=%s size=%d/%d err=%v", entry.Path, mime, len(got), entry.Size, err)
				}
			}
		})
	}
}

func TestCSSRejectsExternalActiveAmbiguousAndUndeclaredReferences(t *testing.T) {
	assets := map[string]string{"EPUB/Styles/import.css": "text/css", "EPUB/Images/page.png": "image/png", "EPUB/Fonts/font.ttf": "font/ttf", "EPUB/chapter.xhtml": "application/xhtml+xml"}
	for _, css := range []string{
		`@import "https://origin.invalid/private.css";`,
		`@import url(//origin.invalid/private.css);`,
		`@import "../Images/page.png";`,
		`@namespace url("https://origin.invalid/namespace");`,
		`@namespace epub "https://origin.invalid/namespace"`,
		`@namespace 2epub "urn:publication";`,
		`@namespace e/**/pub "urn:publication";`,
		`@namespace epub "urn:publication" extra;`,
		`p { @namespace epub "urn:publication"; }`,
		`p { color: red; } @namespace epub "urn:publication";`,
		`@namespace "urn:publication"; @import "import.css";`,
		`@namespace "</style>";`,
		`p { background: url(https://origin.invalid/private); }`,
		`p { background: url(//origin.invalid/private); }`,
		`p { background: url(/EPUB/Images/page.png); }`,
		`p { background: url(../../../secret.png); }`,
		`p { background: url(../Images/missing.png); }`,
		`p { background: url(../chapter.xhtml); }`,
		`p { background: url(../Images/page.png?secret=token); }`,
		`p { background: url(data:image/png;base64,AA); }`,
		`p { background: u\72l(../Images/page.png); }`,
		`p { background: u/**/rl(https://origin.invalid); }`,
		`p { width: expression(alert(1)); }`,
		`p { behavior: url(../Images/page.png); }`,
		`p { -moz-binding: url(../Images/page.png); }`,
		`p { background: image-set("https://origin.invalid/page.png" 1x); }`,
		`p { background: image("https://origin.invalid/page.png"); }`,
		`p { background: src("https://origin.invalid/page.png"); }`,
		`p { background: url("../Images/page.png" "https://origin.invalid"); }`,
		`p { background: url(../Images/page.png/*comment*/); }`,
		`p { background: url("../Images/page.png); }`,
		"p { content: \"unterminated\nstring\"; }",
		`p { color: red; /*unterminated`,
		`p { color: red;`,
	} {
		if err := validateCSS("EPUB/Styles/main.css", []byte(css), assets); err == nil {
			t.Fatalf("unsafe CSS accepted: %s", css)
		}
	}
	for _, body := range []string{
		`<html><style>p { background: u<!--split-->rl(https://origin.invalid/private); }</style></html>`,
		`<html><STYLE>@im<!--split-->port "https://origin.invalid/private.css";</STYLE></html>`,
		`<html><p style="background: url(https://origin.invalid/private)"/></html>`,
	} {
		if err := validateXHTML("EPUB/chapter.xhtml", []byte(body), assets); err == nil {
			t.Fatal("unsafe split or inline CSS accepted", body)
		}
	}
}

func TestCSSCommentsAndQuotedNamespacesAreInertIdentifiers(t *testing.T) {
	assets := map[string]string{"EPUB/Styles/import.css": "text/css"}
	for _, css := range []string{
		`/* comparisons < are inert; @import url(https://origin.invalid/ignored) */ p { color: red; }`,
		`@namespace epub 'http://www.idpf.org/2007/ops'; p { color: red; }`,
		`@namespace "https://origin.invalid/not-an-asset"; p { color: red; }`,
		`@namespace x "urn:publisher:non-fetching"; p { color: red; }`,
		`@namespace 日本語 "urn:publisher"; p { color: red; }`,
		`@charset "UTF-8"; @import "import.css" screen; @namespace epub "urn:publisher"; p { color: red; }`,
	} {
		original := []byte(css)
		if err := validateCSS("EPUB/Styles/main.css", original, assets); err != nil {
			t.Fatalf("inert CSS refused: %v", err)
		}
		if string(original) != css {
			t.Fatal("publisher bytes changed")
		}
	}
}
