package facettext

import (
	"bufio"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
	"golang.org/x/text/cases"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
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
	var out strings.Builder
	if err := WalkSearchField(value, func(r rune) bool { out.WriteRune(r); return true }); err != nil {
		return ""
	}
	return out.String()
}

// WalkSearchField streams the same folded 64KiB normalized prefix to a bounded
// consumer. '-' denotes a punctuation boundary and ' ' a whitespace boundary;
// they retain component-token semantics without retaining raw delimiter runs.
// Returning false stops normalization immediately (used by token preflight).
func WalkSearchField(value string, visit func(rune) bool) error {
	used := 0
	space := false
	separator := rune(0)
	emit := func(r rune) bool {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			size := utf8.RuneLen(r)
			if space && used > 0 {
				size++
			}
			if used+size > SearchFieldBytes {
				return false
			}
			if !visit(r) {
				return false
			}
			used += size
			space = false
			separator = 0
			return used < SearchFieldBytes
		}
		if used == 0 {
			return true
		}
		if unicode.IsSpace(r) {
			space = true
			if separator != ' ' {
				separator = ' '
				return visit(' ')
			}
		} else if separator == 0 {
			separator = '-'
			return visit('-')
		}
		return true
	}
	// Ordinary query/title values avoid the streaming buffers. This path has
	// a fixed raw ceiling; large source fields always use bounded transformers.
	if len(value) <= 4096 {
		for _, r := range sorttext.Fold(value) {
			if !emit(r) {
				break
			}
		}
		return nil
	}
	folded := transform.NewReader(strings.NewReader(value), transform.Chain(cases.Fold(), norm.NFD, searchMarks{}, norm.NFC))
	reader := bufio.NewReaderSize(folded, 4096)
	for {
		r, _, err := reader.ReadRune()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if !emit(r) {
			return nil
		}
	}
}

// The streaming equivalent of sorttext.Fold's mark removal/substitutions.
// Each transformer stage has fixed buffers; no growing raw prefix is refolded.
type searchMarks struct{}

func (searchMarks) Reset() {}
func (searchMarks) Transform(dst, src []byte, atEOF bool) (nDst, nSrc int, err error) {
	for nSrc < len(src) {
		if !atEOF && !utf8.FullRune(src[nSrc:]) {
			return nDst, nSrc, transform.ErrShortSrc
		}
		r, size := utf8.DecodeRune(src[nSrc:])
		if unicode.Is(unicode.Mn, r) {
			nSrc += size
			continue
		}
		replacement := ""
		switch r {
		case 'ø':
			replacement = "o"
		case 'ł':
			replacement = "l"
		case 'đ', 'ð':
			replacement = "d"
		case 'æ':
			replacement = "ae"
		case 'œ':
			replacement = "oe"
		}
		needed := size
		if replacement != "" {
			needed = len(replacement)
		}
		if needed > len(dst)-nDst {
			return nDst, nSrc, transform.ErrShortDst
		}
		if replacement != "" {
			copy(dst[nDst:], replacement)
		} else {
			copy(dst[nDst:], src[nSrc:nSrc+size])
		}
		nDst += needed
		nSrc += size
	}
	return nDst, nSrc, nil
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
