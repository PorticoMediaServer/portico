package playback

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
)

type discPlayBorrowKey struct{}

// withContext carries only the opaque original borrow through the two actual
// accepting writers. Every use repeats its own proof; the context value is not
// authority and is never accepted from a public request or reconstructed row.
func (b *discPlayBorrow) withContext(ctx context.Context) (context.Context, error) {
	if ctx == nil {
		return nil, playbackdisc.ErrCurrent
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.held(); err != nil {
		return nil, err
	}
	deadline, finite := b.caller.Deadline()
	if !finite {
		return nil, playbackdisc.ErrCurrent
	}
	life, cancel := context.WithDeadline(ctx, deadline)
	stop := context.AfterFunc(b.caller, cancel)
	b.contextMu.Lock()
	if b.released.Load() || len(b.contexts) >= 16 {
		b.contextMu.Unlock()
		stop()
		cancel()
		return nil, playbackdisc.ErrCurrent
	}
	b.contexts = append(b.contexts, func() { stop(); cancel() })
	b.contextMu.Unlock()
	if b.caller.Err() != nil {
		cancel()
	}
	return context.WithValue(life, discPlayBorrowKey{}, b), nil
}

func measuredDiscTitleRef(snapshot playbackdisc.Snapshot) (*catalog.DiscTitleRef, error) {
	return catalog.MeasuredDiscTitleRef(snapshot.Source.AssetID, snapshot.Source.InventoryObjectID, snapshot.Source.InventoryRevision,
		snapshot.Disc.Structure, snapshot.Disc.SelectedTitleKey, snapshot.Disc.FactsRevision)
}

func (b *discPlayBorrow) pendingTitleTx(ctx context.Context, tx *sql.Tx, s *Service, key playbackdisc.Key, id string) (*catalog.DiscTitleRef, error) {
	if s == nil || s.discInputs == nil || b == nil || b.owner == nil || b.owner.db != s.db || b.owner.store != s.discInputs.store {
		return nil, playbackdisc.ErrCurrent
	}
	if err := b.checkContinuationTx(ctx, tx); err != nil {
		return nil, err
	}
	b.measurement.mu.Lock()
	snapshot, bound := b.measurement.snapshot, b.measurement.bound
	b.measurement.mu.Unlock()
	if !bound || snapshot.ID != id || snapshot.SessionID != key.SessionID || snapshot.Generation != key.Generation {
		return nil, playbackdisc.ErrCurrent
	}
	if err := b.measurement.CheckTx(ctx, tx, snapshot); err != nil {
		return nil, err
	}
	var pending bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_disc_snapshots d JOIN playback_disc_lifecycle l ON l.snapshot_id=d.id
 JOIN playback_sessions p ON p.id=d.session_id AND p.generation=d.generation
 WHERE d.id=? AND d.session_id=? AND d.generation=? AND l.state='pending' AND l.sealed=1
 AND p.asset_id=? AND p.request_id=? AND p.state NOT IN('stopped','ended','failed'))`,
		id, key.SessionID, key.Generation, snapshot.Source.AssetID, snapshot.Operation.LegacyRequestID).Scan(&pending)
	if err != nil {
		return nil, err
	}
	if !pending {
		return nil, playbackdisc.ErrCurrent
	}
	ref, err := measuredDiscTitleRef(snapshot)
	if err != nil {
		return nil, err
	}
	if err = b.checkContinuationTx(ctx, tx); err != nil {
		return nil, err
	}
	return ref, nil
}
