package reading

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf16"
)

const maxContents = 512

// ContentsEntry is publication navigation, not a reading position or authority.
// Section is always a member of this exact EPUB's spine. No timing or page
// estimate is inferred from a publisher's label or order.
type ContentsEntry struct {
	Title    string `json:"title"`
	Section  string `json:"section"`
	Fragment string `json:"fragment,omitempty"`
	Depth    int    `json:"depth"`
}

type navigationNode struct {
	name     xml.Name
	attrs    []xml.Attr
	text     string
	children []*navigationNode
}

// navigationTree consumes one bounded XML document to EOF. It never resolves
// entities, executes instructions or follows a target outside the archive.
func navigationTree(raw []byte) (*navigationNode, error) {
	d := xml.NewDecoder(bytes.NewReader(raw))
	root := &navigationNode{}
	stack := []*navigationNode{root}
	nodes := 0
	for {
		token, err := d.Token()
		if err == io.EOF {
			if len(stack) != 1 || len(root.children) != 1 {
				return nil, ErrInvalid
			}
			return root.children[0], nil
		}
		if err != nil {
			return nil, ErrInvalid
		}
		switch v := token.(type) {
		case xml.StartElement:
			nodes++
			if len(stack) >= 128 || nodes > 8192 || len(v.Attr) > 64 {
				return nil, ErrLimit
			}
			node := &navigationNode{name: v.Name, attrs: v.Attr}
			parent := stack[len(stack)-1]
			parent.children = append(parent.children, node)
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) <= 1 {
				return nil, ErrInvalid
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 1 && len(bytes.TrimSpace(v)) > 0 {
				return nil, ErrInvalid
			}
			node := stack[len(stack)-1]
			// Keep whitespace between inline elements for the publisher's label.
			node.children = append(node.children, &navigationNode{text: string(v)})
		case xml.Directive:
			return nil, ErrInvalid
		case xml.ProcInst:
			if v.Target != "xml" {
				return nil, ErrInvalid
			}
		}
	}
}

func (n *navigationNode) attr(name, namespace string) string {
	for _, a := range n.attrs {
		if a.Name.Local == name && a.Name.Space == namespace {
			return a.Value
		}
	}
	return ""
}
func (n *navigationNode) label() string {
	var b strings.Builder
	var visit func(*navigationNode)
	visit = func(v *navigationNode) {
		b.WriteString(v.text)
		for _, c := range v.children {
			visit(c)
		}
	}
	visit(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func contentsText(value string, limit int) bool {
	if len(utf16.Encode([]rune(value))) > limit {
		return false
	}
	return strings.IndexFunc(value, unicode.IsControl) < 0
}

func (e *EPUB) contentsTarget(base, href, title string, depth int) (ContentsEntry, error) {
	if title == "" || !contentsText(title, 1024) || href == "" || len(href) > 4096 || depth < 0 || depth > 16 {
		return ContentsEntry{}, ErrInvalid
	}
	section, err := resolveReference(base, href)
	if err != nil {
		return ContentsEntry{}, err
	}
	u, err := url.Parse(href)
	if err != nil || !contentsText(u.Fragment, 1024) {
		return ContentsEntry{}, ErrInvalid
	}
	found := false
	for _, item := range e.Spine {
		if item == section {
			found = true
			break
		}
	}
	if !found {
		return ContentsEntry{}, ErrInvalid
	}
	return ContentsEntry{Title: title, Section: section, Fragment: u.Fragment, Depth: depth}, nil
}

// Contents withholds the entire optional descriptor if declared navigation is
// malformed, ambiguous or outside its budget. It never substitutes spine file
// names as chapters. Transport/cancellation/source-guard errors still propagate.
func (e *EPUB) Contents(ctx context.Context) ([]ContentsEntry, error) {
	name := e.ncx
	modern := len(e.navigation) > 0
	if modern {
		if len(e.navigation) != 1 {
			return nil, nil
		}
		name = e.navigation[0]
	}
	if name == "" {
		return nil, nil
	}
	raw, err := e.archive.read(ctx, name, 1<<20)
	if errors.Is(err, ErrInvalid) || errors.Is(err, ErrLimit) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if modern && validateXHTML(name, raw, e.allowed) != nil {
		return nil, nil
	}
	root, err := navigationTree(raw)
	if err != nil {
		return nil, nil
	}
	var out []ContentsEntry
	appendEntry := func(href, title string, depth int) error {
		if len(out) >= maxContents {
			return ErrLimit
		}
		entry, err := e.contentsTarget(name, href, title, depth)
		if err == nil {
			out = append(out, entry)
		}
		return err
	}
	if modern {
		var nav *navigationNode
		var find func(*navigationNode) error
		find = func(n *navigationNode) error {
			if n.name.Local == "nav" {
				for _, kind := range strings.Fields(n.attr("type", "http://www.idpf.org/2007/ops")) {
					if kind == "toc" {
						if nav != nil {
							return ErrInvalid
						}
						nav = n
					}
				}
			}
			for _, c := range n.children {
				if err := find(c); err != nil {
					return err
				}
			}
			return nil
		}
		if find(root) != nil || nav == nil {
			return nil, nil
		}
		var walk func(*navigationNode, int) error
		walk = func(n *navigationNode, depth int) error {
			if n.name.Local == "ol" {
				depth++
			}
			if depth > 17 {
				return ErrLimit
			}
			if n.name.Local == "a" {
				return appendEntry(n.attr("href", ""), n.label(), depth-1)
			}
			for _, c := range n.children {
				if err := walk(c, depth); err != nil {
					return err
				}
			}
			return nil
		}
		if walk(nav, 0) != nil {
			return nil, nil
		}
	} else {
		var hasNavigation func(*navigationNode) bool
		hasNavigation = func(n *navigationNode) bool {
			if n.name.Local == "navPoint" || n.name.Local == "navMap" {
				return true
			}
			for _, c := range n.children {
				if hasNavigation(c) {
					return true
				}
			}
			return false
		}
		var navMap *navigationNode
		if root.name.Local != "ncx" {
			return nil, nil
		}
		for _, c := range root.children {
			if c.name.Local == "navMap" {
				if navMap != nil {
					return nil, nil
				}
				navMap = c
			} else if hasNavigation(c) {
				return nil, nil
			}
		}
		if navMap == nil {
			return nil, nil
		}
		var point func(*navigationNode, int) error
		point = func(n *navigationNode, depth int) error {
			var label, href string
			labels, targets := 0, 0
			for _, c := range n.children {
				switch c.name.Local {
				case "navLabel":
					labels++
					label = c.label()
					if hasNavigation(c) {
						return ErrInvalid
					}
				case "content":
					targets++
					href = c.attr("src", "")
					if hasNavigation(c) {
						return ErrInvalid
					}
				case "navPoint":
				default:
					if hasNavigation(c) {
						return ErrInvalid
					}
				}
			}
			if labels != 1 || targets != 1 {
				return ErrInvalid
			}
			if err := appendEntry(href, label, depth); err != nil {
				return err
			}
			for _, c := range n.children {
				if c.name.Local == "navPoint" {
					if err := point(c, depth+1); err != nil {
						return err
					}
				}
			}
			return nil
		}
		for _, c := range navMap.children {
			if c.name.Local == "navPoint" {
				if point(c, 0) != nil {
					return nil, nil
				}
			} else if hasNavigation(c) {
				return nil, nil
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if len(out) > maxContents {
			return nil, nil
		}
	}
	return out, nil
}

const (
	pageListXHTMLNamespace = "http://www.w3.org/1999/xhtml"
	pageListNCXNamespace   = "http://www.daisy.org/z3986/2005/ncx/"
)

// PageListCount returns the publisher's explicit EPUB page-list count when the
// navigation declares exactly one correctly namespaced page-list whose authored
// entries target admitted spine files. The href proof is spine membership only;
// no target file is opened, so a fragment's existence is not verified. Repeated
// identical canonical targets (file plus fragment) are ambiguous and unknown.
// Missing, malformed, duplicate or unresolved page-lists leave the fact unknown;
// no page estimate, content length or spine file count substitutes.
func (e *EPUB) PageListCount(ctx context.Context) (*int64, error) {
	name := e.ncx
	modern := len(e.navigation) > 0
	if modern {
		if len(e.navigation) != 1 {
			return nil, nil
		}
		name = e.navigation[0]
	}
	if name == "" {
		return nil, nil
	}
	raw, err := e.archive.read(ctx, name, 1<<20)
	if errors.Is(err, ErrInvalid) || errors.Is(err, ErrLimit) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if modern && validateXHTML(name, raw, e.allowed) != nil {
		return nil, nil
	}
	root, err := navigationTree(raw)
	if err != nil {
		return nil, nil
	}
	spine := map[string]bool{}
	for _, item := range e.Spine {
		spine[item] = true
	}
	targets := map[string]bool{}
	count := int64(0)
	addTarget := func(href string) bool {
		if href == "" || len(href) > 4096 {
			return false
		}
		section, err := resolveReference(name, href)
		if err != nil || !spine[section] {
			return false
		}
		u, err := url.Parse(href)
		if err != nil {
			return false
		}
		key := section + "\x00" + u.Fragment
		if targets[key] {
			return false
		}
		targets[key] = true
		count++
		return count <= 4096
	}
	var hasAnchor func(*navigationNode) bool
	hasAnchor = func(n *navigationNode) bool {
		if n.name.Local == "a" && n.name.Space == pageListXHTMLNamespace {
			return true
		}
		for _, c := range n.children {
			if hasAnchor(c) {
				return true
			}
		}
		return false
	}
	if modern {
		if root.name.Space != pageListXHTMLNamespace {
			return nil, nil
		}
		var navs []*navigationNode
		var find func(*navigationNode)
		find = func(n *navigationNode) {
			if n.name.Space == pageListXHTMLNamespace && n.name.Local == "nav" {
				for _, kind := range strings.Fields(n.attr("type", "http://www.idpf.org/2007/ops")) {
					if kind == "page-list" {
						navs = append(navs, n)
					}
				}
			}
			for _, c := range n.children {
				find(c)
			}
		}
		find(root)
		if len(navs) != 1 {
			return nil, nil
		}
		var lists []*navigationNode
		for _, c := range navs[0].children {
			if c.name.Space != pageListXHTMLNamespace {
				if hasAnchor(c) {
					return nil, nil
				}
				continue
			}
			switch c.name.Local {
			case "ol":
				lists = append(lists, c)
			case "h1", "h2", "h3", "h4", "h5", "h6":
			default:
				if hasAnchor(c) {
					return nil, nil
				}
			}
		}
		if len(lists) != 1 {
			return nil, nil
		}
		ok := true
		var walkList func(*navigationNode)
		walkList = func(list *navigationNode) {
			if !ok {
				return
			}
			for _, entry := range list.children {
				if entry.name.Local == "" && entry.name.Space == "" {
					if len(strings.TrimSpace(entry.text)) != 0 {
						ok = false
					}
					continue
				}
				if entry.name.Space != pageListXHTMLNamespace || entry.name.Local != "li" {
					// An ol may contain only list items; any other element can
					// hide authored anchors and must decline the fact.
					ok = false
					continue
				}
				anchors, nested := 0, 0
				for _, c := range entry.children {
					if c.name.Space == pageListXHTMLNamespace && c.name.Local == "a" {
						anchors++
						if !addTarget(c.attr("href", "")) {
							ok = false
						}
						continue
					}
					if c.name.Space == pageListXHTMLNamespace && c.name.Local == "ol" {
						nested++
						walkList(c)
						continue
					}
					if hasAnchor(c) {
						ok = false
					}
				}
				if anchors != 1 || nested > 1 {
					ok = false
				}
			}
		}
		walkList(lists[0])
		if !ok || count == 0 {
			return nil, nil
		}
	} else {
		if root.name.Space != pageListNCXNamespace || root.name.Local != "ncx" {
			return nil, nil
		}
		var lists []*navigationNode
		var find func(*navigationNode)
		find = func(n *navigationNode) {
			if n.name.Space == pageListNCXNamespace && n.name.Local == "pageList" {
				lists = append(lists, n)
			}
			for _, c := range n.children {
				find(c)
			}
		}
		find(root)
		if len(lists) != 1 {
			return nil, nil
		}
		ok := true
		for _, entry := range lists[0].children {
			if entry.name.Local == "" && entry.name.Space == "" {
				if len(strings.TrimSpace(entry.text)) != 0 {
					ok = false
				}
				continue
			}
			if entry.name.Space != pageListNCXNamespace {
				ok = false
				continue
			}
			switch entry.name.Local {
			case "navLabel", "navInfo":
				// Metadata children cannot carry page targets.
				var containsTarget func(*navigationNode) bool
				containsTarget = func(n *navigationNode) bool {
					if n.name.Space == pageListNCXNamespace && n.name.Local == "pageTarget" {
						return true
					}
					for _, c := range n.children {
						if containsTarget(c) {
							return true
						}
					}
					return false
				}
				if containsTarget(entry) {
					ok = false
				}
			case "pageTarget":
				targets := 0
				href := ""
				for _, c := range entry.children {
					if c.name.Space == pageListNCXNamespace && c.name.Local == "content" {
						targets++
						href = c.attr("src", "")
					}
				}
				if targets != 1 || !addTarget(href) {
					ok = false
				}
			default:
				// Unknown or foreign element subtree under pageList.
				ok = false
			}
		}
		if !ok || count == 0 {
			return nil, nil
		}
	}
	out := count
	return &out, nil
}
