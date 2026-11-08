package writer

import (
	"context"
	"errors"
	"math"
	"time"
)

// canonicalOfferSchedule is the OPEN-LOOP offered-rate control for the canonical
// WC targets. Original n (0-based) is scheduled at start + n*interval, where
// start is fixed at the first offer. The schedule never moves with latency: a
// slow original makes later ones late (recorded), never later-scheduled, so the
// offered rate cannot silently collapse into a closed loop. Rate 0 is the
// unlimited stress behaviour: no waiting, every offer scheduled "now".
type canonicalOfferSchedule struct {
	rate     float64 // offered originals per second; 0 = unlimited
	interval time.Duration
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error

	started        bool
	start          time.Time
	n              int
	firstStart     time.Time
	lastStart      time.Time
	late, onTime   int
	maxLate, total time.Duration
}

func newCanonicalOfferSchedule(rate float64, now func() time.Time, sleep func(context.Context, time.Duration) error) (*canonicalOfferSchedule, error) {
	if rate < 0 || math.IsNaN(rate) || math.IsInf(rate, 0) || (rate != 0 && (rate < 0.1 || rate > 1000)) {
		return nil, errors.New("offered rate must be 0 (unlimited) or 0.1..1000 originals/s")
	}
	s := &canonicalOfferSchedule{rate: rate, now: now, sleep: sleep}
	if rate > 0 {
		s.interval = time.Duration(float64(time.Second) / rate)
	}
	if s.now == nil {
		s.now = time.Now
	}
	if s.sleep == nil {
		s.sleep = func(ctx context.Context, d time.Duration) error {
			timer := time.NewTimer(d)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	return s, nil
}

// wait blocks until the next original's scheduled instant (immediately if it is
// already due) and returns that scheduled instant. Lateness is measured against
// the schedule, not against the previous original.
func (s *canonicalOfferSchedule) wait(ctx context.Context) (time.Time, error) {
	if s.rate == 0 {
		now := s.now()
		s.record(now, now)
		return now, nil
	}
	if !s.started {
		s.started, s.start = true, s.now()
	}
	scheduled := s.start.Add(time.Duration(s.n) * s.interval)
	if d := scheduled.Sub(s.now()); d > 0 {
		if err := s.sleep(ctx, d); err != nil {
			return scheduled, err
		}
	}
	s.record(scheduled, s.now())
	return scheduled, nil
}

func (s *canonicalOfferSchedule) record(scheduled, began time.Time) {
	if s.n == 0 {
		s.firstStart = began
	}
	s.lastStart = began
	if lateness := began.Sub(scheduled); lateness > time.Millisecond {
		s.late++
		s.total += lateness
		if lateness > s.maxLate {
			s.maxLate = lateness
		}
	} else {
		s.onTime++
	}
	s.n++
}

// report is the ACTUAL achieved offered rate: originals started per second over
// the first-to-last start window, never the requested value echoed back.
func (s *canonicalOfferSchedule) report() map[string]any {
	out := map[string]any{"requested_originals_per_sec": nil, "offered_originals": s.n, "schedule": "open-loop fixed start+n*interval; lateness measured against the schedule"}
	if s.rate > 0 {
		out["requested_originals_per_sec"] = s.rate
		out["interval_ns"] = s.interval.Nanoseconds()
	} else {
		out["schedule"] = "unlimited: no pacing (stress)"
	}
	out["late_originals"] = s.late
	out["on_time_originals"] = s.onTime
	out["max_lateness_ns"] = s.maxLate.Nanoseconds()
	out["total_lateness_ns"] = s.total.Nanoseconds()
	if s.n > 1 {
		span := s.lastStart.Sub(s.firstStart)
		out["first_to_last_start_ns"] = span.Nanoseconds()
		if span > 0 {
			out["achieved_originals_per_sec"] = float64(s.n-1) / span.Seconds()
		}
	} else {
		out["achieved_originals_per_sec"] = nil
	}
	return out
}
