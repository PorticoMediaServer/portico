package reading

import (
	"bytes"
	"context"
	"testing"
)

// RD-1: an EPUB whose manifest contains a legal SVG image item (Standard
// Ebooks/Calibre covers) must open. Today openEPUBArchive refuses the whole
// book because allowedMIME has no image/svg+xml.
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

// RD-1: an XHTML spine document with inline SVG (Calibre's titlepage) is legal
// EPUB content. validateXHTML rejects it, so the client receives 400 for the
// first chapter of a very common book layout.
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
