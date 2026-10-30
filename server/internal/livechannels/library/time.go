// Package librarychannels owns generated library schedules. It does not allocate
// tuners, capture recordings, or manufacture playback source-version evidence.
package librarychannels

import (
	"errors"
	"time"
)

var ErrLocalTime = errors.New("The channel timezone or schedule boundary is invalid.")

type Boundary struct {
	RequestedLocal string    `json:"requestedLocal"`
	ActualLocal    string    `json:"actualLocal"`
	UTC            time.Time `json:"utc"`
	Adjustment     string    `json:"adjustment"`
}
type BlockInterval struct {
	Start    Boundary `json:"start"`
	End      Boundary `json:"end"`
	Timezone string   `json:"timezone"`
}

func civil(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
}

// ResolveBoundary implements PC-LIBRARY-CHANNELS: nonexistent local starts move
// to the earliest valid following wall instant; repeated boundaries use their
// first occurrence. Zone intervals are traversed directly, not guessed from a
// platform-dependent time.Date choice or per-minute client calculations.
func ResolveBoundary(date string, minute int, timezone string) (Boundary, error) {
	if minute < 0 || minute >= 24*60 || timezone == "" || timezone == "Local" || len(timezone) > 120 {
		return Boundary{}, ErrLocalTime
	}
	day, e := time.Parse("2006-01-02", date)
	if e != nil || day.Format("2006-01-02") != date {
		return Boundary{}, ErrLocalTime
	}
	loc, e := time.LoadLocation(timezone)
	if e != nil {
		return Boundary{}, ErrLocalTime
	}
	target := day.Add(time.Duration(minute) * time.Minute)
	low, high := target.Add(-72*time.Hour), target.Add(72*time.Hour)
	var chosen, chosenWall time.Time
	exactCount := 0
	cursor := low
	for visited := 0; ; visited++ {
		if visited >= 32 {
			return Boundary{}, ErrLocalTime
		}
		z := cursor.In(loc)
		_, offset := z.Zone()
		if offset < -24*60*60 || offset > 24*60*60 {
			return Boundary{}, ErrLocalTime
		}
		begin, end := z.ZoneBounds()
		if begin.IsZero() || begin.Before(low) {
			begin = low
		}
		if end.IsZero() || end.After(high) {
			end = high
		}
		if !end.After(cursor) {
			return Boundary{}, ErrLocalTime
		}
		candidate := target.Add(-time.Duration(offset) * time.Second)
		if !candidate.Before(begin) && candidate.Before(end) && civil(candidate.In(loc)).Equal(target) {
			exactCount++
			if chosenWall.IsZero() || !chosenWall.Equal(target) || candidate.Before(chosen) {
				chosen, chosenWall = candidate, target
			}
		}
		// A forward transition's first instant is the first available wall time for
		// every skipped civil time before it. Prefer civil ordering, then UTC ordering.
		wall := civil(begin.In(loc))
		if !wall.Before(target) && (chosenWall.IsZero() || wall.Before(chosenWall) || wall.Equal(chosenWall) && begin.Before(chosen)) {
			chosen, chosenWall = begin, wall
		}
		if !end.Before(high) {
			break
		}
		cursor = end
	}
	if chosen.IsZero() {
		return Boundary{}, ErrLocalTime
	}
	adjustment := "none"
	if exactCount == 0 {
		adjustment = "gap-forward"
	} else if exactCount > 1 {
		adjustment = "repeated-first"
	}
	return Boundary{RequestedLocal: target.Format("2006-01-02T15:04:05"), ActualLocal: chosen.In(loc).Format(time.RFC3339), UTC: chosen.UTC(), Adjustment: adjustment}, nil
}

// ResolveBlock treats end<=start as an overnight block, including an explicit
// full local day when start=end. It preserves concrete nonnegative UTC intervals.
func ResolveBlock(date string, startMinute, endMinute int, timezone string) (BlockInterval, error) {
	start, e := ResolveBoundary(date, startMinute, timezone)
	if e != nil {
		return BlockInterval{}, e
	}
	endDate := date
	if endMinute <= startMinute {
		day, e := time.Parse("2006-01-02", date)
		if e != nil {
			return BlockInterval{}, ErrLocalTime
		}
		endDate = day.AddDate(0, 0, 1).Format("2006-01-02")
	}
	end, e := ResolveBoundary(endDate, endMinute, timezone)
	if e != nil {
		return BlockInterval{}, e
	}
	if end.UTC.Before(start.UTC) {
		return BlockInterval{}, ErrLocalTime
	}
	return BlockInterval{Start: start, End: end, Timezone: timezone}, nil
}

// SevenDayWindow uses seven calendar days in the channel timezone. The elapsed
// duration can differ from168 hours at DST; callers persist the returned instants.
func SevenDayWindow(date, timezone string) (BlockInterval, error) {
	start, e := ResolveBoundary(date, 0, timezone)
	if e != nil {
		return BlockInterval{}, e
	}
	day, e := time.Parse("2006-01-02", date)
	if e != nil {
		return BlockInterval{}, ErrLocalTime
	}
	end, e := ResolveBoundary(day.AddDate(0, 0, 7).Format("2006-01-02"), 0, timezone)
	if e != nil || end.UTC.Before(start.UTC) {
		return BlockInterval{}, ErrLocalTime
	}
	return BlockInterval{Start: start, End: end, Timezone: timezone}, nil
}
