package governor

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/hostlimits"
)

func TestForegroundKnownMemorySourceTypedRefusal(t *testing.T) {
	p := ForegroundProfile{AssignedBytes: 2_000_000_000, EffectiveBytes: 2_000_000_000, NonBrowseBytes: 1360 << 20, MinimumLive: 8}
	n, err := p.CapacityAtLimit(64 << 20)
	var refusal *ForegroundBudgetError
	if n != 0 || !errors.As(err, &refusal) || refusal.Reason != ForegroundEffectiveLimitExceeded || refusal.ConfiguredBytes != 2_000_000_000 || refusal.EffectiveBytes != 64<<20 {
		t.Fatal("64MiB source admitted eight or lost typed reason", n, err, refusal)
	}
	g, _, clock := foregroundSetup(t)
	cfg := g.(*ledger).config
	p.BookkeepingBytes = 4096
	p.MaxWaiters = 2
	p.MaxWait = 40 * time.Millisecond
	cfg.Foreground = &p
	newG, newA, err := newGovernor(cfg, 64<<20)
	if newG != nil || newA != nil || !errors.As(err, &refusal) || refusal.Reason != ForegroundEffectiveLimitExceeded {
		t.Fatal("constructor bypassed source", newG, newA, err)
	}
	// Passing the min is allowed, but the original large recipe remains
	// unfunded: no automatic floor or narrower owner reservation is invented.
	p.AssignedBytes = 64 << 20
	n, err = p.CapacityAtLimit(64 << 20)
	if n != 0 || !errors.As(err, &refusal) || refusal.Reason != ForegroundUnfundedBudget {
		t.Fatal("clamp erased owner costs", n, err)
	}
	p.AssignedBytes = 2_000_000_000
	for _, limit := range []int64{0, -1, 2_000_000_000, 4_000_000_000} {
		n, err = p.CapacityAtLimit(limit)
		if err != nil || n != 8 {
			t.Fatal("unknown/equal/larger source changed configured M", limit, n, err)
		}
	}
	// A new source smaller than the caller's two values refuses; explicitly
	// supplying a funded min accepts the independently adopted four profile.
	p.NonBrowseBytes = 1140 << 20
	p.MinimumLive = 4
	if n, err = p.CapacityAtLimit(1_600_000_000); n != 0 || !errors.As(err, &refusal) || refusal.Reason != ForegroundEffectiveLimitExceeded {
		t.Fatal(n, err)
	}
	p.EffectiveBytes = 1_600_000_000
	newG, newA, err = newGovernor(cfg, 1_600_000_000)
	if err != nil || newA.ForegroundUsage().Capacity != 4 {
		t.Fatal("explicit funded min refused", newG, err)
	}
	d := newA.AdmitForeground(t.Context(), foregroundQ(clock))
	if d.Grant == nil {
		t.Fatal(d)
	}
	retire(t, newA, d.Grant)
}

func TestForegroundNewReadsAuthoritativeHostlimits(t *testing.T) {
	g, _, _ := foregroundSetup(t)
	cfg := g.(*ledger).config
	cfg.Foreground = &ForegroundProfile{AssignedBytes: 2_000_000_000, EffectiveBytes: 2_000_000_000, NonBrowseBytes: 1360 << 20, MinimumLive: 8, BookkeepingBytes: 4096, MaxWaiters: 2, MaxWait: 40 * time.Millisecond}
	// Read-only source: no SetMemoryLimit or host/cgroup/config mutation.
	limit := hostlimits.EffectiveMemoryBytes()
	want, wantErr := cfg.Foreground.CapacityAtLimit(limit)
	newG, newA, err := New(cfg)
	if wantErr != nil {
		var actual, expected *ForegroundBudgetError
		if newG != nil || newA != nil || !errors.As(err, &actual) || !errors.As(wantErr, &expected) || actual.Reason != expected.Reason {
			t.Fatal("real constructor source differs", err, wantErr)
		}
		return
	}
	if err != nil || newA.ForegroundUsage().Capacity != want {
		t.Fatal("New did not validate hostlimits", newG, err, want)
	}
	if limit > 0 && limit < math.MaxInt64 {
		// This denies before any ledger/map/work allocation, even on a large
		// test host. It proves New actually uses the observed source, not only
		// that two independent calculations both happen to fund eight slots.
		cfg.Foreground.AssignedBytes = limit + 1
		cfg.Foreground.EffectiveBytes = limit + 1
		newG, newA, err = New(cfg)
		var refusal *ForegroundBudgetError
		if newG != nil || newA != nil || !errors.As(err, &refusal) || refusal.Reason != ForegroundEffectiveLimitExceeded {
			t.Fatal("New ignored actual known source", limit, newG, newA, err)
		}
	}
}
