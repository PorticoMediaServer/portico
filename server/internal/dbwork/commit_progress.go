package dbwork

import (
	"context"
	"sync/atomic"
)

type commitProgressKey struct{}

// TrackCommitProgress counts changing gated commits made with the returned
// context or its descendants. It is a scheduling hint, never a revision or an
// authority fence. Unrelated work must not make an idle worker look productive.
// A nested tracker starts an independent scope, like other context probes.
func TrackCommitProgress(ctx context.Context) (context.Context, func() uint64) {
	counter := &atomic.Uint64{}
	return context.WithValue(ctx, commitProgressKey{}, counter), counter.Load
}

func noteCommitProgress(ctx context.Context) {
	if counter, ok := ctx.Value(commitProgressKey{}).(*atomic.Uint64); ok {
		counter.Add(1)
	}
}
