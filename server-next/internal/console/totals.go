// Package console is the owner's view of the server as a whole. This file keeps the server's
// lifetime statistics: a few totals for each day (plays started, time played, data sent at
// home and away, how many people played, the most streams at once), counted in memory as
// things happen and added to the day's rows every few minutes and when the server stops, so a
// restart loses at most those minutes. They are never removed.
package console

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Metric names. A count is split by kind of media and by how it was delivered:
// "plays.film.converted", "played_ms.music.original", "bytes_away.episode.repackaged".
const (
	MetricPlays       = "plays"
	MetricPlayedMs    = "played_ms"
	MetricBytesHome   = "bytes_home"
	MetricBytesAway   = "bytes_away"
	MetricPeople      = "people"
	MetricStreamsPeak = "streams_peak"
)

// flushEvery is how often the day's counts are added to its rows.
const flushEvery = 5 * time.Minute

// Totals counts for today and adds to the stored totals.
type Totals struct {
	DB  *db.DB
	Log *slog.Logger
	Now func() time.Time

	mu      sync.Mutex
	day     int
	counts  map[string]int64
	peaks   map[string]int64
	people  map[int64]bool
	written map[int64]bool // the people of today already in the stored count
}

// Day is the server's local date as the number YYYYMMDD.
func Day(t time.Time) int {
	y, m, d := t.Date()
	return y*10000 + int(m)*100 + d
}

// DayTime is the start of a day given as YYYYMMDD, in the server's time zone.
func DayTime(day int) time.Time {
	return time.Date(day/10000, time.Month(day/100%100), day%100, 0, 0, 0, 0, time.Local)
}

// Add counts n of a metric for today.
func (t *Totals) Add(metric string, n int64) {
	if t == nil || n == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.roll()
	t.counts[metric] += n
}

// Most records a level reached today; the day keeps the highest.
func (t *Totals) Most(metric string, n int64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.roll()
	t.peaks[metric] = max(t.peaks[metric], n)
}

// Person records that an account played something today.
func (t *Totals) Person(userID int64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.roll()
	t.people[userID] = true
}

// roll starts a new day's counts, keeping the old ones to be written. Caller holds t.mu.
func (t *Totals) roll() {
	day := Day(t.Now())
	if t.counts == nil {
		t.day, t.counts, t.peaks, t.people, t.written = day, map[string]int64{}, map[string]int64{}, map[int64]bool{}, map[int64]bool{}
	}
	if day == t.day {
		return
	}
	pending := t.takeLocked()
	go t.write(context.Background(), pending)
	t.day, t.people, t.written = day, map[int64]bool{}, map[int64]bool{}
}

type pendingTotals struct {
	day          int
	counts, most map[string]int64
}

// takeLocked hands over what has been counted since the last write. Caller holds t.mu.
func (t *Totals) takeLocked() pendingTotals {
	out := pendingTotals{day: t.day, counts: t.counts, most: t.peaks}
	// People are counted once each: only those not yet written add to the stored count.
	for id := range t.people {
		if !t.written[id] {
			out.counts[MetricPeople]++
			t.written[id] = true
		}
	}
	t.counts, t.peaks = map[string]int64{}, map[string]int64{}
	return out
}

func (t *Totals) write(ctx context.Context, p pendingTotals) {
	if len(p.counts) == 0 && len(p.most) == 0 {
		return
	}
	if err := t.DB.AddDailyTotals(ctx, p.day, p.counts, p.most); err != nil && t.Log != nil {
		t.Log.Error("the day's statistics could not be saved", "day", strconv.Itoa(p.day), "error", err)
	}
}

// Flush adds what has been counted to the stored totals now.
func (t *Totals) Flush(ctx context.Context) {
	t.mu.Lock()
	if t.counts == nil {
		t.mu.Unlock()
		return
	}
	pending := t.takeLocked()
	t.mu.Unlock()
	t.write(ctx, pending)
}

// Today is what has been counted today and not written yet, for a read of today's totals.
func (t *Totals) Today() (int, map[string]int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.roll()
	out := map[string]int64{}
	for k, v := range t.counts {
		out[k] = v
	}
	for k, v := range t.peaks {
		out[k] = max(out[k], v)
	}
	for id := range t.people {
		if !t.written[id] {
			out[MetricPeople]++
		}
	}
	return t.day, out
}

// Run writes the counts every few minutes until ctx ends. The server calls Flush once more as
// it stops, before the database closes.
func (t *Totals) Run(ctx context.Context) {
	tick := time.NewTicker(flushEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			t.Flush(ctx)
		}
	}
}
