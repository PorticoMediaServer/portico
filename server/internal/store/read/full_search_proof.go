package read

import (
	"cmp"
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/facettext"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

var errFullSearchPending = errors.New("full search proof is incomplete")

// These private proof values are reducer inputs/results, NOT a native bank
// attestation, permission or a public caller's successful-proof flag. The bank
// adapter must obtain accepted/current/visible inputs in the read-issued scope.
// Fixed-slot native READ attestation is an independent dependency.
type fullQueryScope struct {
	query           search.Query
	terms           []string
	bank            int64
	revision, input int64
	preview         bool
	peopleOnly      bool
	nativeWork      *nativeCandidateWork
}

func newFullQueryScope(query search.Query, bank, revision, input int64) (*fullQueryScope, error) {
	if query.Mode() != search.ReadFull || query.Fold() == "" || bank <= 0 || revision <= 0 || input < 0 {
		return nil, ErrInput
	}
	terms := query.Terms()
	slices.Sort(terms)
	terms = slices.Compact(terms)
	return &fullQueryScope{query: query, terms: terms, bank: bank, revision: revision, input: input}, nil
}

type logicalSourceProof struct {
	scope   *fullQueryScope
	key     search.FieldKey
	matched []bool
	primary string
}

// Independently packed lexical/normalized columns share a logical FieldKey,
// never a physical ordinal. One accepted sibling may prove a CJK term absent
// from the prefix-candidate row; unrelated fields cannot be concatenated.
type logicalFieldWalk struct {
	scope                  *fullQueryScope
	key                    search.FieldKey
	matched                []bool
	primary                string
	failed                 bool
	cjk                    []*search.CJKFieldMatcher
	after                  int64
	native, nativeComplete bool
}

func newLogicalFieldWalk(scope *fullQueryScope, key search.FieldKey, primaryFold string) (*logicalFieldWalk, error) {
	if key.Field != 1 {
		primaryFold = ""
	}
	if scope == nil || key.Subject != search.ManifestEntity && key.Subject != search.ManifestPerson || key.SubjectID <= 0 || key.PublicationID <= 0 || key.BankID != scope.bank || key.InputGeneration <= 0 || key.Field < 1 || key.Field > 9 || key.LogicalOrdinal != 0 || len(key.Revision) == 0 || len(key.Revision) > 4096 || len(primaryFold) > 65536 || !utf8.ValidString(primaryFold) {
		return nil, ErrInput
	}
	key.Revision = strings.Clone(key.Revision)
	w := &logicalFieldWalk{scope: scope, key: key, matched: make([]bool, len(scope.terms)), primary: strings.Clone(primaryFold), cjk: make([]*search.CJKFieldMatcher, len(scope.terms)), after: -1}
	for i, term := range scope.terms {
		if strings.ContainsFunc(term, facettext.CJK) {
			var err error
			w.cjk[i], err = search.NewCJKFieldMatcher(key, term, 4096)
			if err != nil {
				return nil, err
			}
		}
	}
	return w, nil
}

func (w *logicalFieldWalk) observe(ctx context.Context, key search.FieldKey, lexical, normalized string) error {
	if err := ctx.Err(); err != nil {
		w.failed = true
		return err
	}
	if w.failed || key != w.key || len(lexical) > 1024 || len(normalized) > 4096 || !utf8.ValidString(lexical) || !utf8.ValidString(normalized) {
		w.failed = true
		return search.ErrFieldContinuation
	}
	for i, term := range w.scope.terms {
		if w.cjk[i] != nil {
			if err := w.cjk[i].Observe(ctx, key, normalized); err != nil {
				w.failed = true
				return err
			}
			_, match, err := w.cjk[i].Result(ctx)
			if err != nil {
				w.failed = true
				return err
			}
			w.matched[i] = match
		} else {
			for word := range strings.FieldsSeq(lexical) {
				if strings.HasPrefix(word, term) {
					w.matched[i] = true
					break
				}
			}
		}
	}
	return nil
}

// Exhaustion means the native sibling cursor really ended under its still
// current accepted source, not merely that one window filled its raw limit.
func (w *logicalFieldWalk) finish(ctx context.Context, exhausted bool) (logicalSourceProof, error) {
	if err := ctx.Err(); err != nil {
		w.failed = true
		return logicalSourceProof{}, err
	}
	if w.failed {
		return logicalSourceProof{}, search.ErrFieldContinuation
	}
	if !exhausted || w.native && !w.nativeComplete {
		return logicalSourceProof{}, errFullSearchPending
	}
	return logicalSourceProof{w.scope, w.key, slices.Clone(w.matched), w.primary}, nil
}

func compareSource(a, b search.FieldKey) int {
	for _, c := range []int{cmp.Compare(a.Subject, b.Subject), cmp.Compare(a.SubjectID, b.SubjectID), cmp.Compare(a.PublicationID, b.PublicationID), cmp.Compare(a.Field, b.Field), cmp.Compare(a.LogicalOrdinal, b.LogicalOrdinal), cmp.Compare(a.InputGeneration, b.InputGeneration), strings.Compare(a.Revision, b.Revision), cmp.Compare(a.BankID, b.BankID)} {
		if c != 0 {
			return c
		}
	}
	return 0
}

// Native discovery must enumerate logical keys in this total order, or prove
// the canonical first source before producing a delta. Repeated adjacent
// physical siblings count ONCE. No unbounded seen-ID set is retained.
type logicalPopulation struct {
	scope            *fullQueryScope
	term             int
	last             *search.FieldKey
	count            uint64
	complete, failed bool
	posting          *fullPostingKey
	mode             uint8
	slot             uint8
}

// Rowid order is NOT logical-key order. The native adapter can instead supply
// an exact canonical-first posting witness, obtained only after its complete
// accepted logical-field/slot proof. This is private reducer data, not READ
// attestation or authority; the native witness producer remains gated on HN.
type fullPostingKey struct {
	slot  uint8
	rowid int64
}
type canonicalPostingProof struct {
	source logicalSourceProof
	term   int
	first  fullPostingKey
}
type logicalPostingDelta struct {
	position fullPostingKey
	proof    *canonicalPostingProof
}

func comparePosting(a, b fullPostingKey) int {
	if c := cmp.Compare(a.slot, b.slot); c != 0 {
		return c
	}
	return cmp.Compare(a.rowid, b.rowid)
}

func canonicalPopulationStep(ctx context.Context, p *logicalPopulation, slot uint8, raw []logicalPostingDelta, exhausted bool) error {
	if err := ctx.Err(); err != nil {
		p.failed = true
		return err
	}
	if p.slot == 0 {
		p.slot = 1
	}
	if p.failed || p.complete || p.scope == nil || p.term < 0 || p.term >= len(p.scope.terms) || p.mode == 1 || slot != p.slot || slot > 5 || len(raw) > 64 || len(raw) == 0 && !exhausted {
		p.failed = true
		return ErrInput
	}
	p.mode = 2
	for _, delta := range raw {
		pos := delta.position
		if pos.slot != slot || pos.rowid <= 0 || p.posting != nil && comparePosting(pos, *p.posting) <= 0 {
			p.failed = true
			return ErrStale
		}
		p.posting = &fullPostingKey{pos.slot, pos.rowid}
		proof := delta.proof
		if proof == nil {
			continue
		} // hidden/off-head/rejected positions still advance
		if proof.source.scope != p.scope || proof.term != p.term || len(proof.source.matched) != len(p.scope.terms) || !proof.source.matched[p.term] || proof.first.slot < 1 || proof.first.slot > 5 || proof.first.rowid <= 0 || comparePosting(proof.first, pos) > 0 {
			p.failed = true
			return ErrInput
		}
		if proof.first != pos {
			continue
		} // same logical source, another physical/slot hit
		if p.count == math.MaxUint64 {
			p.failed = true
			return ErrUnqualified
		}
		p.count++
	}
	if exhausted {
		p.slot++
	}
	p.complete = p.slot == 6
	return nil
}

type exactTermPopulation struct {
	scope *fullQueryScope
	term  string
	count uint64
}

func logicalPopulationStep(ctx context.Context, p *logicalPopulation, proofs []logicalSourceProof, rawPositions int, exhausted bool) error {
	if err := ctx.Err(); err != nil {
		p.failed = true
		return err
	}
	if p.failed || p.complete || p.scope == nil || p.term < 0 || p.term >= len(p.scope.terms) || p.mode == 2 || rawPositions < 0 || rawPositions > maxOwnerPositions || len(proofs) > rawPositions || rawPositions == 0 && !exhausted {
		p.failed = true
		return ErrInput
	}
	p.mode = 1
	for _, proof := range proofs {
		if proof.scope != p.scope || len(proof.matched) != len(p.scope.terms) || !proof.matched[p.term] {
			p.failed = true
			return ErrInput
		}
		if p.last != nil {
			c := compareSource(proof.key, *p.last)
			if c < 0 {
				p.failed = true
				return ErrStale
			}
			if c == 0 {
				continue
			}
		}
		if p.count == math.MaxUint64 {
			p.failed = true
			return ErrUnqualified
		}
		p.count++
		key := proof.key
		p.last = &key
	}
	p.complete = exhausted
	return nil
}

func finishExactPopulation(ctx context.Context, p *logicalPopulation) (exactTermPopulation, error) {
	if err := ctx.Err(); err != nil {
		return exactTermPopulation{}, err
	}
	if p.failed || p.scope == nil || p.term < 0 || p.term >= len(p.scope.terms) {
		return exactTermPopulation{}, ErrInput
	}
	if !p.complete {
		return exactTermPopulation{}, errFullSearchPending
	}
	return exactTermPopulation{p.scope, p.scope.terms[p.term], p.count}, nil
}

func chooseLead(scope *fullQueryScope, populations []exactTermPopulation) (exactTermPopulation, error) {
	if scope == nil || len(populations) != len(scope.terms) {
		return exactTermPopulation{}, errFullSearchPending
	}
	var best exactTermPopulation
	for i, term := range scope.terms {
		p := populations[i]
		if p.scope != scope || p.term != term {
			return exactTermPopulation{}, ErrInput
		}
		if i == 0 || p.count < best.count || p.count == best.count && p.term < best.term {
			best = p
		}
	}
	return best, nil
}

type fullResultKey struct {
	family      search.ManifestSubject
	id, library int64
}
type candidateSource struct {
	proof logicalSourceProof
	own   bool
	route fullRouteKey
}
type fullRouteKey struct {
	relation                   ownerRelation
	owner, via, ordinal, image int64
	automatic                  nativeAutomaticRoute
}

func compareRoute(a, b fullRouteKey) int {
	for _, c := range []int{cmp.Compare(a.relation, b.relation), cmp.Compare(a.owner, b.owner), cmp.Compare(a.via, b.via), cmp.Compare(a.ordinal, b.ordinal), cmp.Compare(a.image, b.image)} {
		if c != 0 {
			return c
		}
	}
	if nativeAutoLess(a.automatic, b.automatic) {
		return -1
	}
	if nativeAutoLess(b.automatic, a.automatic) {
		return 1
	}
	return 0
}

type canonicalCandidateProof struct {
	key           fullResultKey
	tier, subtier uint8
	lead          search.FieldKey
	route         fullRouteKey
}

// The inverse/current-source adapter supplies the COMPLETE bounded eligible
// own + allowed owner-NAME cohort. It proves visibility/accepted heads first;
// a budget-limited prefix cannot prove nonmembership or a canonical winner.
func proveCandidate(scope *fullQueryScope, key fullResultKey, lead exactTermPopulation, sources []candidateSource, complete bool) (canonicalCandidateProof, bool, error) {
	if scope == nil || scope.preview || lead.scope != scope {
		return canonicalCandidateProof{}, false, ErrInput
	}
	return proveCandidateTerm(scope, key, lead.term, sources, complete)
}

// Preview uses a deterministic discovery term and makes no population claim.
// Complete same-source/conjunction/best-tier validation still applies per hit.
func provePreviewCandidate(scope *fullQueryScope, key fullResultKey, term string, sources []candidateSource, complete bool) (canonicalCandidateProof, bool, error) {
	if scope == nil || !scope.preview {
		return canonicalCandidateProof{}, false, ErrInput
	}
	return proveCandidateTerm(scope, key, term, sources, complete)
}
func proveCandidateTerm(scope *fullQueryScope, key fullResultKey, term string, sources []candidateSource, complete bool) (canonicalCandidateProof, bool, error) {
	if !complete {
		return canonicalCandidateProof{}, false, errFullSearchPending
	}
	if len(sources) > 512 {
		return canonicalCandidateProof{}, false, errFullSearchPending
	}
	if scope == nil || key.id <= 0 || key.family != search.ManifestEntity && key.family != search.ManifestPerson {
		return canonicalCandidateProof{}, false, ErrInput
	}
	leadIndex := slices.Index(scope.terms, term)
	if leadIndex < 0 {
		return canonicalCandidateProof{}, false, ErrInput
	}
	all, own, primary := make([]bool, len(scope.terms)), make([]bool, len(scope.terms)), make([]bool, len(scope.terms))
	var winner *search.FieldKey
	var route fullRouteKey
	var primaryFold string
	var primarySeen bool
	cohorts := make(map[[2]int64]search.FieldKey)
	// Revision is an opaque FIELD witness. Different fields in the same
	// accepted publication may have distinct native revisions; repeated proofs
	// of the same logical field must retain its exact witness.
	type fieldIdentity struct {
		subject search.ManifestSubject
		id      int64
		field   uint16
		ordinal int64
	}
	fieldRevisions := make(map[fieldIdentity]string)
	for _, source := range sources {
		p := source.proof
		if p.scope != scope || len(p.matched) != len(all) {
			return canonicalCandidateProof{}, false, ErrInput
		}
		if source.own != (p.key.Subject == key.family && p.key.SubjectID == key.id) {
			return canonicalCandidateProof{}, false, ErrInput
		}
		identity := [2]int64{int64(p.key.Subject), p.key.SubjectID}
		if old, ok := cohorts[identity]; ok && (old.PublicationID != p.key.PublicationID || old.BankID != p.key.BankID || old.InputGeneration != p.key.InputGeneration) {
			return canonicalCandidateProof{}, false, ErrStale
		}
		cohorts[identity] = p.key
		field := fieldIdentity{p.key.Subject, p.key.SubjectID, p.key.Field, p.key.LogicalOrdinal}
		if old, ok := fieldRevisions[field]; ok && old != p.key.Revision {
			return canonicalCandidateProof{}, false, ErrStale
		}
		fieldRevisions[field] = p.key.Revision
		if !source.own && p.key.Field > 4 {
			continue
		} // appearance/year/Reading never owner NAME
		for i, matched := range p.matched {
			all[i] = all[i] || matched
			if source.own {
				own[i] = own[i] || matched
				if p.key.Field == 1 {
					primary[i] = primary[i] || matched
				}
			}
		}
		if source.own && p.key.Field == 1 {
			if primarySeen && primaryFold != p.primary {
				return canonicalCandidateProof{}, false, ErrStale
			}
			primaryFold = p.primary
			primarySeen = true
		}
		if p.matched[leadIndex] && (winner == nil || compareSource(p.key, *winner) < 0 || compareSource(p.key, *winner) == 0 && compareRoute(source.route, route) < 0) {
			k := p.key
			winner = &k
			route = source.route
		}
	}
	for _, matched := range all {
		if !matched {
			return canonicalCandidateProof{}, false, nil
		}
	}
	if winner == nil {
		return canonicalCandidateProof{}, false, ErrInput
	}
	allTrue := func(v []bool) bool {
		for _, m := range v {
			if !m {
				return false
			}
		}
		return true
	}
	out := canonicalCandidateProof{key: key, tier: 5, lead: *winner, route: route}
	if allTrue(own) {
		out.tier = 4
	}
	if allTrue(primary) {
		out.tier = 2
		if strings.HasPrefix(primaryFold, scope.query.Fold()) {
			out.tier = 1
		}
		if primaryFold == scope.query.Fold() {
			out.tier = 0
		}
	}
	if key.family == search.ManifestPerson {
		if out.tier > 2 {
			return canonicalCandidateProof{}, false, nil
		}
		out.subtier, out.tier = out.tier, 3
	}
	return out, true, nil
}

func compareFullRank(a, b canonicalCandidateProof) int {
	if c := cmp.Compare(a.tier, b.tier); c != 0 {
		return c
	}
	if c := cmp.Compare(a.subtier, b.subtier); c != 0 {
		return c
	}
	if c := cmp.Compare(a.key.family, b.key.family); c != 0 {
		return c
	}
	if c := cmp.Compare(b.key.id, a.key.id); c != 0 {
		return c
	}
	return cmp.Compare(a.key.library, b.key.library)
}
