package reading

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
)

// RD-1: an EPUB with a passive SVG manifest item must open.
func TestRDEPUBWithSVGManifestItemOpens(t *testing.T) {
	body := `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>c</title></head><body><p>Hello world.</p></body></html>`
	raw := fixtureZIP(t, [][2]string{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"OEBPS/content.opf", `<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">urn:uuid:test</dc:identifier><dc:title>SVG cover</dc:title><dc:language>en</dc:language></metadata><manifest><item id="c" href="chapter.xhtml" media-type="application/xhtml+xml"/><item id="cover" href="cover.svg" media-type="image/svg+xml"/></manifest><spine><itemref idref="c"/></spine></package>`},
		{"OEBPS/chapter.xhtml", body},
		{"OEBPS/cover.svg", `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10"/></svg>`},
	})
	if _, err := OpenEPUB(context.Background(), bytes.NewReader(raw), int64(len(raw))); err != nil {
		t.Fatalf("OpenEPUB refused a standards-compliant EPUB with an image/svg+xml manifest item: %v", err)
	}
}

// RD-1: Calibre title pages embed a declared raster cover inside inline SVG.
func TestRDValidateXHTMLAcceptsInlineSVG(t *testing.T) {
	assets := map[string]string{"OEBPS/cover.jpg": "image/jpeg"}
	doc := []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><head><title>t</title></head><body><div><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 600 800" preserveAspectRatio="xMidYMid meet"><image width="600" height="800" xlink:href="cover.jpg" xmlns:xlink="http://www.w3.org/1999/xlink"/></svg></div></body></html>`)
	if err := validateXHTML("OEBPS/titlepage.xhtml", doc, assets); err != nil {
		t.Fatalf("validateXHTML refused a standards-compliant inline-SVG title page: %v", err)
	}
}

func TestEPUBSVGValidationPreservesPassiveDrawingAndLocalImages(t *testing.T) {
	assets := map[string]string{"OEBPS/cover.svg": "image/svg+xml", "OEBPS/cover.jpg": "image/jpeg"}
	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 600 800"><defs><linearGradient id="paint"><stop offset="0" stop-color="white"/></linearGradient><path id="shape" d="M0 0L10 10"/></defs><style>rect { fill: url(#paint); }</style><rect width="600" height="800" fill="url(#paint)"/><use href="#shape"/><image width="600" height="800" xlink:href="cover.jpg"/></svg>`)
	if err := validateSVG("OEBPS/cover.svg", svg, assets); err != nil {
		t.Fatal(err)
	}
	inline := append([]byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body>`), svg[len(`<?xml version="1.0"?>`):]...)
	inline = append(inline, []byte(`</body></html>`)...)
	if err := validateXHTML("OEBPS/title.xhtml", inline, assets); err != nil {
		t.Fatal(err)
	}
	if err := validateCSS("OEBPS/main.css", []byte(`p { background: url(cover.svg); }`), assets); err != nil {
		t.Fatal(err)
	}
}

func TestEPUBSVGRejectsActiveExternalAndAmbiguousContent(t *testing.T) {
	assets := map[string]string{"OEBPS/cover.svg": "image/svg+xml", "OEBPS/cover.jpg": "image/jpeg"}
	for _, content := range []string{
		`<script>alert(1)</script>`,
		`<foreignObject><html xmlns="http://www.w3.org/1999/xhtml"><script>alert(1)</script></html></foreignObject>`,
		`<animate attributeName="href" values="https://example.invalid/secret"/>`,
		`<image href="https://example.invalid/secret"/>`,
		`<image href="java&#x73;cript:alert(1)"/>`,
		`<image href="../missing.jpg"/>`,
		`<use href="https://example.invalid/secret"/>`,
		`<g onload="alert(1)"/>`,
		`<g xml:base="https://example.invalid/"/>`,
		`<rect fill="url(https://example.invalid/secret)"/>`,
		`<rect style="fill:u\72l(https://example.invalid/secret)"/>`,
		`<style>rect { fill: url(https://example.invalid/secret); }</style>`,
		`<style><![CDATA[</style><script>alert(1)</script>]]></style>`,
		`<g xmlns="http://www.w3.org/1999/xhtml"><image src="https://example.invalid/secret"/></g>`,
		`<?xml-stylesheet href="https://example.invalid/style"?>`,
	} {
		t.Run(content, func(t *testing.T) {
			svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg">` + content + `</svg>`)
			if err := validateSVG("OEBPS/cover.svg", svg, assets); err == nil {
				t.Fatal("unsafe SVG member accepted")
			}
			inline := append([]byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body>`), svg...)
			inline = append(inline, []byte(`</body></html>`)...)
			if err := validateXHTML("OEBPS/title.xhtml", inline, assets); err == nil {
				t.Fatal("unsafe inline SVG accepted")
			}
		})
	}
	for _, svg := range []string{"", `<svg xmlns="http://www.w3.org/2000/svg"/><svg xmlns="http://www.w3.org/2000/svg"/>`, `<!DOCTYPE svg SYSTEM "https://example.invalid/svg.dtd"><svg xmlns="http://www.w3.org/2000/svg"/>`} {
		if err := validateSVG("OEBPS/cover.svg", []byte(svg), assets); err == nil {
			t.Fatalf("ambiguous SVG accepted: %s", svg)
		}
	}
}

func TestEPUBSVGAssetValidatesBeforeReturningPublisherBytes(t *testing.T) {
	for _, active := range []bool{false, true} {
		body := `<svg xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10"/></svg>`
		if active {
			body = `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`
		}
		raw := fixtureZIP(t, [][2]string{{"cover.svg", body}})
		a, err := openArchive(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			t.Fatal(err)
		}
		e := &EPUB{archive: a, allowed: map[string]string{"cover.svg": "image/svg+xml"}}
		data, mime, err := e.Asset(context.Background(), "cover.svg")
		if active {
			if err == nil || len(data) != 0 {
				t.Fatal("active SVG returned bytes")
			}
		} else if err != nil || mime != "image/svg+xml" || string(data) != body {
			t.Fatalf("publisher SVG changed or failed: %s %s %v", data, mime, err)
		}
	}
}

func TestEPUBSVGRejectsEveryUnlistedAttribute(t *testing.T) {
	for _, attrs := range []string{`unreviewed-attribute="value"`, `attributeName="href" to="javascript:alert(1)"`, `contentScriptType="application/ecmascript"`, `xml:id="not-an-admitted-SVG-attribute"`, `xmlns:unknown="urn:unknown" unknown:attribute="value"`, `d="M0 0"`} {
		body := `<svg xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10" ` + attrs + `/></svg>`
		for _, inline := range []bool{false, true} {
			b := []byte(body)
			var err error
			if inline {
				b = []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body>` + body + `</body></html>`)
				err = validateXHTML("EPUB/page.xhtml", b, nil)
			} else {
				err = validateSVG("EPUB/page.svg", b, nil)
			}
			if err == nil {
				t.Errorf("inline=%v accepted unlisted attributes: %s", inline, attrs)
			}
		}
	}
}

func TestEPUBSVGImageReferencesAreOnlyDeclaredRasterMembers(t *testing.T) {
	assets := map[string]string{"EPUB/cover.jpg": "image/jpeg", "EPUB/other.svg": "image/svg+xml", "EPUB/chapter.xhtml": "application/xhtml+xml"}
	for _, target := range []string{"other.svg", "chapter.xhtml", "cover.jpg#fragment", "cover.jpg?query", "cover.jpg#", "cover.jpg?", "cover.jpg%23fragment", "cover.jpg%3Fquery", "data:image/png;base64,AA", "https://example.invalid/cover.jpg", "missing.jpg"} {
		body := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><image href="` + target + `"/></svg>`)
		if err := validateSVG("EPUB/page.svg", body, assets); err == nil {
			t.Errorf("image accepted %s", target)
		}
	}
	for _, body := range []string{`<use href="other.svg#shape"/>`, `<textPath href="other.svg#shape"/>`, `<feImage href="cover.jpg"/>`, `<style type="text/javascript">alert(1)</style>`} {
		if err := validateSVG("EPUB/page.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg">`+body+`</svg>`), assets); err == nil {
			t.Errorf("accepted forbidden element/reference: %s", body)
		}
	}
}

// Unmodified Standard Ebooks titlepage, CC0-1.0, pinned to its source commit;
// see the adjacent fixture license for provenance. The EPUB wrapper is original.
func TestEPUBSVGRealPublisherTitlepageStillOpensAndKeepsItsBytes(t *testing.T) {
	svg, err := os.ReadFile("testdata/standardebooks-pride-and-prejudice-titlepage.svg")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(svg)) != "7260ee39d5081af8087d68cb6d4b7b89968d36ad9a7586b78d3dbdd91993fe8d" {
		t.Fatal("publisher fixture changed")
	}
	raw := fixtureZIP(t, [][2]string{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="EPUB/package.opf"/></rootfiles></container>`},
		{"EPUB/package.opf", `<package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Publisher SVG fixture</dc:title></metadata><manifest><item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/><item id="titlepage" href="titlepage.svg" media-type="image/svg+xml"/></manifest><spine><itemref idref="chapter"/></spine></package>`},
		{"EPUB/chapter.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><body><img src="titlepage.svg"/></body></html>`},
		{"EPUB/titlepage.svg", string(svg)},
	})
	e, err := OpenEPUB(context.Background(), bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	got, mime, err := e.Asset(context.Background(), "EPUB/titlepage.svg")
	if err != nil || mime != "image/svg+xml" || !bytes.Equal(got, svg) {
		t.Fatalf("publisher SVG failed or changed: %s %v", mime, err)
	}
}
