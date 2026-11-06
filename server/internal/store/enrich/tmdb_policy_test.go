package enrich

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestTMDBPolicyPartitionsAndImmutableLimits(t *testing.T) {
	for _, test := range []struct {
		mode    TMDBMode
		workers int
		memory  int64
	}{{TMDBTwoWorkers128MiB, 2, 134217728}, {TMDBOneWorker80MiB, 1, 83886080}} {
		policy, err := NewTMDBPolicy(test.mode)
		if err != nil {
			t.Fatal(err)
		}
		limits, err := policy.Limits()
		want := TMDBLimits{4194304, 6291456, 205520896, 151, 4, test.workers, test.memory}
		if err != nil || limits != want {
			t.Fatal(limits, want, err)
		}
		// Copies cannot enlarge either the reservation or the content contract.
		limits.Workers, limits.StoredContentBytes = 64, 1<<40
		limits, err = policy.Limits()
		if err != nil || limits != want {
			t.Fatal("mutable policy", limits, err)
		}
		transport, err := NewTMDBTransport(policy, admissionFunc(allowed), func(context.Context, RequestKey, int64, Dispatch) (Response, error) {
			t.Fatal("configuration dispatched HTTP")
			return Response{}, nil
		}, time.Minute, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		cfg := transport.cfg
		transport.Close()
		if cfg.Provider != "tmdb" || cfg.RequestsPerSecond != 40 || cfg.MaxInFlight != test.workers || cfg.MaxPending != test.workers || cfg.MaxWaiters != test.workers || cfg.MaxResponseBytes != 4194304 || cfg.CacheBytes != 16777216 || cfg.CacheEntries != 256 {
			t.Fatal("unbudgeted transport pools", cfg)
		}
		if int64(cfg.MaxPending)*cfg.MaxResponseBytes > int64(test.workers)*48<<20 || cfg.CacheBytes+16<<20+int64(test.workers)*48<<20 != test.memory {
			t.Fatal("partition arithmetic", cfg)
		}
	}
	for _, mode := range []TMDBMode{0, 3, 255} {
		if p, err := NewTMDBPolicy(mode); !errors.Is(err, ErrTMDBPolicy) || p != (TMDBPolicy{}) {
			t.Fatal("unknown policy accepted", p, err)
		}
	}
	if p, err := NewTMDBTransport(TMDBPolicy{}, admissionFunc(allowed), nil, time.Minute, time.Second); !errors.Is(err, ErrTMDBPolicy) || p != nil {
		t.Fatal("missing background policy became a transport", p, err)
	}
}

func TestTransportOversizeReportsObservedBytes(t *testing.T) {
	cfg := config()
	cfg.MaxResponseBytes = 3
	transport := broker(t, cfg, admissionFunc(allowed), func(context.Context, RequestKey, int64) (Response, error) {
		return Response{Status: 200, Body: []byte("1234")}, nil
	})
	body, err := transport.Get(t.Context(), key("oversized"))
	var size *ResponseSizeError
	if body != nil || !errors.Is(err, ErrResponse) || !errors.As(err, &size) || size.LimitBytes != 3 || size.ObservedBytes != 4 || size.ObservedIsLowerBound {
		t.Fatal("oversize lost typed observation", body, err)
	}
	if !strings.Contains(err.Error(), "4") {
		t.Fatal("observed size absent", err)
	}
	stats := transport.Stats()
	if stats.Bytes != 0 || stats.CacheBytes != 0 || stats.Failures != 1 {
		t.Fatal("oversize accepted/cached", stats)
	}
	bounded := &ResponseSizeError{LimitBytes: 4194304, ObservedBytes: 4194305, ObservedIsLowerBound: true}
	if !errors.Is(bounded, ErrResponse) || !strings.Contains(bounded.Error(), "at least 4194305") {
		t.Fatal("lower bound relabelled", bounded)
	}
}

func TestTMDBCacheRetainedBackingFitsBudget(t *testing.T) {
	for _, test := range []struct {
		mode           TMDBMode
		bodyBytes      int
		allocationCost int64
	}{{TMDBTwoWorkers128MiB, 65537, 73728}, {TMDBOneWorker80MiB, 131073, 139264}, {TMDBTwoWorkers128MiB, 33, 64}} {
		t.Run(fmt.Sprint(test.bodyBytes), func(t *testing.T) {
			policy, err := NewTMDBPolicy(test.mode)
			if err != nil {
				t.Fatal(err)
			}
			payload := make([]byte, test.bodyBytes)
			tr, err := NewTMDBTransport(policy, admissionFunc(allowed), func(_ context.Context, _ RequestKey, _ int64, d Dispatch) (Response, error) {
				if err := d.Check(); err != nil {
					return Response{}, err
				}
				if err := d.Started(); err != nil {
					return Response{}, err
				}
				return Response{Status: 200, Body: payload}, nil
			}, time.Hour, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer tr.Close()
			clock := newFakeClock()
			tr.clock = clock
			for i := range 300 {
				clock.advance(time.Second)
				if _, err := tr.Get(t.Context(), key(fmt.Sprint(i))); err != nil {
					t.Fatal(err)
				}
			}
			tr.mu.Lock()
			defer tr.mu.Unlock()
			var lengths, capacities, rounded int64
			exact := true
			for _, entry := range tr.cache {
				body := entry.Value.(cachedResponse).body
				lengths += int64(len(body))
				capacities += int64(cap(body))
				// Independent literal allocator bounds for these non-aligned
				// sizes; slice capacity is NOT a retained-heap measurement.
				rounded += test.allocationCost
				exact = exact && cap(body) == len(body)
			}
			t.Logf("entries=%d charged=%d length=%d capacity=%d rounded=%d budget=%d", len(tr.cache), tr.stats.CacheBytes, lengths, capacities, rounded, tr.cfg.CacheBytes)
			if rounded > tr.cfg.CacheBytes {
				t.Fatalf("allocator-rounded body storage exceeds cache budget by %d bytes", rounded-tr.cfg.CacheBytes)
			}
			if !exact || rounded != tr.stats.CacheBytes || capacities != lengths {
				t.Fatal("cache does not charge its allocator-rounded body storage")
			}
		})
	}
}

func TestTMDBCacheAllocationChargeEdges(t *testing.T) {
	for _, test := range []struct {
		bytes  int
		charge int64
	}{
		{0, 0}, {1, 16}, {8, 16}, {16, 16}, {17, 32}, {24, 32}, {33, 64},
		{513, 1024}, {1025, 2048}, {16385, 32768}, {32768, 32768},
		{32769, 40960}, {65537, 73728}, {131073, 139264}, {4194304, 4194304},
	} {
		if got := cacheBodyCharge(test.bytes); got != test.charge {
			t.Fatal(test.bytes, got, test.charge)
		}
	}
}
