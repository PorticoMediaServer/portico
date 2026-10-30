package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
)

// Once this asset's complete holder set is pre-fenced, an OLD observation
// that physically belongs elsewhere is no longer a readable obligation. Release
// that one pin before copying NEW, so two assets exchanging observations cannot
// wait forever on one another's OLD pins. A same-owner revision change retains
// the accepted tree; readers cannot observe this release during phase 2.
func releaseInfoRepointedOwnerTx(ctx context.Context, tx *sql.Tx, change int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE catalog_assets SET info_owner_object=NULL,info_owner_revision=NULL
 WHERE id=(SELECT asset_id FROM catalog_info_changes WHERE id=? AND phase=2)
 AND info_owner_object IS NOT NULL
 AND NOT EXISTS(SELECT 1 FROM inventory_objects o WHERE o.id=catalog_assets.info_owner_object AND o.asset_id=catalog_assets.token)`, change)
	if err != nil {
		return err
	}
	return releaseInfoRepointedTargetTx(ctx, tx, change)
}

// A different owner can also have been selected independently of the probe's
// own witness. Release a moved target before another asset waits on it, and
// recheck at activation so an old header never re-pins a replacement object.
func releaseInfoRepointedTargetTx(ctx context.Context, tx *sql.Tx, change int64) error {
	var object sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT c.target_owner_object FROM catalog_info_changes c
 WHERE c.id=? AND c.phase IN(2,3) AND c.target_owner_object IS NOT NULL
 AND NOT EXISTS(SELECT 1 FROM inventory_objects o WHERE o.id=c.target_owner_object AND o.asset_id=c.asset_token)`, change).Scan(&object)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = markInfoDiscRerunTx(ctx, tx, object.String); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET target_owner_object=NULL,target_owner_revision=NULL WHERE id=? AND phase IN(2,3) AND target_owner_object=?`, change, object.String)
	return err
}

// A physical observation is shared source custody, not an asset generation.
// Do not overwrite its accepted tree while another asset still pins that tree.
// Existing source41 owners release those old pins through their finite passes.
func infoCopyDiscCustodyTx(ctx context.Context, tx *sql.Tx, change int64, token, object string) (live, blocked bool, err error) {
	if object == "" {
		return true, false, nil
	}
	var actual string
	e := tx.QueryRowContext(ctx, `SELECT asset_id FROM inventory_objects WHERE id=?`, object).Scan(&actual)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return false, false, e
	}
	if errors.Is(e, sql.ErrNoRows) || actual != token {
		// The accepted receipt survives; only the vanished object obligation is
		// discharged. Release its NEW target, retaining OLD until normal activation.
		var remaining sql.NullString
		if e = tx.QueryRowContext(ctx, `SELECT target_owner_object FROM catalog_info_changes WHERE id=?`, change).Scan(&remaining); e != nil {
			return false, false, e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET target_owner_object=NULL,target_owner_revision=NULL WHERE id=? AND target_owner_object=?`, change, object); e != nil {
			return false, false, e
		}
		if e = markInfoDiscRerunTx(ctx, tx, object); e != nil {
			return false, false, e
		}
		// A different selected observation can still serve the original item.
		// Its existing producer must also re-probe after losing this input's
		// disc obligation; that is at most one further owner point.
		if remaining.Valid && remaining.String != object {
			if e = markInfoDiscRerunTx(ctx, tx, remaining.String); e != nil {
				return false, false, e
			}
		}
		return false, false, nil
	}
	var prior int64
	var priorToken string
	e = tx.QueryRowContext(ctx, `SELECT id,token FROM catalog_assets INDEXED BY catalog_info_owner_object WHERE info_owner_object=? AND token<>? LIMIT 1`, object, token).Scan(&prior, &priorToken)
	if errors.Is(e, sql.ErrNoRows) {
		e = tx.QueryRowContext(ctx, `SELECT asset_id,asset_token FROM catalog_info_changes INDEXED BY catalog_info_target_owner_object WHERE target_owner_object=? AND asset_token<>? AND phase<5 LIMIT 1`, object, token).Scan(&prior, &priorToken)
	}
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return false, false, e
	}
	if e == nil {
		var queued bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_upstream_sources WHERE domain=41 AND source_id=? AND token=? AND flags&32<>0)`, prior, priorToken).Scan(&queued); e != nil {
			return false, false, e
		}
		if !queued {
			e = queueSource(ctx, tx, DomainAssetSources, prior, priorToken, sourceInfo|sourceAvailability|sourceMissing)
		}
		return true, true, e
	}
	return true, false, nil
}

// Use the existing per-observation rerun state and, when present, its one live
// scan job. Both are point writes. No new job, timeout or persisted reader work.
func markInfoDiscRerunTx(ctx context.Context, tx *sql.Tx, object string) error {
	if _, e := tx.ExecContext(ctx, `UPDATE inventory_objects SET analysis_revision='',analysis_policy=0,analysis_operations_json='[]',analysis_state='pending' WHERE id=? AND (analysis_revision<>'' OR analysis_policy<>0 OR analysis_operations_json<>'[]' OR analysis_state<>'pending')`, object); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO inventory_analysis_queue(job_id,object_id,revision,policy_revision)
 SELECT active.job_id,o.id,o.revision,COALESCE(p.revision,1)
 FROM inventory_objects o JOIN inventory_source_active active ON active.source_id=o.source_id
 JOIN jobs j ON j.id=active.job_id AND j.status='running'
 JOIN library_sources s ON s.id=o.source_id AND s.incarnation=o.root_incarnation
 LEFT JOIN library_scan_policies p ON p.library_id=s.library_id
 WHERE o.id=? AND o.retired=0 AND o.state='available'
 ON CONFLICT(job_id,object_id) DO UPDATE SET revision=excluded.revision,policy_revision=excluded.policy_revision,state='pending'
 WHERE inventory_analysis_queue.revision<>excluded.revision OR inventory_analysis_queue.policy_revision<>excluded.policy_revision OR inventory_analysis_queue.state<>'pending'`, object)
	return e
}
