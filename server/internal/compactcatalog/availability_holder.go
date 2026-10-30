package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Availability (domain 20) is a bounded, resumable per-item pass over the raw
// evidence that decides whether a file can be played: the item's links and
// their assets, the inventory objects of each asset in the item's library, and
// (when no inventory object names the asset) the library's sources and their
// roots. One catalog_availability_work row is the cursor of the pass in
// progress. A turn reads at most the batch's raw rows, checkpoints that row,
// and commits; the pass resumes where it stopped.
//
// The pass follows exactly the inventory_item_availability view. Inventory and
// source rows are read in raw primary-key order, so a rejected row (an object
// of another library, a retired or unsupported object, a source that does not
// cover the file) still consumes the cursor: a filter before LIMIT could walk
// an unbounded number of rows inside one turn and hide the cursor's progress.
// The raw scans are driven by the link row (CROSS JOIN), never by a source or
// asset lookup that could reorder the walk. The pass captures a raw frontier
// when it starts (fence_asset) and when each link's evidence opens
// (fence_object, fence_source); it reads at or below those keys only, so a
// tail appended while it walks is left to the queued rerun instead of moving
// the end of the pass.
//
// A revision touched while the pass runs is a rerun owed, not a restart: the
// pass walks to its end at its own cursor, drops the mixed publication, and
// leaves the newer dirty revision queued for one fresh pass from the new head.
// Source health is read from the view's conditions at each row; a transient
// offline/online change is ignored exactly as the view ignores it, and the
// global source epoch is only captured, never compared. This pass advances
// that epoch itself whenever it changes a link flag, and must not restart on
// its own output.
//
// Every cursor is started explicitly (has_asset, has_object, has_source), so
// the key values 0, negative integers and the empty string are all legal
// consumed keys rather than "no cursor". Asset and object/source text IDs are
// opaque; the pass never assumes they are non-empty.

// availabilityHolder is one in-flight domain20 pass. revision is the dirty
// revision the pass was started from; it is never advanced by this pass.
type availabilityHolder struct {
	entityID    int64
	revision    int64
	sourceEpoch int64
	// phase is the next action for the current link: 0 read the next link,
	// 1 read its inventory objects, 2 read the item library's sources.
	phase int

	// The link walk. hasAsset means afterAsset is a consumed key (which may be
	// 0 or negative); assetID.Valid means a link is loaded and its evidence is
	// being read. assetID's integer may itself be 0 or negative.
	hasAsset       bool
	afterAsset     int64
	assetID        sql.NullInt64
	assetToken     string
	assetPath      string
	assetAvailable bool

	// Fences capture the raw frontier once: the pass only reads keys at or
	// below them, so an appended tail is left to the queued rerun instead of
	// moving the end of the walk. fence_asset is captured when the pass starts,
	// fence_object/fence_source when a link's evidence opens. NULL is the captured empty frontier. Valid empty strings and every
	// integer value remain legal keys.
	fenceAsset  sql.NullInt64
	fenceObject sql.NullString
	fenceSource sql.NullString

	// Inner cursors. hasObject/hasSource mean the corresponding cursor has
	// started, which keeps an empty afterObject/afterSource distinct from "no
	// cursor yet"; phase moves on when a walk reaches its end.
	hasObject   bool
	afterObject string
	hasSource   bool
	afterSource string

	// Evidence accumulated for the current asset.
	inventorySeen      bool
	inventoryAvailable bool
	sourceSeen         bool
	healthySeen        bool
	matchingSeen       bool
	matchingUnhealthy  bool

	// any_available is the pass's running answer, kept for diagnostics; the
	// published item row is read from the link flags at completion instead.
	anyAvailable bool

	// Read at the start of each turn; not part of the durable cursor.
	library string
	retired bool
	gone    bool
	// walked is set when the link walk has no more rows: the caller publishes.
	walked bool
	// Actual flag publications are separately budgeted from raw reads.
	changed int
}

// availabilityHolderTx loads the pass for k, creating it at the head when no
// row exists. A row for the same item is continued whatever its revision: a
// newer dirty revision is the rerun owed, not a reason to reset the cursor.
// A holder without a queued revision can only be a row left behind by an
// interrupted older build; it is not discovered by scanning the table here.
// Other orphan holders are reported, not swept: sweeping them would read every
// live pass row on each turn.
func availabilityHolderTx(ctx context.Context, tx *sql.Tx, k Key) (*availabilityHolder, error) {
	h := &availabilityHolder{entityID: k.ID, revision: k.Revision}
	err := tx.QueryRowContext(ctx, `SELECT revision,source_epoch,phase,has_asset,after_asset,asset_id,asset_token,asset_path,asset_available,
 has_object,after_object,has_source,after_source,inventory_seen,inventory_available,source_seen,healthy_seen,matching_seen,matching_unhealthy,any_available,
 fence_asset,fence_object,fence_source
 FROM catalog_availability_work WHERE entity_id=?`, k.ID).
		Scan(&h.revision, &h.sourceEpoch, &h.phase, &h.hasAsset, &h.afterAsset, &h.assetID, &h.assetToken, &h.assetPath, &h.assetAvailable,
			&h.hasObject, &h.afterObject, &h.hasSource, &h.afterSource, &h.inventorySeen, &h.inventoryAvailable, &h.sourceSeen, &h.healthySeen, &h.matchingSeen, &h.matchingUnhealthy, &h.anyAvailable,
			&h.fenceAsset, &h.fenceObject, &h.fenceSource)
	if err == nil {
		return h, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// A new pass captures the epoch for the record only. Drift never restarts
	// it: a link-flag write of this very pass advances the epoch.
	h.revision, h.phase = k.Revision, 0
	if err := tx.QueryRowContext(ctx, `SELECT source_epoch FROM catalog_facet_source_state WHERE singleton=1`).Scan(&h.sourceEpoch); err != nil {
		return nil, err
	}
	if err := h.captureAssetFence(ctx, tx); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_availability_work(entity_id,revision,source_epoch,fence_asset,fence_object,fence_source) VALUES(?,?,?,?,NULL,NULL)`,
		h.entityID, h.revision, h.sourceEpoch, h.fenceAsset); err != nil {
		return nil, err
	}
	return h, nil
}

// captureAssetFence fixes the pass's link frontier once.
func (h *availabilityHolder) captureAssetFence(ctx context.Context, tx *sql.Tx) error {
	var max sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT max(asset_id) FROM catalog_asset_links WHERE entity_id=?`, h.entityID).Scan(&max); err != nil {
		return err
	}
	h.fenceAsset = max
	return nil
}

// captureObjectFence fixes the asset's inventory frontier when its walk
// opens; an empty max is the captured empty frontier.
func (h *availabilityHolder) captureObjectFence(ctx context.Context, tx *sql.Tx) error {
	var max sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT max(id) FROM inventory_objects INDEXED BY inventory_objects_reading_asset_object WHERE asset_id=?`, h.assetToken).Scan(&max); err != nil {
		return err
	}
	h.fenceObject = max
	return nil
}

// captureSourceFence fixes the item library's source frontier when the
// fallback walk opens.
func (h *availabilityHolder) captureSourceFence(ctx context.Context, tx *sql.Tx) error {
	var max sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT max(id) FROM library_sources INDEXED BY library_sources_library WHERE library_id=?`, h.library).Scan(&max); err != nil {
		return err
	}
	h.fenceSource = max
	return nil
}

func (h *availabilityHolder) save(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `UPDATE catalog_availability_work SET phase=?,has_asset=?,after_asset=?,asset_id=?,asset_token=?,asset_path=?,asset_available=?,
 has_object=?,after_object=?,has_source=?,after_source=?,inventory_seen=?,inventory_available=?,source_seen=?,healthy_seen=?,matching_seen=?,matching_unhealthy=?,any_available=?,
 fence_asset=?,fence_object=?,fence_source=?
 WHERE entity_id=? AND revision=?`,
		h.phase, h.hasAsset, h.afterAsset, h.assetID, h.assetToken, h.assetPath, h.assetAvailable,
		h.hasObject, h.afterObject, h.hasSource, h.afterSource, h.inventorySeen, h.inventoryAvailable, h.sourceSeen, h.healthySeen, h.matchingSeen, h.matchingUnhealthy, h.anyAvailable,
		h.fenceAsset, h.fenceObject, h.fenceSource,
		h.entityID, h.revision)
	return err
}

// chargeAvailabilityScalar spends one row's decoded text from the turn's
// budget and floors it at zero. A single row wider than what is left is the
// gate's explicit O(B) exception; callers stop after spending it.
func chargeAvailabilityScalar(left, charge int) int {
	if left <= 0 || charge <= 0 {
		return max(left, 0)
	}
	if charge >= left {
		return 0
	}
	return left - charge
}

// advance reads at most limit raw rows. It returns the number read. An
// unfinished pass checkpoints its cursor; a finished link walk sets walked so
// the caller publishes. Decoded text (a link's token and path, an object ID, a
// source's IDs and both roots) is charged to one scalar budget for the whole
// turn, not to a fresh budget per inner read.
func (h *availabilityHolder) advance(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	if limit < 1 {
		limit = 1
	}
	// The item's library and retirement are read once per turn: the view's
	// inventory join and folder fallback both key off the item's library, and
	// retirement precedes all file evidence.
	err := tx.QueryRowContext(ctx, `SELECT cl.library_id,EXISTS(SELECT 1 FROM dvr_catalog_retirements d WHERE d.item_id=e.id)
 FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.id=?`, h.entityID).Scan(&h.library, &h.retired)
	if errors.Is(err, sql.ErrNoRows) {
		h.gone, h.walked = true, true
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	visits := 0
	h.changed = 0
	scalars := facetScalarBudget
	for remaining := limit; remaining > 0; {
		if h.changed >= 8 {
			break
		}
		if scalars <= 0 && visits > 0 {
			break
		}
		switch h.phase {
		case 0:
			q := `SELECT l.asset_id,a.token,a.path,a.available FROM catalog_asset_links l CROSS JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=?`
			q += ` AND l.asset_id<=?`
			args := []any{h.entityID, h.fenceAsset}
			if h.hasAsset {
				q += ` AND l.asset_id>?`
				args = append(args, h.afterAsset)
			}
			q += ` ORDER BY l.asset_id LIMIT 1`
			var id int64
			var token, path string
			var available bool
			err := tx.QueryRowContext(ctx, q, args...).Scan(&id, &token, &path, &available)
			if errors.Is(err, sql.ErrNoRows) {
				h.walked = true
				return visits, nil
			}
			if err != nil {
				return visits, err
			}
			visits++
			remaining--
			scalars = chargeAvailabilityScalar(scalars, len(token)+len(path))
			h.hasAsset, h.afterAsset = true, id
			h.assetID = sql.NullInt64{Int64: id, Valid: true}
			h.assetToken, h.assetPath, h.assetAvailable = token, path, available
			h.hasObject, h.afterObject = false, ""
			h.hasSource, h.afterSource = false, ""
			h.inventorySeen, h.inventoryAvailable = false, false
			h.sourceSeen, h.healthySeen, h.matchingSeen, h.matchingUnhealthy = false, false, false, false
			h.phase = 1
			// A retired recording answers 0 before any file evidence, so the
			// asset needs no subwalk at all.
			if h.retired {
				if err := h.complete(ctx, tx); err != nil {
					return visits, err
				}
				break
			}
			if err := h.captureObjectFence(ctx, tx); err != nil {
				return visits, err
			}
			if err := h.captureSourceFence(ctx, tx); err != nil {
				return visits, err
			}
		case 1:
			rows, exhausted, err := h.readObjects(ctx, tx, remaining, &scalars)
			if err != nil {
				return visits, err
			}
			visits += len(rows)
			remaining -= len(rows)
			for _, r := range rows {
				h.afterObject = r.id
				if r.library {
					h.inventorySeen = true
					if r.available {
						h.inventoryAvailable = true
					}
				}
			}
			if len(rows) > 0 {
				h.hasObject = true
			}
			switch {
			case h.inventoryAvailable:
				// A qualifying object is a positive witness: the view's EXISTS
				// is already true, so the rest of the asset's objects need not
				// be read.
				if err := h.complete(ctx, tx); err != nil {
					return visits, err
				}
			case exhausted && h.inventorySeen:
				// Inventory names the asset but none of it qualifies: the
				// view's inventory branch answers 0, without a source walk.
				if err := h.complete(ctx, tx); err != nil {
					return visits, err
				}
			case exhausted:
				// No object in the item's library names the asset: the view
				// falls back to the item's folder sources.
				h.phase = 2
				h.hasSource, h.afterSource = false, ""
			}
		case 2:
			rows, exhausted, err := h.readSources(ctx, tx, remaining, &scalars)
			if err != nil {
				return visits, err
			}
			visits += len(rows)
			remaining -= len(rows)
			for _, r := range rows {
				h.afterSource = r.id
				h.sourceSeen = true
				if r.healthy {
					h.healthySeen = true
				}
				if r.covering {
					h.matchingSeen = true
					if !r.healthy {
						h.matchingUnhealthy = true
					}
				}
			}
			if len(rows) > 0 {
				h.hasSource = true
			}
			if exhausted {
				if err := h.complete(ctx, tx); err != nil {
					return visits, err
				}
			}
		default:
			return visits, errors.New("catalogue availability holder has an unknown phase")
		}
	}
	return visits, nil
}

// complete writes the current link's answer and returns the pass to the link
// walk. Only a changed flag is written, so the link triggers (FI1's atomic
// epoch among them) see a real change.
func (h *availabilityHolder) complete(ctx context.Context, tx *sql.Tx) error {
	available := h.available()
	result, err := tx.ExecContext(ctx, `UPDATE catalog_asset_links SET available=? WHERE entity_id=? AND asset_id=? AND available IS NOT ?`, available, h.entityID, h.assetID.Int64, available)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	h.changed += int(changed)
	if available {
		h.anyAvailable = true
	}
	h.assetID, h.assetToken, h.assetPath, h.assetAvailable = sql.NullInt64{}, "", "", false
	h.hasObject, h.afterObject = false, ""
	h.hasSource, h.afterSource = false, ""
	h.phase = 0
	return nil
}

// available answers one link exactly as the view's CASE does: retirement
// first, then inventory evidence for the asset in the item's library, then the
// file's own flag and the library's folder roots.
func (h *availabilityHolder) available() bool {
	if h.retired {
		return false
	}
	if h.inventorySeen {
		return h.inventoryAvailable
	}
	if !h.assetAvailable {
		return false
	}
	if h.matchingSeen {
		return !h.matchingUnhealthy
	}
	return !h.sourceSeen || h.healthySeen
}

type availabilityObject struct {
	id        string
	library   bool
	available bool
}

// readObjects reads up to limit raw inventory objects of the current asset in
// (asset_id,id) order. Every object of the asset advances the cursor, whether
// its source is in the item's library or not; the library and qualification
// answers are computed per row. The link raw row drives the scan.
func (h *availabilityHolder) readObjects(ctx context.Context, tx *sql.Tx, limit int, scalars *int) ([]availabilityObject, bool, error) {
	q := `SELECT o.id,s.library_id=?,
 o.retired=0 AND o.state='available' AND o.unsupported_reason='' AND s.enabled=1 AND s.health NOT IN('root_changed','removing') AND o.root_incarnation=s.incarnation
 FROM inventory_objects o INDEXED BY inventory_objects_reading_asset_object
 CROSS JOIN library_sources s ON s.id=o.source_id WHERE o.asset_id=?`
	q += ` AND o.id<=?`
	args := []any{h.library, h.assetToken, h.fenceObject}
	if h.hasObject {
		q += ` AND o.id>?`
		args = append(args, h.afterObject)
	}
	q += ` ORDER BY o.id LIMIT ?`
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := make([]availabilityObject, 0, min(limit, 8))
	stop := false
	for rows.Next() {
		var r availabilityObject
		if err = rows.Scan(&r.id, &r.library, &r.available); err != nil {
			return nil, false, err
		}
		charge := len(r.id)
		if len(out) > 0 && charge > *scalars {
			stop = true
			break
		}
		out = append(out, r)
		*scalars = chargeAvailabilityScalar(*scalars, charge)
		if *scalars == 0 {
			stop = true
			break
		}
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	return out, len(out) < limit && !stop, nil
}

type availabilitySource struct {
	id       string
	covering bool
	healthy  bool
}

// readSources reads up to limit folder sources of the item's library in
// (library_id,id) order. Both roots are read into this turn and charged to its
// scalar budget; the view's prefix test is reproduced here rather than hidden
// in a SQL boolean.
func (h *availabilityHolder) readSources(ctx context.Context, tx *sql.Tx, limit int, scalars *int) ([]availabilitySource, bool, error) {
	q := `SELECT s.id,s.root,s.configured_root,s.enabled,s.health
 FROM library_sources s INDEXED BY library_sources_library WHERE s.library_id=?`
	q += ` AND s.id<=?`
	args := []any{h.library, h.fenceSource}
	if h.hasSource {
		q += ` AND s.id>?`
		args = append(args, h.afterSource)
	}
	q += ` ORDER BY s.id LIMIT ?`
	args = append(args, limit)
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := make([]availabilitySource, 0, min(limit, 8))
	stop := false
	for rows.Next() {
		var r availabilitySource
		var root, configured string
		var enabled bool
		var health string
		if err = rows.Scan(&r.id, &root, &configured, &enabled, &health); err != nil {
			return nil, false, err
		}
		charge := len(r.id) + len(root) + len(configured)
		if len(out) > 0 && charge > *scalars {
			stop = true
			break
		}
		r.covering = availabilityCovers(h.assetPath, root) || availabilityCovers(h.assetPath, configured)
		r.healthy = enabled && health != "root_changed" && health != "removing"
		out = append(out, r)
		*scalars = chargeAvailabilityScalar(*scalars, charge)
		if *scalars == 0 {
			stop = true
			break
		}
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	return out, len(out) < limit && !stop, nil
}

// availabilityCovers is the view's folder test: the file's path starts with
// the source root, with either separator, after trailing separators are
// trimmed.
func availabilityCovers(path, root string) bool {
	root = strings.TrimRight(root, "/\\")
	return strings.HasPrefix(path, root+"/") || strings.HasPrefix(path, root+"\\")
}

// publish writes the item's availability from the link flags now in force and
// queues its browse and metric rows. Reading the flags here (an index probe)
// means a link added or removed during the pass is reflected without the pass
// pretending its raw cursor saw it.
func (h *availabilityHolder) publish(ctx context.Context, tx *sql.Tx) error {
	if !h.gone {
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_item_availability(entity_id,available,retired)
 SELECT e.id,EXISTS(SELECT 1 FROM catalog_asset_links INDEXED BY catalog_asset_links_available_seek WHERE entity_id=e.id AND available=1),
 EXISTS(SELECT 1 FROM dvr_catalog_retirements d WHERE d.item_id=e.id)
 FROM catalog_entities e WHERE e.id=?
 ON CONFLICT(entity_id) DO UPDATE SET available=excluded.available,retired=excluded.retired
 WHERE catalog_item_availability.available IS NOT excluded.available OR catalog_item_availability.retired IS NOT excluded.retired`, h.entityID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_item_availability WHERE entity_id=? AND NOT EXISTS(SELECT 1 FROM catalog_entities e WHERE e.id=?)`, h.entityID, h.entityID); err != nil {
		return err
	}
	// Only an item whose published row disagrees is queued, exactly as the
	// whole-holder derivation did.
	ids := mustJSON([]int64{h.entityID})
	for _, domain := range []int{DomainBrowseRows, DomainItemMetrics} {
		if _, err := tx.ExecContext(ctx, availabilityStaleSQL, domain, ids); err != nil {
			return err
		}
	}
	return nil
}

// finish closes the pass. When the live dirty revision is still the captured
// one, it publishes the item and removes the cursor in the same transaction
// the Worker acknowledges that revision in. Any other live revision (newer
// work during the pass, or a re-created queue row) means this pass mixed
// intervals: its cursor is dropped and its publication is skipped, so the
// newer revision is never acknowledged and one fresh pass from the new head
// publishes alone. Link flags this pass wrote were each computed from the
// evidence current when that link was read; only the item aggregate is held
// back.
func (h *availabilityHolder) finish(ctx context.Context, tx *sql.Tx) (bool, error) {
	var live int64
	err := tx.QueryRowContext(ctx, `SELECT revision FROM catalog_dirty WHERE domain=? AND entity_id=?`, DomainAvailability, h.entityID).Scan(&live)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		live = 0
	case err != nil:
		return false, err
	}
	if live != 0 && live != h.revision {
		// Rerun owed: finish the finite pass at its own cursor without
		// resetting it mid-walk, and let the queued revision start from head.
		_, err := tx.ExecContext(ctx, `DELETE FROM catalog_availability_work WHERE entity_id=? AND revision=?`, h.entityID, h.revision)
		return false, err
	}
	if err := h.publish(ctx, tx); err != nil {
		return false, err
	}
	if err := CompleteInfoAvailabilityTx(ctx, tx, h.entityID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_availability_work WHERE entity_id=? AND revision=?`, h.entityID, h.revision); err != nil {
		return false, err
	}
	return live == h.revision, nil
}

// deriveAvailabilityKey runs one turn of an item's availability pass and
// reports whether the pass finished and how many raw rows it read. The caller's
// transaction holds the cursor and the dirty acknowledgement together.
func deriveAvailabilityKey(ctx context.Context, tx *sql.Tx, k Key, limit int) (bool, int, error) {
	if err := BeginInfoAvailabilityTx(ctx, tx, k.ID); err != nil {
		return false, 0, err
	}
	h, err := availabilityHolderTx(ctx, tx, k)
	if err != nil {
		return false, 0, err
	}
	visits, err := h.advance(ctx, tx, limit)
	if err != nil {
		return false, visits, err
	}
	if !h.walked {
		return false, visits, h.save(ctx, tx)
	}
	done, err := h.finish(ctx, tx)
	return done, visits, err
}
