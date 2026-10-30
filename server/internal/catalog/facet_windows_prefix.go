package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

const facetWindowPrefixLimit = 4096

// Decode peaks include the complete candidate array, growing decoder backing
// arrays, the bounded page IDs and query/key buffers. The Result payload copy is
// charged separately by the shared store. No labels or scalar bytes are cached.
const facetWindowDecodeBytes int64 = 256 << 10
const facetWindowPrefixEncodeBytes int64 = 512 << 10

// Small page-only summaries retain the original small decode reservation. A
// complete seed can carry 4096 19-digit IDs, so its decoder gets the full peak.
func facetWindowDecodeReservation(payload []byte) int64 {
	return min(facetWindowDecodeBytes, 32768+8*int64(len(payload)))
}

func facetWindowKey(s FacetBaseSnapshot, spec facetWindowSpec) readprep.Key {
	canonical, _ := json.Marshal(struct {
		Scope                         FacetBaseScope
		Witness, Authority, Algorithm string
		Window                        facetWindowSpec
	}{s.Scope, s.Witness, s.Authority, "simple-fold-byte-substring-count-ascii-prefix-v2", spec})
	return readprep.Key{Namespace: "simple-facets-v1", Basis: strconv.FormatInt(s.ID, 10), Canonical: string(canonical)}
}

// prefix state belongs exclusively to a store-budgeted computation. Completed
// completeMatchIDs live in that SAME store entry as the page summary. There is
// no side map, new store, query write or retained seed lease.
type facetWindowPrefix struct {
	store       *readprep.Store
	caller      readprep.Caller
	basis       FacetBaseSnapshot
	expiry      *atomic.Int64
	initialized bool
	probe       int
	matches     []int64
	overflow    bool
	seed        []int64
	seeded      bool
	position    int
}

func newFacetWindowScan(w *FacetWindows, caller readprep.Caller, s FacetBaseSnapshot, spec facetWindowSpec, initial int64, expiry *atomic.Int64) *facetWindowScan {
	v := &facetWindowScan{db: w.db, base: s.ID, spec: spec, bytes: initial, ids: make([]int64, 0, spec.Limit+1)}
	if spec.Needle == "" {
		v.total = s.Total
	} else if w.store.MaxBytes() >= 1<<20 {
		// With a deliberately tiny configured store, keep the preexisting raw
		// reducer/exceptional-scalar path; seed retention is only an optimization.
		v.prefix = facetWindowPrefix{store: w.store, caller: caller, basis: s, expiry: expiry, probe: len(spec.Needle) - 1}
	}
	return v
}

// preparePrefix probes at most eight literal byte prefixes per quantum, within
// the soft deadline. It does NOT Fold again: Fold is not prefix-homomorphic for
// arbitrary edits. Only exact already-folded strings share. Same page shape is
// deliberately required in this slice, which covers ordinary incremental typing.
func (s *facetWindowScan) preparePrefix(ctx context.Context, b *readprep.Budget) (bool, error) {
	p := &s.prefix
	if p.store == nil {
		return true, nil
	}
	if !p.initialized {
		if p.expiry.Load() == 0 { // submit has not published its actual lease yet
			return false, nil
		}
		// Include escaped canonical-key encoding/string overlap before probing.
		working := facetWindowDecodeBytes + 4*s.bytes
		ok, err := b.TryResize(s.bytes + working)
		if err != nil {
			return false, err
		}
		if !ok {
			// Prefix reuse is optional. Continue the already-admitted raw scan.
			p.store, p.probe = nil, 0
			return true, nil
		}
		s.bytes += working
		p.matches = make([]int64, 0, facetWindowPrefixLimit)
		p.initialized = true
	}
	started := time.Now()
	for n := 0; p.probe > 0 && n < 8 && time.Since(started) < 8*time.Millisecond; n++ {
		spec := s.spec
		length := p.probe
		p.probe--
		// A cut inside a UTF-8 rune would be rewritten by JSON encoding and
		// would not be a literal folded-query key. Fold always emits UTF-8.
		if !utf8.RuneStart(spec.Needle[length]) {
			continue
		}
		spec.Needle = spec.Needle[:length]
		seed, ok := s.prefixSeed(ctx, spec)
		if ok {
			p.seed, p.seeded, p.probe = seed, true, 0
			return true, nil
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
	}
	return p.probe == 0, nil
}

func (s *facetWindowScan) prefixSeed(ctx context.Context, spec facetWindowSpec) ([]int64, bool) {
	p := &s.prefix
	h, err := p.store.OpenUntil(ctx, p.caller, facetWindowKey(p.basis, spec), time.Unix(0, p.expiry.Load()))
	if err != nil {
		return nil, false // miss/eviction/handle capacity: scan raw instead
	}
	// Cleanup must work even when the worker context was cancelled mid-probe.
	defer p.store.Cancel(context.WithoutCancel(ctx), p.caller, h)
	r, err := p.store.Poll(ctx, p.caller, h)
	if err != nil {
		return nil, false
	}
	defer r.Release()
	if r.State != readprep.Ready {
		return nil, false
	}
	var summary facetWindowSummary
	if json.Unmarshal(r.Payload, &summary) != nil || !summary.CompleteMatchSet ||
		summary.Spec != spec || summary.Total != int64(len(summary.CompleteMatchIDs)) || len(summary.CompleteMatchIDs) > facetWindowPrefixLimit {
		return nil, false
	}
	return summary.CompleteMatchIDs, true
}

func (s *facetWindowScan) match(id int64, accept bool) {
	p := &s.prefix
	if p.initialized && !p.overflow {
		if len(p.matches) == facetWindowPrefixLimit {
			p.matches, p.overflow = nil, true
		} else {
			p.matches = append(p.matches, id)
		}
	}
	s.total++
	if accept {
		if s.spec.Direction == 2 {
			if len(s.ids) == s.spec.Limit+1 {
				copy(s.ids, s.ids[1:])
				s.ids = s.ids[:len(s.ids)-1]
			}
			s.ids = append(s.ids, id)
		} else if len(s.ids) < s.spec.Limit+1 {
			s.ids = append(s.ids, id)
		}
	} else if s.spec.Direction == 2 {
		s.later = true
	} else {
		s.earlier = true
	}
}

// candidateBatch visits <=128 primary-key identities in the inherited complete
// count order. There is no SQL sort or candidate expansion: each SELECT releases
// its read snapshot and we test bytes/time after EACH scalar. This preserves the
// explicit single-scalar O(B) exception without an eager 128-scalar sorter.
func (s *facetWindowScan) candidateBatch(ctx context.Context, tx facetWindowReader) (bool, error) {
	p := &s.prefix
	end := min(p.position+128, len(p.seed))
	eligible, args := s.eligibility()
	query := `SELECT b.value_id,length(CAST(x.folded AS BLOB))+length(CAST(b.label_order AS BLOB))+length(CAST(b.value_order AS BLOB)),instr(CAST(x.folded AS BLOB),CAST(? AS BLOB))>0,` + eligible + `
 FROM catalog_facet_base_rows b JOIN catalog_facet_base_texts x ON x.id=b.text_id WHERE b.base_id=? AND b.value_id=?`
	args = append(args, s.base, int64(0))
	started, width := time.Now(), int64(0)
	for p.position < end {
		args[len(args)-1] = p.seed[p.position]
		var id, size int64
		var match, accept bool
		err := tx.QueryRowContext(ctx, query, args...).Scan(&id, &size, &match, &accept)
		if errors.Is(err, sql.ErrNoRows) {
			return false, readprep.ErrStale
		}
		if err != nil {
			return false, err
		}
		p.position++
		if match {
			s.match(id, accept)
		}
		width += size
		if width >= 4<<20 || time.Since(started) >= 8*time.Millisecond {
			return p.position == len(p.seed), nil
		}
	}
	return p.position == len(p.seed), nil
}

func (s *facetWindowScan) eligibility() (string, []any) {
	eligible := "1"
	args := []any{s.spec.Needle}
	if s.spec.Anchor > 0 {
		op := ">"
		if s.spec.Direction == 2 {
			op = "<"
		}
		if s.spec.Direction == 3 {
			op = ">="
		}
		eligible = facetWindowTuple + op + facetWindowAnchor
		args = append(args, s.base, s.spec.Anchor)
	}
	return eligible, args
}
