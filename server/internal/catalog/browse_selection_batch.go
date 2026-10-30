package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

var (
	ErrSelectionBatchUnsupported       = errors.New("selection query needs a bounded domain evaluator")
	ErrSelectionBatchSearchUnsupported = fmt.Errorf("%w: search needs a bounded exact evaluator", ErrSelectionBatchUnsupported)
	ErrSelectionBatchStale             = errors.New("selection preparation witness changed")
	ErrSelectionBatchIncomplete        = errors.New("selection preparation is incomplete")
	ErrSelectionBatchCapacity          = errors.New("selection preparation exceeds memory capacity")
)

const (
	SelectionBatchMaximumRows  = 4096
	SelectionBatchMaximumBytes = 4 << 20
	selectionBatchTime         = 10 * time.Millisecond
)

// SelectionBatchLimits can narrow, but never widen, the shared preparation
// quantum. Rows counts raw candidates, nested rows, scalar probes and EOF probes.
// Bytes bounds cumulative source work; RetainedBytes independently bounds state.
type SelectionBatchLimits struct {
	Rows, Bytes, RetainedBytes int
	// ReserveTransient reserves the complete scalar/native peak before its SQL
	// evaluation. Shared-store adapters include their own old+clone/factory state.
	ReserveTransient func(int64) error
}

const selectionBatchProbeBytes = 64

// Bytes is peak retained reducer bytes. ExaminedBytes sums fixed probe costs and
// decoded source bytes; DecodedBytes sums source scalars independently of capacity.
// TransientScalarBytes reports the largest
// legal source scalar consumed by a unit; it is never retained as a query result.
// An oversize scalar ends the turn after that O(B) unit, without truncation.
type SelectionBatchWork struct {
	Rows, Bytes                             int
	ExaminedBytes, DecodedBytes             int
	TransientScalarBytes                    int
	DocumentTokens, QueryTokens, TokenBytes int64
	Matches                                 [128]int64
	MatchCount                              int
}

// SelectionBatchWitness must capture the complete predicate/serving witness in
// the supplied service's snapshot. The shared store owns deduplication, expiry,
// cancellation and lifetime. A caller must not substitute a TTL/order revision.
type SelectionBatchWitness func(*Service) (string, error)

type selectionBatchPlan struct {
	restricted, blockUnrated               bool
	localSearch                            bool
	key, witness, library, pivot, revision string
	libraryID                              int64
	kinds                                  []int
	query, sorts, policy                   []byte
	tests                                  []selectionBatchTest
	visibilitySQL                          string
	visibilityArgs                         []any
	availabilitySQL                        string
	availabilityArgs                       []any
}

// BrowseSelectionBatchState is process-local reducer state, with no database
// handles, contexts, authority, result IDs or retained SQL cursors. Clone is safe
// to share with the memory store; Step never mutates its input. No partial count
// is exposed as an exact result. All variable-sized retained data is accounted.
type BrowseSelectionBatchState struct {
	scalarBytes     int
	searchWork      selectionSearchWork
	matchedEntity   int64
	singleCandidate bool
	plan            *selectionBatchPlan // immutable, owned by constructor
	after           int64
	done            bool
	counts          [14][2]int
	candidate       selectionBatchCandidate
}

type selectionBatchCandidate struct {
	episodeRange  selectionBatchRangeCursor
	id            int64
	kind          int
	stage         int
	rootMembers   selectionBatchMemberCursor
	seen, visible bool
	check         selectionBatchItemCheck
	test          int
	values        [63]uint64
	relation      selectionBatchRelationCursor
	matched       bool
	available     bool
}

type selectionBatchItemCheck struct {
	item, after                   int64
	started, done, visible, rated bool
}

func (s *BrowseSelectionBatchState) Clone() *BrowseSelectionBatchState {
	if s == nil {
		return nil
	}
	clone := *s
	clone.candidate.relation = s.candidate.relation.clone()
	return &clone
}

func (s *BrowseSelectionBatchState) Complete() bool { return s != nil && s.done }
func (s *BrowseSelectionBatchState) Key() string {
	if s == nil || s.plan == nil {
		return ""
	}
	return s.plan.key
}

// ReplayAfter starts a fresh bounded replay after an already accepted entity.
// It does not change the immutable query, policy or complete held witness.
func (s *BrowseSelectionBatchState) ReplayAfter(after int64) *BrowseSelectionBatchState {
	if s == nil {
		return nil
	}
	next := &BrowseSelectionBatchState{plan: s.plan, after: after}
	return next
}

// Candidate evaluates one externally ordered raw candidate using identical
// predicate/visibility units. Callers retain this small state across quanta and
// must check the same complete witness at every read/accepting write boundary.
func (s *BrowseSelectionBatchState) Candidate(id int64) *BrowseSelectionBatchState {
	if s == nil || id <= 0 {
		return nil
	}
	return &BrowseSelectionBatchState{plan: s.plan, singleCandidate: true, candidate: selectionBatchCandidate{id: id, stage: 2}}
}

// Bytes conservatively charges each clone for the entire immutable plan as
// well, even if the shared store happens to share that allocation. It includes
// backing capacities, string bytes, cursor strings and argument interfaces.
func (s *BrowseSelectionBatchState) Bytes() int {
	if s == nil || s.plan == nil {
		return 0
	}
	p := s.plan
	n := int(unsafe.Sizeof(*s)+unsafe.Sizeof(*p)) + cap(p.kinds)*int(unsafe.Sizeof(int(0)))
	n += len(p.key) + len(p.witness) + len(p.library) + len(p.pivot) + len(p.revision)
	n += cap(p.query) + cap(p.sorts) + cap(p.policy) + len(p.visibilitySQL) + len(p.availabilitySQL)
	n += selectionArgsBytes(p.visibilityArgs) + selectionArgsBytes(p.availabilityArgs)
	n += cap(p.tests) * int(unsafe.Sizeof(selectionBatchTest{}))
	for _, test := range p.tests {
		n += test.bytes()
	}
	return n + s.candidate.relation.bytes()
}

func selectionArgsBytes(args []any) int {
	n := cap(args) * int(unsafe.Sizeof(any(nil)))
	for _, arg := range args {
		if text, ok := arg.(string); ok {
			n += int(unsafe.Sizeof(text)) + len(text)
		} else {
			n += 16
		}
	}
	return n
}

// NewBrowseSelectionBatch captures a validated page's query, not its potentially
// value-driven WHERE plan. Recompilation is domain-owned and performs no census,
// COUNT, GROUP BY, FTS enumeration or whole-view read. Unsupported query forms
// fail explicitly while the existing product path remains unchanged.
func (s *Service) NewBrowseSelectionBatch(request BrowseRequest, viewer Viewer, bound BoundBrowseQuery, witness string) (*BrowseSelectionBatchState, error) {
	if dbwork.Snapshot(s.Context()) == nil || bound.snapshot != dbwork.Snapshot(s.Context()) {
		return nil, sql.ErrTxDone
	}
	if witness == "" {
		return nil, ErrSelectionBatchStale
	}
	var libraryKind string
	err := s.compactProjectionReady()
	if err == nil {
		err = s.read().QueryRow(`SELECT kind FROM libraries WHERE id=?`, bound.Library).Scan(&libraryKind)
	}
	if err != nil {
		return nil, err
	}
	pivot, ok := pivotForKind(libraryKind, bound.Pivot)
	if !ok || !selectionLeafPivot(pivot) || !selectionAggregateLibraries[libraryKind] ||
		!browseQueryReplayable(bound.Query, bound.Sorts) || len(request.Rows) > 0 || len(request.RowSources) > 0 || !viewer.AllowsLibrary(bound.Library) {
		return nil, ErrSelectionBatchUnsupported
	}
	p := &selectionBatchPlan{witness: witness, library: bound.Library, pivot: bound.Pivot, revision: bound.Revision}
	if err = s.read().QueryRow(`SELECT id FROM catalog_libraries WHERE library_id=? AND retired=0`, bound.Library).Scan(&p.libraryID); err != nil {
		return nil, err
	}
	for _, kind := range entityKindMembers(pivot) {
		value, e := compactcatalog.ParseKind(kind)
		if e != nil {
			return nil, e
		}
		p.kinds = append(p.kinds, int(value))
	}
	p.query, err = json.Marshal(bound.Query)
	if err != nil {
		return nil, err
	}
	// Detach caller-owned trees/slices/restrictions before compiling or caching.
	var node *BrowseNode
	if err = json.Unmarshal(p.query, &node); err != nil {
		return nil, err
	}
	p.sorts, err = json.Marshal(bound.Sorts)
	if err != nil {
		return nil, err
	}
	viewer.Libraries = slices.Clone(viewer.Libraries)
	slices.Sort(viewer.Libraries)
	viewer.Libraries = slices.Compact(viewer.Libraries)
	p.policy, err = json.Marshal(viewer)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(p.policy, &viewer); err != nil {
		return nil, err
	}
	p.tests, err = selectionBatchTests(node, viewer.Profile)
	if err != nil {
		return nil, err
	}
	for _, test := range p.tests {
		p.localSearch = p.localSearch || test.search != nil
	}
	p.restricted = viewer.Restrictions.Active()
	p.blockUnrated = viewer.Restrictions.BlockUnrated
	p.visibilitySQL, p.visibilityArgs = selectionBatchRestrictionEdgeSQL(viewer.EffectiveRestrictions())
	p.availabilitySQL, p.availabilityArgs = homeActionAvailableSQLFor(viewer, "e.entity_id")
	p.key = operationHash([]any{"selection-batch-local-search-v1", p.library, p.pivot, string(p.query), string(p.sorts), string(p.policy), witness})
	state := &BrowseSelectionBatchState{plan: p}
	if state.Bytes() > SelectionBatchMaximumBytes {
		return nil, ErrSelectionBatchCapacity
	}
	return state, nil
}

// StepBrowseSelectionBatch owns one short read-only snapshot. It advances
// before testing every raw candidate, and resumes explicit nested cursors.
// Errors (including drift) discard this step's clone. EOF is a charged probe;
// only true EOF marks the reducer complete. The parent must recheck its witness
// and live authority again when exposing a completed descriptor.
func (s *Service) StepBrowseSelectionBatch(ctx context.Context, previous *BrowseSelectionBatchState, witness SelectionBatchWitness, limits SelectionBatchLimits) (*BrowseSelectionBatchState, SelectionBatchWork, error) {
	var work SelectionBatchWork
	if previous == nil || previous.plan == nil || witness == nil {
		return nil, work, ErrSelectionBatchStale
	}
	if dbwork.Snapshot(ctx) != nil || s.readTx != nil {
		return nil, work, sql.ErrTxDone
	}
	rows := limits.Rows
	if rows <= 0 || rows > SelectionBatchMaximumRows {
		rows = SelectionBatchMaximumRows
	}
	bytes := limits.Bytes
	if bytes <= 0 || bytes > SelectionBatchMaximumBytes {
		bytes = SelectionBatchMaximumBytes
	}
	retained := limits.RetainedBytes
	if retained <= 0 || retained > SelectionBatchMaximumBytes {
		retained = SelectionBatchMaximumBytes
	}
	next := previous.Clone()
	work.Bytes = next.Bytes()
	if work.Bytes > retained {
		return nil, work, ErrSelectionBatchCapacity
	}
	// Yield between domain units; SQL in each unit is an indexed singleton or
	// raw LIMIT 1 followed by bounded key joins. One legal scalar can cost O(B).
	deadline := time.Now().Add(selectionBatchTime)
	class := dbwork.ClassBackgroundMedia
	if next.plan.localSearch {
		class = dbwork.ClassMaintenance
	}
	err := dbwork.WithReadSnapshot(dbwork.WithClass(ctx, class), s.db, func(inner context.Context) error {
		read := s.WithContext(inner)
		current, err := witness(read)
		if err != nil {
			return err
		}
		if current == "" || current != next.plan.witness {
			return ErrSelectionBatchStale
		}
		for !next.done && work.MatchCount < len(work.Matches) && work.Rows < rows && (work.Rows == 0 || work.ExaminedBytes+selectionBatchProbeBytes <= bytes) && (work.Rows == 0 || time.Now().Before(deadline)) {
			if err = inner.Err(); err != nil {
				return err
			}
			work.Rows++
			next.scalarBytes = 0
			next.searchWork = selectionSearchWork{}
			next.matchedEntity = 0
			if err = read.selectionBatchUnit(next, limits.ReserveTransient); err != nil {
				return err
			}
			if next.matchedEntity != 0 {
				work.Matches[work.MatchCount] = next.matchedEntity
				work.MatchCount++
			}
			if next.singleCandidate && next.candidate.stage == 0 {
				next.done = true
			}
			work.DocumentTokens += next.searchWork.documentTokens
			work.QueryTokens += next.searchWork.queryTokens
			work.TokenBytes += next.searchWork.tokenBytes
			work.DecodedBytes += next.scalarBytes
			work.ExaminedBytes += selectionBatchProbeBytes + next.scalarBytes + next.searchWork.probeBytes
			work.TransientScalarBytes = max(work.TransientScalarBytes, next.scalarBytes, next.searchWork.transientBytes)
			work.Bytes = max(work.Bytes, next.Bytes())
			if work.Bytes > retained {
				return ErrSelectionBatchCapacity
			}
			if work.ExaminedBytes >= bytes {
				break
			}
		}
		current, err = witness(read)
		if err != nil {
			return err
		}
		if current != next.plan.witness {
			return ErrSelectionBatchStale
		}
		return nil
	})
	if err != nil {
		return nil, work, err
	}
	return next, work, nil
}

// FinishBrowseSelectionBatch derives capabilities with current command and
// library-delete authority in the caller's snapshot. Neither is cached.
func (s *Service) FinishBrowseSelectionBatch(state *BrowseSelectionBatchState, witness SelectionBatchWitness, authority HomeActionAuthority) (BrowseSelectionDescriptor, error) {
	if state == nil || state.plan == nil || !state.done {
		return BrowseSelectionDescriptor{}, ErrSelectionBatchIncomplete
	}
	if dbwork.Snapshot(s.Context()) == nil || witness == nil {
		return BrowseSelectionDescriptor{}, sql.ErrTxDone
	}
	current, err := witness(s)
	if err != nil {
		return BrowseSelectionDescriptor{}, err
	}
	if current == "" || current != state.plan.witness {
		return BrowseSelectionDescriptor{}, ErrSelectionBatchStale
	}
	p := state.plan
	bound := BoundBrowseQuery{Library: p.library, Pivot: p.pivot, Revision: p.revision}
	if err = json.Unmarshal(p.query, &bound.Query); err != nil {
		return BrowseSelectionDescriptor{}, err
	}
	if err = json.Unmarshal(p.sorts, &bound.Sorts); err != nil {
		return BrowseSelectionDescriptor{}, err
	}
	groups := map[string]*selectionAggregateGroup{}
	for kind, count := range state.counts {
		if count[0] == 0 {
			continue
		}
		name, err := compactcatalog.Kind(kind).Name()
		if err != nil {
			return BrowseSelectionDescriptor{}, err
		}
		groups[name] = &selectionAggregateGroup{count: count[0], available: count[1], libraries: map[string]int{p.library: count[0]}}
	}
	out, err := selectionAggregateDescriptor(bound, authority, groups)
	if err != nil {
		return out, err
	}
	if selectionBatchHasLocalSearch(bound.Query) {
		unavailable := &BrowseSelectionUnavailableActions{}
		if !LocalSearchQueueSupported(bound.Query, bound.Sorts) {
			unavailable.Play = "unsupported-sort"
			unavailable.Queue = "unsupported-sort"
		}
		if !localSearchConversionOrderSupported(bound.Query, bound.Sorts) {
			unavailable.Delete = "unsupported-sort"
		}
		if *unavailable != (BrowseSelectionUnavailableActions{}) {
			out.UnavailableActions = unavailable
		}
	}
	return out, nil
}

func (s *Service) selectionBatchUnit(state *BrowseSelectionBatchState, reserve func(int64) error) error {
	p, c := state.plan, &state.candidate
	switch c.stage {
	case 0:
		// Bypass the serving rewrite only for this raw key driver. Include old
		// overlay keys through separate indexed singleton seeks; never UNION-sort
		// the whole serving view, and never filter before the raw LIMIT.
		tx := dbwork.Snapshot(s.Context())
		var id int64
		err := tx.QueryRowContext(s.Context(), `SELECT entity_id FROM catalog_browse_rows WHERE entity_id>? ORDER BY entity_id LIMIT 1`, state.after).Scan(&id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		// The next unit probes the retained stream, so both raw probes are charged.
		*c = selectionBatchCandidate{id: id, stage: 1}
		return nil
	case 1:
		var old int64
		err := dbwork.Snapshot(s.Context()).QueryRowContext(s.Context(), `SELECT entity_id FROM catalog_browse_rows_previous WHERE generation=(SELECT generation FROM catalog_generations WHERE singleton=1 AND retaining=1) AND entity_id>? ORDER BY entity_id LIMIT 1`, state.after).Scan(&old)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if old > 0 && (c.id == 0 || old < c.id) {
			c.id = old
		}
		if c.id == 0 {
			state.done = true
			return nil
		}
		c.stage = 2
		return nil
	case 2:
		var library int64
		err := s.read().QueryRow(`SELECT e.kind,e.library_id FROM catalog_browse_rows e WHERE e.entity_id=?`, c.id).Scan(&c.kind, &library)
		if errors.Is(err, sql.ErrNoRows) {
			state.after = c.id
			*c = selectionBatchCandidate{}
			return nil
		}
		if err != nil {
			return err
		}
		if library != p.libraryID || !slices.Contains(p.kinds, c.kind) {
			state.after = c.id
			*c = selectionBatchCandidate{}
			return nil
		}
		c.stage = 3
		return nil
	case 3:
		// Leaf pivots have self membership in the maintained model, but walk it
		// rather than assuming identity: current EntityRestrictionSQL is an any-
		// visible-member predicate, and genuinely empty membership is allowed.
		if !p.restricted {
			c.visible = true
			c.stage = 5
			return nil
		}
		done, ready, item, err := s.selectionBatchNextMember(c.id, &c.rootMembers)
		if err != nil {
			return err
		}
		if done {
			c.visible = !c.seen
			c.stage = 5
			return nil
		}
		if !ready {
			return nil
		}
		c.seen = true

		c.check = selectionBatchItemCheck{item: item}
		c.stage = 4
		return nil
	case 4:
		if err := s.selectionBatchCheckItem(p, &c.check, &state.scalarBytes); err != nil {
			return err
		}
		if c.check.done {
			if c.check.visible {
				c.visible = true
				c.stage = 5
			} else {
				c.stage = 3
			}
		}
		return nil
	case 5:
		if !c.visible {
			state.after = c.id
			*c = selectionBatchCandidate{}
			return nil
		}
		if c.test == len(p.tests) {
			var query *BrowseNode
			if err := json.Unmarshal(p.query, &query); err != nil {
				return err
			}
			index := 0
			if !selectionBatchTruth(query, c.values, &index) {
				state.after = c.id
				*c = selectionBatchCandidate{}
				return nil
			}
			c.stage = 8
			return nil
		}
		test := p.tests[c.test]
		if test.rangeShow != "" {
			done, yes, err := s.selectionBatchRange(p, test, c.id, &c.episodeRange, &state.scalarBytes)
			if err != nil {
				return err
			}
			if done {
				c.matched = yes
				selectionBatchFinishTest(c, test)
				c.episodeRange = selectionBatchRangeCursor{}
			}
			return nil
		}
		if test.search != nil {
			yes, err := s.selectionBatchSearchMatches(test.search, c.id, &state.scalarBytes, &state.searchWork, reserve)
			if err != nil {
				return err
			}
			if yes {
				c.values[c.test/64] |= uint64(1) << (c.test % 64)
			}
			c.test++
			return nil
		}
		if test.scalar != "" {
			var yes bool
			args := append(slices.Clone(test.args), c.id)
			err := s.read().QueryRow(`SELECT CASE WHEN (`+test.scalar+`) THEN 1 ELSE 0 END,`+test.sizeSQL+` FROM catalog_browse_rows e WHERE e.entity_id=?`, args...).Scan(&yes, &state.scalarBytes)
			if err != nil {
				return err
			}
			if yes {
				c.values[c.test/64] |= uint64(1) << (c.test % 64)
			}
			c.test++
			return nil
		}
		// Each atom owns its own member cursor, and each member has an explicit
		// attribute-policy cursor before relation values are visited.
		done, ready, item, err := s.selectionBatchNextMember(c.id, &c.relation.members)
		if err != nil {
			return err
		}
		if done {
			selectionBatchFinishTest(c, test)
			return nil
		}
		if !ready {
			return nil
		}
		c.relation.member = item

		c.check = selectionBatchItemCheck{item: item}
		c.stage = 6
		return nil
	case 6:
		if err := s.selectionBatchCheckItem(p, &c.check, &state.scalarBytes); err != nil {
			return err
		}
		if c.check.done {
			if c.check.visible {
				c.stage = 7
			} else {
				c.stage = 5
			}
		}
		return nil
	case 7:
		test := p.tests[c.test]
		done, yes, err := s.selectionBatchRelation(test, c.id, &c.relation, &state.scalarBytes)
		if err != nil {
			return err
		}
		if yes {
			c.matched = true
			selectionBatchFinishTest(c, test)
		} else if done {
			members, member := c.relation.members, c.relation.member
			c.relation = selectionBatchRelationCursor{members: members, member: member}
			c.stage = 5
		}
		return nil
	case 8:
		// All admitted kinds are playable leaves, so the shared CASE takes its
		// indexed item-availability arm. No child/version walk is hidden here.
		args := append(slices.Clone(p.availabilityArgs), c.id)
		err := s.read().QueryRow(`SELECT CASE WHEN `+p.availabilitySQL+` THEN 1 ELSE 0 END FROM catalog_browse_rows e JOIN catalog_kinds k ON k.id=e.kind LEFT JOIN catalog_item_availability a ON a.entity_id=e.entity_id AND a.retired=0 WHERE e.entity_id=? AND k.playable=1`, args...).Scan(&c.available)
		if err != nil {
			return err
		}
		state.matchedEntity = c.id
		state.counts[c.kind][0]++
		if c.available {
			state.counts[c.kind][1]++
		}
		state.after = c.id
		*c = selectionBatchCandidate{}
		return nil
	}
	return fmt.Errorf("invalid selection reducer stage")
}

func selectionBatchFinishTest(c *selectionBatchCandidate, test selectionBatchTest) {
	if c.matched != test.negative {
		c.values[c.test/64] |= uint64(1) << (c.test % 64)
	}
	c.test++
	c.matched = false
	c.relation = selectionBatchRelationCursor{}
	c.stage = 5
}

func selectionBatchTruth(n *BrowseNode, values [63]uint64, index *int) bool {
	if n == nil {
		return true
	}
	if n.Field != "" {
		// contains-all expands to independent existential atoms.
		count := 1
		if n.Operator == "contains-all" {
			if list, ok := n.Value.([]any); ok {
				count = len(list)
			}
		}
		yes := true
		for range count {
			yes = yes && values[*index/64]&(uint64(1)<<(*index%64)) != 0
			*index++
		}
		return yes
	}
	if n.Not != nil {
		return !selectionBatchTruth(n.Not, values, index)
	}
	yes := n.All != nil
	for _, child := range n.All {
		childYes := selectionBatchTruth(&child, values, index)
		yes = yes && childYes
	}
	for _, child := range n.Any {
		childYes := selectionBatchTruth(&child, values, index)
		yes = yes || childYes
	}
	return yes
}

// This is itemRestrictionClause reduced over a raw attribute cursor. A single
// edge costs one unit regardless of whether its field/value is rejected.
func selectionBatchRestrictionEdgeSQL(r identity.ContentRestrictions) (string, []any) {
	parts, args := []string{}, []any{}
	if r.MaximumAge != nil {
		parts = append(parts, `(term.field_id=1 AND ra.minimum_age>?)`)
		args = append(args, *r.MaximumAge)
	}
	if len(r.BlockedLabels) > 0 {
		parts = append(parts, `(term.field_id=2 AND term.value_key IN (`+placeholders(len(r.BlockedLabels))+`))`)
		for _, label := range r.BlockedLabels {
			args = append(args, strings.ToLower(label))
		}
	}
	if len(r.MemberDeniedLabels) > 0 {
		parts = append(parts, `(term.field_id IN(2,3) AND lower(edge.source_value) IN (`+placeholders(len(r.MemberDeniedLabels))+`))`)
		for _, label := range r.MemberDeniedLabels {
			args = append(args, strings.ToLower(label))
		}
	}
	if len(parts) == 0 {
		return "0", args
	}
	return strings.Join(parts, " OR "), args
}

func (s *Service) selectionBatchCheckItem(p *selectionBatchPlan, check *selectionBatchItemCheck, scalarBytes *int) error {
	if !check.started {
		check.started = true
		var found bool
		err := s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_entities ci JOIN catalog_libraries lib ON lib.id=ci.library_id WHERE ci.id=? AND lib.retired=0)`, check.item).Scan(&found)
		if err != nil {
			return err
		}
		if !found || !p.restricted {
			check.done = true
			check.visible = found
		}
		return nil
	}
	var rated, denied bool
	args := append(slices.Clone(p.visibilityArgs), check.item, check.after)
	err := s.read().QueryRow(`SELECT edge.term_id,length(CAST(edge.source_value AS BLOB))+COALESCE(length(CAST(term.value_key AS BLOB)),0),CASE WHEN term.field_id=1 AND ra.minimum_age>=0 THEN 1 ELSE 0 END,CASE WHEN (`+p.visibilitySQL+`) THEN 1 ELSE 0 END FROM (SELECT term_id,source_value FROM catalog_item_attribute_edges INDEXED BY sqlite_autoindex_catalog_item_attribute_edges_1 WHERE item_id=? AND term_id>? ORDER BY term_id LIMIT 1) edge LEFT JOIN catalog_attribute_terms term ON term.id=edge.term_id LEFT JOIN content_rating_ages ra ON ra.value_key=term.value_key`, args...).Scan(&check.after, scalarBytes, &rated, &denied)
	if errors.Is(err, sql.ErrNoRows) {
		check.done = true
		check.visible = !p.blockUnrated || check.rated
		return nil
	}
	if err != nil {
		return err
	}
	check.rated = check.rated || rated
	if denied {
		check.done = true
		check.visible = false
	}
	return nil
}
