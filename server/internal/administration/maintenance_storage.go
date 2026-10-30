package administration

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/workpolicy"
)

// ReadMaintenanceConfiguration joins the typed settings and the real scheduler
// policy inside the caller's snapshot. It does not create a second authority.
func ReadMaintenanceConfiguration(ctx context.Context, tx *sql.Tx) (MaintenanceSettingsDocument, int64, error) {
	v, revision, err := readMaintenanceSettings(ctx, tx)
	if err == nil {
		err = maintenanceRuntime(ctx, tx, &v)
	}
	return v, revision, err
}

func WriteMaintenanceConfiguration(ctx context.Context, tx *sql.Tx, v MaintenanceSettingsDocument, now int64) error {
	normalizeMaintenance(&v)
	if err := validateMaintenance(&v); err != nil {
		return err
	}
	_, revision, err := readMaintenanceSettings(ctx, tx)
	if err != nil {
		return err
	}
	p := workpolicy.Policy{BackgroundPriority: dbwork.BackgroundPriorityForSpeed(v.BackgroundSpeed)}
	for _, w := range v.Windows {
		p.Windows = append(p.Windows, workpolicy.Window{ID: w.ID, Name: w.Name, Enabled: w.Enabled, Cadence: w.Cadence, Days: w.Days, StartMinute: w.StartMinute, DurationMinutes: w.DurationMinutes, Timezone: w.Timezone, Tasks: w.Tasks})
	}
	if err = workpolicy.SaveTx(ctx, tx, p); err != nil {
		return err
	}
	return writeMaintenanceSettings(ctx, tx, revision+1, v, now)
}

// Windows and their day/task members live in workpolicy's tables. The remaining
// maintenance values have their own typed singleton and retention rows.
func readMaintenanceSettings(ctx context.Context, tx *sql.Tx) (MaintenanceSettingsDocument, int64, error) {
	out := DefaultMaintenanceSettings()
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision,backup_keep_count,background_speed FROM maintenance_settings WHERE singleton=1`).Scan(&revision, &out.BackupKeepCount, &out.BackgroundSpeed); err != nil {
		return out, 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT category,days FROM maintenance_retention ORDER BY category`)
	if err != nil {
		return out, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var category string
		var days int
		if err = rows.Scan(&category, &days); err != nil {
			return out, 0, err
		}
		out.Retention[category] = days
	}
	return out, revision, rows.Err()
}
func writeMaintenanceSettings(ctx context.Context, tx *sql.Tx, revision int64, v MaintenanceSettingsDocument, now int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE maintenance_settings SET revision=?,backup_keep_count=?,background_speed=?,updated_ms=? WHERE singleton=1`, revision, v.BackupKeepCount, v.BackgroundSpeed, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM maintenance_retention`); err != nil {
		return err
	}
	for key, days := range v.Retention {
		if _, err := tx.ExecContext(ctx, `INSERT INTO maintenance_retention VALUES(?,?)`, key, days); err != nil {
			return err
		}
	}
	return nil
}
