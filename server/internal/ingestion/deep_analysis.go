package ingestion

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
)

// Deep analysis decodes the file one or more times, so its time budget follows
// the file: a floor for start-up and small files, plus whichever is larger of a
// pass over its bytes at a slow-storage rate and a few passes over its running
// time. The ceiling still bounds a stalled or hostile input.
const (
	deepAnalysisFloor        = 10 * time.Minute
	deepAnalysisCeiling      = 24 * time.Hour
	deepAnalysisBytesPerSec  = 8 << 20 // slow network storage
	deepAnalysisRealtimeRate = 3       // passes over the running time
)

func deepAnalysisBudget(size int64, durationSeconds float64) time.Duration {
	byBytes := time.Duration(0)
	if size > 0 {
		byBytes = time.Duration(size/deepAnalysisBytesPerSec) * time.Second
	}
	byTime := time.Duration(0)
	if durationSeconds > 0 && durationSeconds < float64(deepAnalysisCeiling/time.Second) {
		byTime = time.Duration(durationSeconds*deepAnalysisRealtimeRate) * time.Second
	}
	return min(deepAnalysisCeiling, deepAnalysisFloor+max(byBytes, byTime))
}

func (s *Service) analyzeDeep(parent context.Context, job, object, revision string, policy catalog.ScanPolicy, source catalog.LibrarySource) error {
	if s.DeepAnalysis == nil {
		if policy.Allows(catalog.EpisodeThumbnailOperation) {
			return nil
		}
		return s.analysisOutcome(parent, job, object, revision, policy.Revision, true, "analysis_worker_unavailable")
	}
	var relative string
	var size int64
	var duration float64
	if err := dbwork.ReadHandle(parent, s.db).QueryRowContext(parent,
		`SELECT o.relative_path,o.size,COALESCE(a.duration,0) FROM inventory_objects o LEFT JOIN catalog_assets a ON a.token=o.asset_id WHERE o.id=?`, object).Scan(&relative, &size, &duration); err != nil && err != sql.ErrNoRows {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, deepAnalysisBudget(size, duration))
	defer cancel()
	stopped := make(chan struct{})
	watchDone := make(chan struct{})
	defer func() { close(stopped); cancel(); <-watchDone }()
	supervise.Go("ingestion.deep-analysis-watch", func() {
		defer close(watchDone)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopped:
				return
			case <-ticker.C:
				var state string
				if s.db.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id=?`, job).Scan(&state) != nil || state != "running" {
					cancel()
					return
				}
				current, e := s.catalog.ScanPolicy(ctx, source.LibraryID)
				if e != nil || current.Revision != policy.Revision {
					cancel()
					return
				}
				allowed, e := s.DeepAnalysis.ContinueAdmission(ctx, job, object)
				if e != nil || !allowed {
					cancel()
					return
				}

			}
		}
	})
	clearActivityFile := s.activityFile(ctx, job, relative)
	defer clearActivityFile()
	done, warning, err := s.DeepAnalysis.RunOne(ctx, mediaanalysis.Request{JobID: job, ObjectID: object, SourceRevision: revision, PolicyRevision: policy.Revision})
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return nil
	}
	if err != nil {
		return err
	}
	if done {
		if policy.Allows(catalog.EpisodeThumbnailOperation) {
			if s.EpisodeThumbnails == nil {
				return nil
			}
			episodeDone, episodeWarning, e := s.EpisodeThumbnails(ctx, job, source.ID, object, revision, policy.Revision)
			if e != nil {
				return e
			}
			if !episodeDone {
				return nil
			}
			if episodeWarning != "" {
				warning = episodeWarning
			}
		}
		return s.analysisOutcome(parent, job, object, revision, policy.Revision, warning != "", warning)
	}
	return nil
}

// QueueItemAnalysis uses the existing durable job, source deduplication and
// worker. It never broadens the saved Complete/Custom policy. An in-flight scan
// is coalesced rather than replaced or silently cancelled.
func (s *Service) QueueItemAnalysis(ctx context.Context, library, asset, revision string, authorize func(*sql.Tx) error) (Job, error) {
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return Job{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if authorize != nil {
		if e = authorize(tx); e != nil {
			return Job{}, e
		}
	}
	var source, object, relative string
	e = tx.QueryRowContext(ctx, `SELECT o.source_id,o.id,o.relative_path FROM inventory_objects o JOIN library_sources src ON src.id=o.source_id WHERE o.asset_id=? AND o.revision=? AND src.library_id=? AND src.enabled=1 AND o.retired=0 AND o.state='available' AND o.root_incarnation=src.incarnation ORDER BY o.id LIMIT 1`, asset, revision, library).Scan(&source, &object, &relative)
	if e != nil {
		return Job{}, e
	}
	p, e := catalog.ScanPolicyTx(ctx, tx, library)
	if e != nil {
		return Job{}, e
	}
	allowed := false
	for _, op := range catalog.DeepScanOperations {
		allowed = allowed || p.Allows(op)
	}
	if !allowed {
		return Job{}, catalog.ErrAdminQuery
	}
	// Refuse before creating/coalescing a job or resetting any stage. The
	// executing worker repeats this original source gate for bytes/publication;
	// a manual action cannot accept work that the source has not opted into.
	consent, e := catalog.SourceAnalysisConsentTx(ctx, tx, source)
	if e != nil {
		return Job{}, e
	}
	if !consent.Allowed || (consent.Remote && assets.DiscImage(relative)) {
		// Remote disc bytes are fetched only on Play. This origin enqueue has
		// no independently admitted receipt for bytes already owned by playback.
		return Job{}, mediaanalysis.ErrUnsupported
	}
	j, e := queueSourceTx(ctx, tx, source, true)
	if e != nil {
		return Job{}, e
	}
	// Coalescing with an active source job must not lose an item that its enqueue
	// cursor already passed. Retry resets only failed attempts for this explicit
	// owner request; completed immutable stages remain reusable.
	if _, e = tx.ExecContext(ctx, `INSERT INTO inventory_analysis_queue(job_id,object_id,revision,policy_revision,state) VALUES(?,?,?,?,'pending') ON CONFLICT(job_id,object_id) DO UPDATE SET revision=excluded.revision,policy_revision=excluded.policy_revision,state='pending'`, j.ID, object, revision, p.Revision); e != nil {
		return Job{}, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE analysis_stage_runs SET state='pending',attempt=0,error_code='',next_ms=0 WHERE job_id=? AND object_id=? AND source_revision=? AND state IN('failed','unsupported')`, j.ID, object, revision); e != nil {
		return Job{}, e
	}
	// Analysis enqueue examines the whole source in bounded pages, but executes
	// only missing permitted stages. This is explicit in the owner UI.
	if e = gated.Commit(); e != nil {
		return Job{}, e
	}
	return j, nil
}
