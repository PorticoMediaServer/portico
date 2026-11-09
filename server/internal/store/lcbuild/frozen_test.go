package lcbuild

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/read"
)

func TestMGLCFrozenSpansReadTurns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := custody(t, 65536)
		frozen, err := f.driver.Freeze(t.Context(), f.source, f.values)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = frozen.Close(t.Context()) }()
		f.retireSource(t)
		for range 2 {
			if err := frozen.With(t.Context(), func(_ context.Context, values read.SharedChannelRetainedValues) error {
				if values.Info.Generation != "generation" || f.used() != frozen.Size().ChargeBytes {
					t.Fatal("turn lost opaque read custody")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		if err := frozen.Close(t.Context()); err != nil || f.used() != 0 {
			t.Fatal("turn owner did not retire", err, f.used())
		}
		if err := frozen.Close(t.Context()); err != nil || f.used() != 0 {
			t.Fatal("repeated close changed the retirement result", err, f.used())
		}
		if err := frozen.With(t.Context(), func(context.Context, read.SharedChannelRetainedValues) error {
			t.Fatal("closed graph delivered")
			return nil
		}); !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
	})
}

func TestMGLCFrozenStopWaitsForEveryConsumer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := custody(t, 65536)
		frozen, err := f.driver.Freeze(t.Context(), f.source, f.values)
		if err != nil {
			t.Fatal(err)
		}
		entered := make(chan struct{}, 2)
		finish := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
		results := make(chan error, 2)
		for i := range finish {
			go func() {
				results <- frozen.With(t.Context(), func(context.Context, read.SharedChannelRetainedValues) error {
					entered <- struct{}{}
					<-finish[i]
					return nil
				})
			}()
		}
		<-entered
		<-entered
		frozen.Stop()
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if err := frozen.Close(cancelled); !errors.Is(err, context.Canceled) || f.used() != 32768+frozen.Size().ChargeBytes {
			t.Fatal("cancelled close refunded active consumers", err, f.used())
		}
		close(finish[0])
		if err := <-results; err == nil || f.used() != 32768+frozen.Size().ChargeBytes {
			t.Fatal("first consumer attested global exit", err, f.used())
		}
		close(finish[1])
		if err := <-results; err == nil {
			t.Fatal("stopped consumer reported success")
		}
		if err := frozen.Close(t.Context()); err != nil || f.used() != 32768 {
			t.Fatal("last consumer did not retire", err, f.used())
		}
	})
}

func TestMGLCFrozenJobCancellationAndExpiryKeepDebt(t *testing.T) {
	for _, mode := range []string{"cancelled", "expired"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := custody(t, 65536)
				job, cancel := context.WithCancel(t.Context())
				defer cancel()
				frozen, err := f.driver.Freeze(job, f.source, f.values)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "cancelled" {
					cancel()
				} else {
					time.Sleep(time.Hour)
				}
				if err := frozen.With(t.Context(), func(context.Context, read.SharedChannelRetainedValues) error {
					t.Fatal("dead job delivered values")
					return nil
				}); err == nil || f.used() != 32768+frozen.Size().ChargeBytes {
					t.Fatal("dead job refunded retained values", err, f.used())
				}
				if err := frozen.Close(t.Context()); err != nil || f.used() != 32768 {
					t.Fatal(err, f.used())
				}
			})
		})
	}
}

func TestMGLCFrozenClonePanicRetiresAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := custody(t, 65536)
		original := new(int)
		f.port.clone = func(read.BrowseOutput) { panic(original) }
		func() {
			defer func() {
				if recover() != original {
					t.Fatal("clone panic was replaced")
				}
			}()
			_, _ = f.driver.Freeze(t.Context(), f.source, f.values)
		}()
		if f.used() != 32768 {
			t.Fatal("clone panic stranded retained admission", f.used())
		}
	})
}

func TestMGLCFrozenWithGuards(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := custody(t, 65536)
		frozen, err := f.driver.Freeze(t.Context(), f.source, f.values)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = frozen.Close(t.Context()) }()
		if err := frozen.With(nil, func(context.Context, read.SharedChannelRetainedValues) error { return nil }); !errors.Is(err, ErrInput) {
			t.Fatal("nil context accepted", err)
		}
		if err := frozen.With(t.Context(), nil); !errors.Is(err, ErrInput) {
			t.Fatal("nil consumer accepted", err)
		}
		stopped, err := f.driver.Freeze(t.Context(), f.source, f.values)
		if err != nil {
			t.Fatal(err)
		}
		stopped.Stop()
		if err := stopped.With(t.Context(), func(context.Context, read.SharedChannelRetainedValues) error {
			t.Fatal("stopped graph delivered")
			return nil
		}); !errors.Is(err, ErrClosed) {
			t.Fatal(err)
		}
		if err := stopped.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}
