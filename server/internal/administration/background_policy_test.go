package administration

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/workpolicy"
	"testing"
	"time"
)

func TestBackgroundPauseDurableAndIdempotent(t *testing.T) {
	defer dbwork.SetBackgroundPolicy(dbwork.SpeedNormal, 0)
	service, _, _ := newService(t)
	ctx := context.Background()
	first, err := service.PauseBackground(ctx, allow, BackgroundPauseChange{true, "pause-work-hour"})
	if err != nil || first.PausedUntil < time.Now().Add(59*time.Minute).UnixMilli() {
		t.Fatal(first, err)
	}
	again, err := service.PauseBackground(ctx, allow, BackgroundPauseChange{true, "pause-work-hour"})
	if err != nil || first.PausedUntil != again.PausedUntil || first.Revision != again.Revision {
		t.Fatal("retry extended pause", again, err)
	}
	saved, err := service.MaintenanceSettings(ctx, allow)
	if err != nil || saved.PausedUntil != first.PausedUntil {
		t.Fatal("pause not stored", saved, err)
	}
	settings := saved.Settings
	settings.BackgroundSpeed = "fast"
	saved, err = service.SaveMaintenanceSettings(ctx, allow, Change[MaintenanceSettingsDocument]{ExpectedRevision: saved.Revision, OperationID: "speed-while-paused", Settings: settings})
	if err != nil || saved.PausedUntil != first.PausedUntil {
		t.Fatal("settings save erased pause", saved, err)
	}
	resumed, err := service.PauseBackground(ctx, allow, BackgroundPauseChange{false, "resume-work-now"})
	if err != nil || resumed.PausedUntil != 0 {
		t.Fatal(resumed, err)
	}
	if dbwork.BackgroundSpeed(ctx) != "fast" || !dbwork.Yield(ctx) {
		t.Fatal("running policy not applied")
	}
}

func TestMaintenanceWindowsSaveRealHeavyAdmissionWithoutChangingBackup(t *testing.T) {
	service, db, _ := newService(t)
	ctx := context.Background()
	current, err := service.MaintenanceSettings(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Tasks) != 3 {
		t.Fatal("supported scheduling tasks missing", current.Tasks)
	}
	settings := current.Settings
	var backup MaintenanceWindow
	for i := range settings.Windows {
		if settings.Windows[i].ID == "nightly" {
			backup = settings.Windows[i]
		}
		if settings.Windows[i].ID == "heavy" {
			settings.Windows[i].Enabled = true
			settings.Windows[i].Timezone = "UTC"
		}
	}
	saved, err := service.SaveMaintenanceSettings(ctx, allow, Change[MaintenanceSettingsDocument]{ExpectedRevision: current.Revision, OperationID: "supported-heavy-schedule", Settings: settings})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range saved.Settings.Windows {
		if w.ID == "nightly" && (w.StartMinute != backup.StartMinute || w.DurationMinutes != backup.DurationMinutes || w.Enabled != backup.Enabled) {
			t.Fatal("heavy edit changed backup", w, backup)
		}
	}
	for _, hour := range []int{2, 12} {
		gate := workpolicy.Service{DB: db, Now: func() time.Time { return time.Date(2026, 10, 19, hour, 0, 0, 0, time.UTC) }}
		admitted, err := gate.Admit(ctx, nil, "analysis")
		if err != nil || admitted != (hour == 2) {
			t.Fatal("saved schedule did not control actual next batch", hour, admitted, err)
		}
	}
	settings.Windows[0].Tasks = []string{"episode_thumbnail"}
	if _, err = service.SaveMaintenanceSettings(ctx, allow, Change[MaintenanceSettingsDocument]{ExpectedRevision: saved.Revision, OperationID: "unsupported-episode-schedule", Settings: settings}); err == nil {
		t.Fatal("unsupported operation name became a scheduling task")
	}
}

func TestCommittedBackgroundPolicySurvivesCancelledRequest(t *testing.T) {
	defer dbwork.SetBackgroundPolicy(dbwork.SpeedNormal, 0)
	service, _, _ := newService(t)
	saved, e := service.MaintenanceSettings(context.Background(), allow)
	if e != nil {
		t.Fatal(e)
	}
	settings := saved.Settings
	settings.BackgroundSpeed = "fast"
	if _, e = service.SaveMaintenanceSettings(context.Background(), allow, Change[MaintenanceSettingsDocument]{ExpectedRevision: saved.Revision, OperationID: "committed-speed", Settings: settings}); e != nil {
		t.Fatal(e)
	}
	dbwork.SetBackgroundPolicy(dbwork.SpeedNormal, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service.ApplyBackgroundPolicy(ctx)
	if got := dbwork.BackgroundSpeed(context.Background()); got != "fast" {
		t.Fatalf("committed speed left unapplied after request cancelled: %s", got)
	}
}
