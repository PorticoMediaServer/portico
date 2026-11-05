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

func requireLongFloor(t *testing.T, err, cause error) {
	t.Helper()
	if cause != nil && !errors.Is(err, cause) {
		t.Fatalf("lost original cause %v: %v", cause, err)
	}
	var provider *ProviderError
	if !errors.As(err, &provider) || provider == nil || provider.RetryAfter < 48*time.Hour || !provider.Suspended {
		t.Fatalf("missing public 48h/suspended guidance: %v", err)
	}
}

func TestTransportNonProviderErrorsCarryFloor(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"dispatch", ErrDispatch},
		{"canceled", context.Canceled},
		{"deadline", context.DeadlineExceeded},
		{"response", ErrResponse},
		{"fetch", errors.New("body read failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := fakeBroker(t, config(), newFakeClock(), func(_ context.Context, _ RequestKey, _ int64, d Dispatch) (Response, error) {
				if err := acknowledged(d); err != nil {
					return Response{}, err
				}
				return Response{Status: 429, RetryAfter: 48 * time.Hour}, tc.err
			})
			_, err := tr.Get(context.Background(), key("floor"))
			requireLongFloor(t, err, tc.err)
		})
	}
	for _, duplicate := range []bool{false, true} {
		name := "missing_ack"
		if duplicate {
			name = "duplicate_ack"
		}
		t.Run(name, func(t *testing.T) {
			tr := fakeBroker(t, config(), newFakeClock(), func(_ context.Context, _ RequestKey, _ int64, d Dispatch) (Response, error) {
				if duplicate {
					if err := acknowledged(d); err != nil {
						return Response{}, err
					}
					_ = d.Started()
				}
				return Response{Status: 429, RetryAfter: 48 * time.Hour}, nil
			})
			_, err := tr.Get(context.Background(), key("floor"))
			requireLongFloor(t, err, ErrDispatch)
		})
	}
}

func TestTransportConcurrentOversizeCarriesFloor(t *testing.T) {
	clock := newFakeClock()
	aSent, bSent := make(chan struct{}), make(chan struct{})
	returnA, returnB := make(chan struct{}), make(chan struct{})
	var releaseA, releaseB sync.Once
	defer releaseA.Do(func() { close(returnA) })
	defer releaseB.Do(func() { close(returnB) })
	tr := fakeBroker(t, config(), clock, func(ctx context.Context, k RequestKey, budget int64, d Dispatch) (Response, error) {
		if err := acknowledged(d); err != nil {
			return Response{}, err
		}
		ch := returnB
		if k.ExternalID == "a" {
			close(aSent)
			ch = returnA
		} else {
			close(bSent)
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
		if k.ExternalID == "a" {
			return Response{Status: 429, RetryAfter: 48 * time.Hour}, nil
		}
		return Response{Status: 200, Body: make([]byte, budget+1)}, nil
	})
	aDone, bDone := make(chan error, 1), make(chan error, 1)
	go func() { _, err := tr.Get(context.Background(), key("a")); aDone <- err }()
	<-aSent
	go func() { _, err := tr.Get(context.Background(), key("b")); bDone <- err }()
	clock.waitTimer(t)
	clock.advance(25 * time.Millisecond)
	<-bSent
	releaseA.Do(func() { close(returnA) })
	requireLongFloor(t, <-aDone, nil)
	releaseB.Do(func() { close(returnB) })
	requireLongFloor(t, <-bDone, ErrResponse)
}

func TestTransportCallerErrorsCarryFloor(t *testing.T) {
	tr := fakeBroker(t, config(), newFakeClock(), func(_ context.Context, _ RequestKey, _ int64, d Dispatch) (Response, error) {
		if err := acknowledged(d); err != nil {
			return Response{}, err
		}
		return Response{Status: 429, RetryAfter: 48 * time.Hour}, nil
	})
	_, err := tr.Get(context.Background(), key("floor"))
	requireLongFloor(t, err, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = tr.Get(ctx, key("canceled"))
	requireLongFloor(t, err, context.Canceled)
	tr.Close()
	_, err = tr.Get(context.Background(), key("closed"))
	requireLongFloor(t, err, ErrClosed)
}

type panicAsError struct{}

func (panicAsError) Error() string { return "panic in As" }
func (panicAsError) As(any) bool   { panic("synthetic error As panic") }

type panicIsError struct{}

func (panicIsError) Error() string { return "panic in Is" }
func (panicIsError) Is(error) bool { panic("synthetic error Is panic") }

func TestTransportErrorHooksCannotDeadlockCleanup(t *testing.T) {
	const childEnv = "PORTICO_ENRICH_ERROR_CHILD"
	scenario := os.Getenv(childEnv)
	if scenario == "" {
		for _, name := range []string{"typed_nil", "panic_as", "panic_is"} {
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTransportErrorHooksCannotDeadlockCleanup$", "-test.timeout=6s")
				cmd.Env = append(os.Environ(), childEnv+"="+name)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("error handling did not drain worker/waiters: %v\n%s", err, output)
				}
			})
		}
		return
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var retirements atomic.Int32
	tr, err := newTransport(config(), admissionFunc(func(context.Context, int64) (func(), error) {
		return func() { retirements.Add(1) }, nil
	}), func(_ context.Context, _ RequestKey, _ int64, d Dispatch) (Response, error) {
		if err := acknowledged(d); err != nil {
			return Response{}, err
		}
		close(entered)
		<-release
		var err error
		switch scenario {
		case "typed_nil":
			var provider *ProviderError
			err = provider
		case "panic_as":
			err = panicAsError{}
		case "panic_is":
			err = panicIsError{}
		default:
			t.Fatalf("unknown child scenario %q", scenario)
		}
		return Response{Status: 429, RetryAfter: 48 * time.Hour}, err
	}, newFakeClock())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tr.Close)
	done := make(chan error, 2)
	go func() { _, err := tr.Get(context.Background(), key("shared-error")); done <- err }()
	<-entered
	go func() { _, err := tr.Get(context.Background(), key("shared-error")); done <- err }()
	waitFor(t, func() bool { return tr.Stats().Waiters == 2 })
	releaseOnce.Do(func() { close(release) })
	for range 2 {
		select {
		case err := <-done:
			requireLongFloor(t, err, nil)
			if scenario != "typed_nil" && !errors.Is(err, ErrWorkerPanic) {
				t.Fatalf("error-hook panic not contained: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("waiter blocked in panic cleanup")
		}
	}
	tr.Close()
	if retirements.Load() != 1 || len(tr.slots) != 0 || len(tr.pace) != 0 {
		t.Fatalf("custody leaked: retirements=%d slots=%d pace=%d", retirements.Load(), len(tr.slots), len(tr.pace))
	}
	if s := tr.Stats(); s.Pending != 0 || s.Waiters != 0 || s.InFlight != 0 || s.Failures != 1 || s.KeyBytes != int64(len(config().Provider)) {
		t.Fatalf("worker did not drain: %+v", s)
	}
}
