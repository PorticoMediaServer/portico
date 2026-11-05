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

// Logical time is elapsed monotonic service time. Wall jumps are independent.
type fakeTransportClock struct {
	mu      sync.Mutex
	now     time.Duration
	wall    time.Time
	timers  map[chan struct{}]time.Duration
	changed chan struct{}
}

func newFakeClock() *fakeTransportClock {
	return &fakeTransportClock{wall: time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC), timers: make(map[chan struct{}]time.Duration), changed: make(chan struct{})}
}
func (c *fakeTransportClock) Now() time.Duration { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeTransportClock) Wait(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	ch := make(chan struct{})
	c.timers[ch] = later(c.now, d)
	close(c.changed)
	c.changed = make(chan struct{})
	c.mu.Unlock()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.timers, ch)
		c.mu.Unlock()
		return ctx.Err()
	}
}
func (c *fakeTransportClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += d
	for ch, at := range c.timers {
		if at <= c.now {
			close(ch)
			delete(c.timers, ch)
		}
	}
	close(c.changed)
	c.changed = make(chan struct{})
}
func (c *fakeTransportClock) jumpWall(d time.Duration) {
	c.mu.Lock()
	c.wall = c.wall.Add(d)
	c.mu.Unlock()
}
func (c *fakeTransportClock) waitTimer(t *testing.T) {
	t.Helper()
	for {
		c.mu.Lock()
		if len(c.timers) > 0 {
			c.mu.Unlock()
			return
		}
		changed := c.changed
		c.mu.Unlock()
		select {
		case <-changed:
		case <-time.After(3 * time.Second):
			t.Fatal("no pacing timer")
		}
	}
}
func fakeBroker(t *testing.T, cfg TransportConfig, c *fakeTransportClock, f Fetch) *Transport {
	t.Helper()
	tr, err := newTransport(cfg, admissionFunc(allowed), f, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tr.Close)
	return tr
}
func acknowledged(d Dispatch) error {
	if err := d.Check(); err != nil {
		return err
	}
	return d.Started()
}

func TestTransportStalledDispatchAndRollingCeiling(t *testing.T) {
	for _, rate := range []int{32, 40} {
		t.Run(fmt.Sprint(rate), func(t *testing.T) {
			clock := newFakeClock()
			cfg := config()
			cfg.RequestsPerSecond = rate
			cfg.MaxInFlight = 64
			cfg.MaxPending = 64
			cfg.MaxWaiters = 64
			cfg.CacheTTL = 0
			entered := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			starts := make(chan time.Duration, 64)
			var calls atomic.Int32
			tr := fakeBroker(t, cfg, clock, func(ctx context.Context, _ RequestKey, _ int64, d Dispatch) (Response, error) {
				if calls.Add(1) == 1 {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return Response{}, ctx.Err()
					}
				}
				if err := d.Check(); err != nil {
					return Response{}, err
				}
				// This fake's actual send occurs here; acknowledgment follows it.
				at := clock.Now()
				if err := d.Started(); err != nil {
					return Response{}, err
				}
				starts <- at
				return Response{Status: 200}, nil
			})
			var wg sync.WaitGroup
			wg.Go(func() {
				_, err := tr.Get(context.Background(), key("first"))
				if err != nil {
					t.Error(err)
				}
			})
			<-entered
			for i := 0; i < 40; i++ {
				wg.Go(func() {
					_, err := tr.Get(context.Background(), key(fmt.Sprint(i)))
					if err != nil {
						t.Error(err)
					}
				})
			}
			waitFor(t, func() bool { return tr.Stats().Pending == 41 })
			clock.advance(10 * time.Second)
			// The stalled callback has not sent: its permit cannot be handed to another
			// adapter. Advancing time never turns a reservation into dispatch permission.
			if len(tr.pace) != 1 || tr.Stats().Attempts != 0 || calls.Load() != 1 {
				t.Fatal("stalled dispatch released the pacing permit")
			}
			releaseOnce.Do(func() { close(release) })
			times := []time.Duration{<-starts}
			interval := (time.Second + time.Duration(rate) - 1) / time.Duration(rate)
			for len(times) < 41 {
				clock.waitTimer(t)
				clock.advance(interval)
				times = append(times, <-starts)
			}
			wg.Wait()
			for i := 1; i < len(times); i++ {
				if times[i]-times[i-1] < interval {
					t.Fatalf("dispatch gap %s", times[i]-times[i-1])
				}
			}
			for i := range times {
				n := 0
				for _, at := range times[i:] {
					if at-times[i] < time.Second {
						n++
					}
				}
				if n > rate {
					t.Fatalf("%d starts in rolling second at %drps", n, rate)
				}
			}
		})
	}
}

func TestTransportWallJumpsCannotShortenCooldown(t *testing.T) {
	clock := newFakeClock()
	var calls atomic.Int32
	tr := fakeBroker(t, config(), clock, func(_ context.Context, _ RequestKey, _ int64, d Dispatch) (Response, error) {
		if err := acknowledged(d); err != nil {
			return Response{}, err
		}
		if calls.Add(1) == 1 {
			return Response{Status: 429, RetryAfter: 2 * time.Second}, nil
		}
		return Response{Status: 200}, nil
	})
	if _, err := tr.Get(context.Background(), key("rate")); err == nil {
		t.Fatal("429 accepted")
	}
	done := make(chan error, 1)
	go func() { _, err := tr.Get(context.Background(), key("next")); done <- err }()
	clock.waitTimer(t)
	clock.jumpWall(365 * 24 * time.Hour)
	clock.advance(time.Second)
	clock.waitTimer(t)
	if calls.Load() != 1 {
		t.Fatal("wall jump shortened provider floor")
	}
	clock.jumpWall(-730 * 24 * time.Hour)
	clock.advance(time.Second)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("monotonic cooldown did not expire")
	}
}

func TestTransportLongCooldownSuspendsAutomaticRetry(t *testing.T) {
	for _, delay := range []time.Duration{48 * time.Hour, maxDuration} {
		t.Run(fmt.Sprint(delay), func(t *testing.T) {
			clock := newFakeClock()
			var calls atomic.Int32
			tr := fakeBroker(t, config(), clock, func(_ context.Context, _ RequestKey, _ int64, d Dispatch) (Response, error) {
				if err := acknowledged(d); err != nil {
					return Response{}, err
				}
				calls.Add(1)
				return Response{Status: 429, RetryAfter: delay}, nil
			})
			_, err := tr.Get(context.Background(), key("first"))
			var provider *ProviderError
			if !errors.As(err, &provider) || !provider.Suspended || provider.RetryAfter < delay {
				t.Fatalf("provider floor lost: %+v", err)
			}
			clock.advance(24 * time.Hour)
			_, err = tr.Get(context.Background(), key("second"))
			if !errors.As(err, &provider) || !provider.Suspended {
				t.Fatalf("automatic retry not suspended: %v", err)
			}
			if calls.Load() != 1 {
				t.Fatal("retried before provider floor")
			}
		})
	}
}

func TestTransportCooldownCanOnlyExtend(t *testing.T) {
	clock := newFakeClock()
	cfg := config()
	responses := make(chan Response, 2)
	entered := make(chan struct{}, 2)
	tr := fakeBroker(t, cfg, clock, func(ctx context.Context, _ RequestKey, _ int64, d Dispatch) (Response, error) {
		if err := acknowledged(d); err != nil {
			return Response{}, err
		}
		entered <- struct{}{}
		select {
		case r := <-responses:
			return r, nil
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
	})
	done := make(chan error, 2)
	go func() { _, err := tr.Get(context.Background(), key("a")); done <- err }()
	<-entered
	go func() { _, err := tr.Get(context.Background(), key("b")); done <- err }()
	clock.waitTimer(t)
	clock.advance(25 * time.Millisecond)
	<-entered
	responses <- Response{Status: 429, RetryAfter: 5 * time.Second}
	<-done
	tr.mu.Lock()
	floor := tr.next
	tr.mu.Unlock()
	responses <- Response{Status: 503, RetryAfter: time.Second}
	second := <-done
	var provider *ProviderError
	if !errors.As(second, &provider) || provider.RetryAfter < floor-clock.Now() {
		t.Fatalf("durable retry guidance shortened: %v", second)
	}
	tr.mu.Lock()
	got := tr.next
	tr.mu.Unlock()
	if got < floor {
		t.Fatalf("floor shortened %s -> %s", floor, got)
	}
}

func TestTransportLateDispatchRechecksCooldownAndCancel(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			clock := newFakeClock()
			entered := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			var sends atomic.Int32
			tr := fakeBroker(t, config(), clock, func(_ context.Context, _ RequestKey, _ int64, d Dispatch) (Response, error) {
				close(entered)
				<-release
				if err := d.Check(); err != nil {
					return Response{}, err
				}
				sends.Add(1)
				if err := d.Started(); err != nil {
					return Response{}, err
				}
				return Response{Status: 200}, nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := tr.Get(ctx, key("late")); done <- err }()
			<-entered
			if cancelled {
				cancel()
				<-done
				waitFor(t, func() bool {
					tr.mu.Lock()
					defer tr.mu.Unlock()
					for _, c := range tr.flights {
						if c.waiters == 0 {
							return true
						}
					}
					return false
				})
			} else {
				tr.mu.Lock()
				tr.next = 5 * time.Second
				tr.mu.Unlock()
			}
			once.Do(func() { close(release) })
			if !cancelled {
				if err := <-done; err == nil {
					t.Fatal("late dispatch ignored cooldown")
				}
			}
			tr.Close()
			if sends.Load() != 0 {
				t.Fatal("dispatched after cancel/cooldown")
			}
		})
	}
}

func TestTransportRequiresDispatchAcknowledgment(t *testing.T) {
	tr := fakeBroker(t, config(), newFakeClock(), func(context.Context, RequestKey, int64, Dispatch) (Response, error) {
		return Response{Status: 200}, nil
	})
	if _, err := tr.Get(context.Background(), key("missing")); !errors.Is(err, ErrDispatch) {
		t.Fatal(err)
	}
	if tr.Stats().Attempts != 0 {
		t.Fatal("unacknowledged callback counted as dispatch")
	}
}

func TestTransportRejectsDuplicateAcknowledgment(t *testing.T) {
	tr := fakeBroker(t, config(), newFakeClock(), func(_ context.Context, _ RequestKey, _ int64, d Dispatch) (Response, error) {
		if err := acknowledged(d); err != nil {
			return Response{}, err
		}
		_ = d.Started()
		return Response{Status: 200}, nil
	})
	if _, err := tr.Get(context.Background(), key("duplicate")); !errors.Is(err, ErrDispatch) {
		t.Fatal(err)
	}
	if s := tr.Stats(); s.Attempts != 1 || s.InFlight != 0 || s.CacheBytes != 0 {
		t.Fatal(s)
	}
}

func TestTransportResponseAfterBoundaryCooldown(t *testing.T) {
	for _, reportBoundaryError := range []bool{false, true} {
		t.Run(fmt.Sprint(reportBoundaryError), func(t *testing.T) {
			clock := newFakeClock()
			aSent, bSent := make(chan struct{}), make(chan struct{})
			returnA, ackB := make(chan struct{}), make(chan struct{})
			var releaseA, releaseB sync.Once
			defer releaseA.Do(func() { close(returnA) })
			defer releaseB.Do(func() { close(ackB) })
			tr := fakeBroker(t, config(), clock, func(ctx context.Context, k RequestKey, _ int64, d Dispatch) (Response, error) {
				if k.ExternalID == "a" {
					if err := acknowledged(d); err != nil {
						return Response{}, err
					}
					close(aSent)
					select {
					case <-returnA:
						return Response{Status: 429, RetryAfter: 2 * time.Second}, nil
					case <-ctx.Done():
						return Response{}, ctx.Err()
					}
				}
				if err := d.Check(); err != nil {
					return Response{}, err
				}
				close(bSent) // B actually sends here.
				select {
				case <-ackB:
				case <-ctx.Done():
					return Response{}, ctx.Err()
				}
				boundaryErr := d.Started()
				response := Response{Status: 429, RetryAfter: 48 * time.Hour}
				if reportBoundaryError {
					return response, boundaryErr
				}
				return response, nil
			})
			aDone, bDone := make(chan error, 1), make(chan error, 1)
			go func() { _, err := tr.Get(context.Background(), key("a")); aDone <- err }()
			<-aSent
			go func() { _, err := tr.Get(context.Background(), key("b")); bDone <- err }()
			clock.waitTimer(t)
			clock.advance(25 * time.Millisecond)
			<-bSent
			releaseA.Do(func() { close(returnA) })
			<-aDone // A's 2-second floor is installed.
			releaseB.Do(func() { close(ackB) })
			err := <-bDone
			var provider *ProviderError
			if !errors.As(err, &provider) || provider.RetryAfter < 48*time.Hour || !provider.Suspended {
				t.Fatalf("stronger response lost after boundary error: %+v", err)
			}
			tr.mu.Lock()
			remaining, suspended := tr.next-clock.Now(), tr.suspended
			tr.mu.Unlock()
			if remaining < 48*time.Hour || !suspended {
				t.Fatalf("bucket floor=%s suspended=%v", remaining, suspended)
			}
		})
	}
}

func TestTransportProtocolFailureStillRecordsProviderFloor(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprint(duplicate), func(t *testing.T) {
			clock := newFakeClock()
			tr := fakeBroker(t, config(), clock, func(_ context.Context, _ RequestKey, _ int64, d Dispatch) (Response, error) {
				if duplicate {
					if err := acknowledged(d); err != nil {
						return Response{}, err
					}
					_ = d.Started()
				}
				return Response{Status: 429, RetryAfter: 48 * time.Hour}, nil
			})
			if _, err := tr.Get(context.Background(), key("protocol")); !errors.Is(err, ErrDispatch) {
				t.Fatalf("protocol failure replaced: %v", err)
			}
			tr.mu.Lock()
			remaining, suspended := tr.next-clock.Now(), tr.suspended
			tr.mu.Unlock()
			if remaining < 48*time.Hour || !suspended {
				t.Fatalf("observed provider floor lost: %s %v", remaining, suspended)
			}
		})
	}
}
