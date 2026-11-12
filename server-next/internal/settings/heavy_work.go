package settings

import (
	"slices"
	"time"
)

// HeavyWorkSettings are Settings › General's hours for heavy background work, the general
// document's heavyWork (area 12 rule 41): seek previews, chapter pictures, intro and credit
// detection, loudness and sound analysis. Scans, matching, playback, conversions and backups
// never wait for them.
type HeavyWorkSettings struct {
	// Hours is "any" (the default), "overnight" (1 to 6 am) or "chosen": Days, From and To.
	Hours string `json:"hours"`
	// Days are the days a chosen stretch starts on ("sun" to "sat"); From and To are times
	// of day ("23:00"), on the server's clock. A To earlier than From ends the next day.
	Days []string `json:"days,omitempty"`
	From string   `json:"from,omitempty"`
	To   string   `json:"to,omitempty"`
}

// The values of Hours.
const (
	HeavyAnyTime   = "any"
	HeavyOvernight = "overnight"
	HeavyChosen    = "chosen"
)

var weekDays = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// check keeps a valid choice of hours, and drops the days and times the other choices do
// not use. Fields are named as in the general document.
func (h *HeavyWorkSettings) check() error {
	switch h.Hours {
	case HeavyAnyTime, HeavyOvernight:
		h.Days, h.From, h.To = nil, "", ""
		return nil
	case HeavyChosen:
	default:
		return &Invalid{Field: "heavyWork.hours", Message: "Choose Any time, Overnight only, or your own hours."}
	}
	if len(h.Days) == 0 {
		return &Invalid{Field: "heavyWork.days", Message: "Choose at least one day."}
	}
	seen := map[string]bool{}
	for _, d := range h.Days {
		if !slices.Contains(weekDays, d) || seen[d] {
			return &Invalid{Field: "heavyWork.days", Message: "Choose days from Sunday to Saturday, each once."}
		}
		seen[d] = true
	}
	from, okFrom := clockTime(h.From)
	to, okTo := clockTime(h.To)
	if !okFrom {
		return &Invalid{Field: "heavyWork.from", Message: "Give a time like 23:00."}
	}
	if !okTo || to == from {
		return &Invalid{Field: "heavyWork.to", Message: "Give a time like 06:00, other than the start."}
	}
	return nil
}

// clockTime reads "HH:MM" as minutes into the day.
func clockTime(s string) (int, bool) {
	t, err := time.Parse("15:04", s)
	if err != nil || len(s) != 5 {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

// Open says whether heavy work may run at now, and when it may not, when it may next start.
// Times are read on now's own clock (the server's), so a change of the clocks moves the
// hours with it.
func (h HeavyWorkSettings) Open(now time.Time) (open bool, opens time.Time) {
	type stretch struct {
		days     []string
		from, to int
	}
	var s stretch
	switch h.Hours {
	case HeavyOvernight:
		s = stretch{weekDays, 60, 360}
	case HeavyChosen:
		from, _ := clockTime(h.From)
		to, _ := clockTime(h.To)
		s = stretch{h.Days, from, to}
	default:
		return true, now
	}
	// A stretch that ends the next day may have started yesterday: look from the day before.
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -1)
	for n := 0; n < 9; n++ {
		d := day.AddDate(0, 0, n)
		if !slices.Contains(s.days, weekDays[d.Weekday()]) {
			continue
		}
		start := time.Date(d.Year(), d.Month(), d.Day(), s.from/60, s.from%60, 0, 0, d.Location())
		end := time.Date(d.Year(), d.Month(), d.Day(), s.to/60, s.to%60, 0, 0, d.Location())
		if s.to <= s.from {
			end = end.AddDate(0, 0, 1)
		}
		if !now.Before(start) && now.Before(end) {
			return true, now
		}
		if start.After(now) {
			return false, start
		}
	}
	return false, now // never reached: a valid document has a day every week
}

// OpenShare is the share of a week the hours are open, for how long heavy work will take:
// 1 at any time.
func (h HeavyWorkSettings) OpenShare() float64 {
	switch h.Hours {
	case HeavyOvernight:
		return 5.0 / 24
	case HeavyChosen:
		from, _ := clockTime(h.From)
		to, _ := clockTime(h.To)
		minutes := to - from
		if minutes <= 0 {
			minutes += 24 * 60
		}
		return float64(len(h.Days)*minutes) / (7 * 24 * 60)
	}
	return 1
}
