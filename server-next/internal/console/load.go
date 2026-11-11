package console

import (
	"context"
	"math"
	"sync"
	"time"
)

// The server's load, for the Dashboard's charts: processor and memory (the whole machine and
// Portico itself), bandwidth at home and away, and conversions running. It is sampled every two
// seconds and kept in memory only: the last two minutes as sampled, and the last 24 hours as
// hourly averages. A restart starts the charts again.

const (
	loadEvery = 2 * time.Second
	liveSpan  = 2 * time.Minute
	dayHours  = 24
)

// Streams says what the streams are doing now: the bitrates of those playing at home and away,
// and how many are being converted.
type Streams func() (homeKbps, awayKbps, conversions int)

// Load samples the server's load and keeps it.
type Load struct {
	Streams Streams
	Now     func() time.Time

	mu      sync.Mutex
	machine machine
	live    []sample // the last two minutes, oldest first
	hours   []hour   // the last 24 hours, the current one last
}

type sample struct {
	at     time.Time
	values map[string]float64
}

type hour struct {
	start  time.Time
	sums   map[string]float64
	counts map[string]int
}

// Point is one value of a series.
type Point struct {
	At    time.Time
	Value float64
}

// Run samples now and every two seconds until ctx ends.
func (l *Load) Run(ctx context.Context) {
	ticker := time.NewTicker(loadEvery)
	defer ticker.Stop()
	for {
		l.Sample()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Sample takes one sample. A series this machine cannot measure is left out of it.
func (l *Load) Sample() {
	values := map[string]float64{}
	if l.Streams != nil {
		home, away, conversions := l.Streams()
		values["homeMbps"], values["awayMbps"] = float64(home)/1000, float64(away)/1000
		values["conversions"] = float64(conversions)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.Now()
	l.machine.sample(now, values)

	l.live = append(l.live, sample{at: now, values: values})
	first := 0
	for first < len(l.live) && !l.live[first].at.After(now.Add(-liveSpan)) {
		first++
	}
	l.live = l.live[first:]

	start := now.Truncate(time.Hour)
	if n := len(l.hours); n == 0 || !l.hours[n-1].start.Equal(start) {
		l.hours = append(l.hours, hour{start: start, sums: map[string]float64{}, counts: map[string]int{}})
	}
	for len(l.hours) > 0 && !l.hours[0].start.After(start.Add(-dayHours*time.Hour)) {
		l.hours = l.hours[1:]
	}
	current := l.hours[len(l.hours)-1]
	for name, v := range values {
		current.sums[name] += v
		current.counts[name]++
	}
}

// Live is every sample of the last two minutes, by series.
func (l *Load) Live(now time.Time) map[string][]Point {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string][]Point{}
	for _, s := range l.live {
		if s.at.After(now.Add(-liveSpan)) && !s.at.After(now) {
			for name, v := range s.values {
				out[name] = append(out[name], Point{At: s.at, Value: round(v)})
			}
		}
	}
	return out
}

// Day is the average of each of the last 24 hours, the current one so far, by series: one
// point at the start of each hour that has samples. from is the start of the first hour.
func (l *Load) Day(now time.Time) (from time.Time, series map[string][]Point) {
	from = now.Truncate(time.Hour).Add(-(dayHours - 1) * time.Hour)
	l.mu.Lock()
	defer l.mu.Unlock()
	series = map[string][]Point{}
	for _, h := range l.hours {
		if h.start.Before(from) || h.start.After(now) {
			continue
		}
		for name, sum := range h.sums {
			series[name] = append(series[name], Point{At: h.start, Value: round(sum / float64(h.counts[name]))})
		}
	}
	return from, series
}

// round keeps one decimal place.
func round(v float64) float64 { return math.Round(v*10) / 10 }

// percent is a share as a percentage from 0 to 100.
func percent(share float64) float64 { return math.Max(0, math.Min(100, share*100)) }
