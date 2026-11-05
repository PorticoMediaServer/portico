package enrich

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type admissionFunc func(context.Context, int64) (func(), error)

func (f admissionFunc) Try(ctx context.Context, n int64) (func(), error) { return f(ctx, n) }
func allowed(context.Context, int64) (func(), error)                     { return func() {}, nil }
func config() TransportConfig {
	return TransportConfig{Provider: "tmdb", RequestsPerSecond: 40, MaxInFlight: 16, MaxPending: 32, MaxWaiters: 64, MaxResponseBytes: 1024, CacheBytes: 4096, CacheEntries: 4, CacheTTL: time.Minute, RequestTimeout: 10 * time.Second}
}
func key(id string) RequestKey {
	return RequestKey{Provider: "tmdb", Kind: "season", ExternalID: id, Locale: "en", Shape: "details+credits+images", Authority: "policy-1"}
}
func broker(t *testing.T, cfg TransportConfig, a NetworkAdmission, f func(context.Context, RequestKey, int64) (Response, error)) *Transport {
	t.Helper()
	tr, err := NewTransport(cfg, a, f)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tr.Close)
	return tr
}
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition timed out")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestTransportSharesRequestsAndReusesPrivateBodies(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var attempts atomic.Int32
	tr := broker(t, config(), admissionFunc(allowed), func(context.Context, RequestKey, int64) (Response, error) {
		attempts.Add(1)
		close(entered)
		<-release
		return Response{Status: 200, Body: []byte("facts")}, nil
	})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			body, err := tr.Get(context.Background(), key("shared"))
			if err != nil || string(body) != "facts" {
				t.Errorf("%q %v", body, err)
			}
			if len(body) > 0 {
				body[0] = 'X'
			}
		})
	}
	<-entered
	waitFor(t, func() bool { return tr.Stats().Waiters == 8 })
	close(release)
	wg.Wait()
	body, err := tr.Get(context.Background(), key("shared"))
	if err != nil || string(body) != "facts" {
		t.Fatalf("%q %v", body, err)
	}
	s := tr.Stats()
	if attempts.Load() != 1 || s.Requests != 9 || s.Attempts != 1 || s.Deduplicated != 7 || s.Reused != 1 || s.Pending != 0 || s.Waiters != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestTransportCancellationRetainsPhysicalCustody(t *testing.T) {
	cfg := config()
	cfg.MaxInFlight = 1
	cfg.MaxPending = 1
	entered, release := make(chan struct{}), make(chan struct{})
	var retired atomic.Int32
	tr := broker(t, cfg, admissionFunc(func(context.Context, int64) (func(), error) { return func() { retired.Add(1) }, nil }), func(ctx context.Context, _ RequestKey, _ int64) (Response, error) {
		close(entered)
		<-ctx.Done()
		<-release
		return Response{}, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := tr.Get(ctx, key("a")); done <- err }()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := tr.Get(context.Background(), key("b")); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	if _, err := tr.Get(context.Background(), key("a")); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	if retired.Load() != 0 || tr.Stats().Pending != 1 {
		t.Fatal("returned capacity before physical completion")
	}
	close(release)
	tr.Close()
	if retired.Load() != 1 || tr.Stats().Pending != 0 {
		t.Fatal("physical resources leaked")
	}
	if _, err := tr.Get(context.Background(), key("a")); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestTransportOneCallerCannotCancelOtherWaiters(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	tr := broker(t, config(), admissionFunc(allowed), func(ctx context.Context, _ RequestKey, _ int64) (Response, error) {
		close(entered)
		select {
		case <-release:
			return Response{Status: 200, Body: []byte("ok")}, nil
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 2)
	go func() { _, err := tr.Get(ctx, key("a")); done <- err }()
	<-entered
	go func() { _, err := tr.Get(context.Background(), key("a")); done <- err }()
	waitFor(t, func() bool { return tr.Stats().Waiters == 2 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestTransportKeysAndCacheBounds(t *testing.T) {
	cfg := config()
	cfg.CacheBytes = 6
	cfg.CacheEntries = 2
	tr := broker(t, cfg, admissionFunc(allowed), func(context.Context, RequestKey, int64) (Response, error) {
		return Response{Status: 200, Body: []byte("abc")}, nil
	})
	keys := []RequestKey{key("x"), key("x"), key("x"), key("x"), key("x")}
	keys[1].Locale = "fr"
	keys[2].Shape = "details"
	keys[3].Authority = "policy-2"
	keys[4].Kind = "series"
	for _, k := range keys {
		if _, err := tr.Get(context.Background(), k); err != nil {
			t.Fatal(err)
		}
	}
	if tr.Stats().Attempts != 5 || tr.Stats().CacheBytes != 6 {
		t.Fatal(tr.Stats())
	}
	if _, err := tr.Get(context.Background(), keys[4]); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Get(context.Background(), keys[0]); err != nil {
		t.Fatal(err)
	}
	if tr.Stats().Attempts != 6 || tr.Stats().Reused != 1 {
		t.Fatal(tr.Stats())
	}
	bad := key("x")
	bad.Provider = "tvdb"
	if _, err := tr.Get(context.Background(), bad); err == nil {
		t.Fatal("provider crossed bucket")
	}
	tr.mu.Lock()
	tr.cache[keys[0]].Value = cachedResponse{key: keys[0], body: []byte("abc"), expires: time.Now().Add(-time.Second)}
	tr.mu.Unlock()
	if _, err := tr.Get(context.Background(), keys[0]); err != nil {
		t.Fatal(err)
	}
	if tr.Stats().Attempts != 7 {
		t.Fatal("expired document reused")
	}
}

func TestTransportFailuresAndCooldownAreShared(t *testing.T) {
	var starts []time.Time
	tr := broker(t, config(), admissionFunc(allowed), func(_ context.Context, k RequestKey, _ int64) (Response, error) {
		starts = append(starts, time.Now())
		if k.ExternalID == "429" {
			return Response{Status: 429, RetryAfter: time.Second}, nil
		}
		return Response{Status: 200}, nil
	})
	if _, err := tr.Get(context.Background(), key("429")); err == nil {
		t.Fatal("429 accepted")
	}
	if _, err := tr.Get(context.Background(), key("ok")); err != nil {
		t.Fatal(err)
	}
	if gap := starts[1].Sub(starts[0]); gap < time.Second {
		t.Fatalf("cooldown ignored: %s", gap)
	}
	if tr.Stats().Failures != 1 || tr.Stats().Attempts != 2 {
		t.Fatal(tr.Stats())
	}
}

func TestTransportRefusesAdmissionAndOversizeWithoutCaching(t *testing.T) {
	tr := broker(t, config(), admissionFunc(func(context.Context, int64) (func(), error) { return nil, ErrBusy }), func(context.Context, RequestKey, int64) (Response, error) {
		t.Error("network without grant")
		return Response{}, nil
	})
	if _, err := tr.Get(context.Background(), key("a")); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	if tr.Stats().Attempts != 0 {
		t.Fatal(tr.Stats())
	}
	cfg := config()
	cfg.MaxResponseBytes = 2
	tr2 := broker(t, cfg, admissionFunc(allowed), func(context.Context, RequestKey, int64) (Response, error) {
		return Response{Status: 200, Body: []byte("abc")}, nil
	})
	for range 2 {
		if _, err := tr2.Get(context.Background(), key("a")); !errors.Is(err, ErrResponse) {
			t.Fatal(err)
		}
	}
	if tr2.Stats().Attempts != 2 || tr2.Stats().CacheBytes != 0 {
		t.Fatal(tr2.Stats())
	}
}

func TestTransportWaiterLimitAndConfiguration(t *testing.T) {
	cfg := config()
	cfg.MaxWaiters = cfg.MaxPending
	entered, release := make(chan struct{}), make(chan struct{})
	tr := broker(t, cfg, admissionFunc(allowed), func(context.Context, RequestKey, int64) (Response, error) {
		close(entered)
		<-release
		return Response{Status: 200}, nil
	})
	var wg sync.WaitGroup
	for range cfg.MaxWaiters {
		wg.Go(func() {
			_, err := tr.Get(context.Background(), key("same"))
			if err != nil {
				t.Error(err)
			}
		})
	}
	<-entered
	waitFor(t, func() bool { return tr.Stats().Waiters == cfg.MaxWaiters })
	if _, err := tr.Get(context.Background(), key("same")); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	cfg.RequestsPerSecond = 41
	if _, err := NewTransport(cfg, admissionFunc(allowed), nil); err == nil {
		t.Fatal("invalid config accepted")
	}
}

// Loopback transport capacity, not live TMDB or whole-pipeline qualification.
// This catches the old four-in-flight/200ms RTT cap (~20 req/s) without making
// hosted scheduler jitter a flaky throughput gate. Every timestamp is retained
// by the benchmark and request starts must still respect the provider ceiling.
func TestTransportLatencyOverlapAndPacing(t *testing.T) {
	cfg := config()
	cfg.MaxPending = 128
	cfg.MaxWaiters = 256
	cfg.CacheTTL = 0
	var mu sync.Mutex
	var starts []time.Time
	tr := broker(t, cfg, admissionFunc(allowed), func(ctx context.Context, _ RequestKey, _ int64) (Response, error) {
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		timer := time.NewTimer(200 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return Response{Status: 200, Body: []byte("ok")}, nil
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
	})
	var wg sync.WaitGroup
	for i := range 80 {
		wg.Go(func() {
			_, err := tr.Get(context.Background(), key(fmt.Sprint(i)))
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(starts) != 80 {
		t.Fatal(len(starts))
	}
	for i := 1; i < len(starts); i++ {
		if gap := starts[i].Sub(starts[i-1]); gap < 24*time.Millisecond {
			t.Fatalf("start burst: %s", gap)
		}
	}
	s := tr.Stats()
	if s.PeakInFlight < 5 || s.PeakInFlight > cfg.MaxInFlight {
		t.Fatalf("inflight overlap %+v", s)
	}
	t.Logf("loopback: requests=%d attempts=%d peak_inflight=%d start_rate=%.3f req/s, RTT=200ms; reuse=%d dedupe=%d", s.Requests, s.Attempts, s.PeakInFlight, float64(len(starts)-1)/starts[len(starts)-1].Sub(starts[0]).Seconds(), s.Reused, s.Deduplicated)
}

func TestTransportRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{{"", time.Second}, {"nonsense", time.Second}, {"-2", time.Second}, {"0", time.Second}, {"12", 12 * time.Second}, {"86401", 24 * time.Hour}, {now.Add(3 * time.Second).Format(time.RFC1123), 3 * time.Second}, {now.Add(-time.Minute).Format(time.RFC1123), time.Second}} {
		// HTTP-date spells UTC as GMT.
		value := tc.value
		if len(value) > 3 && value[len(value)-3:] == "UTC" {
			value = value[:len(value)-3] + "GMT"
		}
		if got := ParseRetryAfter(value, now); got != tc.want {
			t.Errorf("%q: %s want %s", value, got, tc.want)
		}
	}
}

func TestTransportRechecksCooldownAfterAdmission(t *testing.T) {
	var tr *Transport
	var calls atomic.Int32
	var retired atomic.Int32
	tr = broker(t, config(), admissionFunc(func(context.Context, int64) (func(), error) {
		if calls.Add(1) == 1 {
			tr.mu.Lock()
			tr.next = time.Now().Add(50 * time.Millisecond)
			tr.mu.Unlock()
		}
		return func() { retired.Add(1) }, nil
	}), func(context.Context, RequestKey, int64) (Response, error) {
		if retired.Load() != 1 {
			t.Error("held first grant during cooldown")
		}
		return Response{Status: 200}, nil
	})
	if _, err := tr.Get(context.Background(), key("a")); err != nil {
		t.Fatal(err)
	}
	tr.Close()
	if calls.Load() != 2 || retired.Load() != 2 || tr.Stats().Attempts != 1 {
		t.Fatalf("calls=%d retired=%d %+v", calls.Load(), retired.Load(), tr.Stats())
	}
}
