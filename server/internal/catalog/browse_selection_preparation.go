package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

const browseSelectionPreparationDomain = "browse-selection-local-v2-" + sqlite.SearchMatchVersion
const BrowseSelectionRetryAfterMs = 1000

// BrowseSelectionPreparing is the strict pending branch beside an immediate
// ordinary page and on the selection-only poll resource.
type BrowseSelectionPreparing struct {
	basis        string
	Variant      string `json:"variant"`
	Kind         string `json:"kind"`
	State        string `json:"state"`
	QueryID      string `json:"queryId"`
	RetryAfterMs int    `json:"retryAfterMs"`
}

// BrowseSelectionUnavailable describes an explicit unsupported whole-view
// action. It carries no guessed total or selector; ordinary page selection stays.
type BrowseSelectionUnavailable struct {
	Reason string `json:"reason"`
}

func (BrowseSelectionUnavailable) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"reason"}, "properties": map[string]any{"reason": map[string]any{"type": "string", "enum": []string{"search_not_bounded"}}}}
}

// CapturedBasis is server-only provenance for the immediate page exposure check.
// It is absent after JSON transport and never grants access.
func (p BrowseSelectionPreparing) CapturedBasis() string { return p.basis }

func (BrowseSelectionPreparing) JSONSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "required": []string{"variant", "kind", "state", "queryId", "retryAfterMs"}, "properties": map[string]any{
		"variant":      map[string]any{"type": "string", "const": "preparing"},
		"kind":         map[string]any{"type": "string", "enum": []string{"browse-selection-preparing"}},
		"state":        map[string]any{"type": "string", "enum": []string{"preparing"}},
		"queryId":      map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
		"retryAfterMs": map[string]any{"type": "integer", "minimum": 1},
	}}
}

// SelectionPreparationAccess is caller authority, not shared content. Check
// must reauthorize the captured session, library and effective policy in the
// supplied snapshot. CapturedBytes covers its private closure/string captures.
// The parent never stores command grants or library-delete policy in a result.
type SelectionPreparationAccess struct {
	Caller        readprep.Caller
	Check         func(*Service) error
	CapturedBytes int64
}

// A single-library content key binds effective content policy/profile, while
// session/family authority remains in private handles. Other authorized libraries
// and a route-specific fence must not prevent the same held view's conversion.
func selectionPreparationViewer(v Viewer, library string) Viewer {
	v.Fence = ""
	v.Libraries = []string{library}
	return v
}

type selectionPreparationSpec struct {
	Revision string `json:",omitempty"`
	Library  string
	Pivot    string
	Query    json.RawMessage
	Sorts    []BrowseSortSelection
	Viewer   Viewer
}

// Only content counters and bounded query intent are shared. Full reducer work,
// the authority callback and all SQL handles disappear at completion.
type selectionPreparationContent struct {
	Library       string
	Pivot         string
	Query         json.RawMessage
	Sorts         []BrowseSortSelection
	Counts        [14][2]int
	IDs           []int64
	Ordered       bool
	OrderInstant  time.Time
	Profile       string
	ActivityFence string
}

type selectionPreparationComputation struct {
	service      *Service
	state        *BrowseSelectionBatchState
	check        func(*Service) error
	factoryBytes int64
	payloadBytes int64
	smallIDs     [201]int64
	smallCount   int
	order        *SmallSelectionOrder
	instant      time.Time
	memoScope    SmallForYouMemoScope
}

func (c *selectionPreparationComputation) AccountedBytes() int64 {
	return c.factoryBytes + int64(unsafe.Sizeof(*c)) + int64(c.state.Bytes()+c.order.Bytes()+c.memoScope.Bytes()) + c.payloadBytes
}

func (c *selectionPreparationComputation) Step(ctx context.Context, budget *readprep.Budget) (readprep.StepResult, error) {
	// Reserve the old state, its clone, bounded SQL/JSON input work and transient
	// ordinary scalar window before allocation. Legal oversize scalars remain
	// one disclosed O(B) backend unit; no scalar is put in this store's payload.
	if err := budget.Resize(c.AccountedBytes() + int64(c.state.Bytes()) + int64(4*len(c.state.plan.policy)) + SelectionBatchMaximumBytes + 65536); err != nil {
		return readprep.StepResult{}, err
	}
	plan := c.state.plan
	witness := func(read *Service) (string, error) {
		if err := c.check(read); err != nil {
			return "", readprep.ErrStale
		}
		var viewer Viewer
		if err := json.Unmarshal(plan.policy, &viewer); err != nil {
			return "", err
		}
		return read.browseSelectionWitness(plan.library, viewer.Profile, viewer.EffectiveRestrictions())
	}
	baseBytes := c.AccountedBytes() + int64(c.state.Bytes()) + int64(4*len(c.state.plan.policy)) + 65536
	next, work, err := c.service.StepBrowseSelectionBatch(ctx, c.state, witness, SelectionBatchLimits{ReserveTransient: func(peak int64) error { return budget.Resize(baseBytes + max(int64(SelectionBatchMaximumBytes), peak)) }})
	if err != nil {
		return readprep.StepResult{}, selectionPreparationError(err)
	}
	for i := 0; i < work.MatchCount && c.smallCount < len(c.smallIDs); i++ {
		c.smallIDs[c.smallCount] = work.Matches[i]
		c.smallCount++
	}
	c.state = next
	if !next.Complete() {
		return readprep.StepResult{}, nil
	}
	pendingOrder, err := c.prepareSmallOrder(ctx, budget, witness)
	if err != nil {
		return readprep.StepResult{}, err
	}
	if pendingOrder {
		return readprep.StepResult{}, nil
	}
	// json.Marshal can overlap its encoder and returned buffer. The bound covers
	// escaping, the small fixed counter vector, both buffers and decode scratch.
	p := next.plan
	encodedBound := int64(6*(len(p.library)+len(p.pivot)+len(p.revision)+len(p.query)+len(p.sorts)+len(p.policy)) + 8192)
	if err = budget.Resize(c.AccountedBytes() + 4*encodedBound); err != nil {
		return readprep.StepResult{}, err
	}
	var sorts []BrowseSortSelection
	if err = json.Unmarshal(p.sorts, &sorts); err != nil {
		return readprep.StepResult{}, err
	}
	content := selectionPreparationContent{Library: p.library, Pivot: p.pivot, Query: p.query, Sorts: sorts, Counts: next.counts, IDs: c.smallIDs[:c.smallCount], Ordered: c.order != nil && c.order.Complete(), OrderInstant: c.instant}
	if c.order != nil && c.order.forYou != nil {
		content.Profile = c.order.forYou.profile
		content.ActivityFence = c.order.forYou.activityFence
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		return readprep.StepResult{}, err
	}
	c.payloadBytes = int64(cap(encoded))
	c.state = nil
	c.order = nil
	return readprep.StepResult{Done: true, Payload: encoded}, nil
}

func selectionPreparationError(err error) error {
	if errors.Is(err, ErrSelectionBatchStale) || errors.Is(err, ErrStaleContinuation) || errors.Is(err, ErrReadingProjectionChanged) || errors.Is(err, ErrVisibilityBuilding) || errors.Is(err, sql.ErrNoRows) {
		return readprep.ErrStale
	}
	if errors.Is(err, ErrSelectionBatchUnsupported) {
		return readprep.ErrInvalid
	}
	if errors.Is(err, ErrSelectionBatchCapacity) {
		return readprep.ErrCapacity
	}
	return err
}

// SubmitBrowseSelectionPreparation admits work, never executes a reduction on
// the page request. The supplied Bound belongs to this ordinary page snapshot.
// The factory captures query bytes only, never a request/context/transaction.
func (s *Service) SubmitBrowseSelectionPreparation(request BrowseRequest, viewer Viewer, bound BoundBrowseQuery, access SelectionPreparationAccess) (BrowseSelectionPreparing, error) {
	store := s.ReadPreparations()
	if store == nil {
		return BrowseSelectionPreparing{}, readprep.ErrCapacity
	}
	if dbwork.Snapshot(s.Context()) == nil || bound.snapshot != dbwork.Snapshot(s.Context()) {
		return BrowseSelectionPreparing{}, sql.ErrTxDone
	}
	if access.Caller == "" || access.Check == nil || access.CapturedBytes < 0 {
		return BrowseSelectionPreparing{}, readprep.ErrInvalid
	}
	if err := access.Check(s); err != nil {
		return BrowseSelectionPreparing{}, err
	}
	var libraryKind string
	err := s.compactProjectionReady()
	if err == nil {
		err = s.read().QueryRow(`SELECT kind FROM libraries WHERE id=?`, bound.Library).Scan(&libraryKind)
	}
	if err != nil {
		return BrowseSelectionPreparing{}, err
	}
	pivot, ok := pivotForKind(libraryKind, bound.Pivot)
	if !ok || !selectionLeafPivot(pivot) || !selectionAggregateLibraries[libraryKind] || !browseQueryReplayable(bound.Query, bound.Sorts) || len(request.Rows) > 0 || len(request.RowSources) > 0 {
		return BrowseSelectionPreparing{}, ErrSelectionBatchUnsupported
	}
	if err := selectionBatchSearchQualified(bound.Query); err != nil {
		return BrowseSelectionPreparing{}, err
	}
	basis, err := s.browseSelectionWitness(bound.Library, viewer.Profile, viewer.EffectiveRestrictions())
	if err != nil {
		return BrowseSelectionPreparing{}, err
	}
	query, err := json.Marshal(bound.Query)
	if err != nil {
		return BrowseSelectionPreparing{}, err
	}
	// Private page revisions are rebound at poll; equivalent content shares one build.
	spec, err := json.Marshal(selectionPreparationSpec{Library: bound.Library, Pivot: bound.Pivot, Query: query, Sorts: bound.Sorts, Viewer: selectionPreparationViewer(viewer, bound.Library)})
	if err != nil {
		return BrowseSelectionPreparing{}, err
	}
	if len(spec) > SelectionBatchMaximumBytes {
		return BrowseSelectionPreparing{}, readprep.ErrCapacity
	}
	// Equality is the full canonical specification plus witness, not the
	// reducer's diagnostic hash. Separate private handles bind each caller.
	key := readprep.Key{Namespace: browseSelectionPreparationDomain, Basis: basis, Canonical: string(spec)}
	root := Service{db: s.db, storage: s.storage, state: s.state, Clock: s.Clock}
	factoryBytes := int64(cap(spec)) + access.CapturedBytes + int64(unsafe.Sizeof(root)) + 2048
	check := access.Check
	instant := s.recommendationNow(s.browseInstant)
	if !bound.orderInstant.IsZero() {
		instant = bound.orderInstant
	}
	var memoScope SmallForYouMemoScope
	if len(bound.Sorts) == 1 && bound.Sorts[0].Field == "forYou" {
		memoScope, err = NewSmallForYouMemoScope(bound, viewer)
		if err != nil {
			return BrowseSelectionPreparing{}, err
		}
	}
	factoryBytes += int64(memoScope.Bytes())
	localSearch := selectionBatchHasLocalSearch(bound.Query)
	class := dbwork.ClassBackgroundMedia
	if localSearch {
		class = dbwork.ClassMaintenance
	}
	plan := readprep.Plan{InitialBytes: factoryBytes, ColdSearch: localSearch, ShouldYield: func() bool { return localSearch && dbwork.ColdReadShouldYield() }, New: func(ctx context.Context, budget *readprep.Budget) (readprep.Computation, error) {
		if err := budget.Resize(factoryBytes + int64(4*cap(spec)) + 2*SelectionBatchMaximumBytes); err != nil {
			return nil, err
		}
		var input selectionPreparationSpec
		if err := json.Unmarshal(spec, &input); err != nil {
			return nil, err
		}
		var state *BrowseSelectionBatchState
		err := dbwork.WithReadSnapshot(dbwork.WithClass(ctx, class), root.db, func(inner context.Context) error {
			read := root.WithContext(inner)
			if err := check(read); err != nil {
				return readprep.ErrStale
			}
			current, err := read.browseSelectionWitness(input.Library, input.Viewer.Profile, input.Viewer.EffectiveRestrictions())
			if err != nil {
				return err
			}
			if current != basis {
				return readprep.ErrStale
			}
			var node *BrowseNode
			if err = json.Unmarshal(input.Query, &node); err != nil {
				return err
			}
			bound := BoundBrowseQuery{Library: input.Library, Pivot: input.Pivot, Query: node, Sorts: input.Sorts, snapshot: dbwork.Snapshot(inner)}
			state, err = read.NewBrowseSelectionBatch(BrowseRequest{}, input.Viewer, bound, basis)
			return selectionPreparationError(err)
		})
		if err != nil {
			return nil, selectionPreparationError(err)
		}
		return &selectionPreparationComputation{service: &root, state: state, check: check, factoryBytes: factoryBytes, instant: instant, memoScope: memoScope}, nil
	}}
	handle, err := store.Submit(s.Context(), access.Caller, key, plan)
	if err != nil {
		return BrowseSelectionPreparing{}, err
	}
	// Recheck after admission in this snapshot. Fresh post-snapshot authority and
	// basis checks remain the HTTP caller's last step before exposing the page.
	if err = access.Check(s); err != nil {
		_ = store.Cancel(context.WithoutCancel(s.Context()), access.Caller, handle)
		return BrowseSelectionPreparing{}, err
	}
	pending := selectionPreparationPending(string(handle))
	pending.basis = basis
	return pending, nil
}

func selectionPreparationPending(queryID string) BrowseSelectionPreparing {
	return BrowseSelectionPreparing{Variant: "preparing", Kind: "browse-selection-preparing", State: "preparing", QueryID: queryID, RetryAfterMs: BrowseSelectionRetryAfterMs}
}

// BrowseSelectionPreparationResult owns a delivery reservation through response
// serialization. A ready descriptor is reprojected with live command authority;
// it is never stored in readprep. Always defer Release on successful return.
type BrowseSelectionPreparationResult struct {
	Ready     *BrowseSelectionDescriptor
	Preparing *BrowseSelectionPreparing
	Basis     string
	lease     readprep.Result
}

func (r BrowseSelectionPreparationResult) Release() { r.lease.Release() }

func (s *Service) PollBrowseSelectionPreparation(library string, viewer Viewer, queryID string, access SelectionPreparationAccess, authority HomeActionAuthority) (BrowseSelectionPreparationResult, error) {
	store := s.ReadPreparations()
	if store == nil {
		return BrowseSelectionPreparationResult{}, readprep.ErrStale
	}
	if dbwork.Snapshot(s.Context()) == nil || access.Check == nil {
		return BrowseSelectionPreparationResult{}, sql.ErrTxDone
	}
	if err := access.Check(s); err != nil {
		return BrowseSelectionPreparationResult{}, err
	}
	result, err := store.Poll(s.Context(), access.Caller, readprep.Handle(queryID))
	if err != nil {
		result.Release()
		return BrowseSelectionPreparationResult{}, err
	}
	retain := false
	defer func() {
		if !retain {
			result.Release()
		}
	}()
	if err = s.CheckBrowseSelectionPreparationBasis(library, viewer, result.Basis, access); err != nil {
		return BrowseSelectionPreparationResult{}, err
	}
	out := BrowseSelectionPreparationResult{Basis: result.Basis, lease: result}
	if result.State == readprep.Preparing {
		if err = result.ResizeDelivery(4096); err != nil {
			return BrowseSelectionPreparationResult{}, err
		}
		pending := selectionPreparationPending(queryID)
		out.Preparing = &pending
	} else {
		if err = result.ResizeDelivery(int64(8*len(result.Payload) + 65536)); err != nil {
			return BrowseSelectionPreparationResult{}, err
		}
		var content selectionPreparationContent
		if err = json.Unmarshal(result.Payload, &content); err != nil {
			return BrowseSelectionPreparationResult{}, err
		}
		if content.Ordered && len(content.Sorts) == 1 && content.Sorts[0].Field == "forYou" && content.Profile != viewer.Profile {
			return BrowseSelectionPreparationResult{}, readprep.ErrStale
		}
		if err = s.checkSmallForYouActivityContent(content, func(n int64) error { return result.ResizeDelivery(int64(8*len(result.Payload)+65536) + n) }); err != nil {
			return BrowseSelectionPreparationResult{}, selectionPreparationError(err)
		}
		if content.Library != library || content.Pivot == "" {
			return BrowseSelectionPreparationResult{}, readprep.ErrStale
		}
		for _, counts := range content.Counts {
			if counts[0] < 0 || counts[1] < 0 || counts[1] > counts[0] {
				return BrowseSelectionPreparationResult{}, readprep.ErrInvalid
			}
		}
		var sorts []byte
		sorts, err = json.Marshal(content.Sorts)
		if err != nil {
			return BrowseSelectionPreparationResult{}, err
		}
		var node *BrowseNode
		if err = json.Unmarshal(content.Query, &node); err != nil {
			return BrowseSelectionPreparationResult{}, err
		}
		var libraryKind string
		if err = s.read().QueryRow(`SELECT kind FROM libraries WHERE id=?`, library).Scan(&libraryKind); err != nil {
			return BrowseSelectionPreparationResult{}, err
		}
		pivot, ok := pivotForKind(libraryKind, content.Pivot)
		if !ok {
			return BrowseSelectionPreparationResult{}, readprep.ErrInvalid
		}
		// The ordinary page revision includes private caller authority. Rebind
		// it in this snapshot rather than retaining the first submitter's fence.
		revision, err := s.browseRevision(BrowseRequest{Library: library, Profile: viewer.Profile, ViewerFence: viewer.Fence, Query: node, Restrictions: viewer.EffectiveRestrictions()}, pivot, content.Sorts)
		if err != nil {
			return BrowseSelectionPreparationResult{}, err
		}
		state := &BrowseSelectionBatchState{done: true, counts: content.Counts, plan: &selectionBatchPlan{library: content.Library, pivot: content.Pivot, revision: revision, query: content.Query, sorts: sorts, witness: result.Basis, localSearch: selectionBatchHasLocalSearch(node)}}
		descriptor, err := s.FinishBrowseSelectionBatch(state, func(read *Service) (string, error) {
			return read.browseSelectionWitness(library, viewer.Profile, viewer.EffectiveRestrictions())
		}, authority)
		if err != nil {
			return BrowseSelectionPreparationResult{}, selectionPreparationError(err)
		}
		descriptor.Selector.Query.SelectionBasis = result.Basis
		out.Ready = &descriptor
	}
	if err = s.CheckBrowseSelectionPreparationBasis(library, viewer, result.Basis, access); err != nil {
		return BrowseSelectionPreparationResult{}, err
	}
	retain = true
	return out, nil
}

// CheckBrowseSelectionPreparationBasis is also called in a fresh snapshot after
// composition and immediately before serialization of a poll result.
func (s *Service) CheckBrowseSelectionPreparationBasis(library string, viewer Viewer, basis string, access SelectionPreparationAccess) error {
	if access.Check == nil {
		return readprep.ErrInvalid
	}
	if err := access.Check(s); err != nil {
		return err
	}
	current, err := s.browseSelectionWitness(library, viewer.Profile, viewer.EffectiveRestrictions())
	if err != nil {
		return selectionPreparationError(err)
	}
	if basis == "" || current != basis {
		return readprep.ErrStale
	}
	return nil
}

func (s *Service) CancelBrowseSelectionPreparation(queryID string, access SelectionPreparationAccess) error {
	if access.Check == nil {
		return readprep.ErrInvalid
	}
	if err := access.Check(s); err != nil {
		return err
	}
	if store := s.ReadPreparations(); store != nil {
		return store.Cancel(s.Context(), access.Caller, readprep.Handle(queryID))
	}
	return nil
}
