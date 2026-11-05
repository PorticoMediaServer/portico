package search

import (
	"crypto/sha256"
	"encoding/binary"
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

// FragmentBudget bounds aggregate prepared bytes and conservative native token
// work per row. TokenWork includes prefix-index amplification, not just words.
// These are preparation limits, not a certificate of transaction latency.
type FragmentBudget struct {
	Bytes, TokenWork int
}

func DefaultFragmentBudget() FragmentBudget {
	return FragmentBudget{Bytes: 6144, TokenWork: 8192}
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
		work += 1 + min(12, len(word)/6)
	}
	// unicode61 may split a Go-normalized token differently. At most one
	// native token per scalar, each with its main and three prefix postings,
	// is a conservative bound independent of that tokenizer's Unicode tables.
	for word := range strings.FieldsSeq(f.lexical) {
		work += 4 * utf8.RuneCountInString(word)
	}
	return work
}

// FragmentPlan owns a complete immutable prepared field and ordered manifest.
// Callers persist/stage one fragment per admitted writer turn, never the plan
// in a single transaction. Count/hash do not substitute for the writer's CAS.
type FragmentPlan struct {
	fragments []Fragment
	digest    [32]byte
}

func (p FragmentPlan) Len() int          { return len(p.fragments) }
func (p FragmentPlan) At(i int) Fragment { return p.fragments[i] }
func (p FragmentPlan) Digest() [32]byte  { return p.digest }

// PrepareFragments preserves Q's exact prepared token sequences and 512+512
// folded windows. SearchDocument's frozen 65536-byte input clip is preserved;
// SearchFold still sees the full field. No token is split, omitted or allowed
// to exceed a budget. An unchunkable token returns no partial plan; accepted
// publisher intent must remain pending for an independently qualified remedy.
func PrepareFragments(value string, budget FragmentBudget) (FragmentPlan, error) {
	if budget.Bytes <= 0 || budget.TokenWork <= 0 {
		return FragmentPlan{}, fmt.Errorf("%w: nonpositive limit", ErrFragmentBudget)
	}
	document := facettext.SearchDocument(value)
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
		for pi < len(prefix) {
			candidate := f
			candidate.prefix = joinToken(f.prefix, prefix[pi])
			if len(candidate.prefix) > tokenColumnBytes || !fits(candidate, budget) {
				break
			}
			f = candidate
			pi++
		}
		for li < len(lexical) {
			candidate := f
			candidate.lexical = joinToken(f.lexical, lexical[li])
			if len(candidate.lexical) > tokenColumnBytes || !fits(candidate, budget) {
				break
			}
			f = candidate
			li++
		}
		if f.Bytes() == 0 && (pi < len(prefix) || li < len(lexical)) {
			return FragmentPlan{}, fmt.Errorf("%w: indivisible token", ErrFragmentBudget)
		}
		fragments = append(fragments, f)
	}
	// Length prefixes and coordinates make the ordered manifest unambiguous,
	// including empty columns. Domain/source/bank/recipe fences remain separate.
	h := sha256.New()
	for _, f := range fragments {
		for _, s := range []string{f.prefix, f.lexical, f.normalized} {
			_ = binary.Write(h, binary.BigEndian, uint64(len(s)))
			_, _ = h.Write([]byte(s))
		}
		_ = binary.Write(h, binary.BigEndian, uint64(f.start))
		_ = binary.Write(h, binary.BigEndian, uint64(f.end))
	}
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return FragmentPlan{fragments, digest}, nil
}

func joinToken(s, token string) string {
	if s == "" {
		return token
	}
	return s + " " + token
}

func fits(f Fragment, b FragmentBudget) bool {
	return f.Bytes() <= b.Bytes && f.TokenWork() <= b.TokenWork
}
