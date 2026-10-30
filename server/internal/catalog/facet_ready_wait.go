package catalog

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// WaitPreparedForTest is a test fixture's wait on the real preparation behind
// an in-flight SIMPLE facet query ID. The fixture holds the ID from an ordinary
// pending/fallback HTTP response and calls this after that handler has
// returned, before its next ordinary authenticated poll, so no handler, request
// deadline or Retry-After interval is changed. It returns no page, no values
// and no access: the next Poll still authenticates, rechecks live authority and
// charges its own delivery.
//
// The ID names exactly one private readprep handle, whether it is a cold-scope
// "C" handle or a signed window token; that handle's own lease supplies the
// caller. This adds no query cache, channel registry, clock or state, and it is
// refused outside a test binary. Callers must pass the fixture's existing
// context deadline; this adds none.
func (r *FacetRuntime) WaitPreparedForTest(ctx context.Context, queryID string) error {
	if !testing.Testing() || r == nil || r.windows == nil || r.store == nil {
		return ErrFacetBaseUnavailable
	}
	h, ok := scopeHandle(queryID)
	if !ok {
		token, err := r.windows.decode(queryID)
		if err != nil {
			return readprep.ErrStale
		}
		h = token.handle
	}
	if h == "" {
		return readprep.ErrStale
	}
	return r.store.WaitPreparedForTest(ctx, h)
}
