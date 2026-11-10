// Package searchtext turns a title, a name or what a person typed into the words search
// compares. Both sides go through Words, so they always agree: case and accents are folded
// away, punctuation separates words, and a word broken by punctuation is also kept whole, so
// "Spider-Man" is found by "spider man", "spiderman" and "spider-man" alike.
package searchtext

import (
	"fmt"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// rules is raised whenever Words would give different words for the same text.
const rules = 1

// Stamp names everything that decides what Words returns: these rules and the Unicode tables
// of the Go version and text library the server was built with. An index made under another
// stamp may not match what is typed now, so it is rebuilt.
func Stamp() string {
	return fmt.Sprintf("rules %d, unicode %s, text %s", rules, unicode.Version, norm.Version)
}

// Bounds on what one text contributes. Real titles are far inside them.
const (
	maxWords     = 64
	maxWordRunes = 48
)

// Words returns the words of text in order. split has every run of letters and numbers as a
// word: "Spider-Man: Homecoming" is spider, man, homecoming. joined keeps what stands between
// spaces together: spiderman, homecoming. joined is nil when it would equal split.
//
// Chinese, Japanese and Korean are written without spaces, so each of their characters is a
// word of its own and a title is found by any characters in it.
func Words(text string) (split, joined []string) {
	var word, chunk []rune
	parts, differs := 0, false
	endWord := func() {
		if len(word) > 0 {
			split = append(split, string(word))
			word = word[:0]
			parts++
		}
	}
	endChunk := func() {
		endWord()
		if len(chunk) > 0 {
			joined = append(joined, string(chunk))
			differs = differs || parts > 1
		}
		chunk, parts = chunk[:0], 0
	}
	for _, r := range sorttext.Fold(text) {
		if len(split) >= maxWords {
			break
		}
		switch {
		case unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul):
			endChunk()
			split, joined = append(split, string(r)), append(joined, string(r))
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			if len(word) < maxWordRunes {
				word = append(word, r)
			}
			if len(chunk) < maxWordRunes {
				chunk = append(chunk, r)
			}
		case unicode.IsMark(r):
			// A mark belongs to the letter before it; it neither joins the word nor ends it.
		case unicode.IsSpace(r):
			endChunk()
		default:
			endWord()
		}
	}
	endChunk()
	if !differs {
		joined = nil
	}
	return split, joined
}
