package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

const BulkPreviewPreparationNamespace = "metadata-preview-v2"

// BulkPreviewPreparationAccess captures immutable input and live session checks,
// never an HTTP request/context or a snapshot. CapturedBytes includes closures,
// request backing allocations and their decoding/canonicalization overlap.
type BulkPreviewPreparationAccess struct {
	Source        BulkPreviewSource
	Authorize     func(context.Context, *sql.Tx) error
	CapturedBytes int64
	// NewWalk selects the bounded shared predicate walker. Source then remains
	// a stateless, repeatable basis check and never recompiles membership SQL.
	NewWalk func(context.Context, *sql.Tx) (*catalog.BulkMetadataSelectionWalk, error)
}

type bulkPreviewReduction struct {
	s                            *Service
	source                       BulkPreviewSource
	authorize                    func(context.Context, *sql.Tx) error
	aggregate                    bulkPreviewAggregate
	out                          BulkMetadataPreview
	after                        int64
	authority                    uint64
	fence, revision, expected    string
	initialized, exhausted, done bool
	transient                    int64
	readPage                     func(context.Context, *sql.Tx, catalog.BulkMetadataSelection) (bulkPreviewPage, error)
	accumulate                   func(context.Context, *BulkMetadataPreview, bulkPreviewPage) error
	fields                       func(context.Context, []RepairFieldSpec) (map[string]BulkPreviewField, error)
}

func newBulkPreviewReduction(s *Service, source BulkPreviewSource, authorize func(context.Context, *sql.Tx) error, reserve func(int64) error, expected string) (*bulkPreviewReduction, error) {
	if s == nil || source == nil || authorize == nil {
		return nil, ErrRepairInput
	}
	r := &bulkPreviewReduction{s: s, source: source, authorize: authorize, authority: dbwork.AuthorityGeneration(), expected: expected}
	r.aggregate.reserve = reserve
	return r, nil
}

func (r *bulkPreviewReduction) basis() string {
	b, _ := json.Marshal([]any{r.authority, r.fence, r.revision})
	return string(b)
}

func (r *bulkPreviewReduction) read(ctx context.Context, final bool) (page bulkPreviewPage, next catalog.BulkMetadataSelectionPage, err error) {
	tx := dbwork.Snapshot(ctx)
	if err = r.authorize(ctx, tx); err != nil {
		return
	}
	if dbwork.AuthorityGeneration() != r.authority {
		err = catalog.ErrStaleContinuation
		return
	}
	next, err = r.source(ctx, tx, r.after)
	if (r.initialized || r.expected != "") && (r.initialized && next.Fence != "" && next.Fence != r.fence || errors.Is(err, sql.ErrNoRows)) {
		err = catalog.ErrStaleContinuation
	}
	if err != nil {
		return
	}
	if !r.initialized {
		r.fence, r.revision = strings.Clone(next.Fence), strings.Clone(next.Selection.Revision)
		r.initialized = true
		if r.expected != "" && r.expected != r.basis() {
			err = catalog.ErrStaleContinuation
			return
		}
	}
	if next.Fence != r.fence || next.Selection.Revision != r.revision {
		err = catalog.ErrStaleContinuation
		return
	}
	if !final && strings.TrimSpace(next.Selection.SQL) != "" {
		if r.readPage != nil {
			page, err = r.readPage(ctx, tx, next.Selection)
		} else {
			page, err = r.s.bulkPreviewPageTx(ctx, tx, next.Selection)
		}
		if err != nil {
			return
		}
	}
	checked, e := r.source(ctx, tx, r.after)
	if e != nil {
		err = e
		return
	}
	if checked.Fence != r.fence || checked.Selection.Revision != r.revision {
		err = catalog.ErrStaleContinuation
		return
	}
	if err = r.authorize(ctx, tx); err != nil {
		return
	}
	if dbwork.AuthorityGeneration() != r.authority {
		err = catalog.ErrStaleContinuation
	}
	return
}

func (r *bulkPreviewReduction) step(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if dbwork.Snapshot(ctx) != nil || r.done {
		return ErrRepairInput
	}
	if r.exhausted {
		if r.out.Total == 0 {
			return ErrRepairInput
		}
		var err error
		if r.fields != nil {
			r.out.Fields, err = r.fields(ctx, r.out.Schema)
		} else {
			r.out.Fields, err = r.aggregate.result(r.out.Schema)
		}
		if err != nil {
			return err
		}
		r.out.SelectionRevision = r.revision
		// Finish reduction first, then validate in a new short snapshot. Cached
		// deliveries recheck this same complete basis again at the HTTP boundary.
		err = dbwork.WithReadSnapshot(ctx, r.s.db, func(inner context.Context) error { _, _, e := r.read(inner, true); return e })
		if err != nil {
			return err
		}
		if dbwork.AuthorityGeneration() != r.authority {
			return catalog.ErrStaleContinuation
		}
		r.done = true
		return nil
	}
	var page bulkPreviewPage
	var next catalog.BulkMetadataSelectionPage
	err := dbwork.WithReadSnapshot(ctx, r.s.db, func(inner context.Context) error { var e error; page, next, e = r.read(inner, false); return e })
	if err != nil {
		return err
	}
	if dbwork.AuthorityGeneration() != r.authority {
		return catalog.ErrStaleContinuation
	}
	if err = r.addPage(ctx, page); err != nil {
		return err
	}
	if next.Done {
		r.exhausted = true
	} else {
		if next.After <= r.after {
			return ErrRepairInput
		}
		r.after = next.After
	}
	return nil
}

func (r *bulkPreviewReduction) addPage(ctx context.Context, page bulkPreviewPage) error {
	var err error
	if page.preview.Total > 0 {
		if r.out.Total == 0 {
			r.out = page.preview
			r.out.Total = 0
			r.out.Targets = []BulkPreviewTarget{}
		}
		if r.out.ContentKind != page.preview.ContentKind {
			return ErrRepairInput
		}
		if r.accumulate != nil {
			if err = r.accumulate(ctx, &r.out, page); err != nil {
				return err
			}
		} else {
			// The singleton editor/vector is the existing indivisible O(B) source
			// exception. It dies this step; retained keys/output are admitted first.
			if err = r.aggregate.add(ctx, page.observations); err != nil {
				return err
			}
			if len(page.preview.Targets) > catalog.BulkMetadataPreviewTargetLimit-len(r.out.Targets) {
				return ErrRepairInput
			}
			if err = r.aggregate.charge(1024 * int64(len(page.preview.Targets))); err != nil {
				return err
			}
			if err = previewAddCount(&r.out.Total, page.preview.Total); err != nil {
				return err
			}
			r.out.Targets = append(r.out.Targets, page.preview.Targets...)
		}
	}
	return nil
}

// PlanBulkPreview admits only a tiny descriptor and a single candidate per
// worker quantum. Source MUST advance raw candidates before predicates (the
// production adapter uses BulkMetadataSelectionUnitTx). No whole-query census
// or value dictionary is constructed on the foreground admission path.
func (s *Service) PlanBulkPreview(ctx context.Context, canonical string, access BulkPreviewPreparationAccess) (readprep.Key, readprep.Plan, error) {
	if dbwork.Snapshot(ctx) != nil || canonical == "" || access.Source == nil || access.Authorize == nil || access.CapturedBytes < 0 {
		return readprep.Key{}, readprep.Plan{}, ErrRepairInput
	}
	r, err := newBulkPreviewReduction(&Service{db: s.db}, access.Source, access.Authorize, nil, "")
	if err != nil {
		return readprep.Key{}, readprep.Plan{}, err
	}
	err = dbwork.WithReadSnapshot(ctx, s.db, func(inner context.Context) error { _, _, e := r.read(inner, true); return e })
	if err != nil {
		return readprep.Key{}, readprep.Plan{}, err
	}
	basis := r.basis()
	key := readprep.Key{Namespace: BulkPreviewPreparationNamespace, Basis: basis, Canonical: canonical}
	base := access.CapturedBytes + int64(len(canonical)+len(basis)) + 64<<10
	db := s.db
	plan := readprep.Plan{InitialBytes: base, ColdSearch: access.NewWalk != nil, ShouldYield: func() bool { return access.NewWalk != nil && dbwork.ColdReadShouldYield() }, New: func(ctx context.Context, b *readprep.Budget) (readprep.Computation, error) {
		w := &bulkPreviewComputation{base: base}
		state, err := newBulkPreviewReduction(&Service{db: db}, access.Source, access.Authorize, nil, basis)
		if err != nil {
			return nil, err
		}
		w.state = state
		if access.NewWalk != nil {
			// Descriptor decoding, immutable plan, old/clone state and bounded
			// proof-delivery overlap are admitted before constructing the walker.
			if err = b.Resize(base + 2*catalog.SelectionBatchMaximumBytes); err != nil {
				return nil, err
			}
			err = dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassMaintenance), db, func(inner context.Context) error {
				if _, _, err := state.read(inner, true); err != nil {
					return err
				}
				w.walk, err = access.NewWalk(inner, dbwork.Snapshot(inner))
				return err
			})
			if err != nil {
				return nil, bulkPreviewPreparationError(err)
			}
			if w.walk == nil {
				return nil, readprep.ErrInvalid
			}
		}
		return w, nil
	}}
	return key, plan, nil
}

type bulkPreviewComputation struct {
	state                 *bulkPreviewReduction
	base                  int64
	walk                  *catalog.BulkMetadataSelectionWalk
	matches               [128]int64
	matchCount, matchNext int
}

func (w *bulkPreviewComputation) AccountedBytes() int64 {
	return w.base + w.state.aggregate.used + w.state.transient + w.walk.Bytes()
}

func (w *bulkPreviewComputation) Step(ctx context.Context, b *readprep.Budget) (readprep.StepResult, error) {
	// Reserve ordinary singleton scratch before reading. An exceptional legal
	// source scalar may transiently exceed it, per the approved O(B) exception;
	// it is neither cached nor an exemption for retained aggregates or delivery.
	w.state.transient = 1 << 20
	if err := b.Resize(w.AccountedBytes()); err != nil {
		return readprep.StepResult{}, err
	}
	w.state.aggregate.reserve = func(n int64) error { return b.Resize(w.base + n + w.state.transient + w.walk.Bytes()) }
	var err error
	if w.walk != nil {
		err = w.stepWalk(ctx, b)
	} else {
		err = w.state.step(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia))
	}
	w.state.aggregate.reserve = nil
	if err != nil {
		return readprep.StepResult{}, bulkPreviewPreparationError(err)
	}
	w.state.transient = 0
	if !w.state.done {
		return readprep.StepResult{}, b.Resize(w.AccountedBytes())
	}
	// Conservative retained charges include up to 32 bytes/source byte for
	// escaped JSON and serialization overlap; reserve before marshaling.
	if err = b.Resize(w.AccountedBytes() + w.state.aggregate.used); err != nil {
		return readprep.StepResult{}, err
	}
	payload, err := json.Marshal(w.state.out)
	if err != nil {
		return readprep.StepResult{}, err
	}
	w.state.transient = int64(cap(payload))
	return readprep.StepResult{Done: true, Payload: payload}, nil
}

// readprep deliberately retains only its own finite error classes, not domain
// error graphs. Translate before it terminalizes background errors, preserving
// invalid input and stale authority/source semantics at the poll boundary.
func bulkPreviewPreparationError(err error) error {
	switch {
	case errors.Is(err, ErrRepairInput), errors.Is(err, catalog.ErrSelectionBatchUnsupported):
		return readprep.ErrInvalid
	case errors.Is(err, catalog.ErrSelectionBatchCapacity):
		return readprep.ErrCapacity
	case errors.Is(err, catalog.ErrStaleContinuation), errors.Is(err, catalog.ErrReadingProjectionChanged), errors.Is(err, catalog.ErrSelectionBatchStale), errors.Is(err, sql.ErrNoRows), errors.Is(err, identity.ErrForbidden), errors.Is(err, identity.ErrUnauthorized):
		return readprep.ErrStale
	default:
		return err
	}
}

// CheckBulkPreviewBasis is used on preparing and ready cache deliveries. A
// handle is not a grant; its original request is resolved against live scope.
func (s *Service) CheckBulkPreviewBasis(ctx context.Context, basis string, access BulkPreviewPreparationAccess) error {
	r, err := newBulkPreviewReduction(&Service{db: s.db}, access.Source, access.Authorize, nil, basis)
	if err != nil {
		return err
	}
	return dbwork.WithReadSnapshot(ctx, s.db, func(inner context.Context) error { _, _, e := r.read(inner, true); return e })
}

// Shared snapshot/final-fence orchestration for the released <=200 memory
// adapter. Its complete conversion reservation remains owned by its caller.
func (s *Service) bulkPreview(ctx context.Context, source BulkPreviewSource, authorize func(context.Context, *sql.Tx) error, readPage func(context.Context, *sql.Tx, catalog.BulkMetadataSelection) (bulkPreviewPage, error), accumulate func(context.Context, *BulkMetadataPreview, bulkPreviewPage) error, fields func(context.Context, []RepairFieldSpec) (map[string]BulkPreviewField, error)) (BulkMetadataPreview, error) {
	r, err := newBulkPreviewReduction(s, source, authorize, nil, "")
	if err != nil {
		return BulkMetadataPreview{}, err
	}
	r.readPage, r.accumulate, r.fields = readPage, accumulate, fields
	for !r.done {
		if err = r.step(ctx); err != nil {
			return BulkMetadataPreview{}, err
		}
	}
	return r.out, nil
}
