package auth

import (
	"slices"
	"time"
	// The timezone names are built in: a server in a bare container has no zone files, and
	// hours could not be set on it at all.
	_ "time/tzdata"
)

// ViewingHours are an account's viewing hours: the times of the week it may be used. With no
// windows there is no limit.
type ViewingHours struct {
	// Timezone is an IANA name such as "America/Halifax". Hours always carry one: a server
	// in a container keeps UTC, which is nobody's evening.
	Timezone string          `json:"timezone"`
	Windows  []ViewingWindow `json:"windows"`
}

// ViewingWindow is one band of time on some days of the week. Days are 0 (Sunday) to 6. Start and
// End are minutes after midnight; a window whose end is not after its start runs past midnight.
type ViewingWindow struct {
	Days        []int `json:"days"`
	StartMinute int   `json:"startMinute"`
	EndMinute   int   `json:"endMinute"`
}

// Valid reports whether the hours can be kept: a named, known timezone, at most 64 windows, and
// each window with its days and minutes in range.
func (h ViewingHours) Valid() bool {
	if _, err := h.location(); err != nil || h.Timezone == "" || len(h.Windows) > 64 {
		return false
	}
	for _, w := range h.Windows {
		if len(w.Days) == 0 || len(w.Days) > 7 || w.StartMinute < 0 || w.StartMinute > 1439 || w.EndMinute < 0 || w.EndMinute > 1440 || w.StartMinute == w.EndMinute {
			return false
		}
		for n, d := range w.Days {
			if d < 0 || d > 6 || slices.Contains(w.Days[:n], d) {
				return false
			}
		}
	}
	return true
}

func (h ViewingHours) location() (*time.Location, error) { return time.LoadLocation(h.Timezone) }

// Open reports whether the account may be used now. When it may and there is a limit,
// until is when the window it is in ends.
func (h ViewingHours) Open(now time.Time) (open bool, until time.Time) {
	if len(h.Windows) == 0 {
		return true, time.Time{}
	}
	loc, err := h.location()
	if err != nil {
		loc = time.UTC // never stored: Valid refuses a timezone this server does not know
	}
	local := now.In(loc)
	y, m, d := local.Date()
	// A window that began yesterday and runs past midnight may still be open.
	for _, back := range []int{0, 1} {
		day := time.Date(y, m, d-back, 0, 0, 0, 0, loc)
		for _, w := range h.Windows {
			if !slices.Contains(w.Days, int(day.Weekday())) {
				continue
			}
			start := time.Date(y, m, d-back, 0, w.StartMinute, 0, 0, loc)
			end := time.Date(y, m, d-back, 0, w.EndMinute, 0, 0, loc)
			if w.EndMinute <= w.StartMinute {
				end = time.Date(y, m, d-back+1, 0, w.EndMinute, 0, 0, loc)
			}
			if !local.Before(start) && local.Before(end) && end.After(until) {
				open, until = true, end
			}
		}
	}
	return open, until
}
