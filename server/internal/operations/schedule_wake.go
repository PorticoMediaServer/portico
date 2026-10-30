package operations

import (
	"context"
	"database/sql"
	"time"
)

// The worker resets its timer after each pass and table wake, including edits
// to a schedule. Zero is reserved for an idle scheduler with no known due work.
func (s *Scheduler) nextWake(ctx context.Context) time.Duration {
	now := s.Store.Now()
	var next time.Time
	var due sql.NullInt64
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT min(next_ms) FROM console_operations WHERE state IN ('queued','running','cancellation-requested','reconciling','paused') AND NOT(state='paused' AND domain_id='')`).Scan(&due); err != nil {
		return time.Second
	}
	if due.Valid {
		next = time.UnixMilli(due.Int64)
	}
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT `+scheduleColumns+` FROM console_schedules WHERE enabled=1 LIMIT 100`)
	if err != nil {
		return time.Second
	}
	defer rows.Close()
	for rows.Next() {
		v, err := scanSchedule(rows)
		if err != nil {
			return time.Second
		}
		at := nextScheduleDue(v, now)
		if !at.IsZero() && (next.IsZero() || at.Before(next)) {
			next = at
		}
	}
	if rows.Err() != nil {
		return time.Second
	}
	// Uploaded app logs have an absolute payload deadline even when no
	// operation or maintenance schedule is active. The table wake handles a
	// newly accepted report; this timer handles its later expiry.
	var expiry sql.NullInt64
	if err := s.Store.DB.QueryRowContext(ctx, `SELECT min(expires_ms) FROM app_reports`).Scan(&expiry); err != nil {
		return time.Second
	}
	if expiry.Valid {
		at := time.UnixMilli(expiry.Int64)
		if next.IsZero() || at.Before(next) {
			next = at
		}
	}
	if next.IsZero() {
		return 0
	}
	// Failed admissions and unavailable lanes retry without spinning.
	return max(time.Second, next.Sub(s.Store.Now()))
}

func nextScheduleDue(v Schedule, now time.Time) time.Time {
	if _, ready := ScheduleSlot(v, now); ready {
		return now
	}
	loc, err := time.LoadLocation(v.Timezone)
	if err != nil || !v.Enabled {
		return time.Time{}
	}
	// Step real minutes through two local days. This preserves ScheduleSlot's
	// midnight, spring gap and repeated-hour semantics instead of assuming 24h.
	at := now.Truncate(time.Minute).Add(time.Minute)
	end := now.In(loc).AddDate(0, 0, 2)
	for !at.After(end) {
		if _, ready := scheduleSlotLocal(v, at.In(loc)); ready {
			return at
		}
		at = at.Add(time.Minute)
	}
	return time.Time{}
}

// appReportsDue makes the automatic retention pass run at the exact payload
// deadline rather than waiting for the ordinary ten-minute pruning interval.
func (s *Store) appReportsDue(ctx context.Context) bool {
	var due bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM app_reports WHERE expires_ms<=?)`, s.now()).Scan(&due)
	return err == nil && due
}
