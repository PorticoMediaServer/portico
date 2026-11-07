package lcbuild

import (
	"context"
	"errors"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/read"
)

var ErrClosed = errors.New("lcbuild: retained values closed")

// Frozen belongs to trusted composition. It retains RB's copied opaque values
// across read turns without exposing retirement authority, a grant or a mutable
// getter. All consumers must keep the values frozen and finish every alias use
// before their callback returns. This is memory custody, not read authorization.
// Composition must Stop/Close every successful Freeze, including on job failure.
type Frozen struct {
	driver *Driver
	job    context.Context
	output read.BrowseOutput
	size   read.SharedChannelCopySize

	mu        sync.Mutex
	values    read.SharedChannelRetainedValues
	consumers int
	stopped   bool
	done      chan struct{}
	retireErr error
}

// Freeze measures under live source custody, admits DISTINCT memory-only
// ChargeBytes, then delegates the owned clone to RB. Source output can retire
// after this returns. Cancellation/expiry refuses use but does not refund values
// still retained by this owner. No cancellation callback attests consumer exit.
func (d *Driver) Freeze(ctx context.Context, source read.BrowseOutput, values read.SharedChannelRetainedValues) (*Frozen, error) {
	if d == nil || ctx == nil || d.port == nil || !d.authority.Owns(d.governor) || source.Authority != d.authority || source.Request.Owner != d.owner || source.Request.Class != governor.Background || source.Request.Writer || values.Info == nil || values.Info.Generation != d.generation {
		return nil, ErrInput
	}
	if err := d.authority.CheckContext(ctx, source.Grant, source.Request); err != nil {
		return nil, err
	}
	size, err := d.port.MeasureSharedChannelValues(ctx, source, values)
	if err != nil {
		return nil, err
	}
	if size.PayloadBytes <= 0 || size.ChargeBytes < size.PayloadBytes {
		return nil, ErrInput
	}
	deadline := source.Request.Deadline
	if until, ok := ctx.Deadline(); ok && until.Before(deadline) {
		deadline = until
	}
	request := governor.Request{Key: governor.JobKey{Domain: 860, ID: d.generation}, Owner: d.owner, Class: governor.Background, Deadline: deadline,
		Need: governor.Amounts{governor.MemoryBytes: size.ChargeBytes}}
	decision := d.governor.Try(request)
	if decision.Grant == nil {
		if decision.Refusal == nil {
			return nil, ErrInput
		}
		return nil, &AdmissionRefusal{*decision.Refusal}
	}
	f := &Frozen{driver: d, job: ctx, output: read.BrowseOutput{Authority: d.authority, Grant: decision.Grant, Request: request}, size: size, done: make(chan struct{})}
	ready := false
	defer func() {
		if !ready {
			f.Stop()
		}
	}()
	f.values, err = d.port.CloneSharedChannelValues(ctx, source, f.output, values)
	if err == nil {
		err = d.authority.CheckContext(ctx, f.output.Grant, request)
	}
	if err != nil {
		return nil, errors.Join(err, f.Close(context.WithoutCancel(ctx)))
	}
	ready = true
	return f, nil
}

func (f *Frozen) Size() read.SharedChannelCopySize {
	if f == nil {
		return read.SharedChannelCopySize{}
	}
	return f.size
}

// With lends the immutable graph only during this callback. Concurrent uses
// share the frozen graph under the SAME retained custody. Stop refuses new use;
// the final active callback's exit, including panic, makes retirement possible.
func (f *Frozen) With(ctx context.Context, consume func(context.Context, read.SharedChannelRetainedValues) error) (err error) {
	if f == nil || f.driver == nil || f.job == nil || f.done == nil || ctx == nil || consume == nil {
		return ErrInput
	}
	f.mu.Lock()
	if f.stopped {
		f.mu.Unlock()
		return ErrClosed
	}
	if err = f.job.Err(); err != nil {
		f.mu.Unlock()
		return err
	}
	if err = f.driver.authority.CheckContext(ctx, f.output.Grant, f.output.Request); err != nil {
		f.mu.Unlock()
		return err
	}
	values := f.values
	f.consumers++
	f.mu.Unlock()
	defer func() {
		values = read.SharedChannelRetainedValues{}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.consumers--
		if f.stopped && f.consumers == 0 {
			f.retireLocked()
			err = errors.Join(err, f.retireErr)
		}
	}()
	if err = consume(ctx, values); err != nil {
		return err
	}
	if err = f.job.Err(); err != nil {
		return err
	}
	return f.driver.authority.CheckContext(ctx, f.output.Grant, f.output.Request)
}

// Stop revokes new use immediately and never waits for a consumer. The memory
// remains charged until the final active consumer returns. It is safe to call
// Stop inside a callback; Close must be joined outside that callback.
func (f *Frozen) Stop() {
	if f == nil || f.driver == nil || f.done == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopped {
		return
	}
	f.stopped = true
	f.driver.governor.Cancel(f.output.Grant)
	if f.consumers == 0 {
		f.retireLocked()
	}
}

// Close starts Stop and waits for observed consumer exit. A cancelled wait
// returns its context error without refunding active memory; eventual consumer
// exit still retires it. Repeated Close calls report the same retirement result.
func (f *Frozen) Close(ctx context.Context) error {
	if f == nil || f.driver == nil || f.done == nil || ctx == nil {
		return ErrInput
	}
	f.Stop()
	select {
	case <-f.done:
		return f.retireErr
	default:
	}
	select {
	case <-f.done:
		return f.retireErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Called exactly once, with no remaining consumers and the mutex held.
func (f *Frozen) retireLocked() {
	f.values = read.SharedChannelRetainedValues{}
	f.retireErr = f.driver.authority.Retired(f.output.Grant)
	close(f.done)
}
