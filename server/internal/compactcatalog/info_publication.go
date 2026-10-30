package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var (
	ErrInfoPreparing = errors.New("informational facts are preparing")
	ErrInfoInvariant = errors.New("informational publication invariant")
)

// InfoHead is item-local. Neither an unrelated source's dirty state nor a
// library content clock is a readiness input. Authority is checked separately.
type InfoHead struct {
	Desired, Applied                int64
	SourceBlockers, BlockedChildren int64
	AvailabilityPending             bool
}

func (h InfoHead) Ready() bool {
	return h.Desired == h.Applied && h.SourceBlockers == 0 && !h.AvailabilityPending && h.BlockedChildren == 0
}

func InfoHeadTx(ctx context.Context, q ReadQuery, item int64) (InfoHead, error) {
	var h InfoHead
	err := q.QueryRowContext(ctx, `SELECT info_desired_epoch,info_applied_epoch,info_source_blockers,info_availability_pending,info_blocked_children FROM catalog_entities WHERE id=? AND retired=0`, item).
		Scan(&h.Desired, &h.Applied, &h.SourceBlockers, &h.AvailabilityPending, &h.BlockedChildren)
	return h, err
}

// CompleteInfoAvailabilityTx is the domain20 completion hook. Call only in the
// transaction accepting the complete exact holder output, before acknowledging
// that captured dirty revision. An aborted/raw-prefix holder stays pending.
func CompleteInfoAvailabilityTx(ctx context.Context, tx *sql.Tx, item int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE catalog_entities SET info_availability_pending=0,
 info_desired_epoch=info_desired_epoch+1,
 info_applied_epoch=CASE WHEN info_source_blockers=0 AND info_blocked_children=0 THEN info_desired_epoch+1 ELSE info_applied_epoch END
 WHERE id=? AND info_availability_pending<>0`, item)
	return err
}

// BeginInfoAvailabilityTx is idempotent for a coalesced holder job. Source
// health never invokes it. It precedes any real per-link display publication.
func BeginInfoAvailabilityTx(ctx context.Context, tx *sql.Tx, item int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE catalog_entities SET info_availability_pending=1,
 info_desired_epoch=info_desired_epoch+1 WHERE id=? AND info_availability_pending=0`, item)
	return err
}

type infoChange struct {
	id, asset, expected              int64
	token                            string
	phase                            int
	hasAfter                         bool
	after, upper                     int64
	familyMask                       int64
	targetAvailable                  sql.NullInt64
	targetShape                      sql.NullInt64
	targetOwner, targetOwnerRevision sql.NullString
}

func activeInfoChange(ctx context.Context, tx *sql.Tx, asset int64, token string) (infoChange, error) {
	var c infoChange
	err := tx.QueryRowContext(ctx, `SELECT id,asset_id,asset_token,phase,expected_epoch,has_after,after_entity,upper_entity,family_mask,target_available,target_shape_id,target_owner_object,target_owner_revision
 FROM catalog_info_changes WHERE asset_id=? AND asset_token=? AND slot=0`, asset, token).
		Scan(&c.id, &c.asset, &c.token, &c.phase, &c.expected, &c.hasAfter, &c.after, &c.upper, &c.familyMask, &c.targetAvailable, &c.targetShape, &c.targetOwner, &c.targetOwnerRevision)
	return c, err
}

// InfoMaterializer executes one bounded normalized-source quantum. It may not
// activate or release holders. Returning done requires every changed family to
// be completely installed, including removal of obsolete canonical rows.
type InfoMaterializer func(context.Context, *sql.Tx, int64) (done bool, err error)

// AdvanceInfoPublicationTx runs ONE source-publication quantum in the existing
// source41 writer. It starts no worker and has no query state. Source41 invokes
// it after exact asset evidence is resolved and before acknowledging its source
// work. A nil materializer can advance scalar-only changes; it fails closed for
// changed technical families. Caller measures the whole transaction including
// point and ledger triggers against the existing writer plan.
func AdvanceInfoPublicationTx(ctx context.Context, tx *sql.Tx, asset int64, token string, materialize InfoMaterializer) (bool, error) {
	c, err := activeInfoChange(ctx, tx, asset, token)
	if errors.Is(err, sql.ErrNoRows) {
		return beginDeferredInfoAvailability(ctx, tx, asset, token)
	}
	if err != nil {
		return false, err
	}
	var current int64
	err = tx.QueryRowContext(ctx, `SELECT info_epoch FROM catalog_assets WHERE id=? AND token=?`, asset, token).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		// Physical delete has already discharged link obligations atomically.
		if c.phase == 5 {
			return retireInfoChange(ctx, tx, c)
		}
		_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET phase=5,copy_family=0 WHERE id=?`, c.id)
		return false, err
	}
	if err != nil {
		return false, err
	}
	if (c.phase < 4 && current != c.expected) || (c.phase == 4 && current != c.expected+1) {
		return false, ErrInfoInvariant
	}
	switch c.phase {
	case 0:
		if handled, done, e := recoverInfoInputTx(ctx, tx, c); handled || e != nil {
			return done, e
		}
		var unsealed bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_info_input_families WHERE change_id=? AND (sealed=0 OR rows_staged<>rows_expected))`, c.id).Scan(&unsealed); err != nil {
			return false, err
		}
		if unsealed {
			return false, nil
		}
		// Typed input is accepted only by AcceptInfoInputTx after its producer
		// validates and seals the complete observation. A worker cannot turn a
		// staged body into an accepted receipt merely by seeing sealed rows.
		var typed bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_info_input_witnesses WHERE change_id=?)`, c.id).Scan(&typed); err != nil {
			return false, err
		}
		if c.familyMask != 0 || typed {
			return false, nil
		}
		if _, err = EnsureInfoShapeTargetTx(ctx, tx, c.id); errors.Is(err, ErrInfoInputDeferred) {
			// No holder has been pre-fenced. Reclaim only maintained unused
			// dictionary rows and truthfully defer this source acceptance.
			return false, CollectInfoShapesTx(ctx, tx)
		} else if err != nil {
			return false, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET phase=1,has_after=0,
 target_available=COALESCE((SELECT o.old_integer FROM catalog_info_changes n JOIN catalog_info_scalar_old o ON o.change_id=n.id AND o.field=1 WHERE n.asset_id=? AND n.asset_token=? AND n.slot=1),(SELECT available FROM catalog_assets WHERE id=? AND token=?)),
 upper_entity=COALESCE((SELECT MAX(entity_id) FROM catalog_asset_links INDEXED BY catalog_asset_links_asset WHERE asset_id=?),0) WHERE id=?`, asset, token, asset, token, asset, c.id)
	case 1:
		// Disc-capable choices are supplied by source41's coherent A5 pass.
		// Non-disc sources can resolve an empty choice without another pass.
		ready, e := infoOwnerTargetReadyTx(ctx, tx, c)
		if e != nil || !ready {
			return false, e
		}
		err = prefenceInfoHolders(ctx, tx, c)
	case 2:
		if c.familyMask != 0 {
			if materialize == nil {
				return false, ErrInfoPreparing
			}
			done, e := materialize(ctx, tx, c.id)
			if e != nil || !done {
				return false, e
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET phase=3 WHERE id=?`, c.id)
	case 3:
		// Custody can change after the final materializer quantum. Discharge
		// that exact scope before activation as well; a replacement must not
		// inherit the old asset's selected observation.
		if e := releaseInfoRepointedTargetTx(ctx, tx, c.id); e != nil {
			return false, e
		}
		object, e := infoCopyWitnessObject(ctx, tx, c.id, c.familyMask)
		if e != nil {
			return false, e
		}
		_, blocked, e := infoCopyDiscCustodyTx(ctx, tx, c.id, c.token, object)
		if e != nil || blocked {
			return false, e
		}
		if e = tx.QueryRowContext(ctx, `SELECT target_owner_object,target_owner_revision FROM catalog_info_changes WHERE id=?`, c.id).Scan(&c.targetOwner, &c.targetOwnerRevision); e != nil {
			return false, e
		}
		// A raw NEW2/NEW3 may already exist. The sealed target is NEW1's
		// availability, and must win over those later physical observations.
		if !c.targetAvailable.Valid || !c.targetShape.Valid {
			return false, ErrInfoInvariant
		}
		_, err = tx.ExecContext(ctx, `UPDATE catalog_assets SET info_epoch=info_epoch+1,info_order_available=?,info_owner_object=?,info_owner_revision=?
 WHERE id=? AND token=? AND info_epoch=?`, c.targetAvailable.Int64, c.targetOwner, c.targetOwnerRevision, asset, token, c.expected)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET phase=4 WHERE id=?`, c.id)
		}
	case 4:
		err = applyInfoHolders(ctx, tx, c)
	case 5:
		return retireInfoChange(ctx, tx, c)
	default:
		return false, ErrInfoInvariant
	}
	return false, err
}

// A mandatory physical revocation is never refused because the source-input
// ledger is full or both immutable slots are occupied. The accepted order head
// stays unchanged. Once a slot/capacity is free, source41 accepts the latest raw
// desired availability into its ordinary finite publication protocol.
func beginDeferredInfoAvailability(ctx context.Context, tx *sql.Tx, asset int64, token string) (bool, error) {
	var raw, accepted bool
	var epoch int64
	err := tx.QueryRowContext(ctx, `SELECT available,info_order_available,info_epoch FROM catalog_assets WHERE id=? AND token=?`, asset, token).Scan(&raw, &accepted, &epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil || raw == accepted {
		return err == nil, err
	}
	var room bool
	if err = tx.QueryRowContext(ctx, `SELECT cap_bytes-used_bytes>=1024+length(CAST(? AS BLOB)) FROM catalog_info_input_budget WHERE singleton=1`, token).Scan(&room); err != nil {
		return false, err
	}
	if !room {
		return false, nil
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO catalog_info_changes(asset_id,asset_token,slot,expected_epoch,fingerprint,created_ms)
 VALUES(?,?,0,?,zeroblob(32),CAST(strftime('%s','now') AS INTEGER)*1000)`, asset, token, epoch)
	if err != nil {
		return false, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO catalog_info_scalar_old(change_id,field,kind,old_integer) VALUES(?,1,1,?)`, id, accepted)
	return false, err
}

func prefenceInfoHolders(ctx context.Context, tx *sql.Tx, c infoChange) error {
	query := `SELECT entity_id FROM catalog_asset_links INDEXED BY catalog_asset_links_asset WHERE asset_id=? AND entity_id<=?`
	args := []any{c.asset, c.upper}
	if c.hasAfter {
		query += ` AND entity_id>?`
		args = append(args, c.after)
	}
	query += ` ORDER BY entity_id LIMIT 8`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	var ids [8]int64
	n := 0
	for rows.Next() {
		if err = rows.Scan(&ids[n]); err != nil {
			break
		}
		n++
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids[:n] {
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_asset_links SET info_pending_change=? WHERE asset_id=? AND entity_id=? AND info_pending_change IS NULL`, c.id, c.asset, id); err != nil {
			return err
		}
	}
	if n > 0 {
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET has_after=1,after_entity=? WHERE id=?`, ids[n-1], c.id); err != nil {
			return err
		}
	}
	if n < len(ids) {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET phase=2,copy_family=0 WHERE id=?`, c.id)
	}
	return err
}

func applyInfoHolders(ctx context.Context, tx *sql.Tx, c infoChange) error {
	if !c.targetShape.Valid {
		return ErrInfoInvariant
	}
	var availability bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_info_scalar_old WHERE change_id=? AND field=1)
 OR EXISTS(SELECT 1 FROM catalog_info_path_old WHERE change_id=?)`, c.id, c.id).Scan(&availability); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT entity_id FROM catalog_asset_links INDEXED BY catalog_info_pending_links WHERE info_pending_change=? ORDER BY entity_id LIMIT 8`, c.id)
	if err != nil {
		return err
	}
	var ids [8]int64
	n := 0
	for rows.Next() {
		if err = rows.Scan(&ids[n]); err != nil {
			break
		}
		n++
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if n > 0 && c.familyMask&infoSubtitleProjectionMask != 0 {
		done, err := projectInfoDiscSubtitles(ctx, tx, c, ids[0])
		if err != nil || !done {
			return err
		}
		// Only this holder's resource projection is complete in this turn.
		n = 1
	}
	for _, id := range ids[:n] {
		if availability {
			if err = BeginInfoAvailabilityTx(ctx, tx, id); err != nil {
				return err
			}
			if err = TouchTx(ctx, tx, DomainAvailability, id); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE catalog_asset_links SET info_shape_id=?,info_duration=COALESCE(
 (SELECT o.old_real FROM catalog_info_changes n JOIN catalog_info_scalar_old o ON o.change_id=n.id AND o.field=9 WHERE n.asset_id=? AND n.asset_token=? AND n.slot=1),
 (SELECT duration FROM catalog_assets WHERE id=? AND token=?)),info_pending_change=NULL
 WHERE entity_id=? AND asset_id=? AND info_pending_change=?`, c.targetShape.Int64, c.asset, c.token, c.asset, c.token, id, c.asset, c.id)
		if err != nil {
			return err
		}
	}
	if n == 0 {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET phase=5,copy_family=0 WHERE id=?`, c.id)
	}
	return err
}

// Reverse dependency order is fixed source schema, never caller-controlled SQL.
var infoRetireTables = []string{
	"catalog_info_new_disc_title_media_streams", "catalog_info_new_disc_title_media_members", "catalog_info_new_disc_title_media",
	"catalog_info_new_disc_clips", "catalog_info_new_disc_streams", "catalog_info_new_disc_chapters", "catalog_info_new_disc_media_members", "catalog_info_new_disc_titles", "catalog_info_new_disc_facts",
	"catalog_info_new_stream_breadth", "catalog_info_new_streams", "catalog_info_new_stream_facts",
	"catalog_info_new_subtitles", "catalog_info_new_subtitle_sidecars", "catalog_info_new_subtitle_inventory_evidence", "catalog_info_new_subtitle_facts",
	"catalog_info_new_chapters", "catalog_info_new_chapter_facts",
	"catalog_info_scalar_old", "catalog_info_path_old", "catalog_info_input_families", "catalog_info_input_witnesses",
}

func retireInfoChange(ctx context.Context, tx *sql.Tx, c infoChange) (bool, error) {
	// At most one populated table and <=8 raw PK rows per call. Empty prefixes
	// may advance through the fixed family list in the same writer: they change
	// no rows and need only full change_id-prefix seeks. Save copy_family once,
	// so an availability-only change does not need 18 empty writer admissions
	// before reaching its scalar witness. A populated child always ends the
	// turn. Once every remaining prefix is empty in this writer, the parent
	// can retire immediately without saving a cursor for an empty-only turn.
	var phase int
	if err := tx.QueryRowContext(ctx, `SELECT copy_family FROM catalog_info_changes WHERE id=?`, c.id).Scan(&phase); err != nil {
		return false, err
	}
	if phase < 0 || phase > len(infoRetireTables) {
		return false, ErrInfoInvariant
	}
	started := phase
	for phase < len(infoRetireTables) {
		table := infoRetireTables[phase]
		// Every source table is keyed first by change_id; row-value IN uses
		// its complete declared PK, including WITHOUT ROWID tables.
		keys := infoRetirePrimaryKeys[table]
		if keys == "" {
			return false, fmt.Errorf("%w: unknown retirement table", ErrInfoInvariant)
		}
		query := `DELETE FROM ` + table + ` WHERE (` + keys + `) IN(SELECT ` + keys + ` FROM ` + table + ` WHERE change_id=? ORDER BY ` + keys + ` LIMIT 8)`
		result, err := tx.ExecContext(ctx, query, c.id)
		if err != nil {
			return false, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		if n > 0 {
			if phase != started {
				_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET copy_family=? WHERE id=? AND phase=5`, phase, c.id)
			}
			return false, err
		}
		phase++
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_info_changes WHERE id=? AND phase=5`, c.id); err != nil {
		return false, err
	}
	_, err := tx.ExecContext(ctx, `UPDATE catalog_info_changes SET slot=0,has_after=0,
 upper_entity=COALESCE((SELECT MAX(entity_id) FROM catalog_asset_links INDEXED BY catalog_asset_links_asset WHERE asset_id=?),0)
 WHERE asset_id=? AND asset_token=? AND slot=1`, c.asset, c.asset, c.token)
	if err == nil {
		err = CollectInfoShapesTx(ctx, tx)
	}
	return false, err // caller observes promoted successor or empty source next turn.
}
