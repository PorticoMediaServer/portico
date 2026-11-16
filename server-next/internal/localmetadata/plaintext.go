package localmetadata

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// markup finds a tag or a character reference in a text.
var markup = regexp.MustCompile(`<[a-zA-Z/!]|&(#[0-9]+|#[xX][0-9a-fA-F]+|[a-zA-Z]+);`)

var spaces = regexp.MustCompile(`\s+`)

// PlainText is a description as plain text. Files often write one in HTML (an EPUB's or OPF's
// description, Audiobookshelf's, a tagging tool's comment, an NFO's plot), and every app shows
// a summary as text, so the markup is never kept: tags go, character references are decoded,
// and a paragraph or line break stays a line break. Text with no markup is left as it is.
func PlainText(s string) string {
	if !markup.MatchString(s) {
		return s
	}
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	hidden := 0 // inside a script or style, whose text is not words
	for {
		kind := z.Next()
		switch kind {
		case html.ErrorToken:
			return tidy(b.String())
		case html.TextToken:
			if hidden == 0 {
				// Line ends inside HTML are spaces; its breaks are the tags'.
				b.WriteString(spaces.ReplaceAllString(string(z.Text()), " "))
			}
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "script", "style":
				if kind == html.StartTagToken {
					hidden++
				} else if kind == html.EndTagToken && hidden > 0 {
					hidden--
				}
			case "br":
				b.WriteByte('\n')
			case "p", "div", "li", "ul", "ol", "blockquote", "h1", "h2", "h3", "h4", "h5", "h6", "tr", "table":
				b.WriteString("\n\n")
			}
		}
	}
}

// tidy trims each line and keeps at most one empty line between paragraphs.
func tidy(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" && (len(out) == 0 || out[len(out)-1] == "") {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
