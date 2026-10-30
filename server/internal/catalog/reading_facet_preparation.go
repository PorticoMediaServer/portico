package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// ReadingFacetAccess must reconstruct live session/library authority in ctx's
// snapshot. It must not capture a request, context, transaction or catalog Service.
// Return an error if the original full principal/authority scope has changed.
type ReadingFacetAccess struct {
	Check func(context.Context) (Viewer, error)
	// RetainedBytes includes all captured strings, structs and backing arrays.
	RetainedBytes int64
}

const readingFacetNamespace = "reading-facet-v1"

// ReadingFacetBasis is the complete current source witness, in the caller's
// snapshot. Authorization remains the caller's responsibility, including on hits.
func (s *Service) ReadingFacetBasis(viewer Viewer, library string) (string, error) {
	if !viewer.AllowsLibrary(library) {
		return "", sql.ErrNoRows
	}
	return s.browseExactWitness(library, viewer.Profile, viewer.EffectiveRestrictions())
}

// PlanReadingFacets captures the canonical request and basis in one admission
// snapshot. Source construction runs after reservation in a fresh snapshot with
// exactly that basis. The plan retains bounded text, never an AST or Service. It
// uses only the process store installed by the composition root. This is a hook
// for the all-client cohort; Facets deliberately keeps its original dispatch.
func (s *Service) PlanReadingFacets(viewer Viewer, request FacetRequest, instant time.Time, access ReadingFacetAccess) (readprep.Key, readprep.Plan, error) {
	var key readprep.Key
	if access.Check == nil || access.RetainedBytes < 0 || s.ReadPreparations() == nil || instant.IsZero() {
		return key, readprep.Plan{}, readprep.ErrInvalid
	}
	witness, err := s.ReadingFacetBasis(viewer, request.Library)
	if err != nil {
		return key, readprep.Plan{}, err
	}
	if request.Field != "author" && request.Field != "series" {
		return key, readprep.Plan{}, browseIssue("field", "Reading counts author and series facets")
	}
	if request.Sort != "" && request.Sort != "title" && request.Sort != "count" {
		return key, readprep.Plan{}, browseIssue("sort", "Reading facets sort by title or count")
	}
	lib, err := s.library(request.Library)
	if err != nil {
		return key, readprep.Plan{}, err
	}
	if request.Pivot != "" {
		if _, ok := pivotForKind(lib.Kind, request.Pivot); !ok {
			return key, readprep.Plan{}, ErrBrowsePivot
		}
	}
	// Validate bounded request text before returning a handle, preserving the
	// foreground validation errors. This creates no source/cursor/policy buffer;
	// source construction itself waits for the admitted factory reservation.
	request.Query, err = canonicalReadingFacetQuery(request.Query)
	if err != nil {
		return key, readprep.Plan{}, err
	}
	request.Group, err = canonicalReadingFacetQuery(request.Group)
	if err != nil {
		return key, readprep.Plan{}, err
	}
	if len(request.Q) > 100 || request.Offset < 0 {
		return key, readprep.Plan{}, browseIssue("q", "facet window is invalid")
	}
	if request.Sort == "" {
		request.Sort = "title"
	}
	if request.Limit < 1 || request.Limit > browseFacetLimit {
		request.Limit = browseFacetLimit
	}
	request.Q = compactcatalog.ReadingValueKey(request.Q)
	// The bounded canonical request is immutable queue/factory input. No AST,
	// compiled source, Service or snapshot is retained before store admission.
	spec, err := json.Marshal(struct {
		Library, Field, Pivot, Q, Sort string
		Limit, Offset                  int
		Query, Group                   *BrowseNode
	}{request.Library, request.Field, request.Pivot, request.Q, request.Sort, request.Limit, request.Offset, request.Query, request.Group})
	if err != nil {
		return key, readprep.Plan{}, err
	}
	if len(spec) > 2*BrowseMaximumBytes {
		return key, readprep.Plan{}, browseIssue("query", "facet preparation specification is too large")
	}
	relativeAt := ""
	if browseRelative(browseAnd(request.Group, request.Query)) {
		relativeAt = instant.UTC().Format(time.RFC3339Nano)
	}
	canonical, err := json.Marshal(struct {
		Request json.RawMessage
		Policy  identity.ContentRestrictions
		Instant string
	}{spec, viewer.EffectiveRestrictions(), relativeAt})
	if err != nil {
		return key, readprep.Plan{}, err
	}
	if len(canonical) > 4*BrowseMaximumBytes {
		return key, readprep.Plan{}, readprep.ErrCapacity
	}
	key = readprep.Key{Namespace: readingFacetNamespace, Basis: witness, Canonical: string(canonical)}
	w := &readingFacetComputation{db: s.db, mobi: &s.state.readingMobi, access: access, basis: witness, spec: spec, instant: instant, limit: int(s.ReadPreparations().MaxBytes())}
	return key, readprep.Plan{InitialBytes: w.AccountedBytes(), New: func(ctx context.Context, b *readprep.Budget) (readprep.Computation, error) {
		// Reserve source construction/decoder/policy/statement scratch before
		// allocations and reads. The factory closure retains only w, whose full
		// capture stays charged through completion by AccountedBytes.
		if err := b.Resize(w.AccountedBytes() + 2*readingFacetByteLimit + 8*BrowseMaximumBytes + 8192); err != nil {
			return nil, err
		}
		if err := w.initialize(ctx); err != nil {
			return nil, readingFacetWorkError(err)
		}
		if err := b.Resize(w.AccountedBytes()); err != nil {
			return nil, err
		}
		return w, nil
	}}, nil
}

func canonicalReadingFacetQuery(node *BrowseNode) (*BrowseNode, error) {
	if node == nil {
		return nil, nil
	}
	raw, err := json.Marshal(node)
	if err != nil {
		return nil, err
	}
	parsed, err := ParseBrowseQuery(raw, "query")
	if err != nil {
		return nil, err
	}
	var check func(*BrowseNode) error
	check = func(n *BrowseNode) error {
		if n == nil {
			return nil
		}
		if n.Field != "" && !readingBrowseField(n.Field) {
			return browseIssue("query.field", "field is not published for Reading")
		}
		for i := range n.All {
			if err := check(&n.All[i]); err != nil {
				return err
			}
		}
		for i := range n.Any {
			if err := check(&n.Any[i]); err != nil {
				return err
			}
		}
		return check(n.Not)
	}
	return parsed, check(parsed)
}

type readingFacetComputation struct {
	db        *sql.DB
	mobi      *atomic.Bool
	access    ReadingFacetAccess
	basis     string
	spec      []byte
	instant   time.Time
	limit     int
	reduction *readingFacetReduction
	payload   []byte
}

func (w *readingFacetComputation) AccountedBytes() int64 {
	n := int64(unsafe.Sizeof(*w)) + w.access.RetainedBytes + int64(len(w.basis)+cap(w.spec)+cap(w.payload))
	if w.reduction != nil {
		n += int64(w.reduction.bytes())
	}
	return n
}

func (w *readingFacetComputation) initialize(ctx context.Context) error {
	var request FacetRequest
	if err := json.Unmarshal(w.spec, &request); err != nil {
		return err
	}
	s := &Service{db: w.db, state: &serviceState{}}
	s.ConfigureReadingMobi(w.mobi.Load())
	err := dbwork.WithReadSnapshot(ctx, w.db, func(snapshot context.Context) error {
		viewer, err := w.access.Check(snapshot)
		if err != nil {
			return err
		}
		ss := s.WithContext(snapshot)
		witness, err := ss.ReadingFacetBasis(viewer, request.Library)
		if err != nil {
			return err
		}
		if witness != w.basis {
			return readprep.ErrStale
		}
		input, err := ss.newReadingFacetSQLInput(viewer, request, w.instant)
		if err != nil {
			return err
		}
		w.reduction, err = newReadingFacetReduction(request, witness, input, w.limit)
		return err
	})
	if err == nil {
		w.spec = nil
	}
	return err
}

func (w *readingFacetComputation) Step(ctx context.Context, b *readprep.Budget) (readprep.StepResult, error) {
	r := w.reduction
	// This Service exists only for this turn. No request, read transaction,
	// cursor, budget or Service escapes the call.
	s := &Service{db: w.db, state: &serviceState{}}
	s.ConfigureReadingMobi(w.mobi.Load())
	done := false
	err := dbwork.WithReadSnapshot(ctx, w.db, func(snapshot context.Context) error {
		viewer, err := w.access.Check(snapshot)
		if err != nil {
			return err
		}
		ss := s.WithContext(snapshot)
		witness, err := ss.ReadingFacetBasis(viewer, r.request.Library)
		if err != nil {
			return err
		}
		r.reservePeak = func(peak int) error {
			return b.Resize(int64(unsafe.Sizeof(*w)) + w.access.RetainedBytes + int64(len(w.basis)) + int64(peak))
		}
		defer func() { r.reservePeak = nil }()
		done, err = r.step(ss, witness, newReadingFacetBudget(readingFacetRawLimit, readingFacetByteLimit, readingFacetQuantum))
		return err
	})
	if err != nil {
		r.fail(err)
		return readprep.StepResult{}, readingFacetWorkError(err)
	}
	// A fresh snapshot catches authority/witness changes after the read turn,
	// including at final EOF. HTTP repeats this check at every exposure too.
	err = dbwork.WithReadSnapshot(ctx, w.db, func(snapshot context.Context) error {
		s.ConfigureReadingMobi(w.mobi.Load())
		viewer, err := w.access.Check(snapshot)
		if err != nil {
			return err
		}
		witness, err := s.WithContext(snapshot).ReadingFacetBasis(viewer, r.request.Library)
		if err == nil && witness != r.witness {
			err = ErrReadingProjectionChanged
		}
		return err
	})
	if err != nil {
		r.fail(err)
		return readprep.StepResult{}, readingFacetWorkError(err)
	}
	if !done {
		return readprep.StepResult{}, b.Resize(w.AccountedBytes())
	}
	// Oversize legal input was consumed losslessly. If retained state/output
	// cannot fit, terminal typed capacity is honest; no giant cache or loop.
	if !r.cacheable() {
		return readprep.StepResult{}, readprep.ErrCapacity
	}
	// Reserve the projected DTO and worst-case escaped JSON plus encoder copies
	// BEFORE allocating them (JSON text can expand to six bytes per input byte).
	if err := b.Resize(w.AccountedBytes() + int64(r.resultBytes())*24 + 4096); err != nil {
		return readprep.StepResult{}, err
	}
	page, ok := r.result()
	if !ok {
		return readprep.StepResult{}, readprep.ErrInvalid
	}
	w.payload, err = json.Marshal(page)
	if err != nil {
		return readprep.StepResult{}, err
	}
	// The reducer charges the DTO alongside state; payload backing capacity is
	// charged separately until the store accepts exclusive ownership.
	if err := b.Resize(w.AccountedBytes()); err != nil {
		w.payload = nil
		return readprep.StepResult{}, err
	}
	return readprep.StepResult{Done: true, Payload: w.payload}, nil
}

func readingFacetWorkError(err error) error {
	if errors.Is(err, errReadingFacetCapacity) {
		return readprep.ErrCapacity
	}
	if errors.Is(err, ErrReadingProjectionChanged) || errors.Is(err, ErrStaleContinuation) || errors.Is(err, identity.ErrUnauthorized) || errors.Is(err, identity.ErrForbidden) || errors.Is(err, sql.ErrNoRows) {
		return readprep.ErrStale
	}
	return err
}
