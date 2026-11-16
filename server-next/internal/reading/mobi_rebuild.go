package reading

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/text/encoding/charmap"
)

// How a MOBI or AZW3 is rebuilt as an EPUB: see mobi.go.

// mobiBuilder rebuilds one book.
type mobiBuilder struct {
	p        *palm
	h        *mobiHeader
	parts    []string
	docs     map[string][]byte
	pictures map[string]int
	byRecord map[int]string
	flows    map[int]string
	contents []mobiContents
	guide    [][3]string // the book's landmarks: type, title, place
	cover    string
}

type mobiContents struct {
	title, href string
	depth       int
}

// convertMobi rebuilds a MOBI or AZW3 as an EPUB. A book with both kinds of text is read from
// its newer, KF8, half.
func convertMobi(r io.ReaderAt, size int64) (*mobiBook, error) {
	p, err := openPalm(r, size)
	if err != nil {
		return nil, err
	}
	h, err := readMobiHeader(p, 0)
	if err != nil {
		return nil, err
	}
	if h.encryption != 0 {
		return nil, ErrLocked
	}
	if boundary, ok := h.number(121); ok && h.version < 8 && boundary > 0 && boundary < p.count() {
		if kf8, err := readMobiHeader(p, boundary); err == nil && kf8.version >= 8 {
			h = kf8
		}
	}
	text, err := h.text(p)
	if err != nil {
		return nil, err
	}
	b := &mobiBuilder{p: p, h: h, docs: map[string][]byte{}, pictures: map[string]int{}, byRecord: map[int]string{}, flows: map[int]string{}}
	if n, ok := h.number(201); ok {
		b.cover = b.picture(n)
	}
	if h.version >= 8 {
		err = b.kf8(text)
	} else {
		err = b.mobi6(text)
	}
	if err != nil {
		return nil, err
	}
	if len(b.parts) == 0 {
		return nil, errDamaged
	}
	return b.finish(), nil
}

// picture names the n-th resource (from 0) when it is a picture, reading only its first bytes.
func (b *mobiBuilder) picture(n int) string {
	rec := b.h.firstResource + n
	if b.h.firstResource < 0 || n < 0 || rec >= b.p.count() {
		return ""
	}
	if name, ok := b.byRecord[rec]; ok {
		return name
	}
	b.byRecord[rec] = ""
	head, err := b.p.record(rec, 8, true)
	if err != nil {
		return ""
	}
	ext := ""
	switch {
	case bytes.HasPrefix(head, []byte{0xff, 0xd8, 0xff}):
		ext = "jpg"
	case bytes.HasPrefix(head, []byte("\x89PNG")):
		ext = "png"
	case bytes.HasPrefix(head, []byte("GIF8")):
		ext = "gif"
	default:
		return ""
	}
	name := fmt.Sprintf("image%05d.%s", n+1, ext)
	b.byRecord[rec], b.pictures[name] = name, rec
	return name
}

var (
	kindlePos   = regexp.MustCompile(`kindle:pos:fid:([0-9A-Va-v]{4}):off:([0-9A-Va-v]{10})`)
	kindleEmbed = regexp.MustCompile(`kindle:embed:([0-9A-Va-v]{4})(\?mime=[A-Za-z0-9/+.-]*)?`)
	kindleFlow  = regexp.MustCompile(`kindle:flow:([0-9A-Va-v]{4})(\?mime=([A-Za-z0-9/+.-]*))?`)
	anyTag      = regexp.MustCompile(`<[^<>]*>`)
	idAttribute = regexp.MustCompile(`(?i)^<[^>]*\s(?:id|name)\s*=\s*['"]([^'"]*)['"]`)
)

func base32(s string) int {
	n, err := strconv.ParseInt(s, 32, 32)
	if err != nil {
		return -1
	}
	return int(n)
}

// kf8 puts each XHTML file back together: its skeleton with its fragments inserted in order.
// Links to places (kindle:pos), pictures (kindle:embed) and style sheets (kindle:flow) become
// the rebuilt book's own names.
func (b *mobiBuilder) kf8(text []byte) error {
	flows := [][]byte{text}
	if b.h.fdst >= 0 {
		rec, err := b.p.record(b.h.fdst, maxMobiRecord, false)
		if err != nil || len(rec) < 12 || string(rec[:4]) != "FDST" {
			return errDamaged
		}
		start, n := int(binary.BigEndian.Uint32(rec[4:])), int(binary.BigEndian.Uint32(rec[8:]))
		if n < 1 || n > 4096 || start < 12 || start+8*n > len(rec) {
			return errDamaged
		}
		flows = nil
		for i := range n {
			from, to := int(binary.BigEndian.Uint32(rec[start+8*i:])), int(binary.BigEndian.Uint32(rec[start+8*i+4:]))
			if from > to || to > len(text) {
				return errDamaged
			}
			flows = append(flows, text[from:to])
		}
	}
	if b.h.skeleton < 0 || b.h.frag < 0 {
		return errDamaged
	}
	skeletons, _, err := readIndex(b.p, b.h.skeleton)
	if err != nil {
		return err
	}
	fragEntries, _, err := readIndex(b.p, b.h.frag)
	if err != nil {
		return err
	}
	type fragment struct{ insert, length int }
	frags := make([]fragment, len(fragEntries))
	for i, e := range fragEntries {
		insert, err := strconv.Atoi(e.name)
		if err != nil || len(e.tags[6]) < 2 {
			return errDamaged
		}
		frags[i] = fragment{insert, e.tags[6][1]}
	}
	type file struct {
		start, end int
		doc        []byte
		anchors    []anchor
	}
	main, files, next := flows[0], []file{}, 0
	for _, s := range skeletons {
		if len(s.tags[1]) < 1 || len(s.tags[6]) < 2 {
			return errDamaged
		}
		count, start, length := s.tags[1][0], s.tags[6][0], s.tags[6][1]
		if start < 0 || length < 0 || start+length > len(main) {
			return errDamaged
		}
		doc, at := slices.Clone(main[start:start+length]), start+length
		for range count {
			if next >= len(frags) {
				return errDamaged
			}
			f := frags[next]
			next++
			insert := f.insert - start
			if insert < 0 || insert > len(doc) || f.length < 0 || at+f.length > len(main) {
				return errDamaged
			}
			doc = slices.Insert(doc, insert, main[at:at+f.length]...)
			at += f.length
		}
		files = append(files, file{start: start, end: at, doc: doc, anchors: anchors(doc)})
		b.parts = append(b.parts, fmt.Sprintf("part%04d.xhtml", len(files)-1))
	}
	// target names a position in the text: the part it is in and the nearest id before it.
	target := func(pos int) string {
		i := sort.Search(len(files), func(i int) bool { return files[i].end > pos })
		if i == len(files) || pos < files[i].start {
			return ""
		}
		href := b.parts[i]
		if id := anchorAt(files[i].doc, files[i].anchors, pos-files[i].start); id != "" {
			href += "#" + id
		}
		return href
	}
	posFid := func(fid, off int) string {
		if fid < 0 || fid >= len(frags) || off < 0 {
			return ""
		}
		return target(frags[fid].insert + off)
	}
	links := func(doc []byte) []byte {
		doc = kindlePos.ReplaceAllFunc(doc, func(m []byte) []byte {
			s := kindlePos.FindSubmatch(m)
			return []byte(posFid(base32(string(s[1])), base32(string(s[2]))))
		})
		doc = kindleEmbed.ReplaceAllFunc(doc, func(m []byte) []byte {
			return []byte(b.picture(base32(string(kindleEmbed.FindSubmatch(m)[1])) - 1))
		})
		return kindleFlow.ReplaceAllFunc(doc, func(m []byte) []byte {
			s := kindleFlow.FindSubmatch(m)
			n := base32(string(s[1]))
			if n <= 0 || n >= len(flows) {
				return nil
			}
			if b.flows[n] == "" {
				ext := "css"
				if strings.Contains(string(s[3]), "svg") {
					ext = "svg"
				}
				b.flows[n] = fmt.Sprintf("flow%04d.%s", n, ext)
			}
			return []byte(b.flows[n])
		})
	}
	for i, f := range files {
		b.docs[b.parts[i]] = links(f.doc)
	}
	for n, name := range b.flows {
		b.docs[name] = kindleEmbed.ReplaceAllFunc(flows[n], func(m []byte) []byte {
			return []byte(b.picture(base32(string(kindleEmbed.FindSubmatch(m)[1])) - 1))
		})
	}
	if b.h.ncx >= 0 {
		entries, strs, err := readIndex(b.p, b.h.ncx)
		if err == nil {
			for _, e := range entries {
				href := ""
				if v := e.tags[6]; len(v) >= 2 {
					href = posFid(v[0], v[1])
				} else if v := e.tags[1]; len(v) >= 1 {
					href = target(v[0])
				}
				b.addContents(e, strs, href, false)
			}
		}
	}
	// The guide's landmarks ("text" is where the book itself starts) name a fragment.
	if b.h.guide >= 0 {
		entries, strs, err := readIndex(b.p, b.h.guide)
		for _, e := range entries {
			fid := append(e.tags[6], e.tags[3]...)
			if err == nil && len(fid) > 0 && len(e.tags[1]) > 0 {
				if href := posFid(fid[0], 0); href != "" {
					b.guide = append(b.guide, [3]string{e.name, strs[e.tags[1][0]], href})
				}
			}
		}
	}
	return nil
}

func (b *mobiBuilder) addContents(e mobiEntry, strs map[int]string, href string, latin1 bool) {
	if href == "" || len(e.tags[3]) == 0 || len(b.contents) >= maxContents*4 {
		return
	}
	title := strs[e.tags[3][0]]
	if latin1 {
		title = windows1252(title)
	}
	depth := 0
	if v := e.tags[4]; len(v) > 0 {
		depth = min(v[0], maxContentDepth)
	}
	b.contents = append(b.contents, mobiContents{title: title, href: href, depth: depth})
}

// anchor is a tag that can be linked to: one with an id or name, or the body.
type anchor struct {
	at   int
	id   string
	body bool
}

func anchors(doc []byte) []anchor {
	var out []anchor
	for _, m := range anyTag.FindAllIndex(doc, -1) {
		tag := doc[m[0]:m[1]]
		switch {
		case bytes.HasPrefix(tag, []byte("<body")):
			out = append(out, anchor{at: m[0], body: true})
		case bytes.HasPrefix(tag, []byte("<meta")):
		default:
			if id := idAttribute.FindSubmatch(tag); id != nil {
				out = append(out, anchor{at: m[0], id: string(id[1])})
			}
		}
	}
	return out
}

// anchorAt is the id of the nearest tag at or before a place in a file, counting a tag the
// place is inside of; none when the body starts after it, which is the top of the file.
func anchorAt(doc []byte, list []anchor, at int) string {
	at = min(max(at, 0), len(doc))
	if gt, lt := bytes.IndexByte(doc[at:], '>'), bytes.IndexByte(doc[at:], '<'); lt == 0 || gt >= 0 && (lt < 0 || gt < lt) {
		at += gt + 1
	}
	i := sort.Search(len(list), func(i int) bool { return list[i].at >= at })
	if i == 0 || list[i-1].body {
		return ""
	}
	return list[i-1].id
}

var (
	pageBreak = regexp.MustCompile(`(?i)<mbp:pagebreak[^>]*>`)
	reference = regexp.MustCompile(`(?i)<reference\s[^>]*>`)
	filepos   = regexp.MustCompile(`(?i)filepos=['"]?0*([0-9]+)`)
	xmlName   = regexp.MustCompile(`^[a-z_][a-z0-9._-]*$`)
)

// mobi6 splits an older MOBI's one HTML text into parts at its page breaks. A link to a place
// (filepos) gets an anchor at that place; each part is parsed as HTML and written as XHTML.
func (b *mobiBuilder) mobi6(text []byte) error {
	latin1 := b.h.encoding == 1252
	places := map[int]bool{}
	for _, m := range filepos.FindAllSubmatch(text, maxMobiEntries) {
		if n, err := strconv.Atoi(string(m[1])); err == nil && n < len(text) {
			places[n] = true
		}
	}
	// The guide's landmarks, at places in the text: "text" is where the book itself starts.
	type landmark struct {
		kind, title string
		place       int
	}
	var landmarks []landmark
	for _, m := range reference.FindAll(text, 64) {
		kind, title, place := referenceAttribute(m, "type"), referenceAttribute(m, "title"), referenceAttribute(m, "filepos")
		if n, err := strconv.Atoi(place); err == nil && n < len(text) && kind != "" {
			if latin1 {
				title = windows1252(title)
			}
			landmarks = append(landmarks, landmark{kind, title, n})
			places[n] = true
		}
	}
	var contents []mobiEntry
	var strs map[int]string
	if b.h.ncx >= 0 {
		contents, strs, _ = readIndex(b.p, b.h.ncx)
		for _, e := range contents {
			if v := e.tags[1]; len(v) > 0 && v[0] < len(text) {
				places[v[0]] = true
			}
		}
	}
	// The parts: the text between page breaks.
	var bounds [][2]int
	at := 0
	for _, m := range pageBreak.FindAllIndex(text, -1) {
		bounds = append(bounds, [2]int{at, m[0]})
		at = m[1]
	}
	bounds = append(bounds, [2]int{at, len(text)})
	sorted := make([]int, 0, len(places))
	for n := range places {
		sorted = append(sorted, n)
	}
	sort.Ints(sorted)
	var roots []*html.Node
	for k, span := range bounds {
		// Anchors go in from the end, so the places before them stay where they were. A place
		// inside the page break before this part starts it.
		segment, after := slices.Clone(text[span[0]:span[1]]), 0
		if k > 0 {
			after = bounds[k-1][1]
		}
		from, to := sort.SearchInts(sorted, after), sort.SearchInts(sorted, span[1])
		for k := to - 1; k >= from; k-- {
			pos := max(sorted[k]-span[0], 0)
			if lt, gt := bytes.LastIndexByte(segment[:pos], '<'), bytes.LastIndexByte(segment[:pos], '>'); lt > gt {
				if end := bytes.IndexByte(segment[pos:], '>'); end >= 0 {
					pos += end + 1
				}
			}
			segment = slices.Insert(segment, pos, fmt.Appendf(nil, `<span id="filepos%d"></span>`, sorted[k])...)
		}
		source := string(segment)
		if latin1 {
			source = windows1252(source)
		}
		root, err := html.Parse(strings.NewReader(xmlText(source)))
		if err != nil {
			return errDamaged
		}
		roots = append(roots, root)
	}
	// A part with nothing to see (the text between two breaks is often only tags) is left out;
	// its places belong to the next part kept, or the last one.
	kept, partFor := []*html.Node{}, make([]int, len(roots))
	for k, root := range roots {
		partFor[k] = len(kept)
		if visible(root) {
			kept = append(kept, root)
			b.parts = append(b.parts, fmt.Sprintf("part%04d.xhtml", len(kept)-1))
		}
	}
	if len(kept) == 0 {
		return errDamaged
	}
	partOf := func(pos int) string {
		k := min(sort.Search(len(bounds), func(k int) bool { return bounds[k][1] > pos }), len(bounds)-1)
		return b.parts[min(partFor[k], len(kept)-1)]
	}
	for i, root := range kept {
		b.rewrite(root, partOf)
		doc, err := xhtml(root)
		if err != nil {
			return err
		}
		b.docs[b.parts[i]] = doc
	}
	for _, e := range contents {
		if v := e.tags[1]; len(v) > 0 {
			b.addContents(e, strs, fmt.Sprintf("%s#filepos%d", partOf(v[0]), v[0]), latin1)
		}
	}
	for _, l := range landmarks {
		b.guide = append(b.guide, [3]string{l.kind, l.title, fmt.Sprintf("%s#filepos%d", partOf(l.place), l.place)})
	}
	return nil
}

// referenceAttribute is one attribute of an older MOBI's guide reference, quoted or not.
func referenceAttribute(tag []byte, name string) string {
	m := regexp.MustCompile(`(?i)\s` + name + `\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s/>]+))`).FindSubmatch(tag)
	if m == nil {
		return ""
	}
	return strings.TrimLeft(string(m[1])+string(m[2])+string(m[3]), " ")
}

// rewrite turns an older MOBI's links and pictures into the rebuilt book's, and leaves out
// what XHTML cannot hold: scripts, comments, and names it does not allow.
func (b *mobiBuilder) rewrite(n *html.Node, partOf func(int) string) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch {
		case c.Type == html.CommentNode || c.Type == html.DoctypeNode,
			c.Type == html.ElementNode && (c.Data == "script" || c.Data == "guide"):
			n.RemoveChild(c)
		case c.Type == html.ElementNode && !xmlName.MatchString(c.Data):
			// mbp:nu and the like: their contents stay, in their place.
			b.rewrite(c, partOf)
			for c.FirstChild != nil {
				child := c.FirstChild
				c.RemoveChild(child)
				n.InsertBefore(child, c)
			}
			n.RemoveChild(c)
		case c.Type == html.ElementNode:
			attrs := c.Attr[:0]
			for _, a := range c.Attr {
				switch {
				case a.Namespace != "" || !xmlName.MatchString(a.Key):
				case a.Key == "filepos":
					if pos, err := strconv.Atoi(strings.TrimSpace(a.Val)); err == nil {
						attrs = append(attrs, html.Attribute{Key: "href", Val: fmt.Sprintf("%s#filepos%d", partOf(pos), pos)})
					}
				case a.Key == "recindex" && c.Data == "img":
					if n, err := strconv.Atoi(a.Val); err == nil {
						if name := b.picture(n - 1); name != "" {
							attrs = append(attrs, html.Attribute{Key: "src", Val: name})
						}
					}
				default:
					attrs = append(attrs, a)
				}
			}
			c.Attr = attrs
			b.rewrite(c, partOf)
		}
		c = next
	}
}

// visible reports whether a parsed part has any words or pictures.
func visible(n *html.Node) bool {
	if n.Type == html.TextNode && strings.TrimSpace(n.Data) != "" || n.Type == html.ElementNode && n.Data == "img" {
		return true
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if visible(c) {
			return true
		}
	}
	return false
}

// xhtml writes a parsed HTML document as XHTML.
func xhtml(root *html.Node) ([]byte, error) {
	var page *html.Node
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "html" {
			page = c
		}
	}
	if page == nil {
		return nil, errDamaged
	}
	page.Attr = append([]html.Attribute{{Key: "xmlns", Val: "http://www.w3.org/1999/xhtml"}}, page.Attr...)
	var out bytes.Buffer
	out.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	if err := html.Render(&out, page); err != nil {
		return nil, errDamaged
	}
	return out.Bytes(), nil
}

// xmlText leaves out the characters XML may not contain.
func xmlText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || r >= 0x20 && r != utf8.RuneError && r != 0xfffe && r != 0xffff {
			return r
		}
		return -1
	}, strings.ToValidUTF8(s, ""))
}

func windows1252(s string) string {
	out, err := charmap.Windows1252.NewDecoder().String(s)
	if err != nil {
		return s
	}
	return out
}

// finish writes the rebuilt book's container, package document and contents.
func (b *mobiBuilder) finish() *mobiBook {
	book := &mobiBook{docs: b.docs, pictures: b.pictures}
	esc := func(s string) string {
		var out strings.Builder
		xml.EscapeText(&out, []byte(xmlText(s)))
		return out.String()
	}
	text := func(kind uint32) []string {
		var out []string
		for _, v := range b.h.exth[kind] {
			s := string(v)
			if b.h.encoding == 1252 {
				s = windows1252(s)
			}
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	var opf strings.Builder
	opf.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n" + `<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="id">` +
		`<metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">`)
	title := append(text(503), b.h.title)[0]
	identifier := "urn:portico:mobi"
	if isbn := text(104); len(isbn) > 0 {
		identifier = "urn:isbn:" + isbn[0]
	}
	fmt.Fprintf(&opf, `<dc:title>%s</dc:title><dc:identifier id="id">%s</dc:identifier>`, esc(title), esc(identifier))
	for _, author := range text(100) {
		fmt.Fprintf(&opf, `<dc:creator opf:role="aut">%s</dc:creator>`, esc(author))
	}
	for kind, element := range map[uint32]string{101: "publisher", 103: "description", 106: "date", 524: "language"} {
		if v := text(kind); len(v) > 0 {
			fmt.Fprintf(&opf, `<dc:%s>%s</dc:%s>`, element, esc(v[0]), element)
		}
	}
	for _, subject := range text(105) {
		fmt.Fprintf(&opf, `<dc:subject>%s</dc:subject>`, esc(subject))
	}
	if b.cover != "" {
		opf.WriteString(`<meta name="cover" content="cover"/>`)
	}
	opf.WriteString(`</metadata><manifest><item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>`)
	book.names = []string{"META-INF/container.xml", "content.opf", "toc.ncx"}
	for i, name := range b.parts {
		fmt.Fprintf(&opf, `<item id="p%d" href="%s" media-type="application/xhtml+xml"/>`, i, name)
	}
	book.names = append(book.names, b.parts...)
	var others []string
	for _, name := range b.flows {
		others = append(others, name)
	}
	for name := range b.pictures {
		others = append(others, name)
	}
	sort.Strings(others)
	for i, name := range others {
		id := fmt.Sprintf("r%d", i)
		if name == b.cover {
			id = "cover"
		}
		fmt.Fprintf(&opf, `<item id="%s" href="%s" media-type="%s"/>`, id, name, mediaType("", name))
	}
	book.names = append(book.names, others...)
	opf.WriteString(`</manifest><spine toc="ncx">`)
	for i := range b.parts {
		fmt.Fprintf(&opf, `<itemref idref="p%d"/>`, i)
	}
	opf.WriteString(`</spine>`)
	if len(b.guide) > 0 {
		opf.WriteString(`<guide>`)
		for _, g := range b.guide {
			fmt.Fprintf(&opf, `<reference type="%s" title="%s" href="%s"/>`, esc(g[0]), esc(g[1]), esc(g[2]))
		}
		opf.WriteString(`</guide>`)
	}
	opf.WriteString(`</package>`)

	var ncx strings.Builder
	fmt.Fprintf(&ncx, `<?xml version="1.0" encoding="utf-8"?>`+"\n"+`<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><head/><docTitle><text>%s</text></docTitle><navMap>`, esc(title))
	open := 0
	for i, c := range b.contents {
		depth := min(c.depth, open)
		for ; open > depth; open-- {
			ncx.WriteString(`</navPoint>`)
		}
		fmt.Fprintf(&ncx, `<navPoint id="n%d" playOrder="%d"><navLabel><text>%s</text></navLabel><content src="%s"/>`, i, i+1, esc(c.title), esc(c.href))
		open++
	}
	for ; open > 0; open-- {
		ncx.WriteString(`</navPoint>`)
	}
	ncx.WriteString(`</navMap></ncx>`)
	b.docs["META-INF/container.xml"] = []byte(`<?xml version="1.0" encoding="utf-8"?>` + "\n" +
		`<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles>` +
		`<rootfile full-path="content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)
	b.docs["content.opf"] = []byte(opf.String())
	b.docs["toc.ncx"] = []byte(ncx.String())
	return book
}
