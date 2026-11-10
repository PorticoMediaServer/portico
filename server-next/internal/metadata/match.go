package metadata

import (
	"strings"
	"unicode"

	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// Why a title was not matched by itself. The Unmatched items list says each in plain words.
const (
	ReasonNoResults     = "no_results"     // the provider found nothing for the name
	ReasonNoCloseMatch  = "no_close_match" // it found titles, none close enough to the name and year
	ReasonSeveralTitles = "several_titles" // more than one title fits equally well
	ReasonIDNotFound    = "id_not_found"   // the id in the name or the NFO file is not the provider's
)

// Choose picks the candidate that is the title asked about, when the evidence is strong
// enough to use it without asking anyone: the same name, with a small spelling slip allowed,
// and the same year, give or take one, or no year in the names at all. It answers -1 and the
// reason when no candidate is that sure, or when two are equally good.
func Choose(q Query, candidates []Candidate) (int, string) {
	if len(candidates) == 0 {
		return -1, ReasonNoResults
	}
	want := foldTitle(q.Title)
	best, bestScore, tied := -1, 0, false
	for i, c := range candidates {
		if c.Exact {
			return i, ""
		}
		name := max(closeness(want, foldTitle(c.Title)), closeness(want, foldTitle(c.OriginalTitle)))
		if name == 0 {
			continue
		}
		year := 0
		switch {
		case q.Year == 0 || c.Year == 0:
		case q.Year == c.Year:
			year = 2
		case q.Year-c.Year == 1 || c.Year-q.Year == 1:
			year = 1 // a film is often filed under the year it reached another country
		default:
			continue
		}
		score := name*10 + year
		switch {
		case score > bestScore:
			best, bestScore, tied = i, score, false
		case score == bestScore:
			tied = true
		}
	}
	switch {
	case best < 0:
		return -1, ReasonNoCloseMatch
	case tied:
		return -1, ReasonSeveralTitles
	}
	return best, ""
}

// foldTitle reduces a title to what is compared: no case, accents, punctuation or spaces, and
// "&" read as "and".
func foldTitle(title string) string {
	folded := sorttext.Fold(strings.ReplaceAll(title, "&", " and "))
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, folded)
}

// closeness is 2 for the same name, 1 for a small spelling slip (one letter in a name of five
// or more, two in a name of twelve or more) and 0 otherwise.
func closeness(want, got string) int {
	if want == "" || got == "" {
		return 0
	}
	if want == got {
		return 2
	}
	a, b := []rune(want), []rune(got)
	allowed := 0
	switch shorter := min(len(a), len(b)); {
	case shorter >= 12:
		allowed = 2
	case shorter >= 5:
		allowed = 1
	}
	if allowed == 0 || len(a)-len(b) > allowed || len(b)-len(a) > allowed {
		return 0
	}
	if editDistance(a, b) <= allowed {
		return 1
	}
	return 0
}

// editDistance counts the letters to add, drop, change or swap with a neighbour to turn one
// name into the other: "matirx" is one slip from "matrix".
func editDistance(a, b []rune) int {
	before, row := make([]int, len(b)+1), make([]int, len(b)+1)
	for j := range row {
		row[j] = j
	}
	for i := 1; i <= len(a); i++ {
		next := make([]int, len(b)+1)
		next[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			next[j] = min(row[j]+1, next[j-1]+1, row[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				next[j] = min(next[j], before[j-2]+1)
			}
		}
		before, row = row, next
	}
	return row[len(b)]
}
