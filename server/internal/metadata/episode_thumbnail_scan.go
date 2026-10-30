package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

const episodeThumbnailAlgorithm = "portico-episode-thumbnail-1"

// ScanEpisodeThumbnails is one quantum of the existing inventory job. A shared
// video can own several logical episodes; at most one picture is decoded in a
// quantum. Actual selected art makes successful publication restart-safe.
func (s *Service) ScanEpisodeThumbnails(ctx context.Context, job, sourceID, object, revision string, policyRevision int64) (bool, string, error) {
	g, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return false, "", err
	}
	defer g.Rollback()
	tx := g.Tx()
	source, err := catalog.InventoryFenceTx(ctx, tx, job)
	if err != nil {
		return false, "", err
	}
	if source.ID != sourceID {
		return false, "", ErrRepairConflict
	}
	p, err := catalog.ScanPolicyTx(ctx, tx, source.LibraryID)
	if err != nil {
		return false, "", err
	}
	if p.Revision != policyRevision {
		return false, "", context.Canceled
	}
	if !p.Allows(catalog.EpisodeThumbnailOperation) {
		return true, "", nil
	}
	var asset, basic string
	err = tx.QueryRowContext(ctx, `SELECT o.asset_id,o.analysis_revision FROM inventory_objects o JOIN catalog_assets a ON a.token=o.asset_id WHERE o.id=? AND o.source_id=? AND o.revision=? AND o.retired=0 AND o.state='available' AND o.root_incarnation=? AND a.available=1`, object, sourceID, revision, source.Incarnation).Scan(&asset, &basic)
	if err != nil {
		return false, "", err
	}
	var state, warning string
	var next int64
	err = tx.QueryRowContext(ctx, `SELECT state,error_code,next_ms FROM analysis_stage_runs WHERE job_id=? AND object_id=? AND source_revision=? AND policy_revision=? AND stage=? AND algorithm=?`, job, object, revision, policyRevision, catalog.EpisodeThumbnailOperation, episodeThumbnailAlgorithm).Scan(&state, &warning, &next)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, "", err
	}
	if state == "complete" || state == "unsupported" || state == "failed" {
		return true, warning, nil
	}
	if next > s.publicationTime().UnixMilli() {
		return false, "", nil
	}
	mode, err := catalog.EpisodeThumbnailPolicyTx(ctx, tx, source.LibraryID)
	if err != nil {
		return false, "", err
	}
	var captured *episodeThumbnailCapture
	if mode.Mode == "provider_then_made" {
		rows, err := tx.QueryContext(ctx, `SELECT pid(e.public_id) FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id JOIN catalog_entities e ON e.id=l.entity_id JOIN catalog_libraries cl ON cl.id=e.library_id WHERE a.token=? AND e.kind=4 AND cl.library_id=? ORDER BY l.part_index,e.id`, asset, source.LibraryID)
		if err != nil {
			return false, "", err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return false, "", err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return false, "", err
		}
		for _, id := range ids {
			facts, err := s.episodeThumbnailArtworkStateTx(ctx, tx, source.LibraryID, id)
			if err != nil {
				return false, "", err
			}
			if !facts.GenerationNeeded {
				continue
			}
			if !facts.ProviderResolved {
				return false, "", nil
			}
			if facts.ProviderStill {
				continue
			}
			c, err := captureEpisodeThumbnailTx(ctx, tx, RepairTarget{"item", id}, sourceID, object, revision)
			if err != nil {
				return false, "", err
			}
			captured = &c
			break
		}
	}
	if captured == nil {
		if err = g.Commit(); err != nil {
			return false, "", err
		}
		return s.finishEpisodeThumbnailScan(ctx, job, sourceID, object, revision, policyRevision, "")
	}
	if basic != revision {
		return false, "", ErrRepairConflict
	}
	runtime := s.EpisodeThumbnails
	if !runtime.ready() || s.Admission == nil {
		return false, "", nil
	}
	allowed, err := s.Admission(ctx, tx, "analysis")
	if err != nil || !allowed {
		return false, "", err
	}
	supported, err := runtime.Sources.EpisodeRangesSupportedTx(ctx, tx, captured.sourceTarget())
	if err != nil {
		return false, "", err
	}
	if !supported {
		if err = g.Commit(); err != nil {
			return false, "", err
		}
		return s.finishEpisodeThumbnailScan(ctx, job, sourceID, object, revision, policyRevision, "decoder_or_stream_unsupported")
	}
	input, err := runtime.Sources.ResolveEpisodeThumbnailTx(ctx, tx, EpisodeThumbnailInvocation{Target: captured.sourceTarget(), Purpose: EpisodeThumbnailAutomatic, Operation: catalog.EpisodeThumbnailOperation, CostClass: mediaanalysis.EpisodeThumbnailCostClass, JobID: job})
	if err != nil {
		if errors.Is(err, storage.ErrBusy) {
			return false, "", nil
		}
		return false, "", err
	}
	if input == nil {
		// The adapter may return this only for the owned typed positive
		// automatic source-tier exclusion. It can never authorize a decode.
		if err = g.Commit(); err != nil {
			return false, "", err
		}
		return s.finishEpisodeThumbnailScan(ctx, job, sourceID, object, revision, policyRevision, "")
	}
	defer input.Close()
	if err = g.Commit(); err != nil {
		return false, "", err
	}
	_, err = s.generateEpisodeThumbnail(ctx, *captured, mode.Mode, false, input, MBActor{}, nil)
	if err == nil {
		return false, "", nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, storage.ErrBusy) || errors.Is(err, ErrRepairConflict) {
		return false, "", nil
	}
	return s.episodeThumbnailScanFailure(ctx, job, sourceID, object, revision, policyRevision, err)
}

func (s *Service) finishEpisodeThumbnailScan(ctx context.Context, job, sourceID, object, revision string, policyRevision int64, warning string) (bool, string, error) {
	err := s.writeEpisodeThumbnailScan(ctx, job, sourceID, object, revision, policyRevision, func(tx *sql.Tx) error {
		state := "complete"
		if warning != "" {
			state = "unsupported"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO analysis_stage_runs(job_id,object_id,source_revision,policy_revision,stage,algorithm,state,attempt,error_code,updated_ms) VALUES(?,?,?,?,?,?,?,0,?,?) ON CONFLICT(job_id,object_id,source_revision,policy_revision,stage,algorithm) DO UPDATE SET state=excluded.state,error_code=excluded.error_code,next_ms=0,updated_ms=excluded.updated_ms`, job, object, revision, policyRevision, catalog.EpisodeThumbnailOperation, episodeThumbnailAlgorithm, state, warning, s.publicationTime().UnixMilli()); err != nil {
			return err
		}
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT analysis_operations_json FROM inventory_objects WHERE id=? AND source_id=? AND revision=?`, object, sourceID, revision).Scan(&raw); err != nil {
			return err
		}
		var ops []string
		if err := json.Unmarshal([]byte(raw), &ops); err != nil {
			return err
		}
		for _, op := range ops {
			if op == catalog.EpisodeThumbnailOperation {
				return nil
			}
		}
		ops = append(ops, catalog.EpisodeThumbnailOperation)
		data, err := json.Marshal(ops)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE inventory_objects SET analysis_operations_json=? WHERE id=? AND source_id=? AND revision=?`, string(data), object, sourceID, revision)
		return err
	})
	return err == nil, warning, err
}

func (s *Service) writeEpisodeThumbnailScan(ctx context.Context, job, sourceID, object, revision string, policyRevision int64, write func(*sql.Tx) error) error {
	g, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	defer g.Rollback()
	source, err := catalog.InventoryFenceTx(ctx, g.Tx(), job)
	if err != nil {
		return err
	}
	if source.ID != sourceID {
		return ErrRepairConflict
	}
	p, err := catalog.ScanPolicyTx(ctx, g.Tx(), source.LibraryID)
	if err != nil {
		return err
	}
	if p.Revision != policyRevision || !p.Allows(catalog.EpisodeThumbnailOperation) {
		return context.Canceled
	}
	var current bool
	if err = g.Tx().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_objects WHERE id=? AND source_id=? AND revision=? AND retired=0 AND state='available' AND root_incarnation=?)`, object, sourceID, revision, source.Incarnation).Scan(&current); err != nil {
		return err
	}
	if !current {
		return ErrRepairConflict
	}
	if err = write(g.Tx()); err != nil {
		return err
	}
	return g.Commit()
}

func (s *Service) episodeThumbnailScanFailure(ctx context.Context, job, sourceID, object, revision string, policyRevision int64, cause error) (bool, string, error) {
	if errors.Is(cause, mediaanalysis.ErrUnsupported) {
		return s.finishEpisodeThumbnailScan(ctx, job, sourceID, object, revision, policyRevision, "decoder_or_stream_unsupported")
	}
	err := s.writeEpisodeThumbnailScan(ctx, job, sourceID, object, revision, policyRevision, func(tx *sql.Tx) error {
		code := "analysis_failed"
		if errors.Is(cause, mediaanalysis.ErrBudget) {
			code = "analysis_budget_exceeded"
		}
		if errors.Is(cause, context.DeadlineExceeded) {
			code = "analysis_time_budget_exceeded"
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO analysis_stage_runs(job_id,object_id,source_revision,policy_revision,stage,algorithm,state,attempt,error_code,next_ms,updated_ms) VALUES(?,?,?,?,?,?,'retry_wait',1,?,?,?) ON CONFLICT(job_id,object_id,source_revision,policy_revision,stage,algorithm) DO UPDATE SET state=CASE WHEN attempt>=2 THEN 'failed' ELSE 'retry_wait' END,attempt=attempt+1,error_code=excluded.error_code,next_ms=excluded.updated_ms+MIN(300000,1000*(1<<MIN(attempt+1,8))),updated_ms=excluded.updated_ms`, job, object, revision, policyRevision, catalog.EpisodeThumbnailOperation, episodeThumbnailAlgorithm, code, s.publicationTime().Add(2*time.Second).UnixMilli(), s.publicationTime().UnixMilli())
		return err
	})
	return false, "", err
}
