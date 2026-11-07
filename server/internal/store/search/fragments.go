package search

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/facettext"
)

const (
	// These preserve Q's bounded-fragment recipe, including substring matches
	// across the core boundary. Query normalization/length admission belongs to
	// the read dispatcher; the overlap is not an arbitrary-query guarantee.
	NormalizedCoreRunes    = 512
	NormalizedOverlapRunes = 512
	tokenColumnBytes       = 1024
)

var ErrFragmentBudget = errors.New("search fragment exceeds preparation budget")
var ErrPreparationBudget = errors.New("search preparation exceeds memory reservation")

// FragmentBudget bounds aggregate prepared bytes and conservative native token
// work per row. TokenWork includes prefix-index amplification, not just words.
// PreparationBytes is the caller's finite total reservation for this field,
// including normalization, token expansion, temporary buffers and the returned
// plan. Obtain it from the governor before calling; a budget is not a grant.
// These are preparation limits, not a certificate of transaction latency.
type FragmentBudget struct {
	Bytes, TokenWork int
	PreparationBytes int64
}

func DefaultFragmentBudget() FragmentBudget {
	return FragmentBudget{Bytes: 6144, TokenWork: 8192, PreparationBytes: 32 << 20}
}

// PreparationMemoryBytes is an allocation-free prospective memory charge for
// the pinned recipe. Charge the full source (never the lexical clip alone).
// The full-field allowance covers Unicode folding/intermediate strings, rune
// storage, overlapping windows and the plan. The separate clipped-input
// allowance covers lexical/CJK expansion, token maps/slices and scalar-hex
// packing. Both include temporary/capacity headroom, not just retained bytes.
// Keep these conservative coefficients with the recipe; allocation regressions
// must cover changes to the normalizer or construction strategy too.
func PreparationMemoryBytes(inputBytes int64) (int64, error) {
	const fixed = int64(64 << 10)
	const fullField, lexical = int64(128), int64(256)
	const maxInt64 = int64(1<<63 - 1)
	if inputBytes < 0 {
		return 0, ErrPreparationBudget
	}
	base := fixed + min(inputBytes, 65536)*lexical
	if inputBytes > (maxInt64-base)/fullField {
		return 0, ErrPreparationBudget
	}
	return base + inputBytes*fullField, nil
}

// Fragment is immutable. The three streams are packed independently, as in Q;
// their positions do not imply lexical/normalized coordinate correspondence.
// Verification joins the subject, field and accepted generation, not row-local
// conjunctions across these independently packed columns.
type Fragment struct {
	prefix, lexical, normalized string
	start, end                  int
}

func (f Fragment) PrefixTokens() string  { return f.prefix }
func (f Fragment) LexicalTokens() string { return f.lexical }
func (f Fragment) Normalized() string    { return f.normalized }

// NormalizedRange is a half-open range in folded Unicode scalars, not source
// bytes or Reading section/UTF16 coordinates. Empty normalized columns use 0,0.
func (f Fragment) NormalizedRange() (int, int) { return f.start, f.end }
func (f Fragment) Bytes() int                  { return len(f.prefix) + len(f.lexical) + len(f.normalized) }

func (f Fragment) TokenWork() int {
	work := max(0, utf8.RuneCountInString(f.normalized)-2)
	for word := range strings.FieldsSeq(f.prefix) {
		work += prefixWork(word)
	}
	// unicode61 may split a Go-normalized token differently. At most one
	// native token per scalar, each with its main and three prefix postings,
	// is a conservative bound independent of that tokenizer's Unicode tables.
	for word := range strings.FieldsSeq(f.lexical) {
		work += lexicalWork(word)
	}
	return work
}

// FragmentPlan owns a complete immutable prepared field.
// Callers persist/stage one fragment per admitted writer turn, never the plan
// in a single transaction. Count/hash do not substitute for the writer's CAS.
type FragmentPlan struct {
	fragments []Fragment
}

func (p FragmentPlan) Len() int          { return len(p.fragments) }
func (p FragmentPlan) At(i int) Fragment { return p.fragments[i] }

// PrepareFragments preserves Q's exact prepared token sequences and 512+512
// folded windows. Discovery and verification use the SAME first 64 KiB of
// normalized UTF-8 text per field. No token is split or allowed
// to exceed a budget. An unchunkable token returns no partial plan; accepted
// publisher intent must remain pending for an independently qualified remedy.
func PrepareFragments(value string, budget FragmentBudget) (FragmentPlan, error) {
	if budget.Bytes <= 0 || budget.TokenWork <= 0 {
		return FragmentPlan{}, fmt.Errorf("%w: nonpositive limit", ErrFragmentBudget)
	}
	need, err := PreparationMemoryBytes(int64(len(value)))
	if err != nil || budget.PreparationBytes < need {
		return FragmentPlan{}, fmt.Errorf("%w: %w", ErrFragmentBudget, ErrPreparationBudget)
	}
	// Both lexical and normalized streams see the same normalized field prefix. Check all
	// indivisible tokens now, before prefix materialization or normalized-window allocation.
	document := facettext.SearchDocument(value)
	for word := range strings.FieldsSeq(document) {
		if len(word) > min(tokenColumnBytes, budget.Bytes) || lexicalWork(word) > budget.TokenWork {
			return FragmentPlan{}, fmt.Errorf("%w: indivisible lexical token", ErrFragmentBudget)
		}
	}
	for word := range strings.FieldsSeq(document) {
		if !prefixEligible(word) {
			continue
		}
		n := clippedRunes(word)
		if 6*n > min(tokenColumnBytes, budget.Bytes) || 1+n > budget.TokenWork {
			return FragmentPlan{}, fmt.Errorf("%w: indivisible prefix token", ErrFragmentBudget)
		}
	}
	prefix := strings.Fields(prefixDocument(document))
	lexical := strings.Fields(document)
	normalized := []rune(facettext.SearchFold(value))
	var fragments []Fragment
	pi, li, start := 0, 0, 0
	for pi < len(prefix) || li < len(lexical) || start < len(normalized) || len(fragments) == 0 {
		var f Fragment
		if start < len(normalized) {
			f.start = start
			f.end = min(len(normalized), start+NormalizedCoreRunes+NormalizedOverlapRunes)
			f.normalized = string(normalized[f.start:f.end])
			start += NormalizedCoreRunes
			if !fits(f, budget) {
				return FragmentPlan{}, fmt.Errorf("%w: normalized window needs %d bytes/%d token work", ErrFragmentBudget, f.Bytes(), f.TokenWork())
			}
		}
		f.prefix, pi = packTokens(prefix, pi, budget.Bytes-f.Bytes(), budget.TokenWork-f.TokenWork(), prefixWork)
		f.lexical, li = packTokens(lexical, li, budget.Bytes-f.Bytes(), budget.TokenWork-f.TokenWork(), lexicalWork)
		if f.Bytes() == 0 && (pi < len(prefix) || li < len(lexical)) {
			return FragmentPlan{}, fmt.Errorf("%w: indivisible token", ErrFragmentBudget)
		}
		fragments = append(fragments, f)
	}
	return FragmentPlan{fragments}, nil
}

func packTokens(words []string, after, bytes, work int, cost func(string) int) (string, int) {
	end, size, used := after, 0, 0
	for end < len(words) {
		n := len(words[end])
		if end > after {
			n++
		}
		w := cost(words[end])
		if n > min(tokenColumnBytes, bytes)-size || w > work-used {
			break
		}
		size += n
		used += w
		end++
	}
	return strings.Join(words[after:end], " "), end
}

func prefixWork(word string) int  { return 1 + min(12, len(word)/6) }
func lexicalWork(word string) int { return 4 * utf8.RuneCountInString(word) }

func fits(f Fragment, b FragmentBudget) bool {
	return f.Bytes() <= b.Bytes && f.TokenWork() <= b.TokenWork
}
