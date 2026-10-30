package playback

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
)

// legacySnapshotTx supplies facts only while the admitted current POST holds
// its original sealed borrow. Neither a ticket, stored ready row nor a public
// choice can construct this path. It remains separate from active-reader proof.
func (b *discPlayBorrow) legacySnapshotTx(ctx context.Context, tx *sql.Tx, s *Service, p identity.Principal, item, asset string) (playbackdisc.Snapshot, error) {
	if s == nil || s.discInputs == nil || b == nil || b.owner == nil || b.owner.db != s.db || b.owner.store != s.discInputs.store || b.owner.captures != s.discInputs.captures {
		return playbackdisc.Snapshot{}, disc.ErrProtocol
	}
	if err := b.checkContinuationTx(ctx, tx); err != nil {
		return playbackdisc.Snapshot{}, err
	}
	b.owner.mu.Lock()
	original := b.owner.operation.Binding
	b.owner.mu.Unlock()
	actor := original.Actor
	if item != original.ItemID || asset != original.AssetID || p.Hash != actor.SessionHash || p.ServerID != actor.ServerID || p.Authority != actor.Authority || p.AccountID != actor.AccountID || p.ProfileID != actor.ProfileID || p.DeviceID != actor.DeviceID || p.Grant != actor.Grant || int64(p.Epoch) != actor.Epoch {
		return playbackdisc.Snapshot{}, playbackdisc.ErrCurrent
	}
	b.measurement.mu.Lock()
	snapshot := b.measurement.snapshot
	b.measurement.mu.Unlock()
	if snapshot.Actor != actor || snapshot.Source != original.Source || snapshot.Operation != original.Queue {
		return playbackdisc.Snapshot{}, playbackdisc.ErrCurrent
	}
	return snapshot, b.held()
}

func (b *discPlayBorrow) checkLegacyRequestTx(ctx context.Context, tx *sql.Tx, s *Service, p identity.Principal, item, asset, request string, choice *V1Choice, replacement *SessionReplacement) error {
	if choice == nil || !choice.PendingActivation || choice.PrivateFor != "" || choice.AssetID != asset || choice.AudioPlanV2 || choice.AudioServerRender || choice.SubtitleKind != "" || choice.SubtitleBesideStream {
		return disc.ErrRemoteUnqualified
	}
	snapshot, err := b.legacySnapshotTx(ctx, tx, s, p, item, asset)
	if err != nil {
		return err
	}
	if choice.DiscTitle != nil {
		selected, err := measuredDiscTitleRef(snapshot)
		if err != nil {
			return err
		}
		if *selected != *choice.DiscTitle {
			return ErrStaleOffer
		}
	}
	b.owner.mu.Lock()
	original := b.owner.operation.Binding
	b.owner.mu.Unlock()
	if sessionRequestKey(p, request) != original.Queue.LegacyRequestID {
		return playbackdisc.ErrCurrent
	}
	if replacement == nil {
		if original.ReplacesLegacySessionID != "" || original.ReplacesGeneration != 0 {
			return playbackdisc.ErrCurrent
		}
	} else if !replacement.Deferred || replacement.ID != original.ReplacesLegacySessionID || replacement.Generation != original.ReplacesGeneration {
		return playbackdisc.ErrCurrent
	}
	return b.held()
}

// createLegacy is an actual legacy writer consumer, not a second measurement
// path. Only the current POST borrow can supply the private choice. The caller
// must retain that borrow until its final V1 writer commits or rolls back; the
// returned candidate is still pending and cannot authorize a reader or grant.
func (b *discPlayBorrow) createLegacy(ctx context.Context, s *Service, p identity.Principal, item, request string, choice V1Choice, replacement *SessionReplacement) (Session, error) {
	if ctx == nil || s == nil || b == nil || choice.discBorrow != nil || !choice.PendingActivation {
		return Session{}, disc.ErrProtocol
	}
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	if err := b.held(); err != nil {
		return Session{}, err
	}
	ctx, err := b.withContext(ctx)
	if err != nil {
		return Session{}, err
	}
	choice.discBorrow = b
	return s.CreateV1(ctx, p, item, request, choice, replacement)
}
