// Package search prepares the schema-v1 search recipe. It is not connected to
// the serving store. Preparation runs outside the private writer, under the
// caller's memory/CPU reservation; accepting commands independently check it.
package search

import (
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/facettext"
)

const RecipeVersion = "A-CJK-uni-bi-v1"

// OwnTitle is an immutable publication image, independent of the live title.
// Native deletion must retain the old image until physical completion.
type OwnTitle struct {
	title, fold, prefix, initial string
}

func (p OwnTitle) Title() string         { return p.title }
func (p OwnTitle) Fold() string          { return p.fold }
func (p OwnTitle) PrefixTokens() string  { return p.prefix }
func (p OwnTitle) InitialTokens() string { return p.initial }

// PrepareTitle preserves Q's scalar-hex prefix and initial-word recipes.
// It does not admit the source title or qualify the atomic native replacement.
func PrepareTitle(title string) OwnTitle {
	fold := facettext.SearchFold(title)
	initial := ""
	if word, _, ok := strings.Cut(fold, " "); ok || word != "" {
		initial = encodeClipped(word)
	}
	return OwnTitle{title, fold, prefixDocument(facettext.SearchDocument(title)), initial}
}

// PreparePersonName is the People bank's exact lexical payload, including CJK
// unigrams/bigrams. It is not the scalar-hex prefix payload used for context.
func PreparePersonName(name string) string { return facettext.SearchDocument(name) }

func prefixDocument(document string) string {
	var out strings.Builder
	for word := range strings.FieldsSeq(document) {
		if strings.ContainsFunc(word, facettext.CJK) {
			if utf8.RuneCountInString(word) > 2 || strings.ContainsFunc(word, func(r rune) bool { return !facettext.CJK(r) }) {
				continue
			}
		}
		if out.Len() > 0 {
			out.WriteByte(' ')
		}
		out.WriteString(encodeClipped(word))
	}
	return out.String()
}

func encodeClipped(word string) string {
	limit := facettext.IndexedSearchPrefixRunes
	if facettext.NumericSearchTerm(word) {
		limit = facettext.NumericSearchPrefixRunes
	}
	const hex = "0123456789abcdef"
	var out strings.Builder
	for _, r := range word {
		if limit == 0 {
			break
		}
		for shift := 20; shift >= 0; shift -= 4 {
			out.WriteByte(hex[(r>>shift)&15])
		}
		limit--
	}
	return out.String()
}
