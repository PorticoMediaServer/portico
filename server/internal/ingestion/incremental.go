package ingestion

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"time"
)

type scanModeKey struct{}

// QueueMode preserves the existing queue and authorization boundary. Empty mode
// chooses first import, weekly integrity, or incremental automatically.
func (s *Service) QueueMode(ctx context.Context, library, source, mode string, authorize func(*sql.Tx) error) (Job, error) {
	if mode != "" && mode != "incremental" && mode != "full" && mode != "integrity" {
		return Job{}, requestinput.New("invalid scan mode")
	}
	return s.QueueContext(context.WithValue(ctx, scanModeKey{}, mode), library, source, authorize)
}
func scanModeTx(ctx context.Context, tx *sql.Tx, source string) (string, error) {
	// Strict targets and explicit integration refreshes request incremental work.
	// Public QueueMode retains
	// its first-import/integrity selection, including for "incremental".
	strict, _ := ctx.Value(strictQueueKey{}).(bool)
	refresh, _ := ctx.Value(integrationRefreshKey{}).(bool)
	if strict || refresh {
		return "incremental", nil
	}
	mode, _ := ctx.Value(scanModeKey{}).(string)
	if mode == "full" || mode == "integrity" {
		return mode, nil
	}
	var complete, integrity string
	err := tx.QueryRowContext(ctx, `SELECT last_complete_at,last_integrity_at FROM library_sources s WHERE id=?`, source).Scan(&complete, &integrity)
	if err != nil {
		return "", err
	}
	if complete == "" {
		return "first-import", nil
	}
	checked, err := time.Parse(time.RFC3339Nano, integrity)
	if err != nil || time.Since(checked) >= 7*24*time.Hour {
		return "integrity", nil
	}
	return "incremental", nil
}
