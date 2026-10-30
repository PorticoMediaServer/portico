// Package reportwindow owns the calendar intervals shared by owner reports.
package reportwindow

import (
	"errors"
	"regexp"
	"sort"
	"time"
)

const Grain = 15 * time.Minute

var ErrInvalid = errors.New("invalid report interval")
var zoneName = regexp.MustCompile(`^[A-Za-z0-9_+\-/]{1,128}$`)

type Bounds struct {
	Period   string `json:"period"`
	TimeZone string `json:"timeZone"`
	From     string `json:"from"`
	Until    string `json:"until"`
}

type Bucket struct {
	Start   string `json:"start"`
	End     string `json:"end"`
	Partial bool   `json:"partial"`
}

type Interval struct {
	Bounds
	Buckets []Bucket `json:"buckets"`
	starts  []int64
	from    int64
	until   int64
	zone    *time.Location
}

func Context(zone, until string) (*time.Location, time.Time, error) {
	if zone == "Local" || !zoneName.MatchString(zone) {
		return nil, time.Time{}, ErrInvalid
	}
	loc, e := time.LoadLocation(zone)
	if e != nil {
		return nil, time.Time{}, ErrInvalid
	}
	at, e := time.Parse(time.RFC3339Nano, until)
	if e != nil || at.UnixMilli() < 0 || !at.Equal(at.Truncate(Grain)) {
		return nil, time.Time{}, ErrInvalid
	}
	return loc, at, nil
}

// Resolve never uses a host or profile time zone. first is the earliest actual
// fact for All time, or -1 when none exists. Other periods ignore it.
func Resolve(period, zone, until string, first int64) (Interval, error) {
	loc, at, e := Context(zone, until)
	if e != nil {
		return Interval{}, e
	}
	return resolve(period, zone, loc, at, first)
}

// Live is the existing moving two-second detail, not a historical aggregate.
func Live(zone, capturedUntil string, now time.Time) (Interval, error) {
	loc, _, e := Context(zone, capturedUntil)
	if e != nil {
		return Interval{}, e
	}
	return resolve("live", zone, loc, now, -1)
}

func resolve(period, zone string, loc *time.Location, at time.Time, first int64) (Interval, error) {
	at = at.Truncate(time.Millisecond)
	local := at.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	out := Interval{Bounds: Bounds{Period: period, TimeZone: zone, Until: stamp(at)}, Buckets: []Bucket{}, until: at.UnixMilli(), zone: loc}
	add := func(start, end, fullEnd time.Time) {
		out.starts = append(out.starts, start.UnixMilli())
		out.Buckets = append(out.Buckets, Bucket{Start: stamp(start), End: stamp(end), Partial: end.Before(fullEnd)})
	}
	days, step := 0, 1
	switch period {
	case "today":
		days = 1
	case "7d":
		days = 7
	case "30d":
		days = 30
	case "90d":
		days = 90
	case "1y":
		days, step = 364, 7
	case "12h", "24h":
		n := 12
		if period == "24h" {
			n = 24
		}
		// UTC quarter boundaries also cover :30/:45 offsets and DST changes.
		// A repeated wall hour has its own UTC identity; a half-hour transition
		// is a boundary, rather than inventing a nonexistent wall-clock hour.
		starts := []time.Time{}
		for t := at.UTC().Truncate(Grain); len(starts) < n; t = t.Add(-Grain) {
			l := t.In(loc)
			_, offset := l.Zone()
			_, before := t.Add(-time.Nanosecond).In(loc).Zone()
			if l.Minute() == 0 || offset != before {
				starts = append(starts, t)
			}
		}
		for i := len(starts) - 1; i >= 0; i-- {
			end := at
			if i > 0 {
				end = starts[i-1]
			}
			add(starts[i], end, end)
		}
		out.Buckets[len(out.Buckets)-1].Partial = true
	case "all":
		if first >= 0 && first < at.UnixMilli() {
			fact := time.UnixMilli(first).In(loc)
			from := time.Date(fact.Year(), fact.Month(), 1, 0, 0, 0, 0, loc)
			for start := from; start.Before(at); {
				if len(out.Buckets) >= 1201 {
					return Interval{}, ErrInvalid
				}
				end := time.Date(start.Year(), start.Month()+1, 1, 0, 0, 0, 0, loc)
				add(start, minTime(end, at), end)
				start = end
			}
		}
	case "live", "10m", "1h":
		span := 2 * time.Minute
		if period == "10m" {
			span = 10 * time.Minute
		} else if period == "1h" {
			span = time.Hour
		}
		add(at.Add(-span), at, at)
	default:
		return Interval{}, ErrInvalid
	}
	if days != 0 {
		start := today.AddDate(0, 0, -(days - 1))
		for i := 0; i < days/step; i++ {
			end := start.AddDate(0, 0, step)
			add(start, minTime(end, at), end)
			start = end
		}
	}
	out.from = at.UnixMilli()
	if len(out.starts) > 0 {
		out.from = out.starts[0]
	}
	out.From = stamp(time.UnixMilli(out.from))
	return out, nil
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
func (r Interval) FromMS() int64            { return r.from }
func (r Interval) UntilMS() int64           { return r.until }
func (r Interval) Local(at int64) time.Time { return time.UnixMilli(at).In(r.zone) }
func (r Interval) Index(at int64) (int, bool) {
	i := sort.Search(len(r.starts), func(i int) bool { return r.starts[i] > at }) - 1
	return i, i >= 0 && at >= r.from && at < r.until
}
