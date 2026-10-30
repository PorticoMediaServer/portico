package workpolicy_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/workpolicy"
)

func TestHeavyWorkFactsReadCurrentWindowsAndIndependentPause(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	inside := time.Date(2026, 10, 19, 2, 0, 0, 0, time.UTC)
	pausedUntil := inside.Add(time.Hour).UnixMilli()
	err = dbwork.WithWriteTx(ctx, db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		if err := workpolicy.SaveTx(ctx, tx, workpolicy.Policy{Windows: []workpolicy.Window{
			{ID: "heavy", Enabled: true, Days: []string{"monday"}, StartMinute: 60, DurationMinutes: 300, Timezone: "UTC", Tasks: []string{"analysis", "trickplay"}},
			{ID: "backup", Enabled: true, Days: []string{"monday"}, StartMinute: 720, DurationMinutes: 60, Timezone: "UTC", Tasks: []string{"backup"}},
		}}); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE maintenance_policy SET paused_until_ms=?,revision=revision+1 WHERE singleton=1`, pausedUntil)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	read := func(task string, at time.Time) workpolicy.HeavyWorkFacts {
		t.Helper()
		snapshot, err := dbwork.BeginSnapshot(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		defer snapshot.Rollback()
		facts, err := workpolicy.ReadHeavyWorkTx(ctx, snapshot.Tx(), task, at)
		if err != nil {
			t.Fatal(err)
		}
		return facts
	}
	first := read("analysis", inside)
	if first.Revision <= 0 || first.Task != "analysis" || !first.ObservedAt.Equal(inside) || first.PausedUntil != pausedUntil || !first.WindowConfigured || !first.WindowOpen || len(first.Windows) != 1 || first.Windows[0].ID != "heavy" {
		t.Fatalf("current scheduling facts: %+v", first)
	}
	if outside := read("analysis", inside.Add(10*time.Hour)); outside.WindowOpen {
		t.Fatalf("closed heavy window reported open: %+v", outside)
	}
	if trickplay := read("trickplay", inside); !trickplay.WindowOpen || trickplay.Revision != first.Revision {
		t.Fatalf("trickplay did not read the same policy: %+v", trickplay)
	}
	for _, check := range []struct {
		at      time.Time
		allowed bool
	}{{inside, false}, {inside.Add(2 * time.Hour), true}, {inside.Add(10 * time.Hour), false}} {
		gate := workpolicy.Service{DB: db, Now: func() time.Time { return check.at }}
		for _, task := range []string{"analysis", "trickplay"} {
			if allowed, err := gate.Admit(ctx, nil, task); err != nil || allowed != check.allowed {
				t.Fatalf("actual %s batch admission at %v = %v, %v", task, check.at, allowed, err)
			}
		}
	}
	// A committed owner edit is visible through a new snapshot and revision.
	if err = dbwork.WithWriteTx(ctx, db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		return workpolicy.SaveTx(ctx, tx, workpolicy.Policy{Windows: []workpolicy.Window{}})
	}); err != nil {
		t.Fatal(err)
	}
	anytime := read("analysis", inside.Add(10*time.Hour))
	if anytime.Revision <= first.Revision || anytime.WindowConfigured || !anytime.WindowOpen || anytime.PausedUntil != pausedUntil {
		t.Fatalf("Any time or independent pause lost: %+v", anytime)
	}
}

func TestHeavyWorkFactsRejectMissingAndUnsupportedAuthority(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	if _, err := workpolicy.ReadHeavyWorkTx(ctx, nil, "analysis", time.Now()); err == nil {
		t.Fatal("nil transaction accepted")
	}
	db, err := persistence.Open(filepath.Join(t.TempDir(), "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	snapshot, err := dbwork.BeginSnapshot(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Rollback()
	for _, task := range []string{"", "episode_thumbnail", "backup", "light-analysis"} {
		if _, err := workpolicy.ReadHeavyWorkTx(ctx, snapshot.Tx(), task, time.Now()); err == nil {
			t.Fatalf("unsupported task %q accepted", task)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := workpolicy.ReadHeavyWorkTx(cancelled, snapshot.Tx(), "analysis", time.Now()); err == nil {
		t.Fatal("canceled facts read accepted")
	}
}

func TestHeavyWorkNextAdmissionUsesTheSameCalendarAndPause(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	monday := time.Date(2026, 10, 19, 2, 0, 0, 0, time.UTC)
	err = dbwork.WithWriteTx(ctx, db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		if err := workpolicy.SaveTx(ctx, tx, workpolicy.Policy{Windows: []workpolicy.Window{{ID: "heavy", Enabled: true, Days: []string{"monday"}, StartMinute: 60, DurationMinutes: 300, Timezone: "UTC", Tasks: []string{"analysis"}}}}); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE maintenance_policy SET paused_until_ms=? WHERE singleton=1`, monday.Add(time.Hour).UnixMilli())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	read := func(at time.Time) workpolicy.HeavyWorkFacts {
		t.Helper()
		snapshot, err := dbwork.BeginSnapshot(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		defer snapshot.Rollback()
		facts, err := workpolicy.ReadHeavyWorkTx(ctx, snapshot.Tx(), "analysis", at)
		if err != nil {
			t.Fatal(err)
		}
		return facts
	}
	paused := read(monday)
	if !paused.WindowOpen || paused.NextAllowedAt == nil || !paused.NextAllowedAt.Equal(monday.Add(time.Hour)) {
		t.Fatal("pause inside an open window lost", paused)
	}
	closed := read(monday.Add(10 * time.Hour))
	want := time.Date(2026, 10, 26, 1, 0, 0, 0, time.UTC)
	if closed.WindowOpen || closed.NextAllowedAt == nil || !closed.NextAllowedAt.Equal(want) || closed.NextWindowStart == nil || !closed.NextWindowStart.Equal(want) {
		t.Fatal("next work did not follow the actual weekly window", closed)
	}
}
