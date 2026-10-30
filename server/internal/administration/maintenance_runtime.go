package administration

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"github.com/PorticoMediaServer/portico/server/internal/workpolicy"
	"reflect"
)

func maintenanceRuntime(ctx context.Context, tx *sql.Tx, settings *MaintenanceSettingsDocument) error {
	p, err := workpolicy.ReadTx(ctx, tx)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(p.Windows)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &settings.Windows); err != nil {
		return err
	}
	return nil
}

// BackupKeepCount reads the retained backup count for the backup scheduler.
// It answers the shipped default when nothing usable is stored. Internal use:
// the value is server policy, and the scheduler cannot present owner auth.
func (s *Service) BackupKeepCount(ctx context.Context) int {
	fallback := DefaultMaintenanceSettings().BackupKeepCount
	if s == nil || s.db == nil {
		return fallback
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return fallback
	}
	defer snapshot.Rollback()
	settings, _, err := readMaintenanceSettings(ctx, snapshot.Tx())
	if err != nil {
		return fallback
	}
	normalizeMaintenance(&settings)
	clampInt(&settings.BackupKeepCount, 1, 365)
	return settings.BackupKeepCount
}
func (s *Service) loadMaintenanceRuntime(ctx context.Context, auth Authorize) (MaintenanceDocument, error) {
	var out MaintenanceDocument
	err := s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		settings, revision, err := readMaintenanceSettings(ctx, tx)
		if err != nil {
			return err
		}
		normalizeMaintenance(&settings)
		if err = maintenanceRuntime(ctx, tx, &settings); err != nil {
			return err
		}
		out = maintenanceDocument(Document[MaintenanceSettingsDocument]{Revision: revision, Digest: digestOf(settings), Settings: settings})
		p, err := workpolicy.ReadTx(ctx, tx)
		if err != nil {
			return err
		}
		out.PausedUntil = p.PausedUntil
		return nil
	})
	return out, err
}
func (s *Service) saveMaintenanceRuntime(ctx context.Context, auth Authorize, change Change[MaintenanceSettingsDocument]) (MaintenanceDocument, error) {
	var out MaintenanceDocument
	if !validOperationID(change.OperationID) {
		return out, ErrInput
	}
	value := change.Settings
	normalizeMaintenance(&value)
	if err := validateMaintenance(&value); err != nil {
		return out, err
	}
	digest := digestOf(change)
	err := s.transaction(ctx, auth, func(tx *sql.Tx) error {
		r, err := usermutation.CheckContext(ctx, tx, s.milliseconds())
		if err != nil {
			return err
		}
		if r != nil {
			settings, revision, err := ReadMaintenanceConfiguration(ctx, tx)
			if err != nil {
				return err
			}
			out = maintenanceDocument(Document[MaintenanceSettingsDocument]{Revision: revision, Digest: digestOf(settings), Settings: settings})
			p, err := workpolicy.ReadTx(ctx, tx)
			out.PausedUntil = p.PausedUntil
			return err
		}

		saved, replayed, err := receipt[MaintenanceDocument](ctx, tx, maintenanceScope, change.OperationID, digest)
		if err != nil {
			return err
		}
		if replayed {
			out = saved
			return nil
		}
		current, revision, err := ReadMaintenanceConfiguration(ctx, tx)
		if err != nil {
			return err
		}
		if revision != change.ExpectedRevision {
			return ErrConflict
		}
		raw, _ := json.Marshal(value.Windows)
		p := workpolicy.Policy{BackgroundPriority: dbwork.BackgroundPriorityForSpeed(value.BackgroundSpeed)}
		if err = json.Unmarshal(raw, &p.Windows); err != nil {
			return err
		}
		if err = workpolicy.SaveTx(ctx, tx, p); err != nil {
			return err
		}
		storedValue := value
		storedValue.Windows = nil
		if err = writeMaintenanceSettings(ctx, tx, revision+1, storedValue, s.milliseconds()); err != nil {
			return err
		}
		changed := map[string]bool{}
		if current.BackgroundSpeed != value.BackgroundSpeed {
			changed["backgroundSpeed"] = true
		}
		if current.BackupKeepCount != value.BackupKeepCount {
			changed["backupKeepCount"] = true
		}
		for key, days := range value.Retention {
			if current.Retention[key] != days {
				changed["retention."+key] = true
			}
		}
		for _, next := range value.Windows {
			for _, before := range current.Windows {
				if next.ID != before.ID {
					continue
				}
				prefix := maintenanceWindowRole(next) + "."
				if next.Enabled != before.Enabled || next.Cadence != before.Cadence || next.Timezone != before.Timezone {
					changed[prefix+"mode"] = true
				}
				if next.StartMinute != before.StartMinute {
					changed[prefix+"start"] = true
				}
				if next.DurationMinutes != before.DurationMinutes {
					changed[prefix+"hours"] = true
				}
				if !reflect.DeepEqual(next.Days, before.Days) {
					changed[prefix+"days"] = true
				}
			}
		}
		for field := range changed {
			if _, err = tx.ExecContext(ctx, `INSERT INTO server_setting_sources VALUES(?,'server') ON CONFLICT(field) DO UPDATE SET source=excluded.source`, "maintenance:"+field); err != nil {
				return err
			}
		}
		if len(changed) > 0 {
			if _, err = tx.ExecContext(ctx, `UPDATE console_settings_revision SET revision=revision+1 WHERE singleton=1`); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE server_configuration_state SET pending_revision=(SELECT revision FROM console_settings_revision WHERE singleton=1) WHERE singleton=1`); err != nil {
				return err
			}
			for field := range changed {
				if _, err = tx.ExecContext(ctx, `INSERT INTO server_configuration_pending(field,revision) VALUES(?,(SELECT revision FROM console_settings_revision WHERE singleton=1)) ON CONFLICT(field) DO UPDATE SET revision=excluded.revision`, "maintenance:"+field); err != nil {
					return err
				}
			}
		}
		out = maintenanceDocument(Document[MaintenanceSettingsDocument]{Revision: revision + 1, Digest: digestOf(value), Settings: value})
		policy, readErr := workpolicy.ReadTx(ctx, tx)
		if readErr != nil {
			return readErr
		}
		out.PausedUntil = policy.PausedUntil
		return saveSettingsReceipt(ctx, tx, maintenanceScope, change.OperationID, digest, out, maintenanceScope, out.Revision, s.milliseconds())
	})
	if err == nil {
		s.ApplyBackgroundPolicy(ctx)
		if s.ConfigurationCommitted != nil {
			err = s.ConfigurationCommitted(context.WithoutCancel(ctx))
		}
	}
	return out, err
}
