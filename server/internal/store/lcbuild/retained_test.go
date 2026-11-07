package lcbuild

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/read"
)

// The stub exercises MG's admission/lifetime bridge, not RB's read proof. The
// native integration control separately obtains and clones a real opaque scope.
type copyPort struct {
	size         read.SharedChannelCopySize
	measureError error
	cloneError   error
	clone        func(read.BrowseOutput)
	window       read.SharedChannelBatch
	windowError  error
	scopeError   error
	scopeReads   int
}

func (p *copyPort) OpenSharedChannelScope(_ context.Context, _ governor.Request, _ time.Duration, _ read.BrowseOutput, in read.SharedChannelScopeInput) (*read.SharedChannelScope, read.SharedChannelScopeInfo, read.BrowseReadResult, error) {
	p.scopeReads++
	return &read.SharedChannelScope{}, read.SharedChannelScopeInfo{Generation: in.Generation}, read.BrowseReadResult{Attempts: 1}, p.scopeError
}

func (p *copyPort) SharedChannelWindow(context.Context, governor.Request, time.Duration, read.BrowseOutput, read.SharedChannelInput) (read.SharedChannelBatch, read.BrowseReadResult, error) {
	return p.window, read.BrowseReadResult{}, p.windowError
}
func (p *copyPort) MeasureSharedChannelValues(context.Context, read.BrowseOutput, read.SharedChannelRetainedValues) (read.SharedChannelCopySize, error) {
	return p.size, p.measureError
}
func (p *copyPort) CloneSharedChannelValues(_ context.Context, source, retained read.BrowseOutput, values read.SharedChannelRetainedValues) (read.SharedChannelRetainedValues, error) {
	if retained.Grant == source.Grant || retained.Request.Writer || retained.Request.Owner != source.Request.Owner || retained.Request.Class != governor.Background || retained.Request.Need != (governor.Amounts{governor.MemoryBytes: p.size.ChargeBytes}) || len(retained.Request.IO) != 0 {
		panic("incorrect retained admission")
	}
	if p.clone != nil {
		p.clone(retained)
	}
	return values, p.cloneError
}

type custodyFixture struct {
	g       governor.Governor
	a       *governor.Authority
	source  read.BrowseOutput
	port    *copyPort
	driver  *Driver
	values  read.SharedChannelRetainedValues
	retired bool
}

func custody(t *testing.T, capacity int64) *custodyFixture {
	t.Helper()
	cfg := governor.Config{Now: time.Now, MaxGrantsPerClass: 8}
	cfg.Partitions[governor.Background][governor.MemoryBytes] = capacity
	g, a, err := governor.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	q := governor.Request{Key: governor.JobKey{Domain: 396, ID: "source"}, Owner: 1, Class: governor.Background, Deadline: time.Now().Add(time.Hour), Need: governor.Amounts{governor.MemoryBytes: 32768}}
	decision := g.Try(q)
	if decision.Grant == nil {
		t.Fatal(decision.Refusal)
	}
	d, err := NewDriver(&read.SharedChannelReader{}, g, a, "generation", 1)
	if err != nil {
		t.Fatal(err)
	}
	p := &copyPort{size: read.SharedChannelCopySize{PayloadBytes: 4096, ChargeBytes: 12288}}
	d.port = p
	f := &custodyFixture{g: g, a: a, source: read.BrowseOutput{Authority: a, Grant: decision.Grant, Request: q}, port: p, driver: d,
		values: read.SharedChannelRetainedValues{Scope: &read.SharedChannelScope{}, Info: &read.SharedChannelScopeInfo{Generation: "generation"}}}
	t.Cleanup(func() {
		if !f.retired {
			f.retireSource(t)
		}
	})
	return f
}
func (f *custodyFixture) retireSource(t *testing.T) {
	t.Helper()
	f.g.Cancel(f.source.Grant)
	if err := f.a.Retired(f.source.Grant); err != nil {
		t.Fatal(err)
	}
	f.retired = true
}
func (f *custodyFixture) used() int64 {
	return f.a.Usage()[governor.Background][governor.MemoryBytes]
}

func TestMGLCRetainedAdmissionAndReturn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := custody(t, 65536)
		f.port.clone = func(read.BrowseOutput) {
			if f.used() != 32768+f.port.size.ChargeBytes {
				t.Fatal("copy began before actual admission", f.used())
			}
		}
		called := false
		size, err := f.driver.WithValues(t.Context(), f.source, f.values, func(_ context.Context, values read.SharedChannelRetainedValues) error {
			called = true
			if values.Info.Generation != "generation" || f.used() != 32768+f.port.size.ChargeBytes {
				t.Fatal("consumer lost copy custody")
			}
			return nil
		})
		if err != nil || !called || size != f.port.size || f.used() != 32768 || f.a.Check(f.source.Grant, f.source.Request) != nil {
			t.Fatal("retirement affected original output", size, err, f.used())
		}
	})
}

func TestMGLCRetainedErrorsDoNotLeak(t *testing.T) {
	for _, mode := range []string{"measure", "clone", "consumer", "capacity", "wrong-generation", "wrong-owner"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				capacity := int64(65536)
				if mode == "capacity" {
					capacity = 32768 + 12288 - 1
				}
				f := custody(t, capacity)
				failure := errors.New("controlled failure")
				if mode == "measure" {
					f.port.measureError = failure
				} else if mode == "clone" {
					f.port.cloneError = failure
				} else if mode == "wrong-generation" {
					f.values.Info.Generation = "other"
				} else if mode == "wrong-owner" {
					f.driver.owner++
				}
				called := false
				size, err := f.driver.WithValues(t.Context(), f.source, f.values, func(context.Context, read.SharedChannelRetainedValues) error {
					called = true
					return failure
				})
				if err == nil || size != (read.SharedChannelCopySize{}) || called != (mode == "consumer") || f.used() != 32768 || f.a.Check(f.source.Grant, f.source.Request) != nil {
					t.Fatal("failed copy leaked custody/result", size, err, called, f.used())
				}
			})
		})
	}
}

func TestMGLCRetainedCancellationKeepsConsumerDebt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := custody(t, 65536)
		ctx, cancel := context.WithCancel(t.Context())
		entered, finish := make(chan struct{}), make(chan struct{})
		result := make(chan error, 1)
		go func() {
			_, err := f.driver.WithValues(ctx, f.source, f.values, func(context.Context, read.SharedChannelRetainedValues) error {
				close(entered)
				<-finish
				return nil
			})
			result <- err
		}()
		<-entered
		cancel()
		if f.used() != 32768+f.port.size.ChargeBytes {
			t.Fatal("cancellation refunded an active consumer")
		}
		close(finish)
		if err := <-result; !errors.Is(err, context.Canceled) || f.used() != 32768 {
			t.Fatal("consumer exit did not retire copied memory", err, f.used())
		}
	})
}

func TestMGLCRetainedPanicAndOriginalOutputRetirement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := custody(t, 65536)
		original := new(int)
		func() {
			defer func() {
				if recover() != original {
					t.Fatal("consumer panic was replaced")
				}
			}()
			_, _ = f.driver.WithValues(t.Context(), f.source, f.values, func(context.Context, read.SharedChannelRetainedValues) error { panic(original) })
		}()
		if f.used() != 32768 {
			t.Fatal("panic stranded copied buffers")
		}
		size, err := f.driver.WithValues(t.Context(), f.source, f.values, func(context.Context, read.SharedChannelRetainedValues) error {
			f.retireSource(t)
			if f.used() != f.port.size.ChargeBytes {
				t.Fatal("owned copy depended on original grant")
			}
			return nil
		})
		if err != nil || size != f.port.size || f.used() != 0 {
			t.Fatal(size, err, f.used())
		}
	})
}

func TestMGLCWindowKeepsRawContinuation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := custody(t, 65536)
		cursor := &read.SharedChannelCursor{}
		f.port.window = read.SharedChannelBatch{After: cursor, RawFetched: 4, RawConsumed: 4, RawEOF: false}
		called := false
		q := governor.Request{Class: governor.Background, Owner: 1}
		_, _, err := f.driver.WithWindow(t.Context(), q, time.Second, f.source, read.SharedChannelInput{Scope: f.values.Scope}, *f.values.Info, func(_ context.Context, values read.SharedChannelRetainedValues) error {
			called = true
			if len(values.Batch.Items) != 0 || values.Batch.RawEOF || values.Batch.RawConsumed != 4 || values.Batch.After == nil || values.Cursor != values.Batch.After {
				t.Fatal("empty non-EOF batch became exhaustion")
			}
			return nil
		})
		if err != nil || !called {
			t.Fatal(err)
		}
	})
}

func TestMGLCScopeReadBeforeRetainedConsumer(t *testing.T) {
	for _, mode := range []string{"success", "read-error", "wrong-generation", "wrong-owner", "foreground", "nil-consumer"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := custody(t, 65536)
				request := governor.Request{Owner: 1, Class: governor.Background}
				in := read.SharedChannelScopeInput{Channel: "channel", Generation: "generation", Rule: "rule", Library: "movies", ExpectedRevision: 1}
				failure := errors.New("scope refused")
				switch mode {
				case "read-error":
					f.port.scopeError = failure
				case "wrong-generation":
					in.Generation = "other"
				case "wrong-owner":
					request.Owner++
				case "foreground":
					request.Class = governor.Foreground
				}
				called := false
				consumer := func(_ context.Context, values read.SharedChannelRetainedValues) error {
					called = true
					if values.Scope == nil || values.Info == nil || values.Info.Generation != in.Generation || f.used() != 32768+f.port.size.ChargeBytes {
						t.Fatal("scope consumed without admitted custody")
					}
					return nil
				}
				if mode == "nil-consumer" {
					consumer = nil
				}
				cost, size, err := f.driver.WithScope(t.Context(), request, time.Second, f.source, in, consumer)
				wantReads := 0
				if mode == "success" || mode == "read-error" {
					wantReads = 1
				}
				if f.port.scopeReads != wantReads || called != (mode == "success") || f.used() != 32768 {
					t.Fatal("scope gate or retirement failed", called, f.port.scopeReads, f.used())
				}
				if mode == "success" {
					if err != nil || size != f.port.size || cost.Attempts != 1 {
						t.Fatal(cost, size, err)
					}
				} else if err == nil || size != (read.SharedChannelCopySize{}) || (mode == "read-error" && !errors.Is(err, failure)) {
					t.Fatal("scope refusal became output", size, err)
				}
			})
		})
	}
}
