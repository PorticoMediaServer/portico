package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

// Enable only after every proof-bearing consumer is qualified. The ordinary
// page keeps its original compiler and never starts a document census here.
const selectionBatchLocalSearchEnabled = true

type selectionBatchSearch struct {
	phrases     string
	queryBytes  int64
	phraseCount int
	cjk         []string
}

func newSelectionBatchSearch(normalized string) (*selectionBatchSearch, error) {
	terms := facettext.SearchTerms(normalized)
	raw, err := json.Marshal(terms)
	if err != nil {
		return nil, err
	}
	p := &selectionBatchSearch{phrases: string(raw), phraseCount: len(terms)}
	for _, term := range terms {
		p.queryBytes += int64(len(term))
	}
	// Browse intentionally uses the original whitespace-normalized words here,
	// unlike /search's folded-word check. Preserve its actual oracle.
	for _, word := range strings.Fields(normalized) {
		if strings.ContainsFunc(word, facettext.CJK) {
			p.cjk = append(p.cjk, strings.Clone(word))
		}
	}
	return p, nil
}

func (p *selectionBatchSearch) bytes() int {
	n := int(unsafe.Sizeof(*p)) + len(p.phrases) + cap(p.cjk)*int(unsafe.Sizeof(""))
	for _, word := range p.cjk {
		n += len(word)
	}
	return n
}

type selectionSearchWork struct {
	documentTokens, queryTokens, tokenBytes int64
	transientBytes                          int
	probeBytes                              int
}

func (s *Service) selectionBatchSearchMatches(p *selectionBatchSearch, entity int64, sourceBytes *int, work *selectionSearchWork, reserve func(int64) error) (bool, error) {
	// s.read resolves the search serving slot in the same snapshot as the
	// complete witness. There is no MATCH/vocabulary scan or metadata rebuild.
	var documentBytes, kindBytes, normalizedBytes int64
	err := s.read().QueryRow(`SELECT length(CAST(d.search AS BLOB)),length(CAST(d.kind AS BLOB)),length(CAST(d.normalized AS BLOB)) FROM catalog_search_documents d WHERE d.entity_id=?`, entity).Scan(&documentBytes, &kindBytes, &normalizedBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Metadata length probes touch the actual SQLite scalar. Charge them as
	// examined source too, even if its pages are hot. No Go/native text copy is
	// made before its peak reservation; one legal cell remains O(B).
	peak := sqlite.SearchMatchTransientBound(documentBytes+kindBytes, p.queryBytes, p.phraseCount) + 4*normalizedBytes + int64(16*len(p.phrases)) + 65536
	maxInt := int64(int(^uint(0) >> 1))
	if documentBytes < 0 || kindBytes < 0 || normalizedBytes < 0 || peak < 0 || peak > maxInt || documentBytes+kindBytes+normalizedBytes > maxInt {
		return false, ErrSelectionBatchCapacity
	}
	work.probeBytes = int(documentBytes + kindBytes + normalizedBytes)
	if reserve != nil {
		if err = reserve(peak); err != nil {
			return false, err
		}
	} else if peak > 32<<20 {
		return false, ErrSelectionBatchCapacity
	}
	var raw, normalized string
	err = s.read().QueryRow(`SELECT portico_search_match(d.search,d.kind,?),d.normalized FROM catalog_search_documents d WHERE d.entity_id=?`, p.phrases, entity).Scan(&raw, &normalized)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		var native *sqlite.Error
		if errors.As(err, &native) && native.Code()&255 == 18 {
			return false, ErrSelectionBatchCapacity
		}
		return false, err
	}
	var result struct {
		Match          bool  `json:"match"`
		SourceBytes    int64 `json:"sourceBytes"`
		QueryBytes     int64 `json:"queryBytes"`
		DocumentTokens int64 `json:"documentTokens"`
		QueryTokens    int64 `json:"queryTokens"`
		TokenBytes     int64 `json:"tokenBytes"`
		TransientBytes int64 `json:"transientBytes"`
	}
	if err = json.Unmarshal([]byte(raw), &result); err != nil {
		return false, err
	}
	// Source scalars, query decoding and JSON are real examined work, including
	// rejected documents. Native temporary allocations are separately reported;
	// no document, normalized text or token list survives this one unit.
	*sourceBytes += int(result.SourceBytes+result.QueryBytes) + len(normalized) + 2*len(raw)
	work.documentTokens, work.queryTokens, work.tokenBytes = result.DocumentTokens, result.QueryTokens, result.TokenBytes
	work.transientBytes = int(peak)
	if !result.Match {
		return false, nil
	}
	for _, word := range p.cjk {
		if !strings.Contains(normalized, word) {
			return false, nil
		}
	}
	return true, nil
}

func selectionBatchHasLocalSearch(node *BrowseNode) bool {
	grouped := browseRequiresGroup(node)
	var visit func(*BrowseNode) bool
	visit = func(n *BrowseNode) bool {
		if n == nil {
			return false
		}
		if n.Field == "search" {
			text, ok := n.Value.(string)
			if !ok {
				return false
			}
			_, indexed := facettext.IndexedSearchTerms(text)
			return !grouped || !indexed
		}
		for i := range n.All {
			if visit(&n.All[i]) {
				return true
			}
		}
		for i := range n.Any {
			if visit(&n.Any[i]) {
				return true
			}
		}
		return visit(n.Not)
	}
	return visit(node)
}

// BrowseNeedsLocalSearch distinguishes only the compiler's fallback branch.
// It is a routing hint, never authority, a count or a ready selection proof.
func BrowseNeedsLocalSearch(node *BrowseNode) bool { return selectionBatchHasLocalSearch(node) }
