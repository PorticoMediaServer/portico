package persistence_test

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/workpolicy"
)

func TestMaintenanceDefaultsPreserveBackupAndRepresentAnyTime(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var revision, keep, updated int64
	var speed string
	if err := db.QueryRow(`SELECT revision,backup_keep_count,background_speed,updated_ms FROM maintenance_settings WHERE singleton=1`).Scan(&revision, &keep, &speed, &updated); err != nil {
		t.Fatal(err)
	}
	if revision != 1 || keep != 7 || speed != "normal" || updated != 0 {
		t.Fatalf("maintenance defaults = %d/%d/%q/%d", revision, keep, speed, updated)
	}
	rows, err := db.Query(`SELECT category,days FROM maintenance_retention`)
	if err != nil {
		t.Fatal(err)
	}
	retention := map[string]int{}
	for rows.Next() {
		var category string
		var days int
		if err := rows.Scan(&category, &days); err != nil {
			t.Fatal(err)
		}
		retention[category] = days
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	want := map[string]int{"trickplay": 365, "prepared": 90, "conversions": 7, "subtitles": 365, "downloads": 30, "logs": 30}
	if !reflect.DeepEqual(retention, want) {
		t.Fatalf("retention defaults = %v, want %v", retention, want)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ctx := context.Background()
	policy, err := workpolicy.ReadTx(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	windows := map[string]workpolicy.Window{}
	for _, window := range policy.Windows {
		windows[window.ID] = window
	}
	days := []string{"friday", "monday", "saturday", "sunday", "thursday", "tuesday", "wednesday"}
	backup, ok := windows["nightly"]
	if !ok || len(windows) != 2 || backup.Name != "Overnight" || !backup.Enabled || backup.Cadence != "nightly" || backup.StartMinute != 180 || backup.DurationMinutes != 240 || backup.Timezone != "" || !reflect.DeepEqual(backup.Days, days) || !reflect.DeepEqual(backup.Tasks, []string{"backup"}) {
		t.Fatalf("nightly backup changed: %+v; windows=%v", backup, windows)
	}
	heavy, ok := windows["heavy"]
	if !ok || heavy.Name != "Heavy background work" || heavy.Enabled || heavy.Cadence != "nightly" || heavy.StartMinute != 60 || heavy.DurationMinutes != 300 || heavy.Timezone != "" || !reflect.DeepEqual(heavy.Days, days) || !reflect.DeepEqual(heavy.Tasks, []string{"analysis", "trickplay"}) {
		t.Fatalf("heavy window = %+v", heavy)
	}
	for _, task := range []string{"analysis", "trickplay"} {
		facts, err := workpolicy.ReadHeavyWorkTx(ctx, tx, task, time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		if facts.WindowConfigured || !facts.WindowOpen || len(facts.Windows) != 1 || facts.Windows[0].ID != "heavy" {
			t.Fatalf("%s does not represent stored Any time: %+v", task, facts)
		}
	}
}

func TestMaintenanceSettingsRejectInvalidValues(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`INSERT INTO maintenance_settings(singleton) VALUES(0)`,
		`INSERT INTO maintenance_settings(singleton) VALUES(2)`,
		`UPDATE maintenance_settings SET backup_keep_count=0`,
		`UPDATE maintenance_settings SET backup_keep_count=366`,
		`UPDATE maintenance_settings SET background_speed='automatic'`,
		`UPDATE maintenance_retention SET days=-1`,
		`UPDATE maintenance_retention SET days=3651`,
		`INSERT INTO maintenance_retention(category,days) VALUES('unknown',30)`,
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Fatalf("accepted invalid maintenance value: %s", statement)
		}
	}
	for _, statement := range []string{
		`UPDATE maintenance_settings SET backup_keep_count=1,background_speed='gentle'`,
		`UPDATE maintenance_settings SET backup_keep_count=365,background_speed='fast'`,
		`UPDATE maintenance_retention SET days=0`,
		`UPDATE maintenance_retention SET days=3650`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("rejected maintenance boundary %s: %v", statement, err)
		}
	}
}

func TestReadingRecentProgressSeeksOwnerCursor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT observed_at,item_id,asset_id FROM reading_progress WHERE owner_key=? AND (observed_at,item_id,asset_id)<(?,?,?) ORDER BY observed_at DESC,item_id DESC,asset_id DESC LIMIT ?`, "owner", 100, 10, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "SEARCH reading_progress USING COVERING INDEX reading_progress_recent") || strings.Contains(joined, "TEMP B-TREE") {
		t.Fatalf("recent Reading progress does not seek its owner/cursor in order:\n%s", joined)
	}
}
