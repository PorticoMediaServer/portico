package read

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

type browseSnapshots interface {
	Read(context.Context, governor.Request, time.Duration, func(*governor.ReadScope) error) (governor.SnapshotResult, error)
	NativeRowReservation() int64
}

// BrowseReader owns the two-view publication protocol, never raw SQL handles.
// Its snapshot pool and exact admission requests come from trusted composition.
type BrowseReader struct {
	snapshots browseSnapshots
	pool      *governor.Snapshots
}

func NewBrowseReader(snapshots *governor.Snapshots) (*BrowseReader, error) {
	if snapshots == nil {
		return nil, ErrInput
	}
	return &BrowseReader{snapshots: snapshots, pool: snapshots}, nil
}

// BrowseOutput is issued by ConsumeForeground after LIVE and separate output
// admission, before any draft allocation.
// Composition keeps this memory-only grant charged through the last consumer,
// including encoding/client IO; Window neither cancels nor retires it. Reserve
// at least twice the snapshot's Go-copy budget for retained values/DTO overlap.
// This is prospective admission, not an RSS or serialization qualification.
type BrowseOutput struct {
	Authority *governor.Authority
	Grant     *governor.Grant
	Request   governor.Request
	live      *foregroundOutput
}

// BrowseReadResult records all physical lookup/publication windows and
// admission elapsed time. ConsumeForeground also measures the final consumer.
type BrowseReadResult struct {
	Lookup, Publication                     governor.SnapshotResult
	LookupWindows                           []governor.SnapshotResult
	LookupStatements, PublicationStatements int
	Refusal                                 *governor.Refusal
	Elapsed                                 time.Duration
	AdmissionElapsed                        time.Duration
	Attempts                                int
	PublicationWindows                      []governor.SnapshotResult
}

// Window prepares a private page, joins the lookup's physical cleanup, then
// validates current principal/library/policy/kind in a NEW snapshot. Exact
// common-fence equality binds the old business view to that publication view.
// Every denial, drift, admission/cleanup error or cancellation returns ZERO
// output. There is no production registration or runtime cutover here.
func (r *BrowseReader) Window(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in BrowseInput) (BrowsePage, BrowseReadResult, error) {
	if r == nil {
		return BrowsePage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, r.pool, in.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (BrowsePage, BrowseReadResult, error) {
		in.Now = at
		return r.window(ctx, request, budget, output, in)
	})
}

func (r *BrowseReader) window(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in BrowseInput) (page BrowsePage, result BrowseReadResult, err error) {
	if r == nil {
		return BrowsePage{}, BrowseReadResult{}, ErrInput
	}
	page, result, err = publishRead(ctx, r.snapshots, request, budget, output, in, []readStep[BrowsePage]{func(ctx context.Context, query browseQuery, f browseFence, draft *BrowsePage) error {
		var err error
		*draft, err = readBrowsePage(ctx, query, in, f)
		return err
	}})
	if err == nil {
		page.Statements = result.LookupStatements + result.PublicationStatements
	}
	return
}

// A readStep uses the remaining calls after any first-window fence.
// GV enforces eight calls per snapshot. No snapshot crosses a step.
type readStep[T any] func(context.Context, browseQuery, browseFence, *T) error

// publishRead implements the shared consolidated protocol: full shared fences
// first/final, root/source comparison at final publication, and one complete retry
// within the original deadline. Browse is its existing single-lookup subset.
func publishRead[T any](ctx context.Context, snapshots browseSnapshots, request governor.Request, budget time.Duration, output BrowseOutput, in BrowseInput, steps []readStep[T], roots ...rootRead) (page T, result BrowseReadResult, err error) {
	started := time.Now()
	if budget <= 0 || in.Now.IsZero() || len(roots) > 1 {
		return page, result, ErrInput
	}
	deadline := started.Add(min(budget, time.Second))
	if request.Deadline.Before(deadline) {
		deadline = request.Deadline
	}
	request.Deadline = deadline
	var copied int64
	attempts := 1
	if len(roots) > 0 {
		attempts = 2
	}
	for n := 0; n < attempts; n++ {
		attemptInput := in
		attemptInput.Now = in.Now.Add(time.Since(started))
		var one BrowseReadResult
		page, one, err = publishAttempt(ctx, snapshots, request, time.Until(deadline), output, attemptInput, steps, &copied, roots...)
		result.Lookup = one.Lookup
		result.Publication = one.Publication
		result.Refusal = one.Refusal
		result.LookupStatements += one.LookupStatements
		result.PublicationStatements += one.PublicationStatements
		result.LookupWindows = append(result.LookupWindows, one.LookupWindows...)
		if !one.Publication.Started.IsZero() {
			result.PublicationWindows = append(result.PublicationWindows, one.Publication)
		}
		result.Attempts = n + 1
		if !errors.Is(err, ErrStale) || ctx == nil || ctx.Err() != nil || !time.Now().Before(deadline) {
			break
		}
	}
	result.Elapsed = time.Since(started)
	if errors.Is(err, ErrStale) && len(roots) > 0 {
		result.Refusal = &governor.Refusal{Code: governor.Conflict}
		err = errors.Join(ErrStale, governor.ErrBusy)
	}
	return
}

type rootRead func(browseQuery) ([]any, error)

func publishAttempt[T any](ctx context.Context, snapshots browseSnapshots, request governor.Request, budget time.Duration, output BrowseOutput, in BrowseInput, steps []readStep[T], copied *int64, roots ...rootRead) (page T, result BrowseReadResult, err error) {

	var zero T
	started := time.Now()
	defer func() { result.Elapsed = time.Since(started) }()
	if snapshots == nil || len(steps) == 0 || ctx == nil || output.Authority == nil || output.Grant == nil || budget <= 0 ||
		len(in.Database) == 0 || len(in.Database) > 256 || len(in.TokenHash) == 0 || len(in.TokenHash) > 256 || in.Library < 0 || in.Kind <= 0 || in.Kind > 99 || in.Limit < 1 || in.Limit > MaxPage || in.Now.IsZero() {
		return zero, result, ErrInput
	}
	if recipe, e := resolveReadPolicy(in); e != nil {
		// Zero-library input keeps its original input-error precedence unless it
		// resolves to the one closed principal scope. No SQL has started here.
		if in.Library == 0 {
			return zero, result, ErrInput
		}
		return zero, result, e
	} else if !validReadPolicyScope(in, recipe) {
		return zero, result, ErrInput
	}
	if err := ctx.Err(); err != nil {
		return zero, result, err
	}
	goBytes := request.Need[governor.MemoryBytes] - snapshots.NativeRowReservation()
	if goBytes <= 0 || output.Request.Writer || output.Request.Owner != request.Owner || output.Request.Class != request.Class || output.Request.Need[governor.MemoryBytes]/2 < goBytes {
		return zero, result, governor.ErrSnapshotBudget
	}
	for resource, n := range output.Request.Need {
		if resource != int(governor.MemoryBytes) && n != 0 {
			return zero, result, governor.ErrSnapshotBudget
		}
	}
	if err := output.Authority.CheckContext(ctx, output.Grant, output.Request); err != nil {
		return zero, result, err
	}
	deadline := started.Add(min(budget, time.Second))
	for _, limit := range []time.Time{request.Deadline, output.Request.Deadline} {
		if limit.Before(deadline) {
			deadline = limit
		}
	}
	if limit, ok := ctx.Deadline(); ok && limit.Before(deadline) {
		deadline = limit
	}
	request.Deadline = deadline
	lookupContext, cancelLookup := context.WithDeadline(ctx, deadline)
	defer cancelLookup()
	var draft T
	var initial browseFence
	var fenceReady bool
	var source []any
	for stepIndex, step := range steps {
		var window governor.SnapshotResult
		if output.live != nil {
			if e := output.live.ctx.Err(); e != nil {
				return zero, result, e
			}
			if e := output.live.checkContext(lookupContext); e != nil {
				return zero, result, e
			}
		}
		window, err = snapshots.Read(lookupContext, request, time.Until(deadline), func(scope *governor.ReadScope) error {
			query := func(name string, args ...any) ([][]any, error) {
				if output.live != nil {
					if e := output.live.ctx.Err(); e != nil {
						return nil, e
					}
					if e := output.live.checkContext(scope.Context()); e != nil {
						return nil, e
					}
				}
				rows, count, e := queryReadScope(scope, name, args...)
				result.LookupStatements += count
				if e != nil {
					return nil, e
				}
				for _, row := range rows {
					*copied += 64 + int64(len(row))*32
					for _, v := range row {
						switch v := v.(type) {
						case string:
							*copied += int64(len(v)) * 2
						case []byte:
							*copied += int64(len(v)) * 3
						}
					}
				}
				if *copied > output.Request.Need[governor.MemoryBytes]/2 {
					return nil, governor.ErrSnapshotBudget
				}
				return rows, nil
			}
			var e error
			if stepIndex == 0 {
				initial, e = readBrowseFence(query, in)
				if e != nil {
					return e
				}
				fenceReady = true
			}
			if stepIndex == 0 && len(roots) > 0 {
				source, e = roots[0](query)
				if e != nil {
					return e
				}
			}
			// Middle windows run business reads only. The fresh final shared
			// fence and root/source comparison reject any torn private draft.

			return step(scope.Context(), query, initial, &draft)
		})
		result.LookupWindows = append(result.LookupWindows, window)
		result.Lookup = window
		if window.Refusal != nil {
			result.Refusal = window.Refusal
			return zero, result, governor.ErrBusy
		}
		if err != nil {
			break
		}
	}
	lookupErr := err
	if result.Lookup.Refusal != nil {
		result.Refusal = result.Lookup.Refusal
		return zero, result, governor.ErrBusy
	}
	if !fenceReady {
		return zero, result, lookupErr
	}
	if !time.Now().Before(deadline) {
		return zero, result, errors.Join(lookupErr, ctx.Err(), context.DeadlineExceeded)
	}
	// A bounded security-only validation may finish after caller cancellation,
	// but NEVER past the original protected deadline. This lets an observed
	// denial win over a simultaneous cancellation without continuing business
	// work or releasing a page. The original caller/lookup cancellation is
	// checked again below even if this independent validation succeeds.
	validationContext, cancelValidation := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer cancelValidation()
	currentInput := in
	currentInput.Now = in.Now.Add(time.Since(started))
	if output.live != nil {
		if e := output.live.checkContext(validationContext); e != nil {
			return zero, result, errors.Join(lookupErr, e)
		}
	}

	result.Publication, err = snapshots.Read(validationContext, request, time.Until(deadline), func(scope *governor.ReadScope) error {
		query := func(name string, args ...any) ([][]any, error) {
			if output.live != nil {
				if e := output.live.checkContext(scope.Context()); e != nil {
					return nil, e
				}
			}
			rows, count, e := queryReadScope(scope, name, args...)
			result.PublicationStatements += count
			return rows, e
		}
		current, e := readBrowseFence(query, currentInput)
		if e != nil {
			return e
		}
		if current != initial {
			return ErrStale
		}
		if len(roots) > 0 {
			now, e := roots[0](query)
			if e != nil {
				return e
			}
			if !equalSource(source, now) {
				return ErrStale
			}
		}
		return nil
	})
	publicationErr := err
	if result.Publication.Refusal != nil {
		result.Refusal = result.Publication.Refusal
		publicationErr = governor.ErrBusy
	}
	if errors.Is(publicationErr, ErrDenied) || errors.Is(publicationErr, ErrUnqualified) || errors.Is(publicationErr, ErrStale) {
		return zero, result, publicationErr
	}
	if publicationErr == nil {
		// The token/horizon can expire while final SQL/cleanup is finishing.
		// Re-evaluate those confirmed facts at the actual publication boundary.
		at := in.Now.Add(time.Since(started))
		expires, expiryErr := time.Parse(time.RFC3339Nano, initial.expiresAt)
		horizon, horizonErr := time.Parse(time.RFC3339Nano, initial.horizon)
		if expiryErr != nil || horizonErr != nil || !at.Before(expires) || !at.Before(horizon) {
			return zero, result, ErrDenied
		}
	}
	if err := ctx.Err(); err != nil {
		return zero, result, err
	}
	if err := errors.Join(lookupErr, publicationErr, validationContext.Err()); err != nil {
		return zero, result, err
	}
	if !time.Now().Before(deadline) {
		return zero, result, context.DeadlineExceeded
	}
	if err := output.Authority.CheckContext(ctx, output.Grant, output.Request); err != nil {
		return zero, result, err
	}
	return draft, result, nil
}

// Compare exact native values, including BLOB incarnations and nullable fields.
func equalSource(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		switch v := v.(type) {
		case []byte:
			w, ok := b[i].([]byte)
			if !ok || !bytes.Equal(v, w) {
				return false
			}
		default:
			if v != b[i] {
				return false
			}
		}
	}
	return true
}
