package workpolicy

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// HeavyWorkFacts describes scheduling state in the caller's current snapshot.
// It grants no actor, source, provider or decode authority. Automatic consumers
// combine these facts with those other authorities; explicit owner commands do
// not acquire automatic-work restrictions by calling this reader.
type HeavyWorkFacts struct {
	Revision         int64
	Task             string
	ObservedAt       time.Time
	PausedUntil      int64
	Windows          []Window
	WindowConfigured bool
	// WindowOpen describes the window alone, independently of PausedUntil.
	// With no enabled matching window, the owner selected Any time.
	WindowOpen bool
	// The next matching calendar start and earliest scheduling-only admission.
	// Neither field grants source/actor/decode authority or promises completion.
	NextWindowStart *time.Time
	NextAllowedAt   *time.Time
}

// ReadHeavyWorkTx reads only supported heavy scheduling tasks. Operation names
// such as episode_thumbnail must be mapped explicitly by their owning producer,
// never passed to the generic Allowed function's unsupported-task fallback.
func ReadHeavyWorkTx(ctx context.Context, tx *sql.Tx, task string, at time.Time) (HeavyWorkFacts, error) {
	if tx == nil {
		return HeavyWorkFacts{}, errors.New("heavy work facts require a transaction")
	}
	if task != "analysis" && task != "trickplay" {
		return HeavyWorkFacts{}, errors.New("unsupported heavy work task")
	}
	policy, err := ReadTx(ctx, tx)
	if err != nil {
		return HeavyWorkFacts{}, err
	}
	facts := HeavyWorkFacts{Revision: policy.Revision, Task: task, ObservedAt: at, PausedUntil: policy.PausedUntil, Windows: []Window{}}
	for _, window := range policy.Windows {
		if contains(window.Tasks, task) {
			facts.Windows = append(facts.Windows, window)
			facts.WindowConfigured = facts.WindowConfigured || window.Enabled
		}
	}
	// Pause is its own fact. Clearing it in this local value lets the caller
	// distinguish a closed window from a pause inside an otherwise open one.
	policy.PausedUntil = 0
	facts.WindowOpen = !facts.WindowConfigured || Open(policy, task, at)
	if next, ok := nextHeavyWindow(policy, task, at); ok {
		facts.NextWindowStart = &next
	}
	earliest := at
	if facts.PausedUntil > at.UnixMilli() {
		earliest = time.UnixMilli(facts.PausedUntil)
	}
	if !facts.WindowConfigured || Open(policy, task, earliest) {
		if earliest.After(at) {
			facts.NextAllowedAt = &earliest
		}
	} else if next, ok := nextHeavyWindow(policy, task, earliest); ok {
		facts.NextAllowedAt = &next
	}
	return facts, nil
}

func nextHeavyWindow(p Policy, task string, after time.Time) (time.Time, bool) {
	var next time.Time
	for _, w := range p.Windows {
		if !w.Enabled || !contains(w.Tasks, task) || w.DurationMinutes <= 0 {
			continue
		}
		loc, err := time.LoadLocation(zone(w.Timezone))
		if err != nil {
			continue
		}
		local := after.In(loc)
		for offset := 0; offset <= 7; offset++ {
			day := time.Date(local.Year(), local.Month(), local.Day()+offset, 0, 0, 0, 0, loc)
			if !contains(w.Days, strings.ToLower(day.Weekday().String())) {
				continue
			}
			start, ok := calendarWindowStart(w, day)
			if !ok || start.Before(after) {
				continue
			}
			if next.IsZero() || start.Before(next) {
				next = start
			}
		}
	}
	return next, !next.IsZero()
}
