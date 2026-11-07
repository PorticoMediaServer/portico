package facettext

import (
	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
	"golang.org/x/text/unicode/norm"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SearchFieldBytes bounds each field after normalization, on complete UTF-8
// scalars. Stored/display text is unchanged; only this prefix is searchable.
const SearchFieldBytes = 64 << 10

// SearchFold preserves word boundaries and removes punctuation within words.
// Thus Spider-Man and spiderman share a token, while WALL·E matches wall e
// through the component tokens supplied by SearchDocument. Every search stage
// uses this SAME first 64 KiB normalized field, never a raw byte slice.
func SearchFold(value string) string { return searchFolded(searchableFoldedPrefix(value)) }

// Keep punctuation in this folded prefix for SearchDocument's component-token
// recipe. Charge the boundary against the resulting normalized letters/numbers
// and collapsed spaces, so folding expansion/contraction and raw whitespace do
// not create a different prefix in discovery and confirmation.
func searchableFoldedPrefix(value string) string {
	value = strings.TrimSpace(value)
	// Find a stable normalization prefix before allocating the whole field.
	// More raw input is read only when contraction/whitespace means it can still
	// contribute to the first 64KiB normalized bytes. The caller still reserves
	// its complete raw-source preparation cost.
	window := min(len(value), SearchFieldBytes+utf8.UTFMax)
	for {
		end := window
		if end < len(value) {
			for end > 0 && !utf8.RuneStart(value[end]) {
				end--
			}
			end = norm.NFC.LastBoundary([]byte(value[:end]))
		}
		folded := sorttext.Fold(value[:end])
		used := 0
		space := false
		for at, r := range folded {
			if unicode.IsLetter(r) || unicode.IsNumber(r) {
				size := utf8.RuneLen(r)
				if space && used > 0 {
					size++
				}
				if used+size > SearchFieldBytes {
					return folded[:at]
				}
				used += size
				space = false
			} else if unicode.IsSpace(r) && used > 0 {
				space = true
			}
		}
		if end == len(value) || used == SearchFieldBytes {
			return folded
		}
		window = min(len(value), window+max(SearchFieldBytes, window))
	}
}

func searchFolded(folded string) string {
	var b strings.Builder
	space := false
	for _, r := range folded {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
		} else if unicode.IsSpace(r) && b.Len() > 0 {
			space = true
		}
	}
	return b.String()
}

func CJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// SearchDocument adds punctuation-separated words and CJK unigrams/bigrams.
// CJK candidates are checked for the actual substring after the indexed seek.
// The expansion is linear in input size; hostile metadata cannot grow it without bound.
func SearchDocument(value string) string {
	folded := searchableFoldedPrefix(value)
	words := strings.FieldsFunc(folded, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	tokens := append(strings.Fields(searchFolded(folded)), words...)
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
	return strings.Clone(strings.Join(out, " "))
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
