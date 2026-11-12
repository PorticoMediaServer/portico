package reading

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/url"
	"path"
	"strings"
	"unicode"
)

// Part is one file inside an EPUB that the reader may ask for: a chapter, a picture, a style
// sheet or a font. Only the parts the book's own package document lists can be asked for.
type Part struct {
	Path string
	Type string
	Size int64
}

// Contents is one line of a book's table of contents. Part is in the spine; Fragment is the
// place inside it, if any. Depth 0 is the top level.
type Contents struct {
	Title    string
	Part     string
	Fragment string
	Depth    int
}

// epub is what the reader needs to know about an EPUB before it asks for any part of it.
type epub struct {
	parts     []Part
	byPath    map[string]Part
	spine     []string
	contents  []Contents
	direction string
}

const (
	maxContents     = 512
	maxContentDepth = 16
	maxTitle        = 1024
)

// readEPUB reads the container, the package document and the table of contents. A book with
// no package document or nothing in its spine is damaged; a table of contents that cannot be
// read is left out, and the book still reads.
func readEPUB(a *archive) (*epub, error) {
	raw, err := a.read("META-INF/container.xml", maxXMLSize)
	if err != nil {
		return nil, errDamaged
	}
	var container struct {
		Rootfiles []struct {
			FullPath  string `xml:"full-path,attr"`
			MediaType string `xml:"media-type,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := decode(raw, &container); err != nil {
		return nil, errDamaged
	}
	opfPath := ""
	for _, root := range container.Rootfiles {
		if root.MediaType == "" || root.MediaType == "application/oebps-package+xml" {
			opfPath = root.FullPath
			break
		}
	}
	if raw, err = a.read(opfPath, maxXMLSize); err != nil {
		return nil, errDamaged
	}
	var pkg struct {
		Manifest []struct {
			ID         string `xml:"id,attr"`
			Href       string `xml:"href,attr"`
			MediaType  string `xml:"media-type,attr"`
			Properties string `xml:"properties,attr"`
		} `xml:"manifest>item"`
		Spine struct {
			TOC       string `xml:"toc,attr"`
			Direction string `xml:"page-progression-direction,attr"`
			Items     []struct {
				IDRef string `xml:"idref,attr"`
			} `xml:"itemref"`
		} `xml:"spine"`
	}
	if err := decode(raw, &pkg); err != nil {
		return nil, errDamaged
	}
	book := &epub{byPath: map[string]Part{}}
	// The reader in the app reads the book as its EPUB engine does, from the container to the
	// package document, so those two are parts of the book too.
	for _, doc := range [][2]string{{"META-INF/container.xml", "application/xml"}, {opfPath, "application/oebps-package+xml"}} {
		if f := a.entries[doc[0]]; f != nil {
			part := Part{Path: doc[0], Type: doc[1], Size: int64(f.UncompressedSize64)}
			book.parts = append(book.parts, part)
			book.byPath[doc[0]] = part
		}
	}
	byID := map[string]string{}
	nav, ncx := "", ""
	for _, item := range pkg.Manifest {
		name, _, ok := resolve(path.Dir(opfPath), item.Href)
		f := a.entries[name]
		if !ok || f == nil || f.UncompressedSize64 > maxPartSize {
			continue
		}
		if _, twice := book.byPath[name]; twice {
			continue
		}
		part := Part{Path: name, Type: mediaType(item.MediaType, name), Size: int64(f.UncompressedSize64)}
		book.parts = append(book.parts, part)
		book.byPath[name] = part
		byID[item.ID] = name
		if hasWord(item.Properties, "nav") {
			nav = name
		}
		if item.ID == pkg.Spine.TOC {
			ncx = name
		}
	}
	for _, ref := range pkg.Spine.Items {
		if name, ok := byID[ref.IDRef]; ok {
			book.spine = append(book.spine, name)
		}
	}
	if len(book.spine) == 0 {
		return nil, errDamaged
	}
	if pkg.Spine.Direction == "rtl" {
		book.direction = "rtl"
	}
	inSpine := map[string]bool{}
	for _, name := range book.spine {
		inSpine[name] = true
	}
	if nav != "" {
		book.contents = navContents(a, nav, inSpine)
	}
	if len(book.contents) == 0 && ncx != "" {
		book.contents = ncxContents(a, ncx, inSpine)
	}
	return book, nil
}

// decode parses an XML document as leniently as books need: HTML entities such as &nbsp; are
// known, and a declared character set other than UTF-8 is read as UTF-8. No document type,
// entity definition or outside reference is ever followed.
//
// The container, the package document and ComicInfo are XML, not HTML: an element such as <meta>
// is closed by its own end tag, as EPUB 3 requires of <meta property="dcterms:modified">, so the
// HTML rule that closes <meta> at once is not applied to them.
func decode(raw []byte, into any) error {
	d := newDecoder(raw)
	d.AutoClose = nil
	return d.Decode(into)
}

func newDecoder(raw []byte) *xml.Decoder {
	d := xml.NewDecoder(bytes.NewReader(raw))
	d.Strict, d.Entity, d.AutoClose = false, xml.HTMLEntity, xml.HTMLAutoClose
	d.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	return d
}

// resolve turns an href written in a document at dir into an entry name and a fragment. An
// href that leaves the archive, names another site or is not a plain path is refused.
func resolve(dir, href string) (name, fragment string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" || u.Path == "" || strings.HasPrefix(u.Path, "/") {
		return "", "", false
	}
	joined := path.Join(dir, u.Path)
	if strings.HasPrefix(joined, "../") || joined == ".." || !safeName(joined) {
		return "", "", false
	}
	return joined, u.Fragment, true
}

func hasWord(list, word string) bool {
	for _, w := range strings.Fields(list) {
		if w == word {
			return true
		}
	}
	return false
}

// mediaType is the type a part is served as: the one the book declares when it is a plain
// type, otherwise one from the name's extension.
func mediaType(declared, name string) string {
	declared = strings.ToLower(strings.TrimSpace(declared))
	if t, _, _ := strings.Cut(declared, ";"); plainType(t) {
		return t
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".xhtml", ".html", ".htm":
		return "application/xhtml+xml"
	case ".css":
		return "text/css"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	}
	return "application/octet-stream"
}

func plainType(t string) bool {
	main, sub, ok := strings.Cut(t, "/")
	if !ok || main == "" || sub == "" || len(t) > 100 {
		return false
	}
	for _, c := range t {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("/.+-", c)) {
			return false
		}
	}
	return true
}

// cleanTitle makes a contents line's text one line of plain words, or "" when there are none.
func cleanTitle(s string) string {
	s = strings.Join(strings.FieldsFunc(s, func(c rune) bool { return unicode.IsSpace(c) || unicode.IsControl(c) }), " ")
	if len([]rune(s)) > maxTitle {
		s = string([]rune(s)[:maxTitle])
	}
	return s
}

// navContents reads an EPUB 3 navigation document's table of contents: the nav marked as the
// toc, its nested lists giving the depth.
func navContents(a *archive, name string, inSpine map[string]bool) []Contents {
	raw, err := a.read(name, maxXMLSize)
	if err != nil {
		return nil
	}
	d := newDecoder(raw)
	var out []Contents
	inTOC, lists := false, 0
	var link *Contents
	var text strings.Builder
	for len(out) < maxContents {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "nav":
				for _, attr := range t.Attr {
					if attr.Name.Local == "type" && hasWord(attr.Value, "toc") {
						inTOC, lists = true, 0
					}
				}
			case "ol", "ul":
				lists++
			case "a":
				if !inTOC {
					continue
				}
				for _, attr := range t.Attr {
					if attr.Name.Local != "href" {
						continue
					}
					if part, fragment, ok := resolve(path.Dir(name), attr.Value); ok && inSpine[part] {
						link = &Contents{Part: part, Fragment: fragment, Depth: min(max(lists-1, 0), maxContentDepth)}
						text.Reset()
					}
				}
			}
		case xml.CharData:
			if link != nil && text.Len() < 4*maxTitle {
				text.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "a":
				if link != nil {
					if link.Title = cleanTitle(text.String()); link.Title != "" {
						out = append(out, *link)
					}
					link = nil
				}
			case "ol", "ul":
				lists = max(lists-1, 0)
			case "nav":
				if inTOC {
					return out
				}
			}
		}
	}
	return out
}

// ncxContents reads an EPUB 2 NCX table of contents: nested navPoints, each with a label and
// the place it points to.
func ncxContents(a *archive, name string, inSpine map[string]bool) []Contents {
	raw, err := a.read(name, maxXMLSize)
	if err != nil {
		return nil
	}
	d := newDecoder(raw)
	var out []Contents
	depth := 0
	type open struct{ title string }
	var stack []*open
	inText := false
	for len(out) < maxContents {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "navPoint":
				depth++
				stack = append(stack, &open{})
			case "text":
				inText = len(stack) > 0
			case "content":
				if len(stack) == 0 {
					continue
				}
				for _, attr := range t.Attr {
					if attr.Name.Local == "src" {
						if part, fragment, ok := resolve(path.Dir(name), attr.Value); ok && inSpine[part] {
							if title := cleanTitle(stack[len(stack)-1].title); title != "" {
								out = append(out, Contents{Title: title, Part: part, Fragment: fragment, Depth: min(max(depth-1, 0), maxContentDepth)})
							}
						}
					}
				}
			}
		case xml.CharData:
			if inText && len(stack[len(stack)-1].title) < 4*maxTitle {
				stack[len(stack)-1].title += string(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "text":
				inText = false
			case "navPoint":
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
				depth = max(depth-1, 0)
			}
		}
	}
	return out
}
