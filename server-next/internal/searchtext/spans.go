package searchtext

import (
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// Span is one word of a long text, folded as Words folds it, with where it stands in the
// text, counted in UTF-16 units as a reader counts.
type Span struct {
	Word       string
	Start, End int
}

// Spans splits a long text, such as a chapter of a book, into its words, as many as there
// are: a run of letters and numbers is a word, a mark stays with the letter before it, a
// Chinese, Japanese or Korean character is a word of its own, and anything else separates
// words. Each word is folded on its own, so a word's place in the text is exact. A word
// longer than Words keeps is cut the same way on both sides of a search.
func Spans(text string) []Span {
	var out []Span
	start, startUnit, unit := -1, 0, 0
	end := func(stop int) {
		if start >= 0 {
			if word := foldWord(text[start:stop]); word != "" {
				out = append(out, Span{Word: word, Start: startUnit, End: unit})
			}
			start = -1
		}
	}
	for i, r := range text {
		switch {
		case unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul):
			end(i)
			start, startUnit = i, unit
			unit += utf16.RuneLen(r)
			end(i + utf8.RuneLen(r))
			continue
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			if start < 0 {
				start, startUnit = i, unit
			}
		case unicode.IsMark(r):
			// A mark belongs to the letter before it; one with no letter before it is nothing.
		default:
			end(i)
		}
		unit += utf16.RuneLen(r)
	}
	end(len(text))
	return out
}

// foldWord folds one word as Words would: plain ASCII is only lowered, anything else goes
// through the full fold, and the marks and anything the fold turns into a non-letter drop out.
func foldWord(word string) string {
	ascii := true
	for i := 0; i < len(word); i++ {
		if word[i] >= 0x80 {
			ascii = false
			break
		}
	}
	var runes []rune
	if ascii {
		runes = []rune(word)
		for n, r := range runes {
			if r >= 'A' && r <= 'Z' {
				runes[n] = r + 'a' - 'A'
			}
		}
	} else {
		for _, r := range sorttext.Fold(word) {
			if unicode.IsLetter(r) || unicode.IsNumber(r) {
				runes = append(runes, r)
			}
		}
	}
	if len(runes) > maxWordRunes {
		runes = runes[:maxWordRunes]
	}
	return string(runes)
}
