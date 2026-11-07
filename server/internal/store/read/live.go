package read

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// ForegroundConsumption measures admission through actual consumer/output
// cleanup. It is a harness measurement, not an HTTP-ready qualification.
type ForegroundConsumption struct {
	Refusal                   *governor.Refusal
	AdmissionElapsed, Elapsed time.Duration
}

// foregroundOutput is issued only inside ConsumeForeground. A scope authorizes
// one public read operation (including its windows/retry), then its consumer.
// The callback must join its encoding/IO/consumers and release references before
// returning. Private active-read custody also joins before either grant retires.
type foregroundOutput struct {
	mu            sync.Mutex
	readErr       error
	readRefusal   *governor.Refusal
	reads         sync.WaitGroup
	closed, used  bool
	authority     *governor.Authority
	pool          *governor.Snapshots
	live, output  *governor.Grant
	request       governor.ForegroundRequest
	outputRequest governor.Request
	ctx           context.Context
	started       time.Time
	admission     time.Duration
}

// ConsumeForeground is trusted harness composition. g and authority MUST come
// from the same governor.New. It admits LIVE before the separate output grant
// and before the callback can allocate a draft. Supply ORIGINAL deadlines; the
// ready budget includes admission waiting. The protected deadline also bounds
// the consumer context, but expiry/cancel never refunds custody before return.
// No goroutine or reference using output may escape the callback's lifetime.
func ConsumeForeground(ctx context.Context, g governor.Governor, authority *governor.Authority, pool *governor.Snapshots, request governor.ForegroundRequest, outputRequest governor.Request, consume func(context.Context, BrowseOutput) error) (result ForegroundConsumption, err error) {
	started := time.Now()
	defer func() { result.Elapsed = time.Since(started) }()
	if ctx == nil || g == nil || authority == nil || !authority.Owns(g) || pool == nil || !pool.OwnedBy(authority) || consume == nil || outputRequest.Owner != request.Owner || outputRequest.Class != governor.Foreground || outputRequest.Writer || outputRequest.Need[governor.MemoryBytes] != 8<<20 || outputRequest.Deadline.IsZero() || outputRequest.Deadline.After(request.ProtectedDeadline) {
		return result, ErrInput
	}
	for resource, n := range outputRequest.Need {
		if resource != int(governor.MemoryBytes) && n != 0 {
			return result, governor.ErrSnapshotBudget
		}
	}
	// Resource-only output purposes carry no mutable device/footprint slices.
	if len(outputRequest.IO) != 0 || len(outputRequest.Footprint) != 0 {
		return result, ErrInput
	}
	decision := authority.AdmitForeground(ctx, request)
	result.AdmissionElapsed = time.Since(started)
	if decision.Refusal != nil {
		result.Refusal = decision.Refusal
		return result, governor.ErrBusy
	}
	scope := &foregroundOutput{authority: authority, pool: pool, live: decision.Grant, request: request, outputRequest: outputRequest, ctx: ctx, started: started, admission: result.AdmissionElapsed}
	defer func() {
		primary := recover()
		cleanup := func() {
			scope.mu.Lock()
			scope.closed = true
			scope.mu.Unlock()
			scope.reads.Wait()
			// If physical output retirement is uncertain, retain LIVE rather than
			// announce a free slot. Correct composition owns both exact issuers.
			var cleanup error
			g.Cancel(scope.live)
			if scope.output != nil {
				g.Cancel(scope.output)
				cleanup = authority.Retired(scope.output)
			}
			if cleanup == nil {
				cleanup = authority.Retired(scope.live)
			}
			scope.mu.Lock()
			readErr := scope.readErr
			if result.Refusal == nil {
				result.Refusal = scope.readRefusal
			}
			scope.mu.Unlock()
			err = errors.Join(err, readErr, cleanup, ctx.Err())
		}
		if primary != nil {
			func() { defer func() { _ = recover() }(); cleanup() }()
		} else {
			cleanup()
		}

		if primary != nil {
			panic(primary)
		}
	}()
	if e := ctx.Err(); e != nil {
		return result, e
	}
	if e := authority.CheckForegroundContext(ctx, scope.live, request); e != nil {
		return result, e
	}
	output := g.Try(outputRequest)
	if output.Refusal != nil {
		result.Refusal = output.Refusal
		return result, governor.ErrBusy
	}
	scope.output = output.Grant
	if e := authority.CheckContext(ctx, scope.output, outputRequest); e != nil {
		return result, e
	}
	consumerContext, cancel := context.WithDeadline(ctx, earliest(request.ProtectedDeadline, outputRequest.Deadline))
	defer cancel()
	scope.ctx = consumerContext
	err = consume(consumerContext, BrowseOutput{Authority: authority, Grant: scope.output, Request: outputRequest, live: scope})
	err = errors.Join(err, consumerContext.Err())
	return result, err
}

func (s *foregroundOutput) check() error {
	if s == nil {
		return governor.ErrGrant
	}
	// Security fences may run under their own original-deadline context after
	// caller cancellation. The context-free check must retain its old semantics.
	return s.checkContext(context.Background())
}
func (s *foregroundOutput) checkContext(ctx context.Context) error {
	if s == nil {
		return governor.ErrGrant
	}
	if err := s.authority.CheckForegroundContext(ctx, s.live, s.request); err != nil {
		return err
	}
	return s.authority.CheckContext(ctx, s.output, s.outputRequest)
}

func (s *foregroundOutput) begin(ctx context.Context, output BrowseOutput, request governor.Request, snapshots *governor.Snapshots) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if snapshots == nil || snapshots != s.pool || !snapshots.OwnedBy(s.authority) || s.closed || s.used || output.Authority != s.authority || output.Grant != s.output || request.Key != s.request.Key || request.Owner != s.request.Owner || request.Class != governor.Foreground {
		return governor.ErrGrant
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}

	if request.Need[governor.Reader] != 1 || request.Need[governor.MemoryBytes] != 12<<20 {
		return governor.ErrSnapshotBudget
	}

	if err := s.authority.CheckContext(ctx, output.Grant, output.Request); err != nil {
		return err
	}
	if err := s.checkContext(ctx); err != nil {
		return err
	}
	s.used = true
	s.reads.Add(1)
	return nil
}

func liveRead[T any](ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, snapshots *governor.Snapshots, now time.Time, read func(context.Context, governor.Request, time.Duration, time.Time) (T, BrowseReadResult, error)) (page T, result BrowseReadResult, err error) {
	scope := output.live
	if ctx == nil || scope == nil || budget <= 0 || now.IsZero() {
		return page, result, ErrInput
	}
	if err = scope.begin(ctx, output, request, snapshots); err != nil {
		return page, result, err
	}
	defer func() {
		scope.mu.Lock()
		scope.readErr = errors.Join(scope.readErr, err)
		if result.Refusal != nil {
			scope.readRefusal = result.Refusal
		}
		scope.mu.Unlock()
		scope.reads.Done()
	}()
	defer func() { result.Elapsed = time.Since(scope.started); result.AdmissionElapsed = scope.admission }()
	deadline := earliest(scope.request.ReadyDeadline, scope.request.ProtectedDeadline, request.Deadline, output.Request.Deadline, scope.started.Add(min(budget, time.Second)))
	readContext, cancel := context.WithDeadline(ctx, deadline)
	cancelled := make(chan struct{})
	stop := context.AfterFunc(scope.ctx, func() { cancel(); close(cancelled) })
	defer func() {
		if !stop() {
			<-cancelled
		}
	}()
	defer cancel()
	if e := readContext.Err(); e != nil {
		return page, result, e
	}
	request.Deadline = deadline
	page, result, err = read(readContext, request, time.Until(deadline), now.Add(time.Since(scope.started)))
	if err != nil {
		var zero T
		return zero, result, err
	}
	if e := errors.Join(readContext.Err(), scope.checkContext(readContext)); e != nil {
		var zero T
		return zero, result, e
	}
	return page, result, nil
}

func earliest(values ...time.Time) time.Time {
	result := values[0]
	for _, value := range values[1:] {
		if value.Before(result) {
			result = value
		}
	}
	return result
}
