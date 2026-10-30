package backup

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"reflect"
)

// Configuration joins only the sanitized policy to the shared transaction and
// file outbox. Secret input and derived archive key material stay in PolicyStore.
type Configuration struct{ Store *PolicyStore }

func (Configuration) Bindings() []operations.ConfigurationBinding {
	return []operations.ConfigurationBinding{
		{ID: "storage.backupFolder", Key: "storage.backup_dir", Field: "backupFolder", Scale: 1},
		{ID: "storage.backupArtwork", Key: "backup.include_artwork", Field: "includeArtwork", Scale: 1},
		{ID: "storage.backupPassphrase", Key: "backup.passphrase_protected", Field: "passphraseProtected", Scale: 1},
	}
}
func (Configuration) SourceField(field string) string { return "backup:" + field }
func backupConfigurationValues(v Settings) map[string]any {
	return map[string]any{"backupFolder": v.BackupFolder, "includeArtwork": v.IncludeArtwork, "passphraseProtected": v.PassphraseProtected}
}
func (d Configuration) defaults() map[string]any {
	return backupConfigurationValues(Settings{BackupFolder: filepath.Join(d.Store.state, BackupsDir), IncludeArtwork: false})
}
func backupConfigurationAtom(v any) *any { return &v }
func (d Configuration) Read(ctx context.Context, tx *sql.Tx, sources map[string]string) ([]operations.ConfigurationValue, error) {
	v, err := d.Store.ReadTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	current, defaults := backupConfigurationValues(v), d.defaults()
	out := []operations.ConfigurationValue{}
	for _, b := range d.Bindings() {
		source := sources[d.SourceField(b.Field)]
		if source == "" {
			source = "default"
			if !reflect.DeepEqual(current[b.Field], defaults[b.Field]) {
				source = "server"
			}
		}
		out = append(out, operations.ConfigurationValue{ID: b.ID, Key: b.Key, Current: backupConfigurationAtom(current[b.Field]), Effective: backupConfigurationAtom(current[b.Field]), Default: backupConfigurationAtom(defaults[b.Field]), Source: source, ApplicationMode: "next-operation"})
	}
	return out, nil
}
func (d Configuration) Import(ctx context.Context, tx *sql.Tx, raw map[string]any, sources map[string]string, now int64) (bool, error) {
	apply, err := d.PrepareImport(ctx, tx, raw, sources, now)
	if err != nil || apply == nil {
		return false, err
	}
	if err = apply(); err != nil {
		return false, err
	}
	return true, nil
}
func (d Configuration) PrepareImport(ctx context.Context, tx *sql.Tx, raw map[string]any, sources map[string]string, _ int64) (operations.ConfigurationApply, error) {
	v, err := d.Store.ReadTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	values, defaults := backupConfigurationValues(v), d.defaults()
	patch := SettingsPatch{}
	changed := false
	type sourceChange struct {
		field   string
		removed bool
	}
	var sourceChanges []sourceChange
	for _, b := range d.Bindings() {
		source := d.SourceField(b.Field)
		value, ok := raw[b.Key]
		removed := !ok && sources[source] == "file"
		if removed {
			value = defaults[b.Field]
			ok = true
		}
		if !ok {
			continue
		}
		switch b.Field {
		case "backupFolder":
			v, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("invalid config key %s", b.Key)
			}
			patch.BackupFolder = &v
		case "includeArtwork":
			v, ok := value.(bool)
			if !ok {
				return nil, fmt.Errorf("invalid config key %s", b.Key)
			}
			patch.IncludeArtwork = &v
		case "passphraseProtected":
			v, ok := value.(bool)
			if !ok {
				return nil, fmt.Errorf("invalid config key %s", b.Key)
			}
			patch.PassphraseProtected = &v
		}
		if !reflect.DeepEqual(values[b.Field], value) || sources[source] == "" || removed {
			changed = true
			sourceChanges = append(sourceChanges, sourceChange{source, removed})
		}
	}
	if !changed {
		return nil, nil
	}
	next, err := d.Store.prepareSettings(v, patch)
	if err != nil {
		return nil, err
	}
	if err = validateProtection(next); err != nil {
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
		_, err = d.Store.UpdateTx(ctx, tx, patch)
		return err
	}, nil
}
func (d Configuration) Projection(ctx context.Context, tx *sql.Tx, sources map[string]string) (map[string]any, error) {
	v, err := d.Store.ReadTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	values := backupConfigurationValues(v)
	out := map[string]any{}
	for _, b := range d.Bindings() {
		if sources[d.SourceField(b.Field)] != "" {
			out[b.Key] = values[b.Field]
		}
	}
	return out, nil
}
func (d Configuration) queueChange(ctx context.Context, tx *sql.Tx, before, after Settings) error {
	previous, next := backupConfigurationValues(before), backupConfigurationValues(after)
	changed := []string{}
	for _, b := range d.Bindings() {
		if !reflect.DeepEqual(previous[b.Field], next[b.Field]) {
			changed = append(changed, d.SourceField(b.Field))
		}
	}
	if len(changed) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE console_settings_revision SET revision=revision+1 WHERE singleton=1`); err != nil {
		return err
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM console_settings_revision WHERE singleton=1`).Scan(&revision); err != nil {
		return err
	}
	for _, field := range changed {
		if _, err := tx.ExecContext(ctx, `INSERT INTO server_setting_sources VALUES(?,'server') ON CONFLICT(field) DO UPDATE SET source=excluded.source`, field); err != nil {
			return err
		}
		if err := operations.QueueConfigurationProjectionTx(ctx, tx, field, revision); err != nil {
			return err
		}
	}
	return nil
}
