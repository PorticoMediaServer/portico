package facettext

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const IndexedSearchPrefixRunes = 12
const NumericSearchPrefixRunes = 6

type SearchPrefix struct {
	Prefix string `json:"prefix"`
	Tier   int    `json:"tier"`
}

func NumericSearchTerm(word string) bool {
	return word != "" && !strings.ContainsFunc(word, func(r rune) bool { return !unicode.IsNumber(r) })
}

// Every word prefix has a direct posting, so every typed word is one term with
// a maintained count and the rarest word can lead a search. A numeric word
// posts its first six characters' prefixes (a longer number is its first six);
// a typed number is then one term like any word, never a range of numbers
// whose size grows with the library.
func SearchPrefixes(document, title string) string {
	prefixes := map[string]SearchPrefix{}
	titleTerms := map[string]bool{}
	for _, word := range strings.Fields(SearchDocument(title)) {
		titleTerms[word] = true
	}
	for _, word := range strings.Fields(document) {
		if strings.ContainsFunc(word, CJK) {
			continue
		}
		runes := []rune(word)
		last := min(len(runes), IndexedSearchPrefixRunes)
		if NumericSearchTerm(word) {
			last = min(last, NumericSearchPrefixRunes)
		}
		for n := 1; n <= last; n++ {
			prefix := string(runes[:n])
			tier := 0
			if titleTerms[word] {
				tier = 1
			}
			if strings.HasPrefix(title, prefix) {
				tier = 2
			}
			if old, ok := prefixes[prefix]; !ok || tier > old.Tier {
				prefixes[prefix] = SearchPrefix{prefix, tier}
			}
		}
	}
	out := make([]SearchPrefix, 0, len(prefixes))
	for _, p := range prefixes {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Prefix < out[j].Prefix })
	raw, _ := json.Marshal(out)
	return string(raw)
}

func IndexedSearchTerms(q string) ([]string, bool) {
	words := strings.Fields(SearchFold(q))
	for _, word := range words {
		maximum := IndexedSearchPrefixRunes
		if NumericSearchTerm(word) {
			maximum = NumericSearchPrefixRunes
		}
		if utf8.RuneCountInString(word) > maximum || strings.ContainsFunc(word, CJK) {
			return nil, false
		}
	}
	return words, len(words) > 0
}
