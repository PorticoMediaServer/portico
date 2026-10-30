package playback

import (
	"context"
	"database/sql"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/ownedcapture"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
)

// DiscPlayOriginal is an internal actual-start handoff, never a wire request or
// permission flag. The V1 owner has selected and frozen this physical target;
// Check must reprove its original caller, content, source selection, queue and
// idempotency/capacity in each provided transaction without selecting again.
type DiscPlayOriginal struct {
	Principal                                               identity.Principal
	DeviceID, Key, BodyDigest, ItemID, AssetID, CandidateID string
	PartIndex                                               int
	Queue                                                   playbackdisc.Operation
	Replaces                                                playbackdisc.Key
	TitleRequest                                            catalog.DiscTitleRef
	TitleKey                                                string
	Check                                                   func(context.Context, *sql.Tx) error
}

// DiscPlayOperation retains one original entry, not a recoverable receipt or a
// playable session. Copies attach to the same entry/claim and finite lifetime.
type DiscPlayOperation struct {
	factory *discPlayFactory
	entry   *discPlayFactoryEntry
}

// ConfigureDiscPlay runs once during actual composition; no operation or byte
// work is started. Recovery retires old unaccepted rows, never resumes them.
// Limits are explicit from composition and use the EXISTING probe-slot budget.
func (s *Service) ConfigureDiscPlay(ctx context.Context, identities *identity.Service, captures *ownedcapture.Service, conditional DiscConditionalSource, limits playbackdisc.PreparationLimits) error {
	if s == nil || identities == nil || s.hls == nil || s.hls.ctx == nil || s.probe == nil || s.discFactory != nil {
		return disc.ErrRemoteUnqualified
	}
	if s.discInputs == nil {
		if err := s.ConfigureDiscInputs(identities.ID(), captures); err != nil {
			return err
		}
	}
	if s.discInputs.captures != captures {
		return disc.ErrProtocol
	}
	f, err := newDiscPlayFactory(discPlayFactoryOptions{Lifetime: s.hls.ctx, DB: s.db, Identity: identities, Store: s.discInputs.store, Captures: captures, Tool: s.discInputs.tool, Probe: *s.probe, Tools: s.discInputs.tools, Conditional: conditional, Limits: limits, ProbeSlots: s.probeSlots})
	if err != nil {
		return err
	}
	if err = f.recoverUnaccepted(ctx); err != nil {
		return err
	}
	s.discInputs.conditional, s.discFactory = conditional, f
	return nil
}

func (s *Service) CloseDiscPlay(ctx context.Context) error {
	if s == nil || s.discFactory == nil {
		return nil
	}
	return s.discFactory.close(ctx)
}

func (s *Service) FindDiscPlay(ctx context.Context, p identity.Principal, device, key, body string) (*DiscPlayOperation, error) {
	if s == nil || s.discFactory == nil {
		return nil, nil
	}
	e, found, err := s.discFactory.findOriginal(ctx, playbackdisc.PreparationKey{DeviceID: device, StartKeyDigest: identity.Digest(key)}, p, body)
	if err != nil || !found {
		return nil, err
	}
	return &DiscPlayOperation{factory: s.discFactory, entry: e}, nil
}

func (s *Service) BeginDiscPlay(ctx context.Context, original DiscPlayOriginal) (*DiscPlayOperation, error) {
	if s == nil || s.discFactory == nil || ctx == nil || dbwork.Snapshot(ctx) != nil || original.Check == nil || original.DeviceID == "" || original.Principal.DeviceID != original.DeviceID {
		return nil, disc.ErrRemoteUnqualified
	}
	f := s.discFactory
	// Duplicates attach before allocating a candidate/ticket/deadline. The V1
	// caller first uses FindDiscPlay before performing its physical selection.
	if existing, err := s.FindDiscPlay(ctx, original.Principal, original.DeviceID, original.Key, original.BodyDigest); existing != nil || err != nil {
		return existing, err
	}
	view, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return nil, err
	}
	err = original.Check(view.Context(), view.Tx())
	family, familyErr := f.o.Identity.SessionFamilyTx(view.Context(), view.Tx(), original.Principal)
	view.Rollback()
	if err != nil {
		return nil, err
	}
	if familyErr != nil {
		return nil, familyErr
	}
	deadline := time.Now().Add(playbackdisc.MaxPreparationLifetime)
	for _, until := range []time.Time{original.Principal.ExpiresAt, family.AuthorizationHorizon} {
		if until.IsZero() {
			return nil, identity.ErrUnauthorized
		}
		if until.Before(deadline) {
			deadline = until
		}
	}
	if until, finite := f.o.Lifetime.Deadline(); finite && until.Before(deadline) {
		deadline = until
	}
	deadline = time.UnixMilli(deadline.UnixMilli())
	if !deadline.After(time.Now()) {
		return nil, context.DeadlineExceeded
	}
	if err := f.o.Lifetime.Err(); err != nil {
		return nil, err
	}
	// Retain original admitted request metadata, not its HTTP cancellation or
	// deadline. The server root independently cancels this exact finite scope.
	life, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	stopRoot := context.AfterFunc(f.o.Lifetime, cancel)
	context.AfterFunc(life, func() { stopRoot() })
	// Observe cancellation during admission/initialization. A successful 202
	// response ending its HTTP context cannot renew or destroy this finite scope;
	// only a later CURRENT original POST may accept its exact prepared facts.
	stop := context.AfterFunc(ctx, cancel)
	seed := discPlayStart{Principal: original.Principal, Key: playbackdisc.PreparationKey{DeviceID: original.DeviceID, StartKeyDigest: identity.Digest(original.Key)}, BodyDigest: original.BodyDigest, ItemID: original.ItemID, AssetID: original.AssetID, PartIndex: original.PartIndex, CandidateID: original.CandidateID, LegacyRequestID: "v1." + original.CandidateID + ".1", Queue: original.Queue, Replaces: original.Replaces, TitleKey: original.TitleKey, TitleRequest: original.TitleRequest, Check: func(check context.Context, tx *sql.Tx) error {
		if err := f.o.Lifetime.Err(); err != nil {
			return err
		}
		return original.Check(check, tx)
	}}
	e, err := f.begin(life, seed)
	stop()
	if err != nil || ctx.Err() != nil {
		cancel()
		if err == nil {
			err = ctx.Err()
		}
		return nil, err
	}
	return &DiscPlayOperation{factory: f, entry: e}, nil
}

// Continue issues no session or grant. False means ONLY that this exact live
// operation is still preparing; expired/failed/capacity/unsupported never poll.
func (o *DiscPlayOperation) Continue(ctx context.Context, service *Service, check playbackdisc.PreparationContinuationCheck) (context.Context, bool, error) {
	if o == nil || o.factory == nil || o.entry == nil || service == nil || service.discFactory != o.factory {
		return ctx, false, disc.ErrProtocol
	}
	return o.entry.continuationContext(ctx, service, check)
}

func (o *DiscPlayOperation) Original() (item, asset, candidate string, part int, err error) {
	if o == nil || o.factory == nil || o.entry == nil {
		err = disc.ErrProtocol
		return
	}
	seed := o.entry.seed
	return seed.ItemID, seed.AssetID, seed.CandidateID, seed.PartIndex, nil
}
