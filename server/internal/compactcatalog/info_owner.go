package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
)

func infoDiscCapableTx(ctx context.Context, tx *sql.Tx, asset int64, token string) (bool, error) {
	var yes bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_assets WHERE id=? AND token=? AND (info_owner_object IS NOT NULL OR container IN('disc','iso','bluray','dvd')))
 OR EXISTS(SELECT 1 FROM inventory_disc_facts INDEXED BY inventory_disc_facts_asset WHERE asset_id=?)
 OR EXISTS(SELECT 1 FROM catalog_info_changes WHERE asset_id=? AND asset_token=? AND family_mask&512<>0)`, asset, token, token, asset, token).Scan(&yes)
	return yes, err
}

// prepareInfoOwnerTargetTx takes the lowest eligible raw object from A5's exact
// coherent pass, before testing whether that object has any probe. It performs
// point reads only. False means capacity deferred before holder pre-fence.
func prepareInfoOwnerTargetTx(ctx context.Context, tx *sql.Tx, asset int64, token string, available bool, object string) (bool, error) {
	c, err := activeInfoChange(ctx, tx, asset, token)
	missing := errors.Is(err, sql.ErrNoRows)
	if err != nil && !missing {
		return false, err
	}
	if !missing && c.phase != 0 && c.phase != 1 {
		return true, nil
	}
	if !missing && c.phase == 0 {
		var typed bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_info_input_witnesses WHERE change_id=?)`, c.id).Scan(&typed); err != nil {
			return false, err
		}
		if typed || c.familyMask != 0 {
			return true, nil
		}
	}
	if !missing && c.phase == 1 {
		var resolved bool
		if err = tx.QueryRowContext(ctx, `SELECT copy_family=1 FROM catalog_info_changes WHERE id=?`, c.id).Scan(&resolved); err != nil || resolved {
			return err == nil, err
		}
	}
	capable, err := infoDiscCapableTx(ctx, tx, asset, token)
	if err != nil {
		return false, err
	}
	var chosen, revision sql.NullString
	if capable && available && object != "" {
		// A5's pass revision is checked by the caller in this same writer. No object
		// or source/root mutation can interleave with this point revalidation.
		if err = tx.QueryRowContext(ctx, `SELECT id,revision FROM inventory_objects WHERE id=? AND asset_id=?`, object, token).Scan(&chosen, &revision); err != nil {
			return false, err
		}
	}
	if missing {
		var oldObject, oldRevision sql.NullString
		var epoch int64
		if err = tx.QueryRowContext(ctx, `SELECT info_owner_object,info_owner_revision,info_epoch FROM catalog_assets WHERE id=? AND token=?`, asset, token).Scan(&oldObject, &oldRevision, &epoch); err != nil {
			return false, err
		}
		if oldObject == chosen && oldRevision == revision {
			return true, nil
		}
		// Leave generous fixed headroom for its shape pin before pre-fence. Real
		// missing state itself has already committed and never uses this ledger.
		var room bool
		if err = tx.QueryRowContext(ctx, `SELECT cap_bytes-used_bytes>=2048+length(CAST(? AS BLOB))+2*length(CAST(? AS BLOB))+length(CAST(? AS BLOB)) FROM catalog_info_input_budget WHERE singleton=1`, token, chosen.String, revision.String).Scan(&room); err != nil || !room {
			return false, err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO catalog_info_changes(asset_id,asset_token,slot,expected_epoch,fingerprint,created_ms) VALUES(?,?,0,?,zeroblob(32),CAST(strftime('%s','now') AS INTEGER)*1000)`, asset, token, epoch)
		if err != nil {
			return false, err
		}
		c.id, err = result.LastInsertId()
		if err != nil {
			return false, err
		}
		// Capture this coherent directory choice before scalar acceptance in
		// this same writer, not in a later mutable phase-1 observation.
		c.phase = 0
	}
	var room bool
	if err = tx.QueryRowContext(ctx, `SELECT cap_bytes-used_bytes>=2*length(CAST(? AS BLOB))+length(CAST(? AS BLOB)) FROM catalog_info_input_budget WHERE singleton=1`, chosen.String, revision.String).Scan(&room); err != nil || !room {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET target_owner_object=?,target_owner_revision=?,copy_family=1 WHERE id=? AND phase IN(0,1)`, chosen, revision, c.id)
	return err == nil, err
}

func infoOwnerTargetReadyTx(ctx context.Context, tx *sql.Tx, c infoChange) (bool, error) {
	var ready bool
	if err := tx.QueryRowContext(ctx, `SELECT copy_family=1 FROM catalog_info_changes WHERE id=?`, c.id).Scan(&ready); err != nil || ready {
		return ready, err
	}
	capable, err := infoDiscCapableTx(ctx, tx, c.asset, c.token)
	if err != nil || capable {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET copy_family=1,target_owner_object=NULL,target_owner_revision=NULL WHERE id=?`, c.id)
	return err == nil, err
}

// PrepareInfoObjectDeleteTx is called by the actual per-object GC/removal paths.
// A held tree is retained, while genuine missing evidence is accepted now. One
// indexed pin owner is woken per call, so a moved object's historical pins never
// create a synchronous all-asset fanout.
func PrepareInfoObjectDeleteTx(ctx context.Context, tx *sql.Tx, object string) (bool, error) {
	var asset int64
	var token string
	err := tx.QueryRowContext(ctx, `SELECT id,token FROM catalog_assets INDEXED BY catalog_info_owner_object WHERE info_owner_object=? LIMIT 1`, object).Scan(&asset, &token)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `SELECT asset_id,asset_token FROM catalog_info_changes INDEXED BY catalog_info_target_owner_object WHERE target_owner_object=? LIMIT 1`, object).Scan(&asset, &token)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_objects SET state='missing' WHERE id=? AND state<>'missing'`, object); err != nil {
		return false, err
	}
	var queued bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_upstream_sources WHERE domain=41 AND source_id=? AND token=? AND flags&32<>0)`, asset, token).Scan(&queued); err != nil {
		return false, err
	}
	if !queued {
		err = queueSource(ctx, tx, DomainAssetSources, asset, token, sourceInfo|sourceAvailability|sourceMissing)
	}
	return false, err
}

// freezeInfoInputOwnerTx captures A5's complete current object directory at the
// same acceptance point as a typed body. Staging can finish while A5 catches up;
// no holder is pre-fenced and no accepted receipt exists during that deferral.
func freezeInfoInputOwnerTx(ctx context.Context, tx *sql.Tx, h infoInputHeader) error {
	capable, e := infoDiscCapableTx(ctx, tx, h.assetID, h.token)
	if e != nil {
		return e
	}
	var object, revision sql.NullString
	// A technical-only input did not observe another disc structure. Carry
	// its existing accepted choice; real observation changes remain owned by
	// the already-queued source41 directory pass.
	if h.familyMask&((1<<18)-(1<<9)) == 0 {
		if e = tx.QueryRowContext(ctx, `SELECT info_owner_object,info_owner_revision FROM catalog_assets WHERE id=? AND token=?`, h.assetID, h.token).Scan(&object, &revision); e != nil {
			return e
		}
	} else if capable {
		w, e := readUpstreamSource(ctx, tx, DomainAssetSources, h.assetID)
		if errors.Is(e, sql.ErrNoRows) {
			return ErrInfoInputDeferred
		}
		if e != nil {
			return e
		}
		var epoch int64
		if e = tx.QueryRowContext(ctx, `SELECT availability_epoch FROM catalog_facet_source_state WHERE singleton=1`).Scan(&epoch); e != nil {
			return e
		}
		if w.token != h.token || w.phase != 3 || w.passRevision != w.revision || w.key == "rerun" || w.epoch != epoch {
			return ErrInfoInputDeferred
		}
		if w.available && w.innerKey != "" {
			if e = tx.QueryRowContext(ctx, `SELECT id,revision FROM inventory_objects WHERE id=? AND asset_id=?`, w.innerKey, h.token).Scan(&object, &revision); e != nil {
				if errors.Is(e, sql.ErrNoRows) {
					return ErrInfoInputDeferred
				}
				return e
			}
		}
	}
	_, e = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET target_owner_object=?,target_owner_revision=?,copy_family=1 WHERE id=? AND phase=0 AND input_revision=?`, object, revision, h.changeID, h.inputRevision)
	return infoInputClassifyWrite(e)
}
