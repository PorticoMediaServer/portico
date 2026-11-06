package enrich

import (
	"context"
	"errors"
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
