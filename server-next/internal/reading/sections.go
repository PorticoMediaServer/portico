package reading

import (
	"context"
	"encoding/xml"
	"io"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Section is one document of an EPUB's reading order as search sees it: its place in the
// spine, its path in the archive and its text, normalised the way the reader normalises it
// to place a search hit (reader-core indexed-text.ts, anchor xhtml-body-blockspace-fields-v1),
// so an offset into Text is the reader's offset too.
type Section struct {
	Index int
	Path  string
	Text  string
}

// maxSectionText is the most UTF-16 units one section's text may have, as the reader allows.
const maxSectionText = 8 << 20

// maxSectionIndex is the highest place in the reading order a hit is sent for.
const maxSectionIndex = 4095

// sectionBlocks are the elements with one space before and after them: the reader's list.
var sectionBlocks = map[string]bool{}

func init() {
	for _, tag := range []string{"address", "article", "aside", "blockquote", "br", "dd", "div", "dl", "dt", "figcaption", "figure",
		"footer", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "li", "main", "nav", "ol", "p", "pre", "section", "table",
		"td", "th", "tr", "ul"} {
		sectionBlocks[tag] = true
	}
}

// EPUBSections reads the text of each document in an EPUB's reading order, once each, with
// its spine index. A document that is not an html page with a body, or is too large, is left out.
func EPUBSections(r io.ReaderAt, size int64) ([]Section, error) {
	a, err := ebookArchive(r, size)
	if err != nil {
		return nil, err
	}
	return epubSections(a)
}

// BookSections reads the sections of a book's file as its reader opens it: an EPUB as it is,
// a MOBI or AZW3 as the EPUB it is rebuilt into. Any other kind of book has none. A MOBI is
// rebuilt here without the reader's cache of rebuilt books, so reading a library for search
// never pushes out the book someone has open.
func (s *Service) BookSections(ctx context.Context, f db.BookTextFile) ([]Section, error) {
	v := db.BookVersion{FileID: f.FileID, FileName: f.FileName, Size: f.Size, ModifiedNS: f.ModifiedNS}
	format := VersionFormat(v)
	if format != FormatEPUB && !mobiFormat(format) {
		return nil, nil
	}
	file, place, err := s.openFile(ctx, v.FileID)
	if err != nil {
		return nil, err
	}
	defer file.close()
	if place.Size != v.Size || place.ModifiedNS != v.ModifiedNS {
		return nil, ErrChanged
	}
	var a *archive
	if mobiFormat(format) {
		book, err := convertMobi(file.at, file.size)
		if err != nil {
			return nil, file.explain(err)
		}
		a, err = book.archive(file.at, file.size)
	} else {
		a, err = openBookArchive(ctx, file, format)
	}
	if err != nil {
		return nil, file.explain(err)
	}
	defer a.close()
	sections, err := epubSections(a)
	return sections, file.explain(err)
}

func epubSections(a *archive) ([]Section, error) {
	book, err := readEPUB(a)
	if err != nil {
		return nil, err
	}
	var out []Section
	seen, total := map[string]bool{}, 0
	for index, name := range book.spine {
		if seen[name] || index > maxSectionIndex {
			continue
		}
		seen[name] = true
		raw, err := a.read(name, maxPartSize)
		if err != nil {
			return nil, err
		}
		if total += len(raw); total > 64<<20 {
			return nil, errDamaged
		}
		if text, ok := sectionText(raw); ok && text != "" {
			out = append(out, Section{Index: index, Path: name, Text: text})
		}
	}
	return out, nil
}

// sectionText is a document's body text: text and CDATA in document order, script, style
// and template left out (by exact name), one ASCII space around each block element, then
// every run of white space (Go's unicode.IsSpace) made one space and the ends trimmed. The
// reader declines to place hits in a document with a doctype or a foreign element; search
// still finds text there, and such a hit opens the section without a highlight.
func sectionText(raw []byte) (string, bool) {
	d := newDecoder(raw)
	var b strings.Builder
	depth, body, skip := 0, 0, 0
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", false
		}
		switch t := token.(type) {
		case xml.StartElement:
			depth++
			switch {
			case depth == 1 && t.Name.Local != "html":
				return "", false
			case depth == 2 && t.Name.Local == "body" && body == 0:
				body = depth
			case body > 0 && skip == 0:
				if t.Name.Local == "script" || t.Name.Local == "style" || t.Name.Local == "template" {
					skip = depth
				} else if sectionBlocks[t.Name.Local] {
					b.WriteByte(' ')
				}
			}
		case xml.EndElement:
			switch {
			case skip == depth:
				skip = 0
			case body == depth:
				body = -1
			case body > 0 && skip == 0 && sectionBlocks[t.Name.Local]:
				b.WriteByte(' ')
			}
			depth--
		case xml.CharData:
			if body > 0 && skip == 0 {
				b.Write(t)
			}
		}
		if b.Len() > 4*maxSectionText {
			return "", false
		}
	}
	text := collapseSpace(b.String())
	if utf16Len(text) > maxSectionText {
		return "", false
	}
	return text, true
}

// utf16Len is the length of s in UTF-16 units, the unit every offset sent to readers uses.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// collapseSpace makes every run of white space one space and trims both ends.
func collapseSpace(s string) string {
	var b strings.Builder
	pending := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			pending = true
			continue
		}
		if pending && b.Len() > 0 {
			b.WriteByte(' ')
		}
		pending = false
		b.WriteRune(r)
	}
	return b.String()
}
