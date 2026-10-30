package reading

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/url"
	"path"
	"sort"
	"strings"
)

type Resource struct {
	Path string
	MIME string
	Size int64
}
type Metadata struct {
	WordCount, PageCount, EstimatedSeconds                                *int64
	SeriesLibraryID, OpeningLines                                         string
	Series                                                                *SeriesValue
	Title, Author, Language, Direction, CoverURL, ExplicitPublicationType string
	PublicationTypeField                                                  string
	PublicationTypeRaw                                                    []string
	PublicationTypeComplete                                               bool
}
type EPUB struct {
	CoverResource string
	Series        string
	SeriesNumber  string
	archive       *archive
	Resources     []Resource
	Metadata      Metadata
	Package       string
	Spine         []string
	allowed       map[string]string
	navigation    []string
	ncx           string
}

// epubMeta is one OPF meta declaration. EPUB 3 carries property text and
// refinements; EPUB 2 and Calibre carry name/content attributes.
type epubMeta struct {
	ID       string `xml:"id,attr"`
	Name     string `xml:"name,attr"`
	Content  string `xml:"content,attr"`
	Property string `xml:"property,attr"`
	Refines  string `xml:"refines,attr"`
	Value    string `xml:",chardata"`
	Inner    string `xml:",innerxml"`
}

type containerXML struct {
	Roots []struct {
		Path string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}
type packageXML struct {
	XMLName  xml.Name
	Metadata struct {
		XMLName  xml.Name
		Title    string        `xml:"title"`
		Creators []epubCreator `xml:"http://purl.org/dc/elements/1.1/ creator"`
		Language string        `xml:"language"`
		Meta     []epubMeta    `xml:"http://www.idpf.org/2007/opf meta"`
		Types    []struct {
			Value string `xml:",chardata"`
			Inner string `xml:",innerxml"`
		} `xml:"http://purl.org/dc/elements/1.1/ type"`
	} `xml:"metadata"`
	Manifest []struct {
		ID         string `xml:"id,attr"`
		Href       string `xml:"href,attr"`
		MIME       string `xml:"media-type,attr"`
		Properties string `xml:"properties,attr"`
	} `xml:"manifest>item"`
	Spine struct {
		Direction string `xml:"page-progression-direction,attr"`
		TOC       string `xml:"toc,attr"`
		Items     []struct {
			ID string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

func decodeXML(b []byte, out any) error {
	// No external entity, DTD or processing-instruction execution is permitted.
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = true
	if err := d.Decode(out); err != nil {
		return ErrInvalid
	}
	for {
		t, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return ErrInvalid
		}
		if c, ok := t.(xml.CharData); !ok || len(bytes.TrimSpace(c)) != 0 {
			return ErrInvalid
		}
	}
}

// epubCreator keeps the exact package value, its raw inner XML and its optional
// relator role.
type epubCreator struct {
	Value string `xml:",chardata"`
	Inner string `xml:",innerxml"`
	Role  string `xml:"role,attr"`
}

// readingCreator returns the package's one exact creator from the Dublin Core
// namespace. An EPUB may carry several dc:creator values, and a creator may
// carry a relator role. Only a single exact author value is accepted; a
// different role (an editor, say), a nested value that would hide further
// names, or more than one distinct author publishes no author rather than
// choosing one.
func readingCreator(values []epubCreator) string {
	if len(values) > 64 {
		return ""
	}
	exact := ""
	for _, creator := range values {
		// Chardata drops markup while innerxml keeps it. Declining the whole
		// set keeps a nested name from being silently discarded.
		if strings.Contains(creator.Inner, "<") {
			return ""
		}
		role := strings.TrimSpace(creator.Role)
		if role != "" && !strings.EqualFold(role, "aut") && !strings.EqualFold(role, "author") {
			continue
		}
		value := strings.TrimSpace(creator.Value)
		if value == "" {
			continue
		}
		if exact != "" && value != exact {
			return ""
		}
		exact = value
	}
	return exact
}

// epubSeries keeps only one explicit series declaration. An EPUB 3 collection
// counts only when its collection-type is explicitly "series"; a generic
// belongs-to-collection is never inferred to be one. The common Calibre series
// meta is an explicit series field. Conflicting or nested declarations decline
// the affected fact rather than choosing another declaration, and every
// conflict flag is durable, so a later duplicate cannot resurrect a value.
func epubSeries(p packageXML) (string, string) {
	type declaration struct {
		name             string
		typeSeen         bool
		typedSeries      bool
		typeConflict     bool
		position         string
		positionSeen     bool
		positionConflict bool
	}
	declarations := []declaration{}
	byID := map[string]int{}
	seriesConflict := false
	for _, meta := range p.Metadata.Meta {
		if meta.Property != "belongs-to-collection" {
			continue
		}
		name := strings.TrimSpace(meta.Value)
		if strings.Contains(meta.Inner, "<") || name == "" {
			// Not a scalar declaration. The series fact is unreliable rather
			// than merely absent.
			seriesConflict = true
			continue
		}
		if meta.ID != "" {
			if _, exists := byID[meta.ID]; exists {
				// One refinement id cannot reliably name two declarations.
				seriesConflict = true
				continue
			}
			byID[meta.ID] = len(declarations)
		}
		declarations = append(declarations, declaration{name: name})
	}
	calibreSeries, calibreIndex := "", ""
	calibreSeriesSeen, calibreIndexSeen := false, false
	calibreSeriesConflict, calibreIndexConflict := false, false
	for _, meta := range p.Metadata.Meta {
		if meta.Property == "collection-type" || meta.Property == "group-position" {
			id := strings.TrimPrefix(meta.Refines, "#")
			index, ok := byID[id]
			if id == meta.Refines || !ok {
				continue
			}
			declaration := &declarations[index]
			if strings.Contains(meta.Inner, "<") {
				if meta.Property == "collection-type" {
					declaration.typeConflict = true
				} else {
					declaration.positionConflict = true
				}
				continue
			}
			value := strings.TrimSpace(meta.Value)
			if value == "" {
				continue
			}
			if meta.Property == "collection-type" {
				if declaration.typeConflict {
					continue
				}
				series := strings.EqualFold(value, "series")
				if declaration.typeSeen && declaration.typedSeries != series {
					declaration.typeConflict = true
					continue
				}
				declaration.typeSeen, declaration.typedSeries = true, series
				continue
			}
			if declaration.positionConflict {
				continue
			}
			if declaration.positionSeen && declaration.position != value {
				declaration.positionConflict = true
				continue
			}
			declaration.position, declaration.positionSeen = value, true
			continue
		}
		value := strings.TrimSpace(meta.Content)
		switch meta.Name {
		case "calibre:series":
			if calibreSeriesSeen && calibreSeries != value {
				calibreSeriesConflict = true
			}
			calibreSeries, calibreSeriesSeen = value, true
		case "calibre:series_index":
			if calibreIndexSeen && calibreIndex != value {
				calibreIndexConflict = true
			}
			calibreIndex, calibreIndexSeen = value, true
		}
	}
	candidates := map[string]bool{}
	positions, positionSeen, positionConflict := map[string]string{}, map[string]bool{}, map[string]bool{}
	for _, declaration := range declarations {
		if declaration.typeConflict {
			seriesConflict = true
			continue
		}
		if !declaration.typedSeries {
			continue
		}
		candidates[declaration.name] = true
		if declaration.positionConflict {
			positionConflict[declaration.name] = true
			continue
		}
		if !declaration.positionSeen {
			continue
		}
		if positionSeen[declaration.name] && positions[declaration.name] != declaration.position {
			positionConflict[declaration.name] = true
			continue
		}
		positions[declaration.name], positionSeen[declaration.name] = declaration.position, true
	}
	if seriesConflict || len(candidates) > 1 {
		return "", ""
	}
	if len(candidates) == 1 {
		var series, number string
		for name := range candidates {
			series = name
			if !positionConflict[name] && positionSeen[name] {
				number = positions[name]
			}
		}
		if calibreSeriesSeen {
			if calibreSeriesConflict || calibreSeries != series {
				return "", ""
			}
		}
		if positionConflict[series] || calibreIndexConflict {
			return series, ""
		}
		if calibreSeriesSeen && calibreIndexSeen && calibreIndex != "" {
			if number == "" {
				number = calibreIndex
			} else if calibreIndex != number {
				return series, ""
			}
		}
		return series, number
	}
	if calibreSeriesConflict || !calibreSeriesSeen || calibreSeries == "" {
		return "", ""
	}
	if calibreIndexConflict {
		return calibreSeries, ""
	}
	return calibreSeries, calibreIndex
}

func OpenEPUB(ctx context.Context, r io.ReaderAt, size int64) (*EPUB, error) {
	a, err := openArchive(r, size)
	if err != nil {
		return nil, err
	}
	return openEPUBArchive(ctx, a)
}
func OpenSequentialEPUB(ctx context.Context, source SequentialSource) (*EPUB, error) {
	a, err := openSequentialArchive(ctx, source)
	if err != nil {
		return nil, err
	}
	return openEPUBArchive(ctx, a)
}
func openEPUBArchive(ctx context.Context, a *archive) (*EPUB, error) {
	if a.files["META-INF/rights.xml"] != nil {
		return nil, ErrInvalid
	}
	mime, err := a.read(ctx, "mimetype", 64)
	if err != nil {
		return nil, err
	}
	if string(mime) != "application/epub+zip" {
		return nil, ErrInvalid
	}
	b, err := a.read(ctx, "META-INF/container.xml", 256<<10)
	if err != nil {
		return nil, err
	}
	var c containerXML
	if err = decodeXML(b, &c); err != nil || len(c.Roots) != 1 || !safePath(c.Roots[0].Path) {
		return nil, ErrInvalid
	}
	pkg := c.Roots[0].Path
	b, err = a.read(ctx, pkg, 1<<20)
	if err != nil {
		return nil, err
	}
	var p packageXML
	if err = decodeXML(b, &p); err != nil || len(p.Manifest) == 0 || len(p.Spine.Items) == 0 {
		return nil, ErrInvalid
	}
	if len(p.Manifest) > MaxEntries || len(p.Spine.Items) > MaxEntries {
		return nil, ErrLimit
	}
	e := &EPUB{archive: a, Package: pkg, Metadata: Metadata{Title: p.Metadata.Title, Author: readingCreator(p.Metadata.Creators), Language: p.Metadata.Language, PublicationTypeField: "{http://purl.org/dc/elements/1.1/}type"}, allowed: map[string]string{"mimetype": "text/plain", "META-INF/container.xml": "application/xml", pkg: "application/oebps-package+xml"}}
	if p.XMLName.Local == "package" && p.XMLName.Space == "http://www.idpf.org/2007/opf" && p.Metadata.XMLName.Local == "metadata" && p.Metadata.XMLName.Space == p.XMLName.Space {
		values := make([]string, 0, len(p.Metadata.Types))
		scalar := true
		for _, field := range p.Metadata.Types {
			values = append(values, field.Value)
			scalar = scalar && !strings.Contains(field.Inner, "<")
		}
		e.Metadata.PublicationTypeRaw, e.Metadata.PublicationTypeComplete = boundedTypeEvidence(values)
		e.Metadata.PublicationTypeComplete = e.Metadata.PublicationTypeComplete && scalar
		if e.Metadata.PublicationTypeComplete {
			e.Metadata.ExplicitPublicationType = explicitPublicationType(values)
		}
	}
	if len(e.Metadata.Title) > 4096 || len(e.Metadata.Author) > 4096 || len(e.Metadata.Language) > 64 {
		return nil, ErrLimit
	}
	if p.Spine.Direction == "rtl" || p.Spine.Direction == "ltr" {
		e.Metadata.Direction = p.Spine.Direction
	}
	ids := map[string]string{}
	for _, item := range p.Manifest {
		name, err := resolveReference(pkg, item.Href)
		if err != nil || ids[item.ID] != "" || item.ID == "" || a.files[name] == nil {
			return nil, ErrInvalid
		}
		if !allowedMIME(item.MIME) {
			return nil, ErrInvalid
		}
		if _, exists := e.allowed[name]; exists {
			return nil, ErrInvalid
		}
		if strings.Contains(item.Properties, "scripted") || strings.Contains(item.Properties, "remote-resources") {
			return nil, ErrInvalid
		}
		ids[item.ID] = name
		e.allowed[name] = item.MIME
		for _, property := range strings.Fields(item.Properties) {
			if property == "nav" && item.MIME == "application/xhtml+xml" {
				e.navigation = append(e.navigation, name)
			}
		}
	}
	e.CoverResource = epubCover(p, ids, e.allowed)
	e.Series, e.SeriesNumber = epubSeries(p)
	for _, item := range p.Spine.Items {
		name := ids[item.ID]
		if name == "" || e.allowed[name] != "application/xhtml+xml" {
			return nil, ErrInvalid
		}
		e.Spine = append(e.Spine, name)
	}
	if name := ids[p.Spine.TOC]; name != "" && e.allowed[name] == "application/x-dtbncx+xml" {
		e.ncx = name
	}
	if a.files["META-INF/encryption.xml"] != nil {
		b, err = a.read(ctx, "META-INF/encryption.xml", 256<<10)
		if err != nil {
			return nil, err
		}
		if err = validateFontObfuscation(b, e.allowed); err != nil {
			return nil, err
		}
		e.allowed["META-INF/encryption.xml"] = "application/xml"
	}
	for name, mime := range e.allowed {
		e.Resources = append(e.Resources, Resource{name, mime, int64(a.files[name].UncompressedSize64)})
	}
	sort.Slice(e.Resources, func(i, j int) bool { return e.Resources[i].Path < e.Resources[j].Path })
	return e, nil
}

func allowedMIME(mime string) bool {
	switch mime {
	case "application/xhtml+xml", "text/css", "image/jpeg", "image/png", "image/webp", "font/otf", "font/ttf", "font/woff", "font/woff2", "application/font-sfnt", "application/vnd.ms-opentype", "application/x-dtbncx+xml":
		return true
	}
	return false
}

func (e *EPUB) Asset(ctx context.Context, name string) ([]byte, string, error) {
	mime, ok := e.allowed[name]
	if !ok {
		return nil, "", ErrInvalid
	}
	b, err := e.archive.read(ctx, name, MaxMember)
	if err != nil {
		return nil, "", err
	}
	switch mime {
	case "application/xhtml+xml":
		err = validateXHTML(name, b, e.allowed)
	case "text/css":
		err = validateCSS(name, b, e.allowed)
	case "image/jpeg", "image/png", "image/webp":
		var info Image
		info, err = inspectImage(b)
		if err == nil && info.MIME != mime {
			err = ErrInvalid
		}
	}
	if err != nil {
		return nil, "", err
	}
	return b, mime, nil
}

var htmlTags = strings.Fields("html head title meta link body div span p br hr h1 h2 h3 h4 h5 h6 a img picture source figure figcaption section article aside header footer main nav address blockquote q cite abbr em strong i b u s small sub sup mark ruby rt rp time pre code kbd samp var ol ul li dl dt dd table caption colgroup col thead tbody tfoot tr th td style")

func validateXHTML(base string, b []byte, assets map[string]string) error {
	allowed := map[string]bool{}
	for _, tag := range htmlTags {
		allowed[tag] = true
	}
	d := xml.NewDecoder(bytes.NewReader(b))
	depth := 0
	inStyle := false
	var style []byte
	for {
		token, err := d.Token()
		if err == io.EOF {
			if depth != 0 {
				return ErrInvalid
			}
			return nil
		}
		if err != nil {
			return ErrInvalid
		}
		switch t := token.(type) {
		case xml.StartElement:
			depth++
			tag := strings.ToLower(t.Name.Local)
			if inStyle || depth > 128 || !allowed[tag] || (t.Name.Space != "" && t.Name.Space != "http://www.w3.org/1999/xhtml") {
				return ErrInvalid
			}
			if tag == "style" {
				inStyle = true
				style = style[:0]
			}
			for _, a := range t.Attr {
				name := strings.ToLower(a.Name.Local)
				value := strings.TrimSpace(a.Value)
				if name == "srcset" {
					if t.Name.Local != "img" || t.Name.Space != "http://www.w3.org/1999/xhtml" || a.Name.Local != "srcset" || a.Name.Space != "" {
						return ErrInvalid
					}
					if err := validateSrcset(base, a.Value, assets); err != nil {
						return err
					}
					continue
				}
				if strings.HasPrefix(name, "on") || name == "base" || name == "srcdoc" || name == "srcset" || name == "http-equiv" || name == "ping" || name == "action" || name == "formaction" || name == "background" {
					return ErrInvalid
				}
				if name == "style" {
					if err := validateCSS(base, []byte(value), assets); err != nil {
						return err
					}
				}
				// An <a href> may carry the publication's own ordinary absolute
				// HTTP(S) address as inert data. It is never resolved as an
				// archive asset, fetched, rewritten or turned into a link by the
				// server. Only an unprefixed anchor in the XHTML namespace gets
				// this exemption: src, poster, srcset, link/import and every
				// other resource-bearing attribute keep their existing rule,
				// and the shared web sanitizer plus each native renderer must
				// deactivate the attribute before it enters a document.
				if a.Name.Local == "href" && t.Name.Local == "a" && t.Name.Space == "http://www.w3.org/1999/xhtml" && a.Name.Space == "" && inertHTTPAnchor(a.Value) {
					continue
				}
				if name == "href" || name == "src" || name == "poster" {
					target, err := resolveReference(base, value)
					if err != nil {
						return err
					}
					if target != base {
						if _, ok := assets[target]; !ok {
							return ErrInvalid
						}
					}
				}
			}
		case xml.EndElement:
			depth--
			if strings.EqualFold(t.Name.Local, "style") {
				if err := validateCSS(base, style, assets); err != nil {
					return err
				}
				inStyle = false
			}
		case xml.CharData:
			if inStyle {
				style = append(style, t...)
				// A style element is raw text in HTML. Its CDATA or
				// entity-decoded content must never carry a raw-text closing
				// or comment sequence that could become markup payload when
				// the document is serialized. A lone '<' inside CSS comments
				// stays the CSS validator's decision.
				start := len(style) - len(t) - styleBreakoutWindow
				if start < 0 {
					start = 0
				}
				if styleBreakout(style[start:]) {
					return ErrInvalid
				}
			}
		case xml.Directive:
			if strings.TrimSpace(strings.ToLower(string(t))) != "doctype html" {
				return ErrInvalid
			}
		case xml.ProcInst:
			if t.Target != "xml" {
				return ErrInvalid
			}
		}
	}
}

func extension(name string) string { return strings.ToLower(path.Ext(name)) }

// styleBreakoutWindow overlaps consecutive character data so a closing or
// comment sequence split across decoder tokens is still seen.
const styleBreakoutWindow = 16

// styleBreakout reports a raw-text closing or HTML comment sequence in the
// aggregated style text. The caller has already rejected real markup; this
// catches sequences that arrive through CDATA or character references and
// would become markup if a consumer serialized the style as HTML.
func styleBreakout(b []byte) bool {
	return asciiFoldContains(b, "</style") || asciiFoldContains(b, "<!--") || asciiFoldContains(b, "-->")
}

// asciiFoldContains is a bounded ASCII case-insensitive search over a small
// window; lower is ASCII lowercase.
func asciiFoldContains(b []byte, lower string) bool {
	if len(lower) == 0 || len(b) < len(lower) {
		return false
	}
	for i := 0; i+len(lower) <= len(b); i++ {
		match := true
		for j := 0; j < len(lower); j++ {
			c := b[i+j]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != lower[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// maxInertAnchorLink bounds the address an XHTML anchor may carry as inert
// publication data. The bytes are validated only; the served asset keeps its
// exact original bytes, length and revision.
const maxInertAnchorLink = 2048

// inertHTTPAnchor reports whether an <a href> is a plain absolute HTTP(S)
// address. It accepts no credentials, empty/opaque authority, control or
// non-ASCII bytes, backslashes, quotes or angle brackets, and no relative or
// protocol-relative form; every other scheme fails. The server never fetches,
// resolves or rewrites the address, so this is a lexical admission, not a
// network capability.
func inertHTTPAnchor(raw string) bool {
	if raw == "" || len(raw) > maxInertAnchorLink {
		return false
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c < 0x21 || c > 0x7e || c == '\\' || c == '"' || c == '\'' || c == '<' || c == '>' {
			return false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.Host == "" || u.Hostname() == "" {
		return false
	}
	return strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https")
}
