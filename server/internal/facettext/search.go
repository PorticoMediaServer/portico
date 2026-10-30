package facettext

import (
	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
	"strings"
	"unicode"
)

// SearchFold preserves word boundaries and removes punctuation within words.
// Thus Spider-Man and spiderman share a token, while WALL·E matches wall e
// through the component tokens supplied by SearchDocument.
func SearchFold(value string) string {
	var b strings.Builder
	for _, r := range sorttext.Fold(value) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
		} else if unicode.IsSpace(r) {
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func CJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// SearchDocument adds punctuation-separated words and CJK unigrams/bigrams.
// CJK candidates are checked for the actual substring after the indexed seek.
// The expansion is linear in input size; hostile metadata cannot grow it without bound.
func SearchDocument(value string) string {
	if len(value) > 65536 {
		value = value[:65536]
	}
	folded := sorttext.Fold(value)
	words := strings.FieldsFunc(folded, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	tokens := append(strings.Fields(SearchFold(folded)), words...)
	for _, word := range words {
		runes := []rune(word)
		for i, r := range runes {
			if !CJK(r) {
				continue
			}
			tokens = append(tokens, string(r))
			if i+1 < len(runes) && CJK(runes[i+1]) {
				tokens = append(tokens, string(runes[i:i+2]))
			}
		}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if !seen[token] {
			out = append(out, token)
			seen[token] = true
		}
	}
	return strings.Join(out, " ")
}

func SearchTerms(value string) []string {
	words := strings.Fields(SearchFold(value))
	out := []string{}
	for _, word := range words {
		runes := []rune(word)
		allCJK := true
		for _, r := range runes {
			if !CJK(r) {
				allCJK = false
				break
			}
		}
		if allCJK && len(runes) > 1 {
			for i := 0; i+1 < len(runes); i++ {
				out = append(out, string(runes[i:i+2]))
			}
		} else {
			out = append(out, word)
		}
	}
	return out
}
