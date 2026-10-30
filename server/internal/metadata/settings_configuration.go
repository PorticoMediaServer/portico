package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// Configuration projects only the real artwork-size policy. Credentials and
// credential-presence facts never enter the shared configuration file.
type Configuration struct{}

func (Configuration) Bindings() []operations.ConfigurationBinding {
	return []operations.ConfigurationBinding{{ID: "metadata.artworkSize", Key: "metadata.artwork_max_edge", Field: "artworkMaxEdge", Scale: 1}}
}
func (Configuration) SourceField(field string) string { return "metadata:" + field }
func (d Configuration) Read(ctx context.Context, tx *sql.Tx, sources map[string]string) ([]operations.ConfigurationValue, error) {
	v, err := ReadMetadataSettingsTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	source := sources[d.SourceField("artworkMaxEdge")]
	if source == "" {
		source = "default"
		if v.ArtworkMaxEdge != 1920 {
			source = "server"
		}
	}
	var current, factory any = v.ArtworkMaxEdge, 1920
	return []operations.ConfigurationValue{{ID: "metadata.artworkSize", Key: "metadata.artwork_max_edge", Current: &current, Effective: &current, Default: &factory, Source: source, ApplicationMode: "next-operation"}}, nil
}
func artworkConfigurationEdge(value any) (int, bool) {
	var edge int
	switch n := value.(type) {
	case int:
		edge = n
	case int64:
		if n != 1280 && n != 1920 && n != 2560 {
			return 0, false
		}
		edge = int(n)
	case float64:
		if n != 1280 && n != 1920 && n != 2560 {
			return 0, false
		}
		edge = int(n)
	default:
		return 0, false
	}
	return edge, validArtworkEdge(edge)
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
	b := d.Bindings()[0]
	field := d.SourceField(b.Field)
	value, present := raw[b.Key]
	removed := !present && sources[field] == "file"
	if removed {
		value, present = 1920, true
	}
	if !present {
		return nil, nil
	}
	edge, valid := artworkConfigurationEdge(value)
	if !valid {
		return nil, fmt.Errorf("invalid config key %s", b.Key)
	}
	current, err := ReadMetadataSettingsTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	if current.ArtworkMaxEdge == edge && sources[field] == "file" && !removed {
		return nil, nil
	}
	update := MetadataSettingsUpdate{ExpectedRevision: current.Revision, LocalArtworkLimitBytes: current.LocalArtworkLimitBytes, IncludeAdult: current.IncludeAdult, ArtworkMaxEdge: &edge}
	if current.ArtworkMaxEdge != edge && !validMetadataSettingsUpdate(update) {
		return nil, ErrRepairInput
	}
	return func() error {
		if current.ArtworkMaxEdge != edge {
			_, err = ApplyMetadataSettingsTx(ctx, tx, update, time.Time{})
			if err != nil {
				return err
			}
		}
		if removed {
			_, err = tx.ExecContext(ctx, `DELETE FROM server_setting_sources WHERE field=?`, field)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO server_setting_sources VALUES(?,'file') ON CONFLICT(field) DO UPDATE SET source=excluded.source`, field)
		}
		return err
	}, nil
}
func (d Configuration) Projection(ctx context.Context, tx *sql.Tx, sources map[string]string) (map[string]any, error) {
	out := map[string]any{}
	if sources[d.SourceField("artworkMaxEdge")] == "" {
		return out, nil
	}
	v, err := ReadMetadataSettingsTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	out["metadata.artwork_max_edge"] = v.ArtworkMaxEdge
	return out, nil
}
func (d Configuration) queueChange(ctx context.Context, tx *sql.Tx, before, after MetadataSettings) error {
	if before.ArtworkMaxEdge == after.ArtworkMaxEdge {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE console_settings_revision SET revision=revision+1 WHERE singleton=1`); err != nil {
		return err
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM console_settings_revision WHERE singleton=1`).Scan(&revision); err != nil {
		return err
	}
	field := d.SourceField("artworkMaxEdge")
	if _, err := tx.ExecContext(ctx, `INSERT INTO server_setting_sources VALUES(?,'server') ON CONFLICT(field) DO UPDATE SET source=excluded.source`, field); err != nil {
		return err
	}
	return operations.QueueConfigurationProjectionTx(ctx, tx, field, revision)
}
