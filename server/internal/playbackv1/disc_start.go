package playbackv1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/ownedcapture"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// PreparedStart is internal to the actual route composition, not a fabricated
// SessionView. A nil Session means genuine still-current private preparation;
// ordinary starts retain their actual session-backed result and errors.
type PreparedStart struct {
	Session      *SessionView
	Preparing    bool
	RetryAfterMs int
}
type originalDiscStartKey struct{}
type originalDiscStart struct {
	principal         identity.Principal
	device, key, body string
	private           bool // internal path classification only; never admission
}
type discStartPending struct{}

func (*discStartPending) Error() string { return "original Play input is preparing" }

func (s *Service) StartPrepared(ctx context.Context, c Caller, key string, req StartRequest) (PreparedStart, error) {
	if err := s.requireObservationProtocol(c); err != nil {
		return PreparedStart{}, err
	}
	if ctx == nil {
		return PreparedStart{}, disc.ErrProtocol
	}
	// Capture the complete original graph before an admission/read can yield.
	raw, err := json.Marshal(req)
	if err != nil {
		return PreparedStart{}, err
	}
	var original StartRequest
	if err = json.Unmarshal(raw, &original); err != nil {
		return PreparedStart{}, err
	}
	start := &originalDiscStart{principal: c.Principal, device: c.DeviceID, key: key, body: digest(original)}
	ctx = context.WithValue(ctx, originalDiscStartKey{}, start)
	v, preparing, err := s.Start(ctx, c, key, original)
	var pending *discStartPending
	if errors.As(err, &pending) {
		return PreparedStart{Preparing: true, RetryAfterMs: PreparingRetryMs}, nil
	}
	if err != nil {
		if start.private {
			err = discStartError(err)
		}
		return PreparedStart{}, err
	}
	return PreparedStart{Session: &v, Preparing: preparing, RetryAfterMs: v.RetryAfterMs}, nil
}

// prepareDiscStart runs under the normal account start admission lock, before
// minting a new candidate or reselecting a physical source on an original poll.
func (s *Service) prepareDiscStart(ctx context.Context, c Caller, key, body string, req StartRequest, base row, stored storedRequest) (context.Context, error) {
	original, enabled := ctx.Value(originalDiscStartKey{}).(*originalDiscStart)
	if !enabled || original == nil || s.Playback == nil {
		return ctx, nil
	}
	if original.principal != c.Principal || original.device != c.DeviceID || original.key != key || original.body != body {
		return ctx, playbackdisc.ErrCurrent
	}
	operation, err := s.Playback.FindDiscPlay(ctx, c.Principal, c.DeviceID, key, body)
	if err != nil {
		original.private = true
		return ctx, err
	}
	if operation != nil {
		original.private = true
	}
	if operation == nil {
		if base.kind != "vod" || base.partIndex != 0 || c.Principal.Grant == identity.GrantCastTarget || req.TransferID != "" || req.CastTransferID != "" {
			return ctx, nil
		}
		choice := playback.V1Choice{AdminMaxVideoBitrateBPS: c.AdminMaxVideoBitrateBPS, Remote: c.Remote}
		stored.Quality.apply(&choice)
		asset := base.version
		if asset == "" {
			asset = s.bestVersion(ctx, c.Principal, base.item, choice, req.ThroughputKbps)
		}
		if asset == "" {
			asset, err = s.Playback.SelectedAsset(ctx, base.item, &choice)
			if err != nil {
				return ctx, err
			}
		}
		var path, kind string
		err = dbwork.QueryRow(ctx, s.DB, `SELECT a.path,source.kind FROM catalog_assets a JOIN inventory_objects object ON object.asset_id=a.token JOIN library_sources source ON source.id=object.source_id WHERE a.token=? AND a.available=1 AND object.retired=0 AND object.state='available' AND source.enabled=1 AND source.identity_confirmed=1 ORDER BY object.id LIMIT 1`, asset).Scan(&path, &kind)
		if errors.Is(err, sql.ErrNoRows) {
			return ctx, nil
		}
		if err != nil {
			return ctx, err
		}
		if kind != "remote" || !assets.DiscImage(path) {
			return ctx, nil
		}
		original.private = true
		if c.Admit == nil {
			return ctx, identity.ErrForbidden
		}
		candidate := newID("ps_")
		var replacing playbackdisc.Key
		if req.ReplacesSessionID != "" {
			old, e := loadRow(ctx, s.DB, req.ReplacesSessionID)
			if e != nil || !owns(c, old) || old.device != c.DeviceID || old.ended != 0 {
				return ctx, ErrNotFound
			}
			replacing.SessionID = old.media
			if e = dbwork.QueryRow(ctx, s.DB, `SELECT generation FROM playback_sessions WHERE id=? AND state NOT IN('stopped','ended','failed')`, old.media).Scan(&replacing.Generation); e != nil {
				return ctx, e
			}
		}
		queue := playbackdisc.Operation{}
		if bound, ok := ctx.Value(queueStartCustodyKey{}).(queueStartCustody); ok {
			if bound.queue != base.queueID || bound.currentEntry != base.entryID {
				return ctx, ErrNotFound
			}
			queue = playbackdisc.Operation{QueueID: bound.queue, QueueEntryID: bound.currentEntry, QueueOwnerKind: bound.ownerKind, QueueOwnerID: bound.ownerID, QueueRevision: bound.revision, QueueCustodyGeneration: bound.generation}
		} else if base.queueID != "" || base.entryID != "" {
			return ctx, ErrNotFound
		}
		check := s.discStartCheck(c, key, body, base, asset, candidate, req.ReplacesSessionID, replacing, ctx)
		originalTitle := catalog.DiscTitleRef{}
		if req.DiscTitle != nil {
			originalTitle = *req.DiscTitle
		}
		operation, err = s.Playback.BeginDiscPlay(ctx, playback.DiscPlayOriginal{Principal: c.Principal, DeviceID: c.DeviceID, Key: key, BodyDigest: body, ItemID: base.item, AssetID: asset, CandidateID: candidate, PartIndex: base.partIndex, Queue: queue, Replaces: replacing, TitleRequest: originalTitle, Check: check})
		if err != nil {
			return ctx, err
		}
	}
	item, asset, candidate, part, err := operation.Original()
	if err != nil {
		return ctx, err
	}
	if item != base.item || part != base.partIndex || base.version != "" && base.version != asset {
		return ctx, playback.ErrStaleOffer
	}
	// This live request owns a DIFFERENT callback from the worker's retained
	// original. Its HTTP cancellation, caller/body/key and current capacity must
	// all remain valid through the accepting outer commit.
	check := s.discStartCheck(c, key, body, base, asset, candidate, req.ReplacesSessionID, playbackdisc.Key{}, ctx)
	bound, ready, err := operation.Continue(ctx, s.Playback, func(current context.Context, tx *sql.Tx, prepared playbackdisc.Preparation) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if prepared.Key.DeviceID != c.DeviceID || prepared.Key.StartKeyDigest != identity.Digest(key) || prepared.Binding.StartBodyDigest != body || prepared.Binding.ItemID != base.item || prepared.Binding.AssetID != asset {
			return playbackdisc.ErrCurrent
		}
		return check(current, tx)
	})
	if err != nil {
		return ctx, err
	}
	if !ready {
		return ctx, &discStartPending{}
	}
	return bound, nil
}

func (s *Service) discStartCheck(c Caller, key, body string, base row, asset, candidate, replaces string, replacing playbackdisc.Key, origin context.Context) func(context.Context, *sql.Tx) error {
	bound, queued := origin.Value(queueStartCustodyKey{}).(queueStartCustody)
	return func(ctx context.Context, tx *sql.Tx) error {
		if tx == nil || c.Admit == nil {
			return identity.ErrForbidden
		}
		boundContext, bindingErr := dbwork.BindSnapshot(ctx, tx)
		if bindingErr != nil {
			return bindingErr
		}
		ctx = boundContext
		var own bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_v1_sessions WHERE id=? AND device_id=? AND start_key=? AND start_digest=? AND account_id=? AND profile_id=? AND authority=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND version_id=? AND queue_id=? AND entry_id=? AND part_index=? AND ended_ms=0)`, candidate, c.DeviceID, key, body, c.Principal.AccountID, c.Principal.ProfileID, c.Principal.Authority, base.item, asset, base.queueID, base.entryID, base.partIndex).Scan(&own)
		if err != nil {
			return err
		}
		var conflict bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_v1_sessions WHERE device_id=? AND start_key=? AND id<>?)`, c.DeviceID, key, candidate).Scan(&conflict); err != nil {
			return err
		}
		if conflict {
			return ErrIdempotencyMismatch
		}
		var linked bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities item JOIN catalog_asset_links link ON link.entity_id=item.id JOIN catalog_assets a ON a.id=link.asset_id WHERE pid(item.public_id)=? AND a.token=? AND item.retired=0 AND a.available=1 AND link.available=1)`, base.item, asset).Scan(&linked); err != nil {
			return err
		}
		if !linked {
			return ErrNotFound
		}
		if queued {
			q, e := loadQueue(ctx, tx, bound.queue)
			if e != nil {
				return e
			}
			if !ownsQueue(c, q) || q.ownerKind != bound.ownerKind || q.ownerID != bound.ownerID || q.custody != bound.generation || q.revision != bound.revision || (q.currentEntry != bound.originalEntry && (!own || q.currentEntry != bound.currentEntry)) || q.session != bound.session && (!own || q.session != candidate) {
				return ErrRevision
			}
		}
		admit := ctx
		pendingMedia := ""
		if !own {
			pendingMedia, err = s.Playback.PendingDiscPlayMediaTx(ctx, tx, candidate)
			if err != nil {
				return err
			}
		}
		if own {
			admit = WithReplan(ctx, candidate, "")
		} else if replaces != "" || pendingMedia != "" {
			admit = WithReplan(ctx, replaces, pendingMedia)
		}
		limit, e := c.Admit(admit, base.item)
		if e != nil {
			return e
		}
		if own && c.Remote && limit > 0 {
			var media string
			if e = tx.QueryRowContext(ctx, `SELECT media_session_id FROM playback_v1_sessions WHERE id=?`, candidate).Scan(&media); e != nil {
				return e
			}
			plan, e := s.Playback.SessionPlan(ctx, media)
			if e != nil {
				return e
			}
			if plan == nil {
				return playback.ErrIncompatible
			}
			if need := planKbps(*plan); need <= 0 || need*1000 > limit {
				return playback.ErrIncompatible
			}
		}
		if replacing.SessionID != "" && !own {
			var current bool
			if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_sessions WHERE id=? AND generation=? AND account_id=? AND profile_id=? AND state NOT IN('stopped','ended','failed'))`, replacing.SessionID, replacing.Generation, c.Principal.AccountID, c.Principal.ProfileID).Scan(&current); e != nil {
				return e
			}
			if !current {
				return playbackdisc.ErrCurrent
			}
		}
		return ctx.Err()
	}
}

// Map only the new private input outcomes onto existing ordinary errors.
// None of capacity, source retirement, expiry or unsupported input becomes202.
func discStartError(err error) error {
	switch {
	case errors.Is(err, playbackdisc.ErrConflict):
		return errors.Join(err, ErrIdempotencyMismatch)
	case errors.Is(err, playback.ErrStaleOffer):
		return errors.Join(err, ErrRevision)
	case errors.Is(err, playbackdisc.ErrCurrent), errors.Is(err, playbackdisc.ErrCapacity), errors.Is(err, ownedcapture.ErrCurrent), errors.Is(err, storage.ErrRemoteChanged), errors.Is(err, storage.ErrRemoteOffline), errors.Is(err, storage.ErrRemoteLimit), errors.Is(err, mediasource.ErrSourceChanged), errors.Is(err, context.DeadlineExceeded):
		return errors.Join(err, catalog.ErrSourceBusy)
	case errors.Is(err, disc.ErrRemoteUnqualified), errors.Is(err, storage.ErrRemoteRange):
		return errors.Join(err, playback.ErrIncompatible)
	default:
		return err
	}
}
