package reading

import (
	"strings"
	"testing"
)

func TestXHTMLAnchorAcceptsOnlyInertAbsoluteHTTPLinks(t *testing.T) {
	assets := map[string]string{
		"EPUB/chapter.xhtml": "application/xhtml+xml",
		"EPUB/next.xhtml":    "application/xhtml+xml",
		"EPUB/cover.png":     "image/png",
	}
	const base = "EPUB/chapter.xhtml"
	anchor := func(attributes string) []byte {
		return []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body><p><a ` + attributes + `>link</a></p></body></html>`)
	}
	for _, attributes := range []string{
		`href="https://example.com/"`,
		`href="http://example.com/a/b?c=d#e"`,
		`href="HTTPS://EXAMPLE.COM/a"`,
		`href="#part"`,
		`href="next.xhtml#part"`,
		`href="cover.png"`,
	} {
		if err := validateXHTML(base, anchor(attributes), assets); err != nil {
			t.Fatalf("ordinary anchor refused: %s: %v", attributes, err)
		}
	}
	for _, attributes := range []string{
		`href="javascript:alert(1)"`,
		`href="data:text/html,x"`,
		`href="file:///etc/passwd"`,
		`href="ftp://example.com/x"`,
		`href="//example.com/x"`,
		`href="https://user:pass@example.com/x"`,
		`href="https:///x"`,
		`href="https://"`,
		`href=" https://example.com/"`,
		`href="https://example.com/a b"`,
		`href="https://example.com/&lt;"`,
		`href="https://example.com/` + strings.Repeat("a", maxInertAnchorLink) + `"`,
		`HREF="https://example.com/"`,
		`Href="https://example.com/"`,
		`hReF="https://example.com/"`,
		`href="missing.xhtml"`,
		`src="https://example.com/x"`,
		`href="https://example.com/x" src="https://example.com/x"`,
	} {
		if err := validateXHTML(base, anchor(attributes), assets); err == nil {
			t.Fatalf("unsafe anchor accepted: %s", attributes)
		}
	}
	for _, body := range [][]byte{
		// The element must be an XHTML anchor, not a same-named node in no
		// namespace or a foreign-prefixed anchor.
		[]byte(`<html><body><a href="https://example.com/">link</a></body></html>`),
		[]byte(`<html xmlns="http://www.w3.org/1999/xhtml" xmlns:xlink="http://www.w3.org/1999/xlink"><body><a xlink:href="https://example.com/">link</a></body></html>`),
		// A resource-bearing attribute never gains the anchor exemption.
		[]byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body><img src="https://example.com/x"/></body></html>`),
		[]byte(`<html xmlns="http://www.w3.org/1999/xhtml"><head><link rel="stylesheet" href="https://example.com/x.css"/></head><body/></html>`),
		[]byte(`<html xmlns="http://www.w3.org/1999/xhtml"><body><a src="https://example.com/x">link</a></body></html>`),
	} {
		if err := validateXHTML(base, body, assets); err == nil {
			t.Fatalf("unsafe resource reference accepted: %s", body)
		}
	}
}

func TestXHTMLStyleRefusesRawTextBreakout(t *testing.T) {
	assets := map[string]string{"EPUB/chapter.xhtml": "application/xhtml+xml"}
	const base = "EPUB/chapter.xhtml"
	doc := func(style string) []byte {
		return []byte(`<html xmlns="http://www.w3.org/1999/xhtml"><head><style>` + style + `</style></head><body><p>text</p></body></html>`)
	}
	if err := validateXHTML(base, doc(`p { color: red; }`), assets); err != nil {
		t.Fatalf("ordinary style refused: %v", err)
	}
	// The XML parser rejects direct markup; these sequences arrive only through
	// CDATA or character references and must still never become serialized
	// markup. A sequence split across character data is caught by the overlap.
	for _, style := range []string{
		`<![CDATA[/* </style> */]]>`,
		`<![CDATA[/* </STYLE > */]]>`,
		`<![CDATA[/* <!-- */]]>`,
		`<![CDATA[/* --> */]]>`,
		`/* &lt;/style&gt; */`,
		`/* &lt;/sty` + `le&gt; */`,
	} {
		if err := validateXHTML(base, doc(style), assets); err == nil {
			t.Fatalf("style raw-text breakout accepted: %s", style)
		}
	}
}
