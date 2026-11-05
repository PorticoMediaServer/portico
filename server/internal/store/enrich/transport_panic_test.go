package enrich

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The old bare worker panic must fail a subprocess rather than the test runner.
func TestTransportWorkerPanicFailsEveryWaiter(t *testing.T) {
	const childEnv = "PORTICO_ENRICH_PANIC_CHILD"
	if os.Getenv(childEnv) != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTransportWorkerPanicFailsEveryWaiter$", "-test.timeout=8s")
		cmd.Env = append(os.Environ(), childEnv+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("worker panic did not complete waiters safely: %v\n%s", err, output)
		}
		return
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var returned, retired atomic.Bool
	tr := brokerFetch(t, config(), admissionFunc(func(context.Context, int64) (func(), error) {
		return func() {
			if !returned.Load() {
				t.Error("retired before adapter unwound")
			}
			retired.Store(true)
		}, nil
	}), func(ctx context.Context, _ RequestKey, _ int64, d Dispatch) (Response, error) {
		if err := acknowledged(d); err != nil {
			return Response{}, err
		}
		defer returned.Store(true)
		close(entered)
		select {
		case <-release:
			panic("synthetic adapter failure")
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 2)
	go func() { _, err := tr.Get(ctx, key("shared-panic")); done <- err }()
	<-entered
	go func() { _, err := tr.Get(ctx, key("shared-panic")); done <- err }()
	waitFor(t, func() bool { return tr.Stats().Waiters == 2 })
	once.Do(func() { close(release) })
	for range 2 {
		if err := <-done; err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			t.Fatalf("waiter not failed promptly: %v", err)
		}
	}
	tr.Close()
	if !retired.Load() {
		t.Fatal("physical admission not retired")
	}
	if s := tr.Stats(); s.Pending != 0 || s.Waiters != 0 || s.InFlight != 0 || s.Failures != 1 {
		t.Fatalf("panic leaked custody: %+v", s)
	}
}

type postAdmissionPanicClock struct {
	transportClock
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (c *postAdmissionPanicClock) Now() time.Duration {
	if c.armed.CompareAndSwap(true, false) {
		close(c.entered)
		<-c.release
		panic("synthetic clock panic after admission")
	}
	return c.transportClock.Now()
}

func TestTransportClockPanicAfterAdmissionRetires(t *testing.T) {
	clock := &postAdmissionPanicClock{transportClock: newFakeClock(), entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(clock.release) })
	var grants, retirements atomic.Int32
	tr, err := newTransport(config(), admissionFunc(func(context.Context, int64) (func(), error) {
		grants.Add(1)
		clock.armed.Store(true)
		return func() { retirements.Add(1) }, nil
	}), func(context.Context, RequestKey, int64, Dispatch) (Response, error) {
		t.Error("fetch reached after admission clock panic")
		return Response{}, nil
	}, clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tr.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 2)
	go func() { _, err := tr.Get(ctx, key("admission-panic")); done <- err }()
	select {
	case <-clock.entered:
	case <-ctx.Done():
		t.Fatal("post-admission clock read not reached")
	}
	go func() { _, err := tr.Get(ctx, key("admission-panic")); done <- err }()
	waitFor(t, func() bool { return tr.Stats().Waiters == 2 })
	releaseOnce.Do(func() { close(clock.release) })
	for range 2 {
		err := <-done
		var provider *ProviderError
		if !errors.Is(err, ErrWorkerPanic) || !errors.As(err, &provider) || provider == nil || !provider.Suspended {
			t.Fatalf("waiter missing panic/suspension: %v", err)
		}
	}
	tr.Close()
	if grants.Load() != 1 || retirements.Load() != 1 {
		t.Fatalf("external custody leaked: grants=%d retirements=%d", grants.Load(), retirements.Load())
	}
	if s := tr.Stats(); s.Pending != 0 || s.Waiters != 0 || s.InFlight != 0 || s.Failures != 1 || s.KeyBytes != int64(len(config().Provider)) || len(tr.slots) != 0 || len(tr.pace) != 0 {
		t.Fatalf("transport custody leaked: %+v slots=%d pace=%d", s, len(tr.slots), len(tr.pace))
	}
}
