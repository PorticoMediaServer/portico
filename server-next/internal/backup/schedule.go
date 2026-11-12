package backup

import (
	"context"
	"slices"
	"time"
)

// The schedule. Backups follow the server's own clock: a backup is due at the owner's hour on
// each of the owner's days. One missed while the server was off runs once when it is back,
// never once for each day missed. One that fails is tried again every half hour for 4 hours.

// missedAfter is how long after a start a backup missed while the server was off runs, so
// it does not compete with the start itself.
var missedAfter = 5 * time.Minute

// retryEvery is how often a scheduled backup that failed is tried again.
var retryEvery = 30 * time.Minute

const retryFor = 4 * time.Hour

// due is the scheduled time on the day of t, and whether the schedule has one that day.
func due(s Settings, day time.Time) (time.Time, bool) {
	if !slices.Contains(s.Days, int(day.Weekday())) {
		return time.Time{}, false
	}
	return time.Date(day.Year(), day.Month(), day.Day(), s.Hour, 0, 0, 0, day.Location()), true
}

// nextDue is the first scheduled time after t, zero for Never.
func nextDue(s Settings, t time.Time) time.Time {
	for n := range 8 {
		if at, ok := due(s, t.AddDate(0, 0, n)); ok && at.After(t) {
			return at
		}
	}
	return time.Time{}
}

// lastDue is the latest scheduled time at or before t, zero for Never.
func lastDue(s Settings, t time.Time) time.Time {
	for n := range 8 {
		if at, ok := due(s, t.AddDate(0, 0, -n)); ok && !at.After(t) {
			return at
		}
	}
	return time.Time{}
}

// Next is when the next scheduled backup runs, zero for Never.
func (s *Service) Next() time.Time { return nextDue(Document.Get(s.Settings), s.Now().In(time.Local)) }

// Run makes the scheduled backups until ctx ends.
func (s *Service) Run(ctx context.Context) {
	s.removeLeftovers()
	s.readNewest()
	changed := make(chan struct{}, 1)
	Document.Watch(s.Settings, func(Settings) {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	wait := time.Duration(-1)
	if s.missed() {
		wait = missedAfter
	}
	var retryUntil time.Time
	for {
		if wait < 0 {
			at := s.Next()
			if at.IsZero() {
				wait = 24 * time.Hour // Never: look again tomorrow, or when the settings change
			} else {
				wait = at.Sub(s.Now())
			}
		}
		timer := time.NewTimer(max(wait, 0))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-changed:
			timer.Stop()
			s.readNewest() // the backup folder may have changed
			// The retries under way were for the old settings; the next due backup has its own.
			retryUntil, wait = time.Time{}, -1
			if len(Document.Get(s.Settings).Days) == 0 {
				s.forgetFailure()
			}
			continue
		case <-timer.C:
		}
		if len(Document.Get(s.Settings).Days) == 0 {
			wait = -1
			continue
		}
		if until := s.Settings.BackgroundPausedUntil(s.Now()); !until.IsZero() {
			wait = min(until.Sub(s.Now()), time.Second)
			continue
		}
		if retryUntil.IsZero() {
			retryUntil = s.Now().Add(retryFor)
		}
		if _, err := s.Start(true); err != nil {
			wait = retryEvery // one is being made now: look again soon
			continue
		}
		// Wait for this backup to end, then decide whether to try again.
		for s.Running() != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
		if s.LastError() != nil {
			// The owner is told once backups have failed for 4 hours: tonight's retries are
			// used up, or they were already failing that long ago, before a restart.
			since := s.failingSince()
			if !s.Now().Before(retryUntil) || (!since.IsZero() && !s.Now().Before(since.Add(retryFor))) {
				s.GiveUp()
			}
			if s.Now().Before(retryUntil) {
				wait = retryEvery
				continue
			}
		}
		retryUntil, wait = time.Time{}, -1
	}
}

// missed says a scheduled backup fell due while the server was off or was cut off by a stop:
// the latest due time is after the newest scheduled backup. It is made after the start, and
// nobody is told.
func (s *Service) missed() bool {
	at := lastDue(Document.Get(s.Settings), s.Now().In(time.Local))
	if at.IsZero() {
		return false
	}
	list, err := s.List()
	if err != nil {
		return false
	}
	for _, b := range list {
		if b.Scheduled {
			return b.CreatedAt.Before(at)
		}
	}
	// None yet. One was missed only if it fell due after the server was set up: a first backup
	// cut off by a restart is missed like any other, and a new server waits for its first
	// scheduled time.
	setUp, ok, err := s.DB.SetUpAt(context.Background())
	return err == nil && ok && at.After(setUp)
}
