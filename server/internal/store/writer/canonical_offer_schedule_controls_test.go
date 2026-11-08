package writer

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Deterministic fake clock: sleeping advances it, and "work" advances it too.
type offerFakeClock struct{ t time.Time }

func (c *offerFakeClock) now() time.Time { return c.t }
func (c *offerFakeClock) sleep(_ context.Context, d time.Duration) error {
	c.t = c.t.Add(d)
	return nil
}

// The open-loop schedule holds: originals are scheduled at start+n*interval no
// matter how long earlier ones took; a slow original makes later ones LATE
// (recorded) and they catch up without shifting the schedule.
func TestCanonicalOfferScheduleIsOpenLoop(t *testing.T) {
	clock := &offerFakeClock{t: time.Unix(1000, 0)}
	s, err := newCanonicalOfferSchedule(20, clock.now, clock.sleep) // 50 ms interval
	if err != nil {
		t.Fatal(err)
	}
	start := clock.t
	work := []time.Duration{10, 10, 130, 10, 10, 10, 60, 10} // ms; 130 and 60 overrun one slot
	for n, w := range work {
		scheduled, err := s.wait(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if want := start.Add(time.Duration(n) * 50 * time.Millisecond); !scheduled.Equal(want) {
			t.Fatalf("original %d scheduled %v want %v: the schedule drifted with latency", n, scheduled.Sub(start), want.Sub(start))
		}
		if clock.t.Before(scheduled) {
			t.Fatalf("original %d began before its scheduled instant", n)
		}
		clock.t = clock.t.Add(w * time.Millisecond)
	}
	r := s.report()
	// The 130 ms and 60 ms originals overrun their slots; the originals behind them
	// start late and catch up. The late set follows from the fixed schedule.
	if r["offered_originals"] != len(work) || r["requested_originals_per_sec"] != 20.0 || r["interval_ns"] != int64(50e6) {
		t.Fatal("schedule report wrong", r)
	}
	if r["late_originals"].(int) == 0 || r["max_lateness_ns"].(int64) <= 0 {
		t.Fatal("overruns must be recorded as lateness, not absorbed", r)
	}
	// Achieved rate is measured from actual start times: first-to-last window.
	got := r["achieved_originals_per_sec"].(float64)
	if math.Abs(got-20) > 3 {
		t.Fatalf("achieved %.2f originals/s, want about 20 once it catches up", got)
	}
}

// Fast work never exceeds the offered rate, and unlimited is today's stress.
func TestCanonicalOfferScheduleRateBoundsAndUnlimited(t *testing.T) {
	clock := &offerFakeClock{t: time.Unix(2000, 0)}
	s, _ := newCanonicalOfferSchedule(4, clock.now, clock.sleep) // 250 ms
	for n := 0; n < 20; n++ {
		if _, err := s.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
		clock.t = clock.t.Add(time.Millisecond) // 1 ms of work
	}
	r := s.report()
	if got := r["achieved_originals_per_sec"].(float64); math.Abs(got-4) > 0.05 || r["late_originals"].(int) != 0 {
		t.Fatalf("fast work must hold the offered rate: %v", r)
	}
	free := &offerFakeClock{t: time.Unix(3000, 0)}
	u, err := newCanonicalOfferSchedule(0, free.now, free.sleep)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 5; n++ {
		at, _ := u.wait(context.Background())
		if !at.Equal(free.t) {
			t.Fatal("unlimited must schedule now")
		}
	}
	if !free.t.Equal(time.Unix(3000, 0)) || u.report()["requested_originals_per_sec"] != nil {
		t.Fatal("unlimited must never wait or claim a requested rate")
	}
	for _, bad := range []float64{-1, 0.01, 1001, math.NaN(), math.Inf(1)} {
		if _, err := newCanonicalOfferSchedule(bad, nil, nil); err == nil {
			t.Fatal("accepted offered rate", bad)
		}
	}
}

func TestCanonicalHeldConfigOfferedRate(t *testing.T) {
	c := canonicalHeldConfig{RunID: "CONTROL", HostEpoch: "CONTROL", SourceCommit: strings.Repeat("a", 40), OutputPath: filepath.Join(t.TempDir(), "raw.ndjson"), Class: "security", Profile: "standard", Attempts: 10000, ArtifactDirectory: filepath.Join(t.TempDir(), "seed"), ManifestSHA256: strings.Repeat("b", 64)}
	decode := func(c canonicalHeldConfig) error {
		raw, _ := json.Marshal(c)
		_, err := decodeCanonicalHeldConfig(raw)
		return err
	}
	if err := decode(c); err != nil {
		t.Fatal("unlimited default", err)
	}
	c.OfferedRate = 20
	if err := decode(c); err != nil {
		t.Fatal("20/s on security", err)
	}
	for _, mutate := range []func(*canonicalHeldConfig){
		func(c *canonicalHeldConfig) { c.OfferedRate = 1001 },
		func(c *canonicalHeldConfig) { c.OfferedRate = -1 },
		func(c *canonicalHeldConfig) { c.Class = "inventory_n1" }, // pacing is WC-only
	} {
		bad := c
		mutate(&bad)
		if err := decode(bad); err == nil {
			t.Fatal("accepted invalid offered-rate config", bad)
		}
	}
}
