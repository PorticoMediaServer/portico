package search

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/facettext"
)

var ErrFieldContinuation = errors.New("search logical-field continuation changed")

// FieldKey identifies ONE logical source, independently of physical chunks.
// LogicalOrdinal is the original credit/alias/source ordinal, never the native
// fragment ordinal. The read adapter must preserve that typed recipe mapping.
type FieldKey struct {
	Subject                                           ManifestSubject
	SubjectID, PublicationID, BankID, InputGeneration int64
	Field                                             uint16
	LogicalOrdinal                                    int64
	Revision                                          string
}

// CJKFieldMatcher checks one folded CJK-containing term across independently
// packed normalized windows. Latin/numeric word-prefix and multiword recipes
// use their separate verifiers; substring matching cannot replace them.
// This reducer owns no SQL, retained windows, authority or resource grant.
type CJKFieldMatcher struct {
	key             FieldKey
	term            string
	maxWindowBytes  int
	matched, failed bool
	normalizedBytes int
}

func NewCJKFieldMatcher(key FieldKey, term string, maxWindowBytes int) (*CJKFieldMatcher, error) {
	// Query.Terms has already expanded the raw input. Validate its folded
	// envelope independently; applying the raw 128-scalar limit loses terms.
	if len(term) > maxFoldedQueryBytes || !utf8.ValidString(term) || utf8.RuneCountInString(term) > maxFoldedQueryRunes {
		return nil, ErrQuery
	}
	fold := facettext.SearchFold(term)
	if utf8.RuneCountInString(fold) > maxFoldedQueryRunes || len(strings.Fields(fold)) != 1 || !strings.ContainsFunc(fold, facettext.CJK) ||
		key.Subject != ManifestEntity && key.Subject != ManifestPerson || key.PublicationID <= 0 ||
		key.BankID <= 0 || key.InputGeneration <= 0 || key.Field == 0 || key.Field > 64 ||
		key.LogicalOrdinal < 0 || maxWindowBytes <= 0 || len(key.Revision) > maxWindowBytes {
		return nil, ErrQuery
	}
	// The retained witness uses the same finite per-value byte envelope. Own
	// its storage so a short borrowed witness cannot retain a huge source row.
	key.Revision = strings.Clone(key.Revision)
	return &CJKFieldMatcher{key: key, term: fold, maxWindowBytes: maxWindowBytes}, nil
}

// Observe must receive bounded values from an independently closed GV read
// window. Evidence may be in a sibling of the prefix candidate. A wrong fence,
// oversized window or cancellation poisons this reduction instead of silently
// skipping evidence and returning a successful partial/no-match result.
func (m *CJKFieldMatcher) Observe(ctx context.Context, key FieldKey, normalized string) error {
	if err := ctx.Err(); err != nil {
		m.failed = true
		return err
	}
	if m.failed || key != m.key || len(normalized) > m.maxWindowBytes || !utf8.ValidString(normalized) {
		m.failed = true
		return ErrFieldContinuation
	}
	// Normalized windows advance by the compiled 512-scalar core and carry
	// 512 scalars of overlap. Charge each core once, while clipping the whole
	// observed window at the same normalized-byte boundary as native postings.
	searchable := normalized
	remaining := max(0, facettext.SearchFieldBytes-m.normalizedBytes)
	if len(searchable) > remaining {
		end := remaining
		for end > 0 && !utf8.RuneStart(searchable[end]) {
			end--
		}
		searchable = searchable[:end]
	}
	m.matched = m.matched || strings.Contains(searchable, m.term)
	coreBytes, runes := 0, 0
	for at, r := range normalized {
		if runes == NormalizedCoreRunes {
			break
		}
		coreBytes = at + utf8.RuneLen(r)
		runes++
	}
	m.normalizedBytes = min(facettext.SearchFieldBytes, m.normalizedBytes+coreBytes)
	return nil
}

// Result produces at most one original logical identity, regardless of matching
// physical windows. False is conclusive only after the adapter completes its
// bounded sibling walk. The adapter must also recheck current live authority,
// accepted source/bank and response generation; this reducer cannot do so.
func (m *CJKFieldMatcher) Result(ctx context.Context) (FieldKey, bool, error) {
	if err := ctx.Err(); err != nil {
		m.failed = true
		return FieldKey{}, false, err
	}
	if m.failed {
		return FieldKey{}, false, ErrFieldContinuation
	}
	return m.key, m.matched, nil
}
