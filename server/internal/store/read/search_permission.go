package read

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// permissionFence runs outside the title executor's native windows, so an
// executor may still use the same scope's one public LIVE reader operation.
// The enclosing synchronous ConsumeForeground callback owns and joins these
// windows as well as the executor/encoder; none can outlive output custody.
func (d *SearchDispatcher) permissionFence(ctx context.Context, request governor.Request, deadline time.Time, output BrowseOutput, in BrowseInput, final bool, cost *BrowseReadResult) (fence browseFence, err error) {
	if err = output.live.checkContext(ctx); err != nil {
		return fence, err
	}
	request.Deadline = deadline
	clock := in.Now
	in.Now = in.Now.Add(time.Since(output.live.started))
	window, err := d.pool.Read(ctx, request, time.Until(deadline), func(scope *governor.ReadScope) error {
		query := func(name string, args ...any) ([][]any, error) {
			if final {
				cost.PublicationStatements++
			} else {
				cost.LookupStatements++
			}
			if e := output.live.checkContext(scope.Context()); e != nil {
				return nil, e
			}
			return scope.Query(statementID(name), args...)
		}
		var e error
		if in.Kind == 0 {
			fence, e = readLibraryFence(query, in)
		} else {
			fence, e = readBrowseFence(query, in)
		}
		return e
	})
	if final {
		cost.Publication = window
		cost.PublicationWindows = append(cost.PublicationWindows, window)
	} else {
		cost.Lookup = window
		cost.LookupWindows = append(cost.LookupWindows, window)
	}
	if window.Refusal != nil {
		cost.Refusal = window.Refusal
		return browseFence{}, governor.ErrBusy
	}
	if err != nil {
		return browseFence{}, err
	}
	// The native window has physically returned. Recheck expiry at this actual
	// boundary rather than letting time spent in SQL/cleanup extend authority.
	at := clock.Add(time.Since(output.live.started))
	expires, e1 := time.Parse(time.RFC3339Nano, fence.expiresAt)
	horizon, e2 := time.Parse(time.RFC3339Nano, fence.horizon)
	if e1 != nil || e2 != nil || !at.Before(expires) || !at.Before(horizon) {
		return browseFence{}, ErrDenied
	}
	return fence, output.live.checkContext(ctx)
}

// Include all native executor work in addition to the dispatcher's own first
// and final fence windows. Missing executor evidence is never fabricated.
func addSearchReadCost(total, part *BrowseReadResult) {
	if part == nil {
		return
	}
	total.LookupStatements += part.LookupStatements
	total.PublicationStatements += part.PublicationStatements
	total.LookupWindows = append(total.LookupWindows, part.LookupWindows...)
	total.PublicationWindows = append(total.PublicationWindows, part.PublicationWindows...)
	if !part.Lookup.Started.IsZero() {
		total.Lookup = part.Lookup
	}
	if !part.Publication.Started.IsZero() {
		total.Publication = part.Publication
	}
	if part.Refusal != nil {
		total.Refusal = part.Refusal
	}
	total.Attempts = max(total.Attempts, part.Attempts)
}

func searchFenceError(prior, current error, cost *BrowseReadResult) error {
	if errors.Is(current, ErrStale) {
		cost.Refusal = &governor.Refusal{Code: governor.Conflict}
		return errors.Join(ErrStale, governor.ErrBusy)
	}
	if errors.Is(current, ErrDenied) || errors.Is(current, ErrUnqualified) {
		return current // an observed security denial wins over cancellation
	}
	return errors.Join(prior, current)
}
