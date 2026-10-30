package catalog

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
)

// CommitInventoryLocalRefresh publishes a file's re-read local metadata
// and subtitle inventory after only its sidecars changed (S6). The file's
// technical facts and streams are this revision's already and remain untouched.
// Audio chapters, tags and grouping are rebuilt from the file-owned baseline
// and current sidecars; video naming is untouched. This publishes under the
// same fences as CommitInventoryAnalysis and settles the analysis queue row the same way. f.AnalysisOperations is the
// object's complete operation list after the refresh.
func (s *Service) commitInventoryLocalRefreshApplied(ctx context.Context, job, object, revision string, policyRevision int64, f assets.Facts) error {
	ctx, audioAdmissions := withAudioMergeAdmissions(ctx)
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	source, err := InventoryFenceTx(ctx, tx, job)
	if err != nil {
		return err
	}
	policy, err := ScanPolicyTx(ctx, tx, source.LibraryID)
	if err != nil {
		return err
	}
	if policy.Revision != policyRevision || !policy.Allows("probe") {
		return context.Canceled
	}
	// The stored probe evidence is reused only for the revision it was taken
	// from: the object must still be that revision and its analysis must be of it.
	var asset, relative string
	var size, modified int64
	err = tx.QueryRowContext(ctx, `SELECT asset_id,relative_path,size,modified_ns FROM inventory_objects WHERE id=? AND source_id=? AND revision=? AND analysis_revision=? AND retired=0 AND state='available'`, object, source.ID, revision, revision).Scan(&asset, &relative, &size, &modified)
	if err != nil {
		return err
	}
	var kind string
	if err = tx.QueryRowContext(ctx, `SELECT kind FROM libraries WHERE id=?`, source.LibraryID).Scan(&kind); err != nil {
		return err
	}
	audio := kind == "music" || kind == "audiobook"
	if !audio && kind != "movie" && kind != "tv" && kind != "anime" {
		return errors.New("unsupported local refresh library")
	}
	if audio {
		if err = dbwork.UsePlan(tx, audioFilePublicationPlan); err != nil {
			return err
		}
		// Baseline presence, asset stat and analysis revision are checked again in
		// the writer; an earlier read never authorizes publication on its own.
		var current bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM audio_file_metadata b JOIN catalog_assets a ON a.token=b.asset_id AND a.size=b.size AND a.modified_ns=b.modified_ns WHERE b.asset_id=? AND b.source_revision=? AND b.size=? AND b.modified_ns=?)`, asset, revision, size, modified).Scan(&current); err != nil {
			return err
		}
		if !current {
			return context.Canceled
		}
	}
	var sidecarObservation *sidecarRead
	if f.ArtworkObserved {
		ctx, sidecarObservation = withSidecarRead(ctx, asset)
	}
	f.ObservedRevision = revision
	if policy.Allows("local_metadata") {
		if audio {
			if err = s.commitInventoryAudioTx(ctx, tx, source, asset, kind, filepath.Join(source.ResolvedPath, relative), f); err != nil {
				return err
			}
		} else if err = localmetadata.PersistVideo(ctx, tx, source.LibraryID, asset, f); err != nil {
			return err
		}
	}
	if err = s.persistScanArtworkTx(ctx, tx, source, object, revision, asset, kind, policy, f, sidecarObservation); err != nil {
		return err
	}
	queueState := "done"
	if f.SubtitleTextPending {
		queueState = "pending"
	}
	for _, op := range DeepScanOperations {
		if policy.Allows(op) {
			queueState = "pending"
		}
	}
	analysisState := "complete"
	if queueState == "pending" {
		analysisState = "pending"
	}
	analyzed := revision
	if f.SubtitleTextPending {
		analyzed = ""
	}
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_objects SET analysis_revision=?,analysis_policy=?,analysis_operations_json=?,analysis_state=?,analysis_error='',analysis_generation=? WHERE id=?`, analyzed, policyRevision, actualScanOperations(policy, f.AnalysisOperations), analysisState, source.Generation, object); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_analysis_queue SET state=? WHERE job_id=? AND object_id=? AND revision=? AND policy_revision=?`, queueState, job, object, revision, policyRevision); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_runs SET analyzed=analyzed+1 WHERE job_id=?`, job); err != nil {
		return err
	}
	if err = inventoryEvent(ctx, tx, source.ID, job, object, asset, revision, "analysis_completed"); err != nil {
		return err
	}
	if err = finishAudioMergeAdmissionsTx(ctx, tx, audioAdmissions); err != nil {
		return err
	}
	return gated.Commit()
}
