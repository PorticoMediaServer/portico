package read

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"time"
)

// Closed reading publication protocol mirrors publication.go; only canonical reading kinds are admitted.
func iaReadingPublish[T any](ctx context.Context, snapshots browseSnapshots, request governor.Request, budget time.Duration, output BrowseOutput, in BrowseInput, steps []readStep[T], roots ...rootRead) (page T, result BrowseReadResult, err error) {
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
		page, one, err = iaReadingAttempt(ctx, snapshots, request, time.Until(deadline), output, attemptInput, steps, &copied, roots...)
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

func iaReadingAttempt[T any](ctx context.Context, snapshots browseSnapshots, request governor.Request, budget time.Duration, output BrowseOutput, in BrowseInput, steps []readStep[T], copied *int64, roots ...rootRead) (page T, result BrowseReadResult, err error) {

	var zero T
	started := time.Now()
	defer func() { result.Elapsed = time.Since(started) }()
	if snapshots == nil || len(steps) == 0 || ctx == nil || output.Authority == nil || output.Grant == nil || budget <= 0 ||
		len(in.Database) == 0 || len(in.Database) > 256 || len(in.TokenHash) == 0 || len(in.TokenHash) > 256 || in.Library <= 0 || in.Kind <= 0 || in.Kind > 99 || in.Limit < 1 || in.Limit > MaxPage || in.Now.IsZero() {
		return zero, result, ErrInput
	}
	if in.Kind != 20 && in.Kind != 21 {
		return zero, result, ErrUnqualified
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
	if err := output.Authority.Check(output.Grant, output.Request); err != nil {
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
			if e := output.live.check(); e != nil {
				return zero, result, e
			}
		}
		window, err = snapshots.Read(lookupContext, request, time.Until(deadline), func(scope *governor.ReadScope) error {
			query := func(name string, args ...any) ([][]any, error) {
				result.LookupStatements++
				if output.live != nil {
					if e := output.live.ctx.Err(); e != nil {
						return nil, e
					}
					if e := output.live.check(); e != nil {
						return nil, e
					}
				}
				rows, e := scope.Query(readStatementID(name), args...)
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
		if e := output.live.check(); e != nil {
			return zero, result, errors.Join(lookupErr, e)
		}
	}

	result.Publication, err = snapshots.Read(validationContext, request, time.Until(deadline), func(scope *governor.ReadScope) error {
		query := func(name string, args ...any) ([][]any, error) {
			result.PublicationStatements++
			if output.live != nil {
				if e := output.live.check(); e != nil {
					return nil, e
				}
			}
			return scope.Query(readStatementID(name), args...)
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
	if err := output.Authority.Check(output.Grant, output.Request); err != nil {
		return zero, result, err
	}
	return draft, result, nil
}
