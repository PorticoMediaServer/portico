package remotesources

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"time"
)

type AnalysisPolicy struct {
	LibraryID string `json:"libraryId"`
	Enabled   bool   `json:"enabled"`
	Revision  int64  `json:"revision"`
	Effective bool   `json:"effective"`
	ScanTier  string `json:"scanTier"`
}

func (s *Service) AnalysisPolicy(ctx context.Context, library string) (AnalysisPolicy, error) {
	p := AnalysisPolicy{LibraryID: library}
	var exists string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM libraries WHERE id=?`, library).Scan(&exists); err != nil {
		return p, err
	}
	err := s.db.QueryRowContext(ctx, `SELECT enabled,revision FROM source_strm_analysis WHERE library_id=?`, library).Scan(&p.Enabled, &p.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err == nil {
		err = s.analysisEffective(ctx, &p)
	}
	return p, err
}
func (s *Service) SetAnalysisPolicy(ctx context.Context, library string, enabled bool, revision int64, authorize func(*sql.Tx) error) (AnalysisPolicy, error) {
	p := AnalysisPolicy{LibraryID: library, Enabled: enabled, Revision: revision + 1}
	if revision < 0 {
		return p, mounts.ErrInvalid
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return p, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if authorize == nil {
		return p, mounts.ErrInvalid
	}
	if err = authorize(tx); err != nil {
		return p, err
	}
	r, err := usermutation.CheckContext(ctx, tx, time.Now().UnixMilli())
	if err != nil {
		return p, err
	}
	if r != nil {
		gated.Rollback()
		return s.AnalysisPolicy(ctx, library)
	}
	var current int64
	var active bool
	err = tx.QueryRowContext(ctx, `SELECT enabled,revision FROM source_strm_analysis WHERE library_id=?`, library).Scan(&active, &current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	if current != revision {
		// A lost response may be recovered without repeating an activation.
		_, _, protocol := usermutation.RequestFrom(ctx)
		if !protocol && current == revision+1 && active == enabled {
			gated.Rollback()
			return s.AnalysisPolicy(ctx, library)
		}
		return p, mounts.ErrCommandConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO source_strm_analysis VALUES(?,?,?) ON CONFLICT(library_id) DO UPDATE SET enabled=excluded.enabled,revision=excluded.revision`, library, enabled, p.Revision)
	if err != nil {
		return p, err
	}
	// The existing scan-policy handoff cancels/requeues bounded work. Its revision
	// also fences in-flight descriptor probes when the separate opt-in changes.
	if _, err = tx.ExecContext(ctx, `UPDATE library_scan_policies SET revision=revision+1 WHERE library_id=?`, library); err != nil {
		return p, err
	}
	if err = usermutation.SaveContext(ctx, tx, usermutation.Result{Status: "applied", TargetID: library, Revision: p.Revision}, time.Now().UnixMilli()); err != nil {
		return p, err
	}
	if err = gated.Commit(); err != nil {
		return p, err
	}
	return s.AnalysisPolicy(ctx, library)
}

func (s *Service) analysisEffective(ctx context.Context, p *AnalysisPolicy) error {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT tier,operations_json FROM library_scan_policies WHERE library_id=?`, p.LibraryID).Scan(&p.ScanTier, &raw); err != nil {
		return err
	}
	var ops []string
	if err := json.Unmarshal([]byte(raw), &ops); err != nil {
		return err
	}
	for _, op := range ops {
		if op == "probe" && p.ScanTier != "file_list_only" && p.Enabled {
			p.Effective = true
		}
	}
	return nil
}

// ViewerStatus is intentionally less diagnostic than the owner source object.
// It never includes origin, local path, credential state or helper configuration.
func (s *Service) ViewerStatus(ctx context.Context, path string) (string, error) {
	var analysis string
	err := s.db.QueryRowContext(ctx, `SELECT code FROM remote_analysis_status WHERE path=?`, path).Scan(&analysis)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "unavailable", err
	}
	analysisState := "ready"
	if analysis == "range_unsupported" {
		analysisState = "unsupported"
	}
	if analysis == "source_changed" {
		analysisState = "changed"
	}
	b, _, ok := s.find(path)
	if !ok {
		return analysisState, nil
	}
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return "unavailable", err
	}
	unqualified, err := unqualifiedWebDAVTx(ctx, snapshot.Tx(), b.ID)
	snapshot.Rollback()
	if err != nil {
		return "unavailable", err
	}
	if unqualified {
		return "unsupported", nil
	}
	var state, code, generation string
	var removed bool
	if err := s.db.QueryRowContext(ctx, `SELECT state,error_code,removed,CASE WHEN kind IN('rclone','sftp','smb') THEN (SELECT CAST(m.generation AS TEXT) FROM mount_backend_configs m WHERE m.mount_id=remote_sources.mount_id) ELSE CAST(generation AS TEXT) END FROM remote_sources WHERE id=?`, b.ID).Scan(&state, &code, &removed, &generation); err != nil {
		return "unavailable", err
	}
	if removed {
		return "unavailable", nil
	}
	if code == "" && s.sourcePacerBackoff(b.ID, generation) {
		return "unavailable", nil
	}
	switch code {
	case "source_changed":
		return "changed", nil
	case "range_unsupported":
		return "unsupported", nil
	case "credentials_required", "binary_changed", "offline", "invalid_configuration", "rate_limited":
		return "unavailable", nil
	}
	// A configuration sample does not establish every file's range support.
	return analysisState, nil
}
