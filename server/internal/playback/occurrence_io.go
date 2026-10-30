package playback

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"time"
)

// The original registered run is retained through every provider/process
// admission. Its durable proof is fresh and cannot borrow a caller snapshot.
func (h *HLS) occurrenceContext(ctx context.Context, id string, entry *occurrenceEntry, run *occurrenceRun) context.Context {
	return withOccurrenceContext(ctx, h.db, &h.occurrences, id, entry, run)
}
func withOccurrenceContext(ctx context.Context, db *sql.DB, registry *occurrenceRegistry, id string, entry *occurrenceEntry, run *occurrenceRun) context.Context {
	ctx = context.WithValue(ctx, occurrenceOwnerKey{}, &occurrenceOwner{registry: registry, entry: entry, run: run, id: id})
	ctx = childprocess.WithOwnedIO(ctx, func() error {
		if dbwork.Snapshot(ctx) != nil {
			return errOccurrenceClaimed
		}
		if !registry.ownsRun(id, entry, run) {
			return errOccurrenceClaimed
		}
		var live bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_sessions WHERE id=? AND generation=? AND request_id=? AND item_id=? AND asset_id=? AND state NOT IN ('stopped','ended','failed') AND expires_at>?)`, id, run.generation, run.requestID, run.itemID, run.assetID, time.Now().UTC().Format(time.RFC3339)).Scan(&live)
		if err != nil {
			return err
		}
		if !live || !registry.ownsRun(id, entry, run) {
			return errOccurrenceClaimed
		}
		return nil
	})
	return childprocess.WithPublication(ctx, func() (func(), error) {
		if dbwork.Snapshot(ctx) != nil {
			return nil, errOccurrenceClaimed
		}
		release, err := registry.publication(id, entry, run)
		if err != nil {
			return nil, err
		}
		var live bool
		err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_sessions WHERE id=? AND generation=? AND request_id=? AND item_id=? AND asset_id=? AND state NOT IN ('stopped','ended','failed') AND expires_at>?)`, id, run.generation, run.requestID, run.itemID, run.assetID, time.Now().UTC().Format(time.RFC3339)).Scan(&live)
		if err != nil || !live {
			release()
			if err != nil {
				return nil, err
			}
			return nil, errOccurrenceClaimed
		}
		return release, nil
	})
}

type occurrenceOwnerKey struct{}
type occurrenceOwner struct {
	registry *occurrenceRegistry
	entry    *occurrenceEntry
	run      *occurrenceRun
	id       string
}

// producerWrite repeats ownership inside the accepting writer as well as the
// process-side barrier. A detached late finalizer cannot update a successor's
// artifact/window/diagnostics merely because its id and segment index match.
func (h *HLS) producerWrite(ctx context.Context, id string, class dbwork.Class, query string, args ...any) (sql.Result, error) {
	owner, _ := ctx.Value(occurrenceOwnerKey{}).(*occurrenceOwner)
	if owner == nil {
		return dbwork.ExecWrite(ctx, h.db, class, query, args...)
	}
	if owner.id != id {
		return nil, errOccurrenceClaimed
	}
	if !owner.registry.ownsRun(id, owner.entry, owner.run) {
		return nil, errOccurrenceClaimed
	}
	var result sql.Result
	err := dbwork.WithWriteTxContext(ctx, h.db, class, func(ctx context.Context, tx *sql.Tx) error {
		var live bool
		run := owner.run
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_sessions WHERE id=? AND generation=? AND request_id=? AND item_id=? AND asset_id=? AND state NOT IN ('stopped','ended'))`, id, run.generation, run.requestID, run.itemID, run.assetID).Scan(&live); err != nil {
			return err
		}
		if !live {
			return errOccurrenceClaimed
		}
		var err error
		result, err = tx.ExecContext(ctx, query, args...)
		return err
	})
	return result, err
}

// Called only under h.mu; a late old defer may remove only its exact slot.
func (h *HLS) installOccurrenceLocked(key string, run *occurrenceRun, cancel context.CancelFunc) {
	if h.activeRuns == nil {
		h.activeRuns = make(map[string]*occurrenceRun)
	}
	h.activeRuns[key] = run
	h.active[key] = cancel
}
func (h *HLS) removeOccurrenceLocked(key string, run *occurrenceRun) {
	if h.activeRuns[key] == run {
		delete(h.activeRuns, key)
		delete(h.active, key)
	}
}

func validateOwnedInput(ctx context.Context, validate func(context.Context) error) error {
	owned, err := childprocess.OwnedIOContext(ctx)
	if err != nil {
		return err
	}
	if err = validate(owned); err != nil {
		return err
	}
	return childprocess.CheckOwnedIO(ctx)
}
func (r *occurrenceRegistry) held(id string, entry *occurrenceEntry) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.entries[id] == entry && entry.blocked && !entry.committed
}
func failureDuringClaim(ctx context.Context) bool {
	owner, _ := ctx.Value(occurrenceOwnerKey{}).(*occurrenceOwner)
	return owner != nil && owner.registry.held(owner.id, owner.entry)
}

// An old primary window may stop only the child slots of its exact installed
// occurrence. Same id/ordinal alone is never a cancellation capability.
// Caller holds h.mu; only captured context cancel functions are invoked.
func (h *HLS) cancelOccurrenceRenditionsLocked(id string, parent *occurrenceRun) {
	if parent == nil || h.activeRuns[id] != parent {
		return
	}
	for key, child := range h.activeRuns {
		if key == id || producerSession(key) != id || child == nil {
			continue
		}
		if child.generation == parent.generation && child.requestID == parent.requestID && child.itemID == parent.itemID && child.assetID == parent.assetID {
			if cancel := h.active[key]; cancel != nil {
				cancel()
			}
		}
	}
}
