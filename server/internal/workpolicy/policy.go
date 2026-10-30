// Package workpolicy retains owner maintenance windows as scheduling hints.
// Backups have their own window. Heavy analysis waits for its window; scans,
// matching and playback keep running. An owner pause stops new heavy batches.
package workpolicy

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

var Tasks = []string{"library-scan", "metadata-refresh", "analysis", "trickplay", "backup"}
var Days = []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}

type Window struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Enabled         bool     `json:"enabled"`
	Cadence         string   `json:"cadence"`
	Days            []string `json:"days"`
	StartMinute     int      `json:"startMinute"`
	DurationMinutes int      `json:"durationMinutes"`
	Timezone        string   `json:"timezone"`
	Tasks           []string `json:"tasks"`
}
type Policy struct {
	Revision           int64
	BackgroundPriority string
	PausedUntil        int64
	Windows            []Window
}

func Supported(task string) bool {
	for _, supported := range Tasks {
		if task == supported {
			return true
		}
	}
	return false
}
func SaveTx(ctx context.Context, tx *sql.Tx, p Policy) error {
	if p.BackgroundPriority == "" {
		p.BackgroundPriority = "lower"
	}
	if p.BackgroundPriority != "lower" && p.BackgroundPriority != "normal" {
		return errors.New("invalid background task priority")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO maintenance_policy(singleton,revision,background_priority) VALUES(1,1,?) ON CONFLICT(singleton) DO UPDATE SET revision=revision+1,background_priority=excluded.background_priority`, p.BackgroundPriority); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM maintenance_windows`); err != nil {
		return err
	}
	for _, w := range p.Windows {
		if _, err := time.LoadLocation(zone(w.Timezone)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO maintenance_windows VALUES(?,?,?,?,?,?,?)`, w.ID, w.Name, w.Enabled, w.Cadence, w.StartMinute, w.DurationMinutes, w.Timezone); err != nil {
			return err
		}
		for _, day := range w.Days {
			if _, err := tx.ExecContext(ctx, `INSERT INTO maintenance_window_days VALUES(?,?)`, w.ID, day); err != nil {
				return err
			}
		}
		for _, task := range w.Tasks {
			if task != "backup" && task != "analysis" && task != "trickplay" {
				return errors.New("unsupported scheduled maintenance task")
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO maintenance_window_tasks VALUES(?,?)`, w.ID, task); err != nil {
				return err
			}
		}
	}
	return nil
}
func ReadTx(ctx context.Context, tx *sql.Tx) (Policy, error) {
	p := Policy{Windows: []Window{}}
	if err := tx.QueryRowContext(ctx, `SELECT revision,background_priority,paused_until_ms FROM maintenance_policy WHERE singleton=1`).Scan(&p.Revision, &p.BackgroundPriority, &p.PausedUntil); err != nil {
		return p, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,name,enabled,cadence,start_minute,duration_minutes,timezone FROM maintenance_windows ORDER BY id`)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var w Window
		w.Days = []string{}
		w.Tasks = []string{}
		if err = rows.Scan(&w.ID, &w.Name, &w.Enabled, &w.Cadence, &w.StartMinute, &w.DurationMinutes, &w.Timezone); err != nil {
			rows.Close()
			return p, err
		}
		p.Windows = append(p.Windows, w)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return p, err
	}
	for i := range p.Windows {
		w := &p.Windows[i]
		for _, kind := range []string{"days", "tasks"} {
			column := "day"
			if kind == "tasks" {
				column = "task"
			}
			rows, err = tx.QueryContext(ctx, `SELECT `+column+` FROM maintenance_window_`+kind+` WHERE window_id=? ORDER BY `+column, w.ID)
			if err != nil {
				return p, err
			}
			for rows.Next() {
				var value string
				if err = rows.Scan(&value); err != nil {
					rows.Close()
					return p, err
				}
				if kind == "days" {
					w.Days = append(w.Days, value)
				} else {
					w.Tasks = append(w.Tasks, value)
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return p, err
			}
		}
	}
	return p, nil
}
func zone(z string) string {
	if z == "" {
		return "Local"
	}
	return z
}

// Allowed keeps light work outside the heavy-work window. With no enabled
// heavy window, analysis may run at any time. Backups use Open separately.
func Allowed(p Policy, task string, now time.Time) bool {
	if !Supported(task) {
		return true
	}
	if now.UnixMilli() < p.PausedUntil {
		return false
	}
	if task != "analysis" && task != "trickplay" {
		return true
	}
	for _, w := range p.Windows {
		if w.Enabled && contains(w.Tasks, task) {
			return Open(p, task, now)
		}
	}
	return true
}

// Open reports whether task may start now under policy p: some enabled window
// holding the task is open at now. A window that crosses midnight belongs to
// its start day; repeated DST hours both belong to that interval and
// nonexistent start times move to the first real minute on that date.
func Open(p Policy, task string, now time.Time) bool {
	_, ok := OccurrenceStart(p, task, now)
	return ok
}

// OccurrenceStart is Open with the open occurrence's start: the moment after
// which a scheduled backup counts as covering this occurrence.
func OccurrenceStart(p Policy, task string, now time.Time) (time.Time, bool) {
	if Supported(task) && now.UnixMilli() < p.PausedUntil {
		return time.Time{}, false
	}
	for _, w := range p.Windows {
		if !w.Enabled || !contains(w.Tasks, task) || w.DurationMinutes <= 0 {
			continue
		}
		loc, err := time.LoadLocation(zone(w.Timezone))
		if err != nil {
			continue
		}
		local := now.In(loc)
		// An occurrence spans at most its start day and the next (durations
		// are at most a day), so only occurrences starting today or
		// yesterday can hold now.
		for back := 0; back < 2; back++ {
			day := time.Date(local.Year(), local.Month(), local.Day()-back, 0, 0, 0, 0, loc)
			if !contains(w.Days, strings.ToLower(day.Weekday().String())) {
				continue
			}
			start, ok := calendarWindowStart(w, day)
			if !ok {
				continue
			}
			end := start.Add(time.Duration(w.DurationMinutes) * time.Minute)
			if !now.Before(start) && now.Before(end) {
				return start, true
			}
		}
	}
	return time.Time{}, false
}

// wallClockMatches reports whether start is really the window's start minute
// on day's date: a nonexistent wall time normalises to something else.
func wallClockMatches(start, day time.Time, startMinute int) bool {
	return start.Year() == day.Year() && start.Month() == day.Month() && start.Day() == day.Day() &&
		start.Hour()*60+start.Minute() == startMinute
}

func contains(set []string, value string) bool {
	for _, item := range set {
		if item == value {
			return true
		}
	}
	return false
}

type Service struct {
	DB  *sql.DB
	Now func() time.Time
}

func (s Service) Admit(ctx context.Context, tx *sql.Tx, task string) (bool, error) {
	if tx == nil {
		snapshot, err := dbwork.BeginSnapshot(ctx, s.DB)
		if err != nil {
			return false, err
		}
		defer snapshot.Rollback()
		return s.Admit(ctx, snapshot.Tx(), task)
	}
	p, err := ReadTx(ctx, tx)
	if err != nil {
		return false, err
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	return Allowed(p, task, now), nil
}

// calendarWindowStart is shared by admission and status projection. A missing
// spring-forward wall time advances to the first real minute of that day.
func calendarWindowStart(w Window, day time.Time) (time.Time, bool) {
	if w.StartMinute < 0 || w.StartMinute >= 1440 {
		return time.Time{}, false
	}
	for minute := w.StartMinute; minute < 1440; minute++ {
		start := time.Date(day.Year(), day.Month(), day.Day(), minute/60, minute%60, 0, 0, day.Location())
		if wallClockMatches(start, day, minute) {
			return start, true
		}
	}
	return time.Time{}, false
}
