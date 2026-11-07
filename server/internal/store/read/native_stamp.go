package read

import (
	"context"
	"slices"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// FullSearchReadStamp is an opaque, read-joined comparison witness. Only the
// native reader creates it AFTER fresh final SQL, physical cleanup and lifetime
// checks. It is not a permission, native receipt, wire cursor or memory grant.
// In particular a previous poll's stamp never authorizes retained-hit delivery.
type FullSearchReadStamp struct {
	pool                     *governor.Snapshots
	output                   *governor.Grant
	fence                    nativeSearchFence
	query                    string
	policies                 []nativePolicyWitness
	checked                  []nativePolicyWitness
	hits                     []nativeStampedHit
	started, clock, deadline time.Time
}

// ValidateRetained joins prior results to THIS operation's fresh read and its
// still-live exact output custody. It performs no SQL or second reader call.
// Call inside the current ConsumeForeground callback, after its native Read.
// Supply prior in FullSearchInput.Retained before the current Read so its
// per-hit policies are actually revalidated in this operation. RS must retain/charge its prior values and source-write footprint separately,
// and still use the shared final publication fence after response preparation.
func (s *FullSearchReadStamp) ValidateRetained(ctx context.Context, out BrowseOutput, prior *FullSearchReadStamp) error {
	if ctx == nil || s == nil || prior == nil || s.pool == nil || s.output == nil || out.live == nil || out.live.pool != s.pool || out.Grant != s.output || out.Authority != out.live.authority || out.Grant != out.live.output {
		return ErrInput
	}
	now := s.clock.Add(time.Since(s.started))
	expires, e1 := time.Parse(time.RFC3339Nano, s.fence.permission.expiresAt)
	horizon, e2 := time.Parse(time.RFC3339Nano, s.fence.permission.horizon)
	if e1 != nil || e2 != nil || !now.Before(expires) || !now.Before(horizon) {
		return ErrDenied
	}
	if err := out.live.checkContext(ctx); err != nil {
		return err
	}
	if s.pool != prior.pool || s.query != prior.query || s.fence != prior.fence {
		return ErrStale
	}
	for _, p := range prior.policies {
		if !slices.Contains(s.checked, p) {
			return ErrStale
		}
	}
	if !time.Now().Before(s.deadline) {
		return context.DeadlineExceeded
	}
	return ctx.Err()
}
