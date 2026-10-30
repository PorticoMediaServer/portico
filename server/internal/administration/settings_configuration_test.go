package administration

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/workpolicy"
	"strings"
	"testing"
	"time"
)

func TestMaintenanceConfigurationFileUsesRealIndependentWindows(t *testing.T) {
	service, db, root := newService(t)
	ctx := context.Background()
	store := operations.New(db)
	store.ConfigurationDomains = []operations.ConfigurationDomain{MaintenanceConfiguration{}}
	path := filepath.Join(root, "portico.toml")
	if err := os.WriteFile(path, []byte("[general]\nheavy_work='overnight'\nbackground_speed='gentle'\n[backup]\nschedule='weekends'\nstart_time=240\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.InitializeConfiguration(ctx, path); err != nil {
		t.Fatal(err)
	}
	doc, err := service.MaintenanceSettings(ctx, allow)
	if err != nil {
		t.Fatal(err)
	}
	var heavy, backup MaintenanceWindow
	for _, w := range doc.Settings.Windows {
		if w.ID == "heavy" {
			heavy = w
		} else {
			backup = w
		}
	}
	if !heavy.Enabled || heavy.StartMinute != 60 || heavy.DurationMinutes != 300 || !backup.Enabled || backup.StartMinute != 240 || len(backup.Days) != 2 || doc.Settings.BackgroundSpeed != "gentle" {
		t.Fatal(doc)
	}
	if got := dbwork.ReadBackgroundSpeed(ctx, db); got != dbwork.SpeedGentle {
		t.Fatalf("worker ignored imported speed: %s", got)
	}
	if err = dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		p, e := workpolicy.ReadTx(ctx, tx)
		if e != nil {
			return e
		}
		at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)
		if workpolicy.Allowed(p, "analysis", at) || !workpolicy.Allowed(p, "library-scan", at) {
			t.Fatal("window did not control real admission", p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// An app edit shares the same outbox and preserves unrelated TOML domains.
	next := doc.Settings
	next.BackgroundSpeed = "fast"
	if _, err = service.SaveMaintenanceSettings(ctx, allow, Change[MaintenanceSettingsDocument]{ExpectedRevision: doc.Revision, OperationID: "configuration-speed", Settings: next}); err != nil {
		t.Fatal(err)
	}
	if got := dbwork.ReadBackgroundSpeed(ctx, db); got != dbwork.SpeedFast {
		t.Fatalf("worker ignored app speed: %s", got)
	}
	if err = store.FlushConfiguration(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "background_speed = 'fast'") {
		t.Fatal(string(raw), err)
	}
	restarted := operations.New(db)
	restarted.ConfigurationDomains = store.ConfigurationDomains
	if err = restarted.InitializeConfiguration(ctx, path); err != nil {
		t.Fatal(err)
	}
	config, err := restarted.Configuration(ctx, operations.AllowServerScope)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range config.Fields {
		if f.ID == "general.backgroundSpeed" && f.Source != "server" {
			t.Fatal("app source lost on restart", f)
		}
	}
	// One invalid domain rolls back all valid changes from the file.
	before, _ := service.MaintenanceSettings(ctx, allow)
	if err = os.WriteFile(path, []byte("[general]\nbackground_speed='gentle'\n[streaming]\nupload_speed=-1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.ConfigFile.Reload(); err != nil {
		t.Fatal(err)
	}
	if err = restarted.ImportConfiguration(ctx); err == nil {
		t.Fatal("invalid file accepted")
	}
	after, _ := service.MaintenanceSettings(ctx, allow)
	if after.Revision != before.Revision || after.Settings.BackgroundSpeed != "fast" {
		t.Fatal("partial import", before, after)
	}
}

func TestMaintenanceConfigurationCadenceMatchesExactDays(t *testing.T) {
	v := DefaultMaintenanceSettings()
	for i := range v.Windows {
		if v.Windows[i].ID == "nightly" {
			v.Windows[i].Enabled = true
			v.Windows[i].Cadence = "custom"
			v.Windows[i].Days = []string{"sunday", "saturday"}
		}
	}
	if got := maintenanceConfigurationValues(v)["backup.mode"]; got != "weekends" {
		t.Fatal("exact day set was not recognized", got)
	}
	for i := range v.Windows {
		if v.Windows[i].ID == "nightly" {
			v.Windows[i].Days = []string{"monday", "saturday", "sunday"}
		}
	}
	if got := maintenanceConfigurationValues(v)["backup.mode"]; got != "custom" {
		t.Fatal("unmatched day set rounded to a preset", got)
	}
}
