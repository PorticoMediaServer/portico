package enrich

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// These controls use the real resource ledger and mocked acquisition only.
// No HTTP connection, listener, credential or provider is used.
func tmdbHoldingGovernor(t *testing.T, memory int64) (governor.Governor, *governor.Authority, *governor.WALGate) {
	t.Helper()
	wal, err := governor.NewWALGate(governor.WALPolicy{
		HardBytes: 1 << 20, ReserveBytes: 64 << 10, SoftDebtBytes: 256 << 10,
		ActivePressureBytes: 512 << 10, MaxSampleAge: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := governor.Config{Now: time.Now, MaxGrantsPerClass: 16, WAL: wal}
	cfg.Partitions[governor.Background] = governor.Amounts{
		governor.MemoryBytes: memory, governor.CPUMilli: 2000, governor.IOBytes: 8 << 20,
	}
	g, authority, err := governor.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return g, authority, wal
}

func tmdbHoldingConfig(t *testing.T, mode TMDBMode) TMDBProducerConfig {
	t.Helper()
	policy, err := NewTMDBPolicy(mode)
	if err != nil {
		t.Fatal(err)
	}
	cfg := TMDBProducerConfig{
		Policy: policy, Owner: 17, LibraryID: 19, Deadline: time.Now().Add(time.Minute),
		HTTP: tmdbHTTPHarnessConfig(),
	}
	cfg.HTTP.MaxMetadataBytes = 64 << 10
	return cfg
}

func tmdbHoldingOpen(cfg TMDBProducerConfig, fetch Fetch) func(NetworkAdmission) (*Transport, error) {
	return func(admission NetworkAdmission) (*Transport, error) {
		transport, err := tmdbTransportConfig(cfg.Policy, time.Hour, time.Minute)
		if err != nil {
			return nil, err
		}
		transport.MaxMetadataBytes = 64 << 10
		return NewTransport(transport, admission, fetch)
	}
}

func tmdbHoldingTarget() TMDBMovieTarget {
	return TMDBMovieTarget{"evidence-native:149", "42", "en-CA", "CA", 151}
}

func tmdbHoldingResponse(dispatch Dispatch) (Response, error) {
	if err := dispatch.Check(); err != nil {
		return Response{}, err
	}
	if err := dispatch.Started(); err != nil {
		return Response{}, err
	}
	return Response{Status: 200, Body: []byte(tmdbMovieFixture), Metadata: documentMetadata()}, nil
}

func tmdbHoldingAwait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("mock holding did not reach its controlled boundary")
		var zero T
		return zero
	}
}

func tmdbHoldingStillBlocked(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("physical holding returned before controlled release: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestTMDBHoldingFullProfileBeforeTransportAndAcquisition(t *testing.T) {
	for _, test := range []struct {
		name string
		mode TMDBMode
	}{{"one_worker_80MiB", TMDBOneWorker80MiB}, {"two_workers_128MiB", TMDBTwoWorkers128MiB}} {
		cfg := tmdbHoldingConfig(t, test.mode)
		limits, _ := cfg.Policy.Limits()
		t.Run(test.name, func(t *testing.T) {
			g, authority, wal := tmdbHoldingGovernor(t, limits.MemoryBytes-1)
			opened := false
			producer, err := newTMDBProducer(t.Context(), cfg, g, authority, wal,
				func(context.Context, writer.EvidenceStagePayload) error { return nil },
				func(NetworkAdmission) (*Transport, error) { opened = true; return nil, nil })
			if producer != nil || !errors.Is(err, ErrBusy) || opened || authority.Usage() != [4]governor.Amounts{} {
				t.Fatal("one-byte profile pressure reached transport construction or retained a partial grant", err, opened, authority.Usage())
			}

			g, authority, wal = tmdbHoldingGovernor(t, limits.MemoryBytes)
			_, foreign, _ := tmdbHoldingGovernor(t, limits.MemoryBytes)
			producer, err = newTMDBProducer(t.Context(), cfg, g, foreign, wal,
				func(context.Context, writer.EvidenceStagePayload) error { return nil },
				func(NetworkAdmission) (*Transport, error) { opened = true; return nil, nil })
			if producer != nil || !errors.Is(err, ErrTMDBPolicy) || opened || authority.Usage() != [4]governor.Amounts{} {
				t.Fatal("foreign authority admitted producer composition", err, opened, authority.Usage())
			}

			consumed := false
			producer, err = newTMDBProducer(t.Context(), cfg, g, authority, wal,
				func(context.Context, writer.EvidenceStagePayload) error { consumed = true; return nil },
				func(admission NetworkAdmission) (*Transport, error) {
					opened = true
					want := governor.Amounts{governor.MemoryBytes: limits.MemoryBytes}
					if got := authority.Usage()[governor.Background]; got != want {
						t.Error("transport opened before full adopted forecast", got, want)
					}
					return tmdbHoldingOpen(cfg, func(_ context.Context, _ RequestKey, limit int64, dispatch Dispatch) (Response, error) {
						want := governor.Amounts{governor.MemoryBytes: limits.MemoryBytes, governor.IOBytes: (4 << 20) + (64 << 10)}
						if got := authority.Usage()[governor.Background]; got != want || limit != 4<<20 {
							t.Error("acquisition forecast/CPU/IO custody changed", got, want, limit)
						}
						return tmdbHoldingResponse(dispatch)
					})(admission)
				})
			if err != nil {
				t.Fatal(err)
			}
			defer producer.Close()
			if err = producer.ProcessMovie(t.Context(), tmdbHoldingTarget()); err != nil || !consumed {
				t.Fatal("funded profile could not reach the native consumer", err, consumed)
			}
			if err = producer.Close(); err != nil || authority.Usage() != [4]governor.Amounts{} {
				t.Fatal("closed producer retained physical debt", err, authority.Usage())
			}
		})
	}
}

func TestTMDBHoldingCancellationRetainsNativeConsumerCustody(t *testing.T) {
	cfg := tmdbHoldingConfig(t, TMDBOneWorker80MiB)
	g, authority, wal := tmdbHoldingGovernor(t, 80<<20)
	entered := make(chan governor.Amounts, 1)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	producer, err := newTMDBProducer(t.Context(), cfg, g, authority, wal,
		func(_ context.Context, payload writer.EvidenceStagePayload) error {
			if payload.Header().ID() == "" || payload.CreditCount() != 2 {
				t.Error("native consumer lost selected output")
			}
			entered <- authority.Usage()[governor.Background]
			<-release // Simulate native code that is still consuming selected output.
			return nil
		}, tmdbHoldingOpen(cfg, func(_ context.Context, _ RequestKey, _ int64, dispatch Dispatch) (Response, error) {
			return tmdbHoldingResponse(dispatch)
		}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { unblock(); _ = producer.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- producer.ProcessMovie(ctx, tmdbHoldingTarget()) }()
	want := governor.Amounts{governor.MemoryBytes: 80 << 20}
	if got := tmdbHoldingAwait(t, entered); got != want {
		t.Fatal("native consumer retained CPU/IO or lost selected memory", got, want)
	}
	cancel()
	if err = producer.ProcessMovie(t.Context(), tmdbHoldingTarget()); !errors.Is(err, ErrBusy) {
		t.Fatal("canceled native consumer recycled its worker slot", err)
	}
	closed := make(chan error, 1)
	go func() { closed <- producer.Close() }()
	tmdbHoldingStillBlocked(t, closed)
	if got := authority.Usage()[governor.Background]; got != want {
		t.Fatal("cancel/Close refunded active native consumer memory", got, want)
	}
	unblock()
	if err = tmdbHoldingAwait(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal("consumer completion lost caller cancellation", err)
	}
	if err = tmdbHoldingAwait(t, closed); err != nil || authority.Usage() != [4]governor.Amounts{} {
		t.Fatal("joined native consumer retained debt", err, authority.Usage())
	}
}

func TestTMDBHoldingCancellationJoinsPhysicalAcquisition(t *testing.T) {
	cfg := tmdbHoldingConfig(t, TMDBOneWorker80MiB)
	g, authority, wal := tmdbHoldingGovernor(t, 80<<20)
	entered, canceled := make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	producer, err := newTMDBProducer(t.Context(), cfg, g, authority, wal,
		func(context.Context, writer.EvidenceStagePayload) error {
			t.Error("canceled acquisition reached consumer")
			return nil
		},
		tmdbHoldingOpen(cfg, func(ctx context.Context, _ RequestKey, _ int64, dispatch Dispatch) (Response, error) {
			if err := dispatch.Check(); err != nil {
				return Response{}, err
			}
			if err := dispatch.Started(); err != nil {
				return Response{}, err
			}
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release // Cancellation cannot attest that this physical adapter exited.
			return Response{}, ctx.Err()
		}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { unblock(); _ = producer.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- producer.ProcessMovie(ctx, tmdbHoldingTarget()) }()
	tmdbHoldingAwait(t, entered)
	cancel()
	tmdbHoldingAwait(t, canceled)
	tmdbHoldingStillBlocked(t, done)
	if err = producer.ProcessMovie(t.Context(), tmdbHoldingTarget()); !errors.Is(err, ErrBusy) {
		t.Fatal("canceled physical acquisition recycled its worker slot", err)
	}
	closed := make(chan error, 1)
	go func() { closed <- producer.Close() }()
	tmdbHoldingStillBlocked(t, closed)
	want := governor.Amounts{governor.MemoryBytes: 80 << 20, governor.IOBytes: (4 << 20) + (64 << 10)}
	if got := authority.Usage()[governor.Background]; got != want {
		t.Fatal("canceled physical acquisition lost memory/IO or held CPU", got, want)
	}
	unblock()
	if err = tmdbHoldingAwait(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal("joined acquisition lost caller cancellation", err)
	}
	if err = tmdbHoldingAwait(t, closed); err != nil || authority.Usage() != [4]governor.Amounts{} {
		t.Fatal("physical acquisition did not retire after exit", err, authority.Usage())
	}
}
