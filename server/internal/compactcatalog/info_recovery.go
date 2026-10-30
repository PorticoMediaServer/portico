package compactcatalog

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
)

const infoReclaim = "unaccepted-reclaim"
const infoFrozen = "old-frozen"

// AbandonInfoInputTx cancels only the exact unaccepted body. Accepted inputs
// always finish. Bumping the input revision prevents a canceled producer from
// resuming staging while bounded reclamation walks its NEW rows.
func AbandonInfoInputTx(ctx context.Context, tx *sql.Tx, ref InfoInputRef) (bool, error) {
	r, e := tx.ExecContext(ctx, `UPDATE catalog_info_changes SET fault='',copy_aux=-1,input_revision=input_revision+1,copy_family=0
 WHERE id=? AND asset_id=? AND asset_token=? AND slot=? AND input_revision=? AND phase=0 AND fault='' AND copy_aux>=0`, ref.ChangeID, ref.AssetID, ref.Token, ref.Slot, ref.InputRevision)
	if e != nil {
		return false, e
	}
	n, e := r.RowsAffected()
	return n == 1, e
}

// Raw scalar ingress can advance input_revision while retaining this exact
// producer's immutable witness. Prove that ownership again before refreshing
// only the CAS revision; a new witness, fingerprint, slot or phase is never
// abandoned by an older producer's cleanup.
func abandonInfoWitnessTx(ctx context.Context, tx *sql.Tx, ref InfoInputRef, witness InfoWitness, fingerprint [32]byte) (bool, error) {
	h, e := infoInputHeaderByID(ctx, tx, ref.ChangeID)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if h.phase != 0 || h.assetID != ref.AssetID || h.token != ref.Token || h.slot != ref.Slot || !bytes.Equal(h.fingerprint, fingerprint[:]) {
		return false, nil
	}
	w, e := infoInputWitnessAt(ctx, tx, h.changeID, 0)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if w != witness {
		return false, nil
	}
	return AbandonInfoInputTx(ctx, tx, h.ref())
}

// A running exact witness is never timed out. These primary-key probes prove
// that the producer's job/object/source/configuration was actually replaced.
func infoWitnessDeadTx(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	var dead bool
	e := tx.QueryRowContext(ctx, `SELECT CASE WHEN w.kind IN(0,3,4) THEN NOT EXISTS(
 SELECT 1 FROM jobs j JOIN inventory_runs r ON r.job_id=j.id
 JOIN library_sources s ON s.id=r.source_id
 JOIN inventory_objects o ON o.id=w.object_id
 LEFT JOIN library_scan_policies p ON p.library_id=s.library_id
 WHERE j.id=w.job_id AND j.status='running' AND s.id=w.source_id AND s.enabled=1
 AND s.generation=w.source_generation AND s.incarnation=w.root_incarnation
 AND r.source_generation=s.generation AND r.root_incarnation=s.incarnation AND r.root_identity=s.root_identity
 AND o.source_id=s.id AND o.root_incarnation=s.incarnation AND o.revision=w.object_revision
 AND o.retired=0 AND o.state='available' AND COALESCE(p.revision,1)=w.policy_revision
 ) ELSE 0 END FROM catalog_info_input_witnesses w WHERE w.change_id=? AND w.ordinal=0`, id).Scan(&dead)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	return dead, e
}

// recoverInfoInputTx returns handled=true only for a canceled/frozen phase0.
// It performs at most eight raw source-row removals or OLD transfers per call.
func recoverInfoInputTx(ctx context.Context, tx *sql.Tx, c infoChange) (handled, done bool, err error) {
	var fault string
	if err = tx.QueryRowContext(ctx, `SELECT CASE copy_aux WHEN -1 THEN 'unaccepted-reclaim' WHEN -2 THEN 'old-frozen' ELSE fault END FROM catalog_info_changes WHERE id=?`, c.id).Scan(&fault); err != nil {
		return
	}
	if fault == "" {
		var dead bool
		dead, err = infoWitnessDeadTx(ctx, tx, c.id)
		if err == nil && !dead {
			// A newer independently witnessed accepted input supersedes only the
			// physical producer's still-unaccepted predecessor, never an accepted
			// receipt. The old producer loses its revision CAS and may retry.
			err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_info_input_witnesses old JOIN catalog_info_changes newer ON newer.asset_id=? AND newer.asset_token=? AND newer.slot=1 AND newer.phase=1 JOIN catalog_info_input_witnesses next ON next.change_id=newer.id AND next.ordinal=0 WHERE old.change_id=? AND old.ordinal=0 AND old.kind IN(1,2) AND (old.kind<>next.kind OR old.fingerprint<>next.fingerprint OR old.object_id<>next.object_id OR old.object_revision<>next.object_revision OR old.job_id<>next.job_id))`, c.asset, c.token, c.id).Scan(&dead)
		}
		if err != nil || !dead {
			return
		}
		_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET fault='',copy_aux=-1,input_revision=input_revision+1,copy_family=0 WHERE id=? AND phase=0 AND fault='' AND copy_aux>=0`, c.id)
		if err != nil {
			return
		}
		fault = infoReclaim
	}
	if fault != infoReclaim && fault != infoFrozen {
		return false, false, nil
	}
	handled = true
	if fault == infoFrozen {
		return true, true, nil
	}
	// Reclaim NEW children, manifests and witnesses, but never the first OLD.
	var phase int
	if err = tx.QueryRowContext(ctx, `SELECT copy_family FROM catalog_info_changes WHERE id=?`, c.id).Scan(&phase); err != nil {
		return
	}
	for phase < len(infoRetireTables) && (infoRetireTables[phase] == "catalog_info_scalar_old" || infoRetireTables[phase] == "catalog_info_path_old") {
		phase++
	}
	if phase < len(infoRetireTables) {
		table := infoRetireTables[phase]
		keys := infoRetirePrimaryKeys[table]
		var r sql.Result
		r, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE (`+keys+`) IN(SELECT `+keys+` FROM `+table+` WHERE change_id=? ORDER BY `+keys+` LIMIT 8)`, c.id)
		if err != nil {
			return
		}
		var n int64
		n, err = r.RowsAffected()
		if err != nil {
			return
		}
		if n == 0 {
			phase++
		}
		_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET copy_family=? WHERE id=?`, phase, c.id)
		return
	}
	// A queued accepted input can inherit the original frame without consuming
	// the abandoned epoch. A live unfinished successor is allowed to finish its
	// own producer; it is never accepted by the recovery worker.
	q, e := infoInputHeaderBySlot(ctx, tx, c.asset, c.token, 1)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		err = e
		return
	}
	if e == nil {
		if q.phase == 0 {
			qc := infoChange{id: q.changeID, asset: q.assetID, token: q.token, phase: 0, expected: q.expectedEpoch}
			var qfault string
			if err = tx.QueryRowContext(ctx, `SELECT CASE copy_aux WHEN -1 THEN 'unaccepted-reclaim' WHEN -2 THEN 'old-frozen' ELSE fault END FROM catalog_info_changes WHERE id=?`, q.changeID).Scan(&qfault); err != nil {
				return
			}
			dead, de := infoWitnessDeadTx(ctx, tx, q.changeID)
			if de != nil {
				err = de
				return
			}
			if qfault == infoReclaim || qfault == infoFrozen || dead {
				if qfault != infoFrozen {
					// Queued recovery must not recursively discover itself as successor.
					ready, re := reclaimQueuedInfoTx(ctx, tx, qc)
					err = re
					if err != nil || !ready {
						return
					}
				}
				var moved bool
				moved, err = transferInfoOldTx(ctx, tx, q.changeID, c.id, false)
				if err != nil || !moved {
					return
				}
				_, err = tx.ExecContext(ctx, `DELETE FROM catalog_info_changes WHERE id=? AND phase=0`, q.changeID)
				return
			}
			return
		}
		if q.phase != 1 || q.expectedEpoch != c.expected+1 {
			err = ErrInfoInvariant
			return
		}
		var moved bool
		moved, err = transferInfoOldTx(ctx, tx, c.id, q.changeID, true)
		if err != nil || !moved {
			return
		}
		// Current asset epoch was checked by Advance before entering recovery.
		_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET expected_epoch=?,input_revision=input_revision+1 WHERE id=? AND phase=1`, c.expected, q.changeID)
		if err != nil {
			return
		}
		_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET phase=5,copy_family=? WHERE id=?`, len(infoRetireTables), c.id)
		return
	}
	var old bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_info_scalar_old WHERE change_id=?) OR EXISTS(SELECT 1 FROM catalog_info_path_old WHERE change_id=?)`, c.id, c.id).Scan(&old); err != nil {
		return
	}
	if old {
		var holders bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_asset_links INDEXED BY catalog_asset_links_asset WHERE asset_id=?)`, c.asset).Scan(&holders); err != nil {
			return
		}
		if !holders {
			_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET phase=5,copy_family=0 WHERE id=?`, c.id)
			return
		}
		_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET fault='',family_mask=0,fingerprint=zeroblob(32),copy_family=0,copy_phase=0,copy_id=0,copy_aux=-2,copy_key='',copy_key2='',copy_key3='',target_available=NULL,target_shape_id=NULL,target_owner_object=NULL,target_owner_revision=NULL,input_revision=input_revision+1 WHERE id=?`, c.id)
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_info_changes WHERE id=? AND phase=0`, c.id)
	}
	done = err == nil
	return
}

func reclaimQueuedInfoTx(ctx context.Context, tx *sql.Tx, c infoChange) (bool, error) {
	var phase int
	if err := tx.QueryRowContext(ctx, `SELECT copy_family FROM catalog_info_changes WHERE id=?`, c.id).Scan(&phase); err != nil {
		return false, err
	}
	for phase < len(infoRetireTables) && (infoRetireTables[phase] == "catalog_info_scalar_old" || infoRetireTables[phase] == "catalog_info_path_old") {
		phase++
	}
	if phase == len(infoRetireTables) {
		return true, nil
	}
	table := infoRetireTables[phase]
	keys := infoRetirePrimaryKeys[table]
	r, e := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE (`+keys+`) IN(SELECT `+keys+` FROM `+table+` WHERE change_id=? ORDER BY `+keys+` LIMIT 8)`, c.id)
	if e != nil {
		return false, e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return false, e
	}
	if n == 0 {
		phase++
	}
	_, e = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET fault='',copy_aux=-1,copy_family=?,input_revision=input_revision+1 WHERE id=? AND phase=0`, phase, c.id)
	return false, e
}

// Move at most eight first-OLD scalars, then one path. Updating the FK avoids a
// temporary duplicate logical charge. Readers see either source or destination
// in the same committed frame, never a gap between them.
func transferInfoOldTx(ctx context.Context, tx *sql.Tx, from, to int64, replace bool) (bool, error) {
	rows, e := tx.QueryContext(ctx, `SELECT field FROM catalog_info_scalar_old WHERE change_id=? ORDER BY field LIMIT 8`, from)
	if e != nil {
		return false, e
	}
	var fields [8]int
	n := 0
	for rows.Next() {
		if e = rows.Scan(&fields[n]); e != nil {
			break
		}
		n++
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return false, e
	}
	for _, field := range fields[:n] {
		if replace {
			_, e = tx.ExecContext(ctx, `DELETE FROM catalog_info_scalar_old WHERE change_id=? AND field=?`, to, field)
		} else {
			_, e = tx.ExecContext(ctx, `DELETE FROM catalog_info_scalar_old WHERE change_id=? AND field=? AND EXISTS(SELECT 1 FROM catalog_info_scalar_old WHERE change_id=? AND field=?)`, from, field, to, field)
		}
		if e != nil {
			return false, e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE catalog_info_scalar_old SET change_id=? WHERE change_id=? AND field=?`, to, from, field); e != nil {
			return false, e
		}
	}
	if n != 0 {
		return false, nil
	}
	if replace {
		_, e = tx.ExecContext(ctx, `DELETE FROM catalog_info_path_old WHERE change_id=? AND EXISTS(SELECT 1 FROM catalog_info_path_old WHERE change_id=?)`, to, from)
	} else {
		_, e = tx.ExecContext(ctx, `DELETE FROM catalog_info_path_old WHERE change_id=? AND EXISTS(SELECT 1 FROM catalog_info_path_old WHERE change_id=?)`, from, to)
	}
	if e != nil {
		return false, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE catalog_info_path_old SET change_id=? WHERE change_id=?`, to, from)
	return e == nil, e
}
