package catalog

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

const SonicOperation = "sonic"

// SonicDefaultEnabled is the factory choice for a configurable Music library.
// It is independent of a saved choice, its CAS revision and engine readiness.
func SonicDefaultEnabled() bool { return true }

type SonicSettings struct {
	Enabled  bool  `json:"enabled"`
	Revision int64 `json:"revision"`
}

func SonicSettingsTx(ctx context.Context, tx *sql.Tx, library string) (SonicSettings, error) {
	if tx == nil {
		return SonicSettings{}, ErrAdminQuery
	}
	var kind string
	if err := tx.QueryRowContext(ctx, `SELECT kind FROM libraries WHERE id=?`, library).Scan(&kind); err != nil {
		return SonicSettings{}, err
	}
	if kind != "music" {
		return SonicSettings{}, ErrAdminQuery
	}
	settings := SonicSettings{Enabled: SonicDefaultEnabled(), Revision: 1}
	err := tx.QueryRowContext(ctx, `SELECT enabled,revision FROM library_sonic_settings WHERE library_id=?`, library).Scan(&settings.Enabled, &settings.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return settings, err
}

// UpdateSonicSettings uses the existing scan policy publication fence and
// durable pending work. It never starts an independent timer or worker.
func (s *Service) UpdateSonicSettings(ctx context.Context, library string, enabled bool, expected int64, authorize func(*sql.Tx) error) (SonicSettings, error) {
	if expected < 1 || authorize == nil {
		return SonicSettings{}, ErrAdminQuery
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassInteractive)
	if err != nil {
		return SonicSettings{}, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if authorize != nil {
		if err = authorize(tx); err != nil {
			return SonicSettings{}, err
		}
	}
	r, err := usermutation.CheckContext(ctx, tx, time.Now().UnixMilli())
	if err != nil {
		return SonicSettings{}, err
	}
	if r != nil {
		return SonicSettingsTx(ctx, tx, library)
	}
	settings, err := SonicSettingsTx(ctx, tx, library)
	if err != nil {
		return settings, err
	}
	if settings.Revision != expected {
		return settings, ErrLibraryConfigurationConflict
	}
	if settings.Enabled == enabled {
		if err = usermutation.SaveContext(ctx, tx, usermutation.Result{Status: "applied", TargetID: library, Revision: settings.Revision}, time.Now().UnixMilli()); err != nil {
			return settings, err
		}
		return settings, gated.Commit()
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO library_sonic_settings(library_id,enabled,revision) VALUES(?,?,?) ON CONFLICT(library_id) DO UPDATE SET enabled=excluded.enabled,revision=excluded.revision`, library, enabled, expected+1); err != nil {
		return settings, err
	}
	var scanRevision int64
	if err = tx.QueryRowContext(ctx, `UPDATE library_scan_policies SET revision=revision+1 WHERE library_id=? RETURNING revision`, library).Scan(&scanRevision); err != nil {
		return settings, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_policy_pending(library_id,revision) VALUES(?,?) ON CONFLICT(library_id) DO UPDATE SET revision=excluded.revision`, library, scanRevision); err != nil {
		return settings, err
	}
	if err = usermutation.SaveContext(ctx, tx, usermutation.Result{Status: "applied", TargetID: library, Revision: expected + 1}, time.Now().UnixMilli()); err != nil {
		return settings, err
	}
	if err = gated.Commit(); err != nil {
		return settings, err
	}
	return SonicSettings{Enabled: enabled, Revision: expected + 1}, nil
}
