package workpolicy_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/workpolicy"
	"testing"
	"time"
)

func TestHeavyWindowLeavesLightWorkRunning(t *testing.T) {
	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	p := workpolicy.Policy{Windows: []workpolicy.Window{{Enabled: true, Days: []string{"monday"}, StartMinute: 60, DurationMinutes: 300, Timezone: "UTC", Tasks: []string{"analysis", "trickplay"}}}}
	for _, task := range []string{"analysis", "trickplay"} {
		if workpolicy.Allowed(p, task, at) {
			t.Fatal("heavy work outside window", task)
		}
		if !workpolicy.Allowed(p, task, at.Add(-10*time.Hour)) {
			t.Fatal("heavy work inside window refused", task)
		}
	}
	for _, task := range []string{"light-analysis", "library-scan", "metadata-refresh", "capture", "playback", "erasure"} {
		if !workpolicy.Allowed(p, task, at) {
			t.Fatal("light work paused", task)
		}
	}
	p.Windows[0].Enabled = false
	if !workpolicy.Allowed(p, "analysis", at) {
		t.Fatal("Any time refused")
	}
}
func TestAdmissionReadsDurablePolicyAfterReopen(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	path := filepath.Join(t.TempDir(), "db")
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	err = dbwork.WithWriteTx(ctx, db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		return workpolicy.SaveTx(ctx, tx, workpolicy.Policy{Windows: []workpolicy.Window{}})
	})
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	gate := workpolicy.Service{DB: db}
	if allowed, err := gate.Admit(ctx, nil, "analysis"); err != nil || !allowed {
		t.Fatal(allowed, err)
	}
	if _, err = db.Exec(`UPDATE maintenance_policy SET background_priority='normal'`); err != nil {
		t.Fatal(err)
	}
	if allowed, err := gate.Admit(ctx, nil, "analysis"); err != nil || !allowed {
		t.Fatal(allowed, err)
	}
}

func TestOpenEvaluatesWindows(t *testing.T) {
	days := []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}
	window := func(days []string, start, duration int, zone string, task string) workpolicy.Policy {
		return workpolicy.Policy{Windows: []workpolicy.Window{{ID: "w", Enabled: true, Days: days, StartMinute: start, DurationMinutes: duration, Timezone: zone, Tasks: []string{task}}}}
	}
	at := func(day time.Time, h, m int) time.Time {
		return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, time.UTC)
	}
	friday := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	// Inside and outside a plain window.
	p := window(days, 3*60, 240, "UTC", "backup")
	if !workpolicy.Open(p, "backup", at(friday, 4, 0)) || workpolicy.Open(p, "backup", at(friday, 8, 0)) {
		t.Fatal("plain window misread")
	}
	// Disabled windows and other tasks stay shut.
	disabled := window(days, 3*60, 240, "UTC", "backup")
	disabled.Windows[0].Enabled = false
	if workpolicy.Open(disabled, "backup", at(friday, 4, 0)) || workpolicy.Open(p, "analysis", at(friday, 4, 0)) {
		t.Fatal("disabled window or other task open")
	}
	// A window crossing midnight belongs to its start day.
	night := window([]string{"friday"}, 22*60, 240, "UTC", "backup")
	saturday := at(friday, 1, 0).Add(24 * time.Hour)
	if !workpolicy.Open(night, "backup", saturday) {
		t.Fatal("midnight crosser closed after midnight")
	}
	if start, ok := workpolicy.OccurrenceStart(night, "backup", saturday); !ok || start.Weekday() != time.Friday || start.Hour() != 22 {
		t.Fatalf("occurrence start: %v %v", start, ok)
	}
	if workpolicy.Open(night, "backup", at(friday, 21, 0)) {
		t.Fatal("midnight crosser open before its start day")
	}
	// A nonexistent DST wall time moves to the first real minute (America/Toronto springs
	// forward on 2026-03-08: 02:00–03:00 does not exist).
	dst := window([]string{"sunday"}, 2*60+30, 30, "America/Toronto", "backup")
	sunday := time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC)
	if !workpolicy.Open(dst, "backup", sunday) {
		t.Fatal("spring-forward backup was skipped")
	}
	// A bad timezone never opens.
	bad := window(days, 0, 1440, "No/Such-Zone", "backup")
	if workpolicy.Open(bad, "backup", at(friday, 12, 0)) {
		t.Fatal("bad timezone open")
	}
}

func TestBackupWindowUsesWallTimeAfterClockChange(t *testing.T) {
	zone, err := time.LoadLocation("America/Toronto")
	if err != nil {
		t.Fatal(err)
	}
	policy := workpolicy.Policy{Windows: []workpolicy.Window{{Enabled: true, Days: []string{"sunday"}, Tasks: []string{"backup"}, StartMinute: 12 * 60, DurationMinutes: 30, Timezone: "America/Toronto"}}}
	for _, date := range []time.Time{time.Date(2026, 3, 8, 12, 5, 0, 0, zone), time.Date(2026, 11, 1, 12, 5, 0, 0, zone)} {
		start, ok := workpolicy.OccurrenceStart(policy, "backup", date)
		if !ok || start.Hour() != 12 || start.Minute() != 0 {
			t.Fatal(date, start, ok)
		}
	}
}
