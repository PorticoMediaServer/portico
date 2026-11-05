package enrich

import (
	"context"
	"errors"
	"time"
)

var ErrDispatch = errors.New("enrichment adapter did not acknowledge dispatch correctly")

// Fetch performs at most one request and consumes/closes its response before
// returning. Redirects and hidden retries require their own broker admission.
// Check must run immediately
// before sending. Started acknowledges an ALREADY dispatched request, before
// waiting for its response. It must never reserve permission for a future send.
// Holding the pacing permit until that acknowledgment prevents overdue bursts.
// An HTTP adapter can acknowledge from its actual request-write callback.
type Fetch func(context.Context, RequestKey, int64, Dispatch) (Response, error)

type Dispatch struct {
	check   func() error
	started func() error
}

func (d Dispatch) Check() error {
	if d.check == nil {
		return ErrDispatch
	}
	return d.check()
}
func (d Dispatch) Started() error {
	if d.started == nil {
		return ErrDispatch
	}
	return d.started()
}

type transportClock interface {
	Now() time.Duration
	Wait(context.Context, time.Duration) error
}
type realTransportClock struct{ origin time.Time }

func (c realTransportClock) Now() time.Duration { return time.Since(c.origin) }
func (c realTransportClock) Wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

const maxDuration = time.Duration(1<<63 - 1)
const automaticRetryHorizon = 24 * time.Hour

func later(now, delay time.Duration) time.Duration {
	if delay > maxDuration-now {
		return maxDuration
	}
	return now + delay
}
