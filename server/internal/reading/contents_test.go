package reading

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

func contentsEPUB(t *testing.T, modern bool, navigation string) *EPUB {
	t.Helper()
	manifest, spine, name := `<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>`, ``, "EPUB/nav.xhtml"
	if !modern {
		manifest = `<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>`
		spine = ` toc="ncx"`
		name = "EPUB/toc.ncx"
	}
	raw := fixtureZIP(t, [][2]string{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", `<container><rootfiles><rootfile full-path="EPUB/package.opf"/></rootfiles></container>`},
		{"EPUB/package.opf", `<package><metadata/><manifest>` + manifest + `<item id="one" href="one.xhtml" media-type="application/xhtml+xml"/><item id="two" href="two.xhtml" media-type="application/xhtml+xml"/></manifest><spine` + spine + `><itemref idref="one"/><itemref idref="two"/></spine></package>`},
		{name, navigation}, {"EPUB/one.xhtml", `<html><body><p id="opening">One.</p></body></html>`}, {"EPUB/two.xhtml", `<html><body><p id="end">Two.</p></body></html>`},
	})
	e, err := OpenEPUB(context.Background(), bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func navDocument(body string) string {
	return `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Real contents</title></head><body><nav epub:type="toc"><ol>` + body + `</ol></nav></body></html>`
}

func TestEPUBContentsRetainsPublisherOrderNestingLabelsAndInternalTargets(t *testing.T) {
	nav := navDocument(`<li><a href="two.xhtml#end"><span>Part</span> <em>two</em></a><ol><li><a href="one.xhtml#opening">Opening</a></li></ol></li>`)
	entries, err := contentsEPUB(t, true, nav).Contents(context.Background())
	if err != nil || len(entries) != 2 {
		t.Fatalf("contents %+v %v", entries, err)
	}
	if entries[0] != (ContentsEntry{Title: "Part two", Section: "EPUB/two.xhtml", Fragment: "end", Depth: 0}) || entries[1] != (ContentsEntry{Title: "Opening", Section: "EPUB/one.xhtml", Fragment: "opening", Depth: 1}) {
		t.Fatalf("publisher navigation altered: %+v", entries)
	}
	ncx := `<ncx><navMap><navPoint><navLabel><text>Second first</text></navLabel><content src="two.xhtml#end"/><navPoint><navLabel><text>First child</text></navLabel><content src="one.xhtml"/></navPoint></navPoint></navMap></ncx>`
	entries, err = contentsEPUB(t, false, ncx).Contents(context.Background())
	if err != nil || len(entries) != 2 || entries[0].Title != "Second first" || entries[1].Depth != 1 {
		t.Fatalf("NCX: %+v %v", entries, err)
	}
}

func TestEPUBContentsWithholdsUnsafeAmbiguousOrOverBudgetDescriptors(t *testing.T) {
	for _, href := range []string{"https://private.invalid/one.xhtml", "//private.invalid/one.xhtml", "../../../private.xhtml", "one.xhtml?token=secret", "missing.xhtml", "package.opf", "one.xhtml#bad%0Afragment"} {
		t.Run(href, func(t *testing.T) {
			entries, err := contentsEPUB(t, true, navDocument(`<li><a href="one.xhtml">Valid first</a></li><li><a href="`+href+`">Unsafe</a></li>`)).Contents(context.Background())
			if err != nil || len(entries) != 0 {
				t.Fatalf("partial or unsafe descriptor: %+v %v", entries, err)
			}
		})
	}
	for _, doc := range []string{
		navDocument(`<li><a href="one.xhtml">` + strings.Repeat("x", 1025) + `</a></li>`),
		navDocument(strings.Repeat(`<li><a href="one.xhtml">Real</a></li>`, 513)),
		navDocument(`<li><a href="one.xhtml"></a></li>`),
		navDocument(`<li><a href="one.xhtml">One</a></li>`) + `<extra/>`,
		`<!DOCTYPE html [<!ENTITY text SYSTEM "file:///private">]>` + navDocument(`<li><a href="one.xhtml">&text;</a></li>`),
		navDocument(`<li><a href="one.xhtml">One</a></li>` + strings.Repeat(`<ol><li>`, 18) + strings.Repeat(`</li></ol>`, 18)),
	} {
		entries, err := contentsEPUB(t, true, doc).Contents(context.Background())
		if err != nil || len(entries) != 0 {
			t.Fatalf("invalid descriptor: %+v %v", entries, err)
		}
	}
}

func TestEPUBContentsDoesNotInventNavigationFromSpine(t *testing.T) {
	for _, modern := range []bool{false, true} {
		doc := `<ncx><navMap/></ncx>`
		if modern {
			doc = `<html><body><nav><ol><li><a href="one.xhtml">Unspecified</a></li></ol></nav></body></html>`
		}
		entries, err := contentsEPUB(t, modern, doc).Contents(context.Background())
		if err != nil || len(entries) != 0 {
			t.Fatalf("invented contents: %+v %v", entries, err)
		}
	}
	// The entire descriptor fits the public bound, rather than truncating at it.
	var body strings.Builder
	for i := 0; i < 512; i++ {
		fmt.Fprintf(&body, `<li><a href="one.xhtml#%d">Chapter %d</a></li>`, i, i)
	}
	entries, err := contentsEPUB(t, true, navDocument(body.String())).Contents(context.Background())
	if err != nil || len(entries) != 512 {
		t.Fatalf("boundary %+v %v", len(entries), err)
	}
}

func TestEPUBContentsNCXWithholdsTheWholeAmbiguousHierarchy(t *testing.T) {
	valid := `<navPoint><navLabel><text>Real first</text></navLabel><content src="one.xhtml"/></navPoint>`
	for _, doc := range []string{
		`<ncx><navMap>` + valid + `</navMap><navMap>` + valid + `</navMap></ncx>`,
		`<ncx><navMap>` + valid + `</navMap>` + valid + `</ncx>`,
		`<ncx><navMap>` + valid + `<navPoint><navLabel><text>Ambiguous</text></navLabel><content src="one.xhtml"/><content src="two.xhtml"/></navPoint></navMap></ncx>`,
		`<ncx><navMap>` + valid + `<navPoint><navLabel><text>One</text></navLabel><navLabel><text>Two</text></navLabel><content src="two.xhtml"/></navPoint></navMap></ncx>`,
		`<ncx><navMap>` + valid + `<wrapper>` + valid + `</wrapper></navMap></ncx>`,
	} {
		entries, err := contentsEPUB(t, false, doc).Contents(context.Background())
		if err != nil || len(entries) != 0 {
			t.Fatalf("partial ambiguous NCX leaked: %+v %v", entries, err)
		}
	}
}
