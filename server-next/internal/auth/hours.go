package auth

import (
	"slices"
	"time"
)

// Hours are an account's viewing hours: the times of the week it may be used. With no
// windows there is no limit.
type Hours struct {
	// Timezone is an IANA name such as "America/Halifax". Empty means the server's own.
	Timezone string   `json:"timezone,omitempty"`
	Windows  []Window `json:"windows"`
}

// Window is one band of time on some days of the week. Days are 0 (Sunday) to 6. Start and
// End are minutes after midnight; a window whose end is not after its start runs past midnight.
type Window struct {
	Days        []int `json:"days"`
	StartMinute int   `json:"startMinute"`
	EndMinute   int   `json:"endMinute"`
}

// Valid reports whether the hours can be kept: a known timezone, at most 64 windows, and
// each window with its days and minutes in range.
func (h Hours) Valid() bool {
	if _, err := h.location(); err != nil || len(h.Windows) > 64 {
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

func (h Hours) location() (*time.Location, error) {
	if h.Timezone == "" {
		return time.Local, nil
	}
	return time.LoadLocation(h.Timezone)
}

// Open reports whether the account may be used now. When it may and there is a limit,
// until is when the window it is in ends.
func (h Hours) Open(now time.Time) (open bool, until time.Time) {
	if len(h.Windows) == 0 {
		return true, time.Time{}
	}
	loc, err := h.location()
	if err != nil {
		loc = time.Local
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
