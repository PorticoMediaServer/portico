// Package sorttext owns deterministic catalog ordering, independent of host locale.
package sorttext

import (
	"fmt"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"strings"
	"unicode"
)

var articles = map[string][]string{
	"en": {"the ", "an ", "a "},
	"fr": {"les ", "une ", "des ", "le ", "la ", "un ", "l'", "l’"},
	"es": {"los ", "las ", "una ", "unos ", "unas ", "el ", "la ", "un "},
	"de": {"der ", "die ", "das ", "ein ", "eine "},
	"it": {"gli ", "uno ", "una ", "il ", "lo ", "la ", "le ", "un ", "i ", "l'", "l’"},
	"pt": {"uma ", "uns ", "umas ", "os ", "as ", "um ", "o ", "a "},
	"nl": {"een ", "het ", "de "},
}

func Fold(value string) string {
	value = cases.Fold().String(strings.TrimSpace(value))
	value = norm.NFD.String(value)
	var b strings.Builder
	for _, r := range value {
		switch r {
		case 'ø':
			b.WriteString("o")
			continue
		case 'ł':
			b.WriteString("l")
			continue
		case 'đ', 'ð':
			b.WriteString("d")
			continue
		case 'æ':
			b.WriteString("ae")
			continue
		case 'œ':
			b.WriteString("oe")
			continue
		}
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(r)
		}
	}
	return norm.NFC.String(b.String())
}

// Key honors an explicit sort title without removing its articles.
// Unknown metadata languages do not guess which words are articles.
func Key(title, explicit, language string) string {
	if strings.TrimSpace(explicit) != "" {
		return natural(stripLeading(Fold(explicit)))
	}
	title = stripLeading(strings.TrimSpace(title))
	base := strings.ToLower(strings.SplitN(language, "-", 2)[0])
	// No metadata language (music and audiobook entities, local-only libraries) sorts as the
	// product's source language, so "The Beatles" files under B like "The Avengers" does.
	if base == "" {
		base = "en"
	}
	lower := strings.ToLower(title)
	for _, article := range articles[base] {
		if strings.HasPrefix(lower, article) && len(title) > len(article) {
			title = title[len(article):]
			break
		}
	}
	return natural(stripLeading(Fold(title)))
}
func Letter(value string) string {
	for _, r := range Fold(value) {
		if unicode.IsLetter(r) {
			return string(unicode.ToUpper(r))
		}
		return "#"
	}
	return "#"
}

// stripLeading leaves the first letter or number as the ordering boundary.
func stripLeading(value string) string {
	return strings.TrimLeftFunc(value, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
}

// natural encodes arbitrary-length ASCII number runs by magnitude, without
// integer overflow. The leading digit keeps numeric titles in the # bucket.
// Equal numeric values (including leading zeroes) share a key; entity id breaks ties.
func natural(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); {
		if value[i] < '0' || value[i] > '9' {
			b.WriteByte(value[i])
			i++
			continue
		}
		end := i
		for end < len(value) && value[end] >= '0' && value[end] <= '9' {
			end++
		}
		digits := strings.TrimLeft(value[i:end], "0")
		if digits == "" {
			digits = "0"
		}
		fmt.Fprintf(&b, "0%010d:%s;", len(digits), digits)
		i = end
	}
	return b.String()
}
