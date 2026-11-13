package reading

import (
	"encoding/xml"
	"strings"
	"unicode/utf8"
)

func frontMatter(types string) bool {
	for _, kind := range strings.Fields(types) {
		switch kind {
		case "frontmatter", "titlepage", "title-page", "copyright-page", "copyright", "dedication", "cover", "toc", "preface", "foreword", "acknowledgments":
			return true
		}
	}
	return false
}

func (b *epub) openingHint(dir, kind, href string, inSpine map[string]bool) {
	name, fragment, ok := resolve(dir, href)
	if !ok || !inSpine[name] {
		return
	}
	if hasWord(kind, "bodymatter") || kind == "text" {
		b.openingStart, b.openingFragment = name, fragment
	} else if frontMatter(kind) {
		b.openingSkip[name] = true
	}
}

func (b *epub) openingLandmarks(raw []byte, dir string, inSpine map[string]bool) {
	d := newDecoder(raw)
	landmarks := false
	for {
		token, err := d.Token()
		if err != nil {
			return
		}
		switch t := token.(type) {
		case xml.StartElement:
			kind, href := "", ""
			for _, a := range t.Attr {
				if a.Name.Local == "type" {
					kind = a.Value
				}
				if a.Name.Local == "href" {
					href = a.Value
				}
			}
			if t.Name.Local == "nav" {
				landmarks = hasWord(kind, "landmarks")
			}
			if landmarks && t.Name.Local == "a" {
				b.openingHint(dir, kind, href, inSpine)
			}
		case xml.EndElement:
			if t.Name.Local == "nav" {
				landmarks = false
			}
		}
	}
}

// openingParagraphs keeps prose only: no headings, navigation, scripts or marked front matter.
// Without a body hint a paragraph needs twenty words to distinguish prose from title-page lines.
func openingParagraphs(raw []byte, fragment string, hinted bool) (string, bool) {
	d := newDecoder(raw)
	depth, hidden, paragraph := 0, 0, 0
	started := fragment == ""
	var text strings.Builder
	var prose []string
	for {
		token, err := d.Token()
		if err != nil {
			break
		}
		switch t := token.(type) {
		case xml.StartElement:
			depth++
			kind := ""
			for _, a := range t.Attr {
				if a.Name.Local == "id" && a.Value == fragment {
					started = true
				}
				if a.Name.Local == "type" {
					kind = a.Value
				}
			}
			if hidden > 0 || frontMatter(kind) || t.Name.Local == "head" || t.Name.Local == "script" || t.Name.Local == "style" || t.Name.Local == "nav" {
				hidden++
			}
			if started && hidden == 0 && t.Name.Local == "p" {
				paragraph = depth
				text.Reset()
			}
		case xml.CharData:
			if paragraph > 0 && hidden == 0 && text.Len() < 4096 {
				part := string(t[:min(len(t), 4096-text.Len())])
				for !utf8.ValidString(part) {
					part = part[:len(part)-1]
				}
				text.WriteString(part)
			}
		case xml.EndElement:
			if paragraph == depth {
				words := strings.Fields(text.String())
				if hinted || len(words) >= 20 || len(prose) > 0 {
					prose = append(prose, words...)
				}
				paragraph = 0
				if len(prose) >= 70 {
					return openingSentences(prose), true
				}
			}
			if hidden > 0 {
				hidden--
			}
			depth--
		}
	}
	return openingSentences(prose), len(prose) > 0
}

// Prefer a sentence end between fifty and seventy words; shorter prose ends at its last sentence.
func openingSentences(words []string) string {
	end := 0
	for n, word := range words[:min(70, len(words))] {
		last := strings.TrimRight(word, "\"'”’)]}")
		if strings.HasSuffix(last, ".") || strings.HasSuffix(last, "?") || strings.HasSuffix(last, "!") {
			end = n + 1
			if end >= 50 {
				break
			}
		}
	}
	result := strings.Join(words[:end], " ")
	if len(result) > 4096 {
		return ""
	}
	return result
}
