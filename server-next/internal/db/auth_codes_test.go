package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// raceRuns is how many times a race test repeats: a few dozen by default, and as many as
// PORTICO_RACE_RUNS says for a milestone run.
func raceRuns() int {
	if n, err := strconv.Atoi(os.Getenv("PORTICO_RACE_RUNS")); err == nil && n > 0 {
		return n
	}
	return 30
}

// However many callers redeem one single-use code at the same moment, exactly one gets it.
func TestOneCodeIsRedeemedOnce(t *testing.T) {
	ctx, now := context.Background(), time.Now()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for run := range raceRuns() {
		hash := fmt.Sprintf("code-%d", run)
		if err := d.CreateCode(ctx, Code{Kind: "test", Hash: hash, ExpiresAt: now.Add(time.Minute)}, now); err != nil {
			t.Fatal(err)
		}
		var won atomic.Int32
		var wg sync.WaitGroup
		for range 16 {
			wg.Go(func() {
				if _, err := d.RedeemCode(ctx, "test", hash, now); err == nil {
					won.Add(1)
				}
			})
		}
		wg.Wait()
		if won.Load() != 1 {
			t.Fatalf("run %d: %d callers redeemed one code; want exactly 1", run, won.Load())
		}
	}
	// An expired code and a code of another kind are refused like a wrong one.
	d.CreateCode(ctx, Code{Kind: "test", Hash: "old", ExpiresAt: now.Add(-time.Second)}, now)
	d.CreateCode(ctx, Code{Kind: "other", Hash: "other", ExpiresAt: now.Add(time.Minute)}, now)
	for _, hash := range []string{"old", "other", "never-made"} {
		if _, err := d.RedeemCode(ctx, "test", hash, now); err != ErrBadCredentials {
			t.Errorf("code %q: got %v, want ErrBadCredentials", hash, err)
		}
	}
}
