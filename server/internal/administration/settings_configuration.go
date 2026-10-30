package administration

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"reflect"
	"slices"
)

func (MaintenanceConfiguration) Bindings() []operations.ConfigurationBinding {
	return []operations.ConfigurationBinding{
		{ID: "general.heavyWork", Key: "general.heavy_work", Field: "heavy.mode", Scale: 1, ConfigOnly: false},
		{ID: "general.heavyStart", Key: "general.heavy_work_start", Field: "heavy.start", Scale: 1, ConfigOnly: false},
		{ID: "general.heavyLength", Key: "general.heavy_work_hours", Field: "heavy.hours", Scale: 1, ConfigOnly: false},
		{ID: "general.heavyDays", Key: "general.heavy_work_days", Field: "heavy.days", Scale: 1, ConfigOnly: false},
		{ID: "general.backgroundSpeed", Key: "general.background_speed", Field: "backgroundSpeed", Scale: 1, ConfigOnly: false},
		{ID: "storage.backupKeepExact", Key: "storage.backups_to_keep_exact", Field: "backupKeepCount", Scale: 1, ConfigOnly: false},
		{ID: "storage.backupSchedule", Key: "backup.schedule", Field: "backup.mode", Scale: 1, ConfigOnly: false},
		{ID: "storage.backupTime", Key: "backup.start_time", Field: "backup.start", Scale: 1, ConfigOnly: false},
		{ID: "storage.backupWindowLength", Key: "backup.window_hours", Field: "backup.hours", Scale: 1, ConfigOnly: false},
		{ID: "storage.backupDays", Key: "backup.chosen_days", Field: "backup.days", Scale: 1, ConfigOnly: false},
		{ID: "storage.keepConversions", Key: "storage.keep_conversions_days", Field: "retention.conversions", Scale: 1, ConfigOnly: false},
		{ID: "storage.keepPrepared", Key: "storage.keep_prepared_days", Field: "retention.prepared", Scale: 1, ConfigOnly: false},
		{ID: "storage.keepDownloads", Key: "storage.keep_downloads_days", Field: "retention.downloads", Scale: 1, ConfigOnly: false},
		{ID: "storage.keepSubtitles", Key: "storage.keep_subtitles_days", Field: "retention.subtitles", Scale: 1, ConfigOnly: false},
		{ID: "storage.keepTrickplay", Key: "storage.keep_trickplay_days", Field: "retention.trickplay", Scale: 1, ConfigOnly: false},
	}
}
func maintenanceWindowRole(w MaintenanceWindow) string {
	if slices.Contains(w.Tasks, "backup") {
		return "backup"
	}
	if slices.Contains(w.Tasks, "analysis") || slices.Contains(w.Tasks, "trickplay") {
		return "heavy"
	}
	return ""
}
func maintenanceConfigurationValues(v MaintenanceSettingsDocument) map[string]any {
	out := map[string]any{"backgroundSpeed": v.BackgroundSpeed, "backupKeepCount": v.BackupKeepCount}
	for k, n := range v.Retention {
		out["retention."+k] = n
	}
	for _, w := range v.Windows {
		days := map[string]any{}
		for _, day := range Days {
			if slices.Contains(w.Days, day) {
				days[day] = "runs"
			} else {
				days[day] = "skips"
			}
		}
		mode := "custom"
		if maintenanceWindowRole(w) == "heavy" {
			if !w.Enabled {
				mode = "any"
			} else if w.StartMinute == 60 && w.DurationMinutes == 300 && len(w.Days) == 7 && w.Timezone == "" {
				mode = "overnight"
			}
		} else {
			if !w.Enabled {
				mode = "never"
			} else {
				for _, cadence := range MaintenanceCadences() {
					if cadence.ID == "custom" || len(cadence.Days) != len(w.Days) {
						continue
					}
					exact := true
					for _, day := range cadence.Days {
						exact = exact && slices.Contains(w.Days, day)
					}
					if exact {
						mode = cadence.ID
						break
					}
				}
			}
		}
		out[maintenanceWindowRole(w)+".mode"] = mode
		out[maintenanceWindowRole(w)+".start"] = w.StartMinute
		out[maintenanceWindowRole(w)+".hours"] = float64(w.DurationMinutes) / 60
		out[maintenanceWindowRole(w)+".days"] = days
	}
	return out
}
func assignMaintenanceConfiguration(v *MaintenanceSettingsDocument, b operations.ConfigurationBinding, raw any) error {
	if b.Field == "backgroundSpeed" {
		s, ok := raw.(string)
		if !ok {
			return operations.ErrInvalid
		}
		v.BackgroundSpeed = s
		return nil
	}
	number := func() (int, error) {
		var n float64
		switch t := raw.(type) {
		case int:
			n = float64(t)
		case int64:
			n = float64(t)
		case float64:
			n = t
		default:
			return 0, operations.ErrInvalid
		}
		if n < 0 || n > 1e6 || n != float64(int(n)) {
			return 0, operations.ErrInvalid
		}
		return int(n), nil
	}
	if b.Field == "backupKeepCount" {
		n, e := number()
		v.BackupKeepCount = n
		return e
	}
	if len(b.Field) > 10 && b.Field[:10] == "retention." {
		n, e := number()
		v.Retention[b.Field[10:]] = n
		return e
	}
	for i := range v.Windows {
		w := &v.Windows[i]
		prefix := maintenanceWindowRole(*w) + "."
		if len(b.Field) < len(prefix) || b.Field[:len(prefix)] != prefix {
			continue
		}
		switch b.Field[len(prefix):] {
		case "mode":
			s, ok := raw.(string)
			if !ok {
				return operations.ErrInvalid
			}
			if maintenanceWindowRole(*w) == "heavy" {
				switch s {
				case "any":
					w.Enabled = false
				case "overnight":
					w.Enabled = true
					w.StartMinute = 60
					w.DurationMinutes = 300
					w.Days = append([]string{}, Days...)
					w.Timezone = ""
					w.Cadence = "nightly"
				case "custom":
					w.Enabled = true
					w.Cadence = "custom"
				default:
					return operations.ErrInvalid
				}
			} else {
				if s == "never" {
					w.Enabled = false
					return nil
				}
				w.Enabled = true
				for _, c := range MaintenanceCadences() {
					if c.ID == s {
						w.Cadence = s
						if s != "custom" {
							w.Days = append([]string{}, c.Days...)
						}
						return nil
					}
				}
				return operations.ErrInvalid
			}
		case "start":
			n, e := number()
			if e != nil {
				return e
			}
			w.StartMinute = n
		case "hours":
			n, e := number()
			if e != nil {
				return e
			}
			w.DurationMinutes = n * 60
		case "days":
			m, ok := raw.(map[string]any)
			if !ok || len(m) != 7 {
				return operations.ErrInvalid
			}
			w.Days = []string{}
			for _, day := range Days {
				switch m[day] {
				case "runs":
					w.Days = append(w.Days, day)
				case "skips":
				default:
					return operations.ErrInvalid
				}
			}
			w.Cadence = "custom"
		default:
			return operations.ErrInvalid
		}
		return nil
	}
	return operations.ErrInvalid
}
func (MaintenanceConfiguration) Read(ctx context.Context, tx *sql.Tx, sources map[string]string) ([]operations.ConfigurationValue, error) {
	v, _, err := ReadMaintenanceConfiguration(ctx, tx)
	if err != nil {
		return nil, err
	}
	current := maintenanceConfigurationValues(v)
	defaults := maintenanceConfigurationValues(DefaultMaintenanceSettings())
	out := []operations.ConfigurationValue{}
	for _, b := range (MaintenanceConfiguration{}).Bindings() {
		source := sources["maintenance:"+b.Field]
		if source == "" {
			source = "default"
		}
		out = append(out, operations.ConfigurationValue{ID: b.ID, Key: b.Key, Current: maintenanceAtom(current[b.Field]), Effective: maintenanceAtom(current[b.Field]), Default: maintenanceAtom(defaults[b.Field]), Source: source, ConfigOnly: b.ConfigOnly, ApplicationMode: "hot"})
	}
	return out, nil
}
func (MaintenanceConfiguration) Import(ctx context.Context, tx *sql.Tx, raw map[string]any, sources map[string]string, now int64) (bool, error) {
	apply, err := (MaintenanceConfiguration{}).PrepareImport(ctx, tx, raw, sources, now)
	if err != nil || apply == nil {
		return false, err
	}
	if err = apply(); err != nil {
		return false, err
	}
	return true, nil
}
func (MaintenanceConfiguration) PrepareImport(ctx context.Context, tx *sql.Tx, raw map[string]any, sources map[string]string, now int64) (operations.ConfigurationApply, error) {
	v, _, err := ReadMaintenanceConfiguration(ctx, tx)
	if err != nil {
		return nil, err
	}
	defaults := maintenanceConfigurationValues(DefaultMaintenanceSettings())
	changed := false
	type sourceChange struct {
		field   string
		removed bool
	}
	var sourceChanges []sourceChange
	for _, b := range (MaintenanceConfiguration{}).Bindings() {
		key := "maintenance:" + b.Field
		value, ok := raw[b.Key]
		removed := !ok && sources[key] == "file"
		if removed {
			value = defaults[b.Field]
			ok = true
		}
		if !ok {
			continue
		}
		before := maintenanceConfigurationValues(v)[b.Field]
		if err = assignMaintenanceConfiguration(&v, b, value); err != nil {
			return nil, fmt.Errorf("invalid config key %s", b.Key)
		}
		different := !reflect.DeepEqual(before, maintenanceConfigurationValues(v)[b.Field])
		if different || sources[key] == "" || removed {
			changed = true
			sourceChanges = append(sourceChanges, sourceChange{key, removed})
		}
	}
	if !changed {
		return nil, nil
	}
	normalizeMaintenance(&v)
	if err := validateMaintenance(&v); err != nil {
		return nil, err
	}
	return func() error {
		for _, change := range sourceChanges {
			if change.removed {
				_, err = tx.ExecContext(ctx, `DELETE FROM server_setting_sources WHERE field=?`, change.field)
			} else {
				_, err = tx.ExecContext(ctx, `INSERT INTO server_setting_sources VALUES(?,'file') ON CONFLICT(field) DO UPDATE SET source=excluded.source`, change.field)
			}
			if err != nil {
				return err
			}
		}
		return WriteMaintenanceConfiguration(ctx, tx, v, now)
	}, nil
}

// MaintenanceConfiguration connects the existing normalized scheduler authority
// to the shared file inside the same transaction as runtime settings.
type MaintenanceConfiguration struct{}

func (MaintenanceConfiguration) SourceField(field string) string { return "maintenance:" + field }

func maintenanceAtom(v any) *any {
	if v == nil {
		return nil
	}
	return &v
}
func (MaintenanceConfiguration) Projection(ctx context.Context, tx *sql.Tx, sources map[string]string) (map[string]any, error) {
	v, _, err := ReadMaintenanceConfiguration(ctx, tx)
	if err != nil {
		return nil, err
	}
	values := maintenanceConfigurationValues(v)
	out := map[string]any{}
	for _, b := range (MaintenanceConfiguration{}).Bindings() {
		if sources["maintenance:"+b.Field] != "" {
			out[b.Key] = values[b.Field]
		}
	}
	return out, nil
}
