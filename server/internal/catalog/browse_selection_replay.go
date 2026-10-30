package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

const browseSelectionReplayDomain = "browse-selection-replay-local-v1-" + sqlite.SearchMatchVersion
const browseSelectionReplaySize = 128

type selectionReplaySpec struct {
	Selection selectionPreparationSpec
	After     int64
}

type selectionReplayContent struct {
	IDs      []int64
	EOF      bool
	SortKeys map[int64]string
}

// BrowseSelectionReplayPage is a bounded delivery, never a complete hit cache.
// EOF is true only after a charged raw EOF probe on the held complete witness.
// Release must follow the accepting command write (also on write failure).
type BrowseSelectionReplayPage struct {
	IDs            []int64
	EOF, Preparing bool
	Basis          string
	lease          readprep.Result
	handle         readprep.Handle
	SortKeys       map[int64]string
}

func (p BrowseSelectionReplayPage) Release() { p.lease.Release() }

type selectionReplayComputation struct {
	service                    *Service
	state                      *BrowseSelectionBatchState
	check                      func(*Service) error
	factoryBytes, payloadBytes int64
	ids                        [browseSelectionReplaySize]int64
	count                      int
	order                      *SmallSelectionOrder
	sortKeys                   map[int64]string
	collected                  bool
}

func (c *selectionReplayComputation) AccountedBytes() int64 {
	n := c.factoryBytes + int64(unsafe.Sizeof(*c)) + int64(c.state.Bytes()+c.order.Bytes()) + c.payloadBytes
	for _, key := range c.sortKeys {
		n += 128 + int64(len(key))
	}
	return n
}

func (c *selectionReplayComputation) Step(ctx context.Context, budget *readprep.Budget) (readprep.StepResult, error) {
	// Both states, factory captures, decoded policy, row buffers and serialization
	// overlap are charged before allocating. Scalars never survive the quantum.
	peak := c.AccountedBytes() + int64(c.state.Bytes()) + int64(4*len(c.state.plan.policy)) + SelectionBatchMaximumBytes + 65536
	if err := budget.Resize(peak); err != nil {
		return readprep.StepResult{}, err
	}
	p := c.state.plan
	witness := func(read *Service) (string, error) {
		if err := c.check(read); err != nil {
			return "", readprep.ErrStale
		}
		var viewer Viewer
		if err := json.Unmarshal(p.policy, &viewer); err != nil {
			return "", err
		}
		return read.browseSelectionWitness(p.library, viewer.Profile, viewer.EffectiveRestrictions())
	}
	if !c.collected {
		// Each reducer quantum can emit 128 hits. Return a page on the first nonempty
		// quantum rather than accumulate across quanta and risk dropping a suffix.
		next, work, stepErr := c.service.StepBrowseSelectionBatch(ctx, c.state, witness, SelectionBatchLimits{ReserveTransient: func(transient int64) error {
			return budget.Resize(peak + max(int64(0), transient-SelectionBatchMaximumBytes))
		}})
		if stepErr != nil {
			return readprep.StepResult{}, selectionPreparationError(stepErr)
		}
		c.state = next
		c.count = work.MatchCount
		copy(c.ids[:], work.Matches[:work.MatchCount])
		if c.count == 0 && !next.Complete() {
			return readprep.StepResult{}, nil
		}
		c.collected = true
	}
	pendingOrder, err := c.prepareGroupKeys(ctx, budget, witness)
	if err != nil {
		return readprep.StepResult{}, err
	}
	if pendingOrder {
		return readprep.StepResult{}, nil
	}
	encodingBound := int64(32768)
	for _, key := range c.sortKeys {
		encodingBound += 4 * int64(len(key))
	}
	if err = budget.Resize(c.AccountedBytes() + encodingBound); err != nil {
		return readprep.StepResult{}, err
	}
	encoded, err := json.Marshal(selectionReplayContent{IDs: c.ids[:c.count], EOF: c.state.Complete(), SortKeys: c.sortKeys})
	if err != nil {
		return readprep.StepResult{}, err
	}
	c.payloadBytes = int64(cap(encoded))
	c.state = nil
	return readprep.StepResult{Done: true, Payload: encoded}, nil
}

// SubmitBrowseSelectionReplay accepts a manually bound, validated query in a
// short snapshot. It never invokes BrowseEntities, filters a page, or counts it.
// Only the worker constructs and advances the replay state. The original query,
// effective policy, full witness, algorithm and accepted ID form the full key.
func (s *Service) SubmitBrowseSelectionReplay(request BrowseRequest, viewer Viewer, bound BoundBrowseQuery, after int64, access SelectionPreparationAccess) (BrowseSelectionPreparing, error) {
	return s.submitBrowseSelectionReplay(request, viewer, bound, after, access, false)
}

func (s *Service) submitBrowseSelectionReplay(request BrowseRequest, viewer Viewer, bound BoundBrowseQuery, after int64, access SelectionPreparationAccess, reuseHandle bool) (BrowseSelectionPreparing, error) {
	store := s.ReadPreparations()
	if store == nil {
		return BrowseSelectionPreparing{}, readprep.ErrCapacity
	}
	if dbwork.Snapshot(s.Context()) == nil || bound.snapshot != dbwork.Snapshot(s.Context()) {
		return BrowseSelectionPreparing{}, sql.ErrTxDone
	}
	if after < 0 || access.Caller == "" || access.Check == nil || access.CapturedBytes < 0 {
		return BrowseSelectionPreparing{}, readprep.ErrInvalid
	}
	if err := access.Check(s); err != nil {
		return BrowseSelectionPreparing{}, err
	}
	if len(request.Rows) > 0 || len(request.RowSources) > 0 || !viewer.AllowsLibrary(bound.Library) || !browseQueryReplayable(bound.Query, bound.Sorts) {
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
	spec, err := json.Marshal(selectionReplaySpec{Selection: selectionPreparationSpec{Library: bound.Library, Pivot: bound.Pivot, Revision: bound.Revision, Query: query, Sorts: bound.Sorts, Viewer: viewer}, After: after})
	if err != nil {
		return BrowseSelectionPreparing{}, err
	}
	if len(spec) > SelectionBatchMaximumBytes {
		return BrowseSelectionPreparing{}, readprep.ErrCapacity
	}
	key := readprep.Key{Namespace: browseSelectionReplayDomain, Basis: basis, Canonical: string(spec)}
	root := *s
	root.rctx = nil
	root.readTx = nil
	root.recSources = nil
	root.recComposition = nil
	root.recRestrictions = identity.ContentRestrictions{}
	check := access.Check
	factoryBytes := int64(cap(spec)) + access.CapturedBytes + int64(unsafe.Sizeof(root)) + 2048
	plan := readprep.Plan{ReuseHandle: reuseHandle, InitialBytes: factoryBytes, ColdSearch: selectionBatchHasLocalSearch(bound.Query), ShouldYield: dbwork.ColdReadShouldYield, New: func(ctx context.Context, budget *readprep.Budget) (readprep.Computation, error) {
		if err := budget.Resize(factoryBytes + int64(4*cap(spec)) + 2*SelectionBatchMaximumBytes); err != nil {
			return nil, err
		}
		var input selectionReplaySpec
		if err := json.Unmarshal(spec, &input); err != nil {
			return nil, err
		}
		var state *BrowseSelectionBatchState
		err := dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassMaintenance), root.db, func(inner context.Context) error {
			read := root.WithContext(inner)
			if err := check(read); err != nil {
				return readprep.ErrStale
			}
			current, err := read.browseSelectionWitness(input.Selection.Library, input.Selection.Viewer.Profile, input.Selection.Viewer.EffectiveRestrictions())
			if err != nil {
				return err
			}
			if current != basis {
				return readprep.ErrStale
			}
			var node *BrowseNode
			if err = json.Unmarshal(input.Selection.Query, &node); err != nil {
				return err
			}
			bound := BoundBrowseQuery{Library: input.Selection.Library, Pivot: input.Selection.Pivot, Revision: input.Selection.Revision, Query: node, Sorts: input.Selection.Sorts, snapshot: dbwork.Snapshot(inner)}
			state, err = read.NewBrowseSelectionBatch(BrowseRequest{}, input.Selection.Viewer, bound, basis)
			if err != nil {
				return err
			}
			state = state.ReplayAfter(input.After)
			return nil
		})
		if err != nil {
			return nil, selectionPreparationError(err)
		}
		return &selectionReplayComputation{service: &root, state: state, check: check, factoryBytes: factoryBytes}, nil
	}}
	handle, err := store.Submit(s.Context(), access.Caller, key, plan)
	if err != nil {
		return BrowseSelectionPreparing{}, err
	}
	if err = access.Check(s); err != nil {
		_ = store.Cancel(context.WithoutCancel(s.Context()), access.Caller, handle)
		return BrowseSelectionPreparing{}, err
	}
	pending := selectionPreparationPending(string(handle))
	pending.basis = basis
	return pending, nil
}

// PollBrowseSelectionReplay checks live authority, effective policy and witness
// before delivering even an empty/EOF page. Handle identity alone grants nothing.
func (s *Service) PollBrowseSelectionReplay(library string, viewer Viewer, queryID string, access SelectionPreparationAccess) (out BrowseSelectionReplayPage, err error) {
	store := s.ReadPreparations()
	if store == nil {
		return out, readprep.ErrStale
	}
	if dbwork.Snapshot(s.Context()) == nil || access.Check == nil {
		return out, sql.ErrTxDone
	}
	if err = access.Check(s); err != nil {
		return out, err
	}
	result, err := store.Poll(s.Context(), access.Caller, readprep.Handle(queryID))
	if err != nil {
		return out, err
	}
	retain := false
	defer func() {
		if !retain {
			result.Release()
		}
	}()
	if result.Key.Namespace != browseSelectionReplayDomain {
		return out, readprep.ErrStale
	}
	if err = result.ResizeDelivery(int64(8*len(result.Key.Canonical) + 8*len(result.Payload) + 16384)); err != nil {
		return out, err
	}
	var spec selectionReplaySpec
	if err = json.Unmarshal([]byte(result.Key.Canonical), &spec); err != nil {
		return out, readprep.ErrInvalid
	}
	// A freshly supplied policy must agree byte-for-byte with the captured one.
	policy, _ := json.Marshal(viewer)
	original, _ := json.Marshal(spec.Selection.Viewer)
	if library != spec.Selection.Library || string(policy) != string(original) {
		return out, readprep.ErrStale
	}
	if err = s.CheckBrowseSelectionPreparationBasis(library, viewer, result.Basis, access); err != nil {
		return out, err
	}
	out = BrowseSelectionReplayPage{Basis: result.Basis, Preparing: result.State == readprep.Preparing, lease: result, handle: readprep.Handle(queryID)}
	if !out.Preparing {
		var content selectionReplayContent
		if err = json.Unmarshal(result.Payload, &content); err != nil {
			return BrowseSelectionReplayPage{}, readprep.ErrInvalid
		}
		if len(content.IDs) > browseSelectionReplaySize || len(content.IDs) == 0 && !content.EOF {
			return BrowseSelectionReplayPage{}, readprep.ErrInvalid
		}
		last := spec.After
		for _, id := range content.IDs {
			if id <= last {
				return BrowseSelectionReplayPage{}, readprep.ErrInvalid
			}
			last = id
		}
		out.IDs = content.IDs
		out.SortKeys = content.SortKeys
		out.EOF = content.EOF
	}
	if err = s.CheckBrowseSelectionPreparationBasis(library, viewer, result.Basis, access); err != nil {
		return BrowseSelectionReplayPage{}, err
	}
	retain = true
	return out, nil
}

// A missing/abandoned handle is restart loss, not lost accepted command work.
func selectionReplayStale(err error) bool { return errors.Is(err, readprep.ErrStale) }
