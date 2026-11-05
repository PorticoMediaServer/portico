package search

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/facettext"
)

var ErrQuery = errors.New("invalid or over-budget search query")

const (
	maxRawQueryBytes    = 128 * utf8.UTFMax
	maxFoldedQueryRunes = 384
	maxFoldedQueryBytes = maxFoldedQueryRunes * utf8.UTFMax
)

type ReadMode uint8

const (
	ReadNothing ReadMode = iota
	ReadTitles
	ReadFull
)

// Query is an immutable dispatch decision. It does not choose a multiword lead:
// the full reader must obtain exact verified logical-source populations first.
type Query struct {
	fold string
	mode ReadMode
}

func (q Query) Fold() string    { return q.fold }
func (q Query) Mode() ReadMode  { return q.mode }
func (q Query) Terms() []string { return strings.Fields(q.fold) }

// PlanQuery applies Q's script-aware scalar thresholds. Explicit show-more
// enables the full membership recipe; it never drops short context matches.
// This values-only planner opens no read snapshots or serving connections.
func PlanQuery(input string, showMore bool) (Query, error) {
	// Include padding in the byte envelope before even scanning for spaces.
	if len(input) > maxRawQueryBytes {
		return Query{}, ErrQuery
	}
	input = strings.TrimSpace(input)
	n, cjk := 0, false
	// Validate/count without copying or materializing the query. Stop once
	// the admitted 128-scalar input span is exceeded.
	for at, r := range input {
		if r == utf8.RuneError {
			_, size := utf8.DecodeRuneInString(input[at:])
			if size == 1 {
				return Query{}, ErrQuery
			}
		}
		n++
		if n > 128 {
			return Query{}, ErrQuery
		}
		cjk = cjk || facettext.CJK(r)
	}
	fold := facettext.SearchFold(input)
	if utf8.RuneCountInString(fold) > maxFoldedQueryRunes {
		return Query{}, ErrQuery
	}
	if fold == "" {
		return Query{}, nil
	}
	mode := ReadFull
	if !showMore && (n < 3 && !cjk || n < 2 && cjk) {
		mode = ReadTitles
	}
	return Query{fold, mode}, nil
}
