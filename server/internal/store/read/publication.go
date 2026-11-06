package read

import (
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
type BrowseReader struct{ snapshots browseSnapshots }

func NewBrowseReader(snapshots *governor.Snapshots) (*BrowseReader, error) {
	if snapshots == nil {
		return nil, ErrInput
	}
	return &BrowseReader{snapshots: snapshots}, nil
}

// BrowseOutput proves separate retained-output custody before draft allocation.
// Composition keeps this memory-only grant charged through the last consumer,
// including encoding/client IO; Window neither cancels nor retires it. Reserve
// at least twice the snapshot's Go-copy budget for retained values/DTO overlap.
// This is prospective admission, not an RSS or serialization qualification.
type BrowseOutput struct {
	Authority *governor.Authority
	Grant     *governor.Grant
	Request   governor.Request
}

// BrowseReadResult includes BOTH physical snapshots. Whole-route measurement
// includes them and output checks, never only BrowsePage.LookupElapsed.
type BrowseReadResult struct {
	Lookup, Publication                     governor.SnapshotResult
	LookupStatements, PublicationStatements int
	Refusal                                 *governor.Refusal
	Elapsed                                 time.Duration
}

// Window prepares a private page, joins the lookup's physical cleanup, then
// validates current principal/library/policy/kind in a NEW snapshot. Exact
// common-fence equality binds the old business view to that publication view.
// Every denial, drift, admission/cleanup error or cancellation returns ZERO
// output. There is no production registration or runtime cutover here.
func (r *BrowseReader) Window(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, in BrowseInput) (page BrowsePage, result BrowseReadResult, err error) {
	started := time.Now()
	defer func() { result.Elapsed = time.Since(started) }()
	if r == nil || r.snapshots == nil || ctx == nil || output.Authority == nil || output.Grant == nil || budget <= 0 ||
		len(in.Database) == 0 || len(in.Database) > 256 || len(in.TokenHash) == 0 || len(in.TokenHash) > 256 || in.Library <= 0 || in.Kind <= 0 || in.Kind > 99 || in.Limit < 1 || in.Limit > MaxPage || in.Now.IsZero() {
		return BrowsePage{}, result, ErrInput
	}
	if in.Kind > 10 {
		return BrowsePage{}, result, ErrUnqualified
	}
	if err := ctx.Err(); err != nil {
		return BrowsePage{}, result, err
	}
	goBytes := request.Need[governor.MemoryBytes] - r.snapshots.NativeRowReservation()
	if goBytes <= 0 || output.Request.Writer || output.Request.Owner != request.Owner || output.Request.Class != request.Class || output.Request.Need[governor.MemoryBytes]/2 < goBytes {
		return BrowsePage{}, result, governor.ErrSnapshotBudget
	}
	for resource, n := range output.Request.Need {
		if resource != int(governor.MemoryBytes) && n != 0 {
			return BrowsePage{}, result, governor.ErrSnapshotBudget
		}
	}
	if err := output.Authority.Check(output.Grant, output.Request); err != nil {
		return BrowsePage{}, result, err
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
	var draft BrowsePage
	var initial browseFence
	var fenceReady bool
	result.Lookup, err = r.snapshots.Read(lookupContext, request, time.Until(deadline), func(scope *governor.ReadScope) error {
		query := func(name string, args ...any) ([][]any, error) {
			result.LookupStatements++
			return scope.Query(statementID(name), args...)
		}
		var e error
		initial, e = readBrowseFence(query, in)
		if e != nil {
			return e
		}
		fenceReady = true
		draft, e = readBrowsePage(scope.Context(), query, in, initial)
		return e
	})
	lookupErr := err
	if result.Lookup.Refusal != nil {
		result.Refusal = result.Lookup.Refusal
		return BrowsePage{}, result, governor.ErrBusy
	}
	if !fenceReady {
		return BrowsePage{}, result, lookupErr
	}
	if !time.Now().Before(deadline) {
		return BrowsePage{}, result, errors.Join(lookupErr, ctx.Err(), context.DeadlineExceeded)
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
	result.Publication, err = r.snapshots.Read(validationContext, request, time.Until(deadline), func(scope *governor.ReadScope) error {
		query := func(name string, args ...any) ([][]any, error) {
			result.PublicationStatements++
			return scope.Query(statementID(name), args...)
		}
		current, e := readBrowseFence(query, currentInput)
		if e != nil {
			return e
		}
		if current != initial {
			return ErrStale
		}
		return nil
	})
	publicationErr := err
	if result.Publication.Refusal != nil {
		result.Refusal = result.Publication.Refusal
		publicationErr = governor.ErrBusy
	}
	if errors.Is(publicationErr, ErrDenied) || errors.Is(publicationErr, ErrUnqualified) || errors.Is(publicationErr, ErrStale) {
		return BrowsePage{}, result, publicationErr
	}
	if publicationErr == nil {
		// The token/horizon can expire while final SQL/cleanup is finishing.
		// Re-evaluate those confirmed facts at the actual publication boundary.
		at := in.Now.Add(time.Since(started))
		expires, expiryErr := time.Parse(time.RFC3339Nano, initial.expiresAt)
		horizon, horizonErr := time.Parse(time.RFC3339Nano, initial.horizon)
		if expiryErr != nil || horizonErr != nil || !at.Before(expires) || !at.Before(horizon) {
			return BrowsePage{}, result, ErrDenied
		}
	}
	if err := ctx.Err(); err != nil {
		return BrowsePage{}, result, err
	}
	if err := errors.Join(lookupErr, publicationErr, validationContext.Err()); err != nil {
		return BrowsePage{}, result, err
	}
	if !time.Now().Before(deadline) {
		return BrowsePage{}, result, context.DeadlineExceeded
	}
	if err := output.Authority.Check(output.Grant, output.Request); err != nil {
		return BrowsePage{}, result, err
	}
	draft.Statements = result.LookupStatements + result.PublicationStatements
	return draft, result, nil
}
