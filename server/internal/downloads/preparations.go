package downloads

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/reading"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// ReasonRemoved marks a claim the viewer deliberately gave up, as opposed to
// one the server cancelled or expired underneath them.
const ReasonRemoved = "removed"

// LibraryGuard is the caller's visibility check. The routes layer owns library
// permission; this package only asks whether this viewer may see that library,
// so an unreadable library is rejected per target instead of failing a batch.
//
// It is handed the admitting transaction rather than left to open its own. The
// database is opened with a single connection, so a guard that queried outside
// this transaction would deadlock against the transaction that called it.
type LibraryGuard func(tx *sql.Tx, library string) error

// Submit admits one preparation request. It is idempotent on operationId: a
// replayed request returns the batch the first one produced, byte for byte,
// rather than admitting the work twice.
func (s *Service) Submit(ctx context.Context, p identity.Principal, r Request, guard LibraryGuard) (Batch, error) {
	return s.submit(ctx, p, r, guard, nil, false)
}
func (s *Service) submit(ctx context.Context, p identity.Principal, r Request, guard LibraryGuard, publication *reading.OfflineDocument, preflight bool) (Batch, error) {
	out := Batch{Items: []Preparation{}, Rejected: []Rejection{}}
	if !validID.MatchString(r.OperationID) || (r.VersionID != "" && (!validID.MatchString(r.VersionID) || r.MediaID == "" || len(r.MediaIDs) > 0 || r.NextAfterMediaID != "")) {
		return out, ErrInput
	}
	if r.Quality != "" {
		if e := preparationQuality(string(r.Quality)); e != nil {
			return out, e
		}
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassForegroundTransfer)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	now := s.millis()
	key := operations.ViewerKey(p)
	scope := "downloads-prepare:" + key
	// Preserve the existing video omission/original receipt identity. Music
	// omission is distinct because it resolves through its own saved default.
	if r.Quality == "" {
		targets, _, err := expandTargets(ctx, tx, r)
		if err != nil {
			return out, err
		}
		music := false
		for _, id := range targets {
			var song bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities WHERE public_id=pid_blob(?) AND kind=7)`, id).Scan(&song); err != nil {
				return out, err
			}
			music = music || song
		}
		if !music {
			r.Quality = QualityOriginal
		}
	}
	raw, digest, e := operations.Receipt(tx, scope, r.OperationID, r, now)
	if e != nil {
		return out, e
	}
	if raw != "" {
		if e = json.Unmarshal([]byte(raw), &out); e != nil {
			return out, e
		}
		items := out.Items[:0]
		for _, item := range out.Items {
			current, err := readPreparation(tx.QueryRowContext(ctx, `SELECT `+preparationColumns+` FROM download_preparations WHERE id=? AND profile_key=?`, item.ID, key))
			if errors.Is(err, sql.ErrNoRows) {
				err = identity.ErrContentRestricted
			} else if err == nil {
				err = s.visiblePreparation(ctx, tx, p, current)
			}
			if errors.Is(err, identity.ErrContentRestricted) {
				if r.MediaID != "" {
					return Batch{}, ErrNotFound
				}
				out.Rejected = append(out.Rejected, Rejection{item.ItemID, ReasonItemDeleted})
				continue
			}
			if err != nil {
				return out, err
			}
			items = append(items, item)
		}
		out.Items = items
		if out.Accepted > len(items) {
			out.Accepted = len(items)
		}
		out.Duplicate = true
		return out, nil
	}
	allowed, e := ProfileAllowsDownloads(tx, p.Viewer)
	if e != nil {
		return out, e
	}
	if !allowed {
		return out, ErrPolicy
	}
	// Only a genuinely new, syntactically valid and currently entitled intent
	// may acquire publication bytes. Replay returned above without re-inspection.
	if !preflight && r.MediaID != "" {
		var publicationTarget bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities WHERE public_id=pid_blob(?) AND kind IN(20,21))`, r.MediaID).Scan(&publicationTarget); e != nil {
			return out, e
		}
		if publicationTarget {
			gated.Rollback()
			doc, err := s.publicationForRequest(ctx, p, r)
			if err != nil {
				return out, err
			}
			// Re-enter the accepting transaction: operation replay, policy, source and
			// descriptor revision are all rechecked after the outside-writer preflight.
			return s.submit(ctx, p, r, guard, doc, true)
		}
	}
	targets, origin, e := expandTargets(ctx, tx, r)
	if e != nil {
		return out, e
	}
	settings, e := readSettings(tx)
	if e != nil {
		return out, e
	}
	batch := identity.Digest(identity.Token())
	out.BatchID = batch
	refused := 0
	// SEC-02: a title that doesn't exist, one in a library the viewer isn't
	// given and one over its restrictions get the same answer: 404 for a
	// one-title request, an item_deleted rejection in a batch. (A one-title
	// request used to answer 404 for a restricted title and 201 with a
	// rejection for an absent one, which told the two apart.)
	unreachable := func(item string) bool {
		if r.MediaID != "" {
			return false
		}
		out.Rejected = append(out.Rejected, Rejection{item, ReasonItemDeleted})
		return true
	}
	room := newStorageAllowance(tx)
	for _, item := range targets {
		entity, err := entityid.Resolve(ctx, tx, item)
		if errors.Is(err, entityid.ErrNotFound) {
			if !unreachable(item) {
				return out, ErrNotFound
			}
			continue
		}
		if err != nil {
			return out, err
		}
		library, err := libraryOf(ctx, tx, item)
		if errors.Is(err, sql.ErrNoRows) {
			if !unreachable(item) {
				return out, ErrNotFound
			}
			continue
		}
		if err != nil {
			return out, err
		}
		if err = contentaccess.VisibleItemTx(ctx, tx, p, item); err != nil {
			if !errors.Is(err, identity.ErrContentRestricted) {
				return out, err
			}
			if !unreachable(item) {
				return out, ErrNotFound
			}
			continue
		}
		if guard != nil {
			if err = guard(tx, library); err != nil {
				if !unreachable(item) {
					return out, ErrNotFound
				}
				continue
			}
		}
		if err = retireStrays(ctx, tx, room, key, entity, item, now); err != nil {
			return out, err
		}

		found, err := resolveVersionSource(ctx, tx, item, r.VersionID)
		if reason := sourceReason(found, err); reason != "" {
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return out, err
			}
			out.Rejected = append(out.Rejected, Rejection{item, reason})
			continue
		}
		if err = s.checkPublicationTx(ctx, tx, p, found, publication); err != nil {
			return out, err
		}
		requested, err := requestedDownloadQuality(ctx, tx, p, string(r.Quality), found)
		if err != nil {
			return out, err
		}
		quality := s.resolveQuality(ctx, tx, playback.CapabilityDocumentTx(ctx, tx, p), requested, found)
		// A live claim on the same item and quality is the same claim. Returning
		// it keeps a client that retried a batch from accumulating duplicates.
		// One that failed is what the viewer is asking to have another go at:
		// asking again starts it again, rather than handing back the failure.
		existing, err := readPreparation(tx.QueryRowContext(ctx, `SELECT `+preparationColumns+` FROM download_preparations WHERE profile_key=? AND item_id=? AND quality=? AND source_asset_id=? AND state IN('queued','running','ready','paused','failed') ORDER BY CASE state WHEN 'ready' THEN 0 WHEN 'running' THEN 1 WHEN 'queued' THEN 2 WHEN 'paused' THEN 3 ELSE 4 END,id LIMIT 1`, key, entity, quality, found.Asset))
		if err == nil && existing.State == StateFailed {
			if err = s.restart(ctx, tx, p, room, existing, now); errors.Is(err, ErrStorageFull) {
				out.Rejected = append(out.Rejected, Rejection{item, ReasonStorageFull})
				refused++
				continue
			} else if err != nil {
				return out, err
			}
			if existing, err = readPreparation(tx.QueryRowContext(ctx, `SELECT `+preparationColumns+` FROM download_preparations WHERE id=?`, existing.ID)); err != nil {
				return out, err
			}
			out.Items = append(out.Items, s.publish(existing, settings.RetentionDays))
			out.Accepted++
			continue
		}
		if err == nil {
			out.Items = append(out.Items, s.publish(existing, settings.RetentionDays))
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		recipe := chooseRecipe(ctx, tx, playback.CapabilityDocumentTx(ctx, tx, p), quality, found, s.deliveryConfig())
		if reason := s.preparationReason(ctx, tx, quality, recipe, found); reason != "" {
			out.Rejected = append(out.Rejected, Rejection{item, reason})
			continue
		}
		estimate, estimated := estimateBytes(quality, recipe, found)
		if err = room.admit(storedQualityBudget(quality, recipe), estimate); errors.Is(err, ErrStorageFull) {
			out.Rejected = append(out.Rejected, Rejection{item, ReasonStorageFull})
			refused++
			continue
		} else if err != nil {
			return out, err
		}
		id := identity.Digest(identity.Token())
		flag := 0
		if estimated {
			flag = 1
		}
		title, err := readTitle(ctx, tx, entity)
		if err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO download_preparations(publication_json,id,profile_key,authority,account_id,profile_id,item_id,item_public,library_id,title_json,source_asset_id,quality,recipe,origin,batch_id,state,bytes_total,estimated,created_ms,updated_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'queued',?,?,?,?)`,
			publicationJSON(publication), id, key, p.Authority, p.AccountID, p.ProfileID, entity, item, library, titleSnapshot(title), found.Asset, quality, recipe, origin, batch, estimate, flag, now, now); err != nil {
			return out, err
		}
		fresh, err := readPreparation(tx.QueryRowContext(ctx, `SELECT `+preparationColumns+` FROM download_preparations WHERE id=?`, id))
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, s.publish(fresh, settings.RetentionDays))
		out.Accepted++
		principal, _ := json.Marshal(p)
		if _, err = tx.ExecContext(ctx, `INSERT INTO download_preparation_authority(preparation_id,principal) VALUES(?,?)`, id, string(principal)); err != nil {
			return out, err
		}

	}
	// A request that named one thing and got nothing but a full store should say
	// so plainly rather than answering an empty batch a client has to interpret.
	if out.Accepted == 0 && len(out.Items) == 0 && refused > 0 && refused == len(out.Rejected) {
		return out, ErrStorageFull
	}
	if len(out.Items) == 0 && len(out.Rejected) == 0 {
		return out, ErrNotFound
	}
	if e = operations.Audit(tx, now, operations.AccountKey(p), "downloads.prepare", batch, int64(out.Accepted)); e != nil {
		return out, e
	}
	if e = operations.SaveReceipt(tx, scope, r.OperationID, digest, out, now); e != nil {
		return out, e
	}
	if e = gated.Commit(); e != nil {
		return out, e
	}
	s.signal()
	return out, nil
}

// retireStrays ends a live claim that holds this title's number in the
// catalogue but was made for a different title: the number was the removed
// title's and has been given to a new one. Housekeeping would end it on its
// next pass; a request for the new title must not be answered with it, or
// refused by it, in the meantime.
func retireStrays(ctx context.Context, tx *sql.Tx, room *storageAllowance, key string, entity int64, item string, now int64) error {
	rows, e := tx.QueryContext(ctx, `SELECT id FROM download_preparations WHERE profile_key=? AND item_id=? AND item_public<>? AND state IN('queued','running','ready','paused','failed')`, key, entity, item)
	if e != nil {
		return e
	}
	ids, e := collectIDs(rows)
	if e != nil {
		return e
	}
	for _, id := range ids {
		if e = endClaim(ctx, tx, id, StateUnavailable, ReasonItemDeleted, now, `('queued','running','ready','paused','failed')`); e != nil {
			return e
		}
	}
	if len(ids) > 0 {
		room.ended()
	}
	return nil
}

// restart puts a failed or ended claim back in the queue from nothing, under
// the authority of the viewer asking now.
func (s *Service) restart(ctx context.Context, tx *sql.Tx, p identity.Principal, room *storageAllowance, current row, now int64) error {
	var device string
	e := tx.QueryRowContext(ctx, `SELECT device_id FROM download_preparation_authority WHERE preparation_id=?`, current.ID).Scan(&device)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if device != "" {
		if _, _, e = s.requestAccess(ctx, tx, p, device); e != nil {
			return e
		}
	}
	principal, _ := json.Marshal(p)
	if _, e = tx.ExecContext(ctx, `UPDATE download_preparation_authority SET principal=?,optimization_requested=0,optimization_operation='' WHERE preparation_id=?`, string(principal), current.ID); e != nil {
		return e
	}
	var recipe string
	if e = tx.QueryRowContext(ctx, `SELECT recipe FROM download_preparations WHERE id=?`, current.ID).Scan(&recipe); e != nil {
		return e
	}
	if e = room.admit(storedQualityBudget(current.Quality, recipe), current.Progress.BytesTotal); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM download_grants WHERE preparation_id=?`, current.ID); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE download_preparations SET state='queued',reason='',artifact_kind='',artifact_ref='',artifact_digest='',artifact_container='',source_modified_ns=0,hash_state=NULL,bytes_done=0,started_ms=0,ready_ms=0,used_ms=0,revision=revision+1,updated_ms=? WHERE id=?`, now, current.ID)
	return e
}

// legal reports whether an action is offered from a state. The published
// actions list and this fence are the same table, so a client that renders the
// controls the server published never has an action refused as illegal.
func legal(state, action string) bool {
	for _, v := range actionsFor(state) {
		if v == action {
			return true
		}
	}
	return false
}

// Act applies one fenced action. Every action names the revision the client
// read; an action against a stale revision is refused with the current row so
// the client can re-render rather than retry blind.
func (s *Service) Act(ctx context.Context, p identity.Principal, id string, c Command) (Preparation, error) {
	var out Preparation
	if !validID.MatchString(id) || !validID.MatchString(c.OperationID) {
		return out, ErrInput
	}
	switch c.Action {
	case ActionPause, ActionResume, ActionCancel, ActionRetry, ActionRemove:
	default:
		return out, ErrInput
	}
	gated2, e := dbwork.Begin(ctx, s.db, dbwork.ClassForegroundTransfer)
	if e != nil {
		return out, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	now := s.millis()
	key := operations.ViewerKey(p)
	scope := "downloads-action:" + key
	raw, digest, e := operations.Receipt(tx, scope, c.OperationID, []any{id, c}, now)
	if e != nil {
		return out, e
	}
	settings, e := readSettings(tx)
	if e != nil {
		return out, e
	}
	current, e := readPreparation(tx.QueryRowContext(ctx, `SELECT `+preparationColumns+` FROM download_preparations WHERE id=? AND profile_key=?`, id, key))
	if errors.Is(e, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if e != nil {
		return out, e
	}
	if e = s.visiblePreparation(ctx, tx, p, current); e != nil {
		if errors.Is(e, identity.ErrContentRestricted) {
			return out, ErrNotFound
		}
		return out, e
	}
	if raw != "" {
		return out, json.Unmarshal([]byte(raw), &out)
	}
	if current.Revision != c.ExpectedRevision {
		return s.publish(current, settings.RetentionDays), ErrConflict
	}
	if !legal(current.State, c.Action) {
		return s.publish(current, settings.RetentionDays), ErrConflict
	}
	switch c.Action {
	case ActionPause:
		_, e = tx.ExecContext(ctx, `UPDATE download_preparations SET state='paused',reason='',revision=revision+1,updated_ms=? WHERE id=?`, now, id)
	case ActionResume:
		_, e = tx.ExecContext(ctx, `UPDATE download_preparations SET state='queued',reason='',revision=revision+1,updated_ms=? WHERE id=?`, now, id)
	case ActionCancel, ActionRemove:
		reason := ReasonCancelled
		if c.Action == ActionRemove {
			reason = ReasonRemoved
		}
		// Giving up the claim ends what the server will transfer for it. A copy
		// already on a device is not the claim: it is taken off a device by
		// removing it there, or by withdrawing its receipt (receipts/revoke).
		if _, e = tx.ExecContext(ctx, `DELETE FROM download_grants WHERE preparation_id=?`, id); e != nil {
			return out, e
		}
		_, e = tx.ExecContext(ctx, `UPDATE download_preparations SET state='cancelled',reason=?,hash_state=NULL,bytes_done=0,revision=revision+1,updated_ms=? WHERE id=?`, reason, now, id)
	case ActionRetry:
		// A claim that ended (unavailable) is outside the one-live-claim rule,
		// so a newer claim for the same title, version and quality may exist.
		// That newer claim is the retry: this one steps aside for it instead of
		// colliding with it.
		var sibling string
		e = tx.QueryRowContext(ctx, `SELECT other.id FROM download_preparations mine JOIN download_preparations other ON other.profile_key=mine.profile_key AND other.item_id=mine.item_id AND other.quality=mine.quality AND other.source_asset_id=mine.source_asset_id AND other.id<>mine.id AND other.state IN('queued','running','ready','paused','failed') WHERE mine.id=?`, id).Scan(&sibling)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		if e == nil {
			if _, e = tx.ExecContext(ctx, `UPDATE download_preparations SET state='cancelled',reason=?,hash_state=NULL,revision=revision+1,updated_ms=? WHERE id=?`, ReasonRemoved, now, id); e != nil {
				return out, e
			}
			id = sibling
			break
		}
		e = s.restart(ctx, tx, p, newStorageAllowance(tx), current, now)
	}
	if e != nil {
		return out, e
	}
	updated, e := readPreparation(tx.QueryRowContext(ctx, `SELECT `+preparationColumns+` FROM download_preparations WHERE id=?`, id))
	if e != nil {
		return out, e
	}
	out = s.publish(updated, settings.RetentionDays)
	if e = operations.Audit(tx, now, operations.AccountKey(p), "downloads."+c.Action, id, out.Revision); e != nil {
		return out, e
	}
	if e = operations.SaveReceipt(tx, scope, c.OperationID, digest, out, now); e != nil {
		return out, e
	}
	if e = gated2.Commit(); e == nil {
		s.signal()
	}
	return out, e
}
