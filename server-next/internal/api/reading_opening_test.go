package api

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestEPUBOpeningLinesAreScannedAndRead(t *testing.T) {
	first := "At dawn the harbour lamps were still shining above the quiet water while Mara carried her father's old map along the empty quay and listened for the bells beyond the island."
	second := "She had promised to return before the fishing boats left, but the folded letter in her pocket gave her a reason to follow the narrow road toward the abandoned lighthouse instead."
	want := first + " " + second
	prose := "<p>" + strings.Replace(first, "harbour", "<em>harbour</em>", 1) + " " + second +
		" This final sentence belongs to the story but stays outside the excerpt because the preceding sentences already give the reader enough words to begin.</p>"
	front := "<p>" + strings.Repeat("Front matter belongs before the story and must never become its opening. ", 6) + "</p>"
	for _, c := range []struct {
		name, guide, nav, before, chapter, expected string
	}{
		{"body hints", `<guide><reference type="text" href="chapter.xhtml#story"/></guide>`,
			`<nav epub:type="landmarks"><a epub:type="bodymatter" href="chapter.xhtml#story">Begin</a></nav>`, front,
			front + `<section id="story">` + prose + `</section>`, want},
		{"paragraph fallback", `<guide><reference type="copyright-page" href="front.xhtml"/></guide>`, "", front,
			`<section epub:type="copyright-page">` + front + `</section><section epub:type="dedication">` + front + `</section><p>Chapter one.</p>` + prose, want},
		{"image only", "", "", "<img src='cover.png'/>", "<p></p><img src='page.png'/>", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := newBookShelf(t)
			wrap := func(body string) string {
				return `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><body>` + body + `</body></html>`
			}
			aside := front
			if c.expected == "" {
				aside = ""
			}
			pkg := fmt.Sprintf(`<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
				<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Opening</dc:title></metadata>
				<manifest><item id="front" href="front.xhtml" media-type="application/xhtml+xml"/>
				<item id="aside" href="aside.xhtml" media-type="application/xhtml+xml"/>
				<item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml"/>
				<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/></manifest>
				<spine><itemref idref="front"/><itemref idref="aside" linear="no"/><itemref idref="chapter"/></spine>%s</package>`, c.guide)
			zipFile(t, filepath.Join(b.books, "Opening.epub"),
				[2]string{"mimetype", "application/epub+zip"},
				[2]string{"META-INF/container.xml", `<container><rootfiles><rootfile full-path="book.opf"/></rootfiles></container>`},
				[2]string{"book.opf", pkg}, [2]string{"nav.xhtml", wrap(c.nav)},
				[2]string{"front.xhtml", wrap(c.before)}, [2]string{"aside.xhtml", wrap(aside)},
				[2]string{"chapter.xhtml", wrap(c.chapter)})
			b.scan()
			id := b.find(db.KindBook, "Opening")
			var answer BookAnswer
			w := call(t, b.s, "GET", "/v1/items/"+id+"/book", b.owner.AccessToken, nil, &answer)
			if w.Code != 200 || answer.OpeningLines != c.expected {
				t.Fatalf("opening lines %q, want %q (HTTP %d)", answer.OpeningLines, c.expected, w.Code)
			}
			if c.expected == "" && strings.Contains(w.Body.String(), `"openingLines"`) {
				t.Fatal("an image-only book published opening lines")
			}
			if c.expected != "" && (len(strings.Fields(answer.OpeningLines)) < 50 || len(strings.Fields(answer.OpeningLines)) > 70) {
				t.Fatal("opening is outside fifty to seventy words")
			}
			if item := get[Item](t, b.s, b.owner.AccessToken, "/v1/items/"+id); item.WordCount == nil {
				t.Fatal("extracting the opening lost the scan's word count")
			}
		})
	}
}
