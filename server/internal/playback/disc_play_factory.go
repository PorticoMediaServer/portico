package playback

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/ownedcapture"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// The actual start resolves these original scalar witnesses before beginning
// preparation. Check repeats that exact request/physical selection and queue
// custody in the provided transaction; it never selects a replacement target.
type discPlayStart struct {
	Principal       identity.Principal
	Key             playbackdisc.PreparationKey
	BodyDigest      string
	ItemID, AssetID string
	PartIndex       int
	CandidateID     string // process-private future V1 id, never a session before acceptance
	LegacyRequestID string // raw original v1.<private candidate>.1; scoped below
	Queue           playbackdisc.Operation
	Replaces        playbackdisc.Key
	TitleRequest    catalog.DiscTitleRef
	TitleKey        string
	Check           func(context.Context, *sql.Tx) error
}

type discPlayFactoryOptions struct {
	Lifetime    context.Context
	DB          *sql.DB
	Identity    *identity.Service
	Store       *playbackdisc.Store
	Captures    *ownedcapture.Service
	Tool        disc.Tool
	Probe       assets.Probe
	Tools       func(context.Context) (playbackdisc.ToolPins, error)
	Limits      playbackdisc.PreparationLimits
	Conditional DiscConditionalSource
	ProbeSlots  chan struct{} // the existing configured playback probe budget
}

type discPlayFactoryEntry struct {
	seed        discPlayStart
	initialized chan struct{}
	operation   *discPlayPreparation
	failure     error
	cancel      context.CancelFunc
	done        chan struct{}
}

// entries retain original-key tombstones within the supplied finite budget.
// Restart never reconstructs a worker from rows or renews its original deadline.
type discPlayFactory struct {
	o         discPlayFactoryOptions
	mu        sync.Mutex
	entries   map[playbackdisc.PreparationKey]*discPlayFactoryEntry
	closed    bool
	recovered bool
}

func newDiscPlayFactory(o discPlayFactoryOptions) (*discPlayFactory, error) {
	if o.Lifetime == nil || o.Lifetime.Done() == nil || o.DB == nil || o.Identity == nil || o.Store == nil || o.Captures == nil || o.Tools == nil || !discPlayConfiguredInputs(o.Tool, o.Probe) || cap(o.ProbeSlots) < 1 || o.Limits.Active < 1 || o.Limits.Active > 256 || o.Limits.Active > cap(o.ProbeSlots) || o.Limits.Retained < o.Limits.Active || o.Limits.Retained > 65536 || o.Limits.PerDevice < 1 || o.Limits.PerDevice > o.Limits.Active {
		return nil, disc.ErrProtocol
	}
	return &discPlayFactory{o: o, entries: make(map[playbackdisc.PreparationKey]*discPlayFactoryEntry)}, nil
}

func sameDiscPlayStart(a, b discPlayStart) bool {
	// Check closures are not authority and are never swapped on an attachment.
	// The original closure remains the one captured by the first invocation.
	return sameDiscPlayPrincipal(a.Principal, b.Principal) && a.Key == b.Key && a.BodyDigest == b.BodyDigest && a.ItemID == b.ItemID && a.AssetID == b.AssetID && a.PartIndex == b.PartIndex && a.CandidateID == b.CandidateID && a.LegacyRequestID == b.LegacyRequestID && a.Queue == b.Queue && a.Replaces == b.Replaces && a.TitleKey == b.TitleKey && a.TitleRequest == b.TitleRequest
}

func sameDiscPlayPrincipal(a, b identity.Principal) bool {
	return a.Viewer == b.Viewer && a.Hash == b.Hash && a.Epoch == b.Epoch && a.DeviceID == b.DeviceID && a.Grant == b.Grant && a.ExpiresAt.Equal(b.ExpiresAt)
}

// begin admits one actual original Play preparation. A
// duplicate attaches BEFORE a source preflight/ticket/row/slot is created. The
// supplied context is the original finite admitted scope, not a renewed poll.
// Source preflight selects the qualified range branch before capture-only admission.
func (f *discPlayFactory) begin(ctx context.Context, seed discPlayStart) (*discPlayFactoryEntry, error) {
	if ctx == nil || dbwork.Snapshot(ctx) != nil || seed.Check == nil || seed.Principal.Hash == "" || seed.Principal.ExpiresAt.IsZero() || seed.Key.DeviceID == "" || seed.CandidateID == "" || seed.LegacyRequestID != "v1."+seed.CandidateID+".1" {
		return nil, disc.ErrProtocol
	}
	if err := discPlayPreparationContext(ctx); err != nil {
		return nil, err
	}
	// Cleanup uncertainty retains its own references/charge. It must not turn
	// another account's admission into that old operation's error; normal store
	// and producer limits still enforce every retained resource below.
	_ = f.sweepExpired(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	if f.closed || !f.recovered {
		f.mu.Unlock()
		return nil, playbackdisc.ErrCurrent
	}
	if original := f.entries[seed.Key]; original != nil {
		f.mu.Unlock()
		if !sameDiscPlayStart(original.seed, seed) {
			return nil, playbackdisc.ErrConflict
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-original.initialized:
			return original, original.failure
		}
	}
	if len(f.entries) >= f.o.Limits.Retained {
		f.mu.Unlock()
		return nil, playbackdisc.ErrCapacity
	}
	select {
	case f.o.ProbeSlots <- struct{}{}:
	default:
		f.mu.Unlock()
		return nil, playbackdisc.ErrCapacity // no operation exists to poll or revive
	}
	life, cancel := context.WithCancel(ctx)
	e := &discPlayFactoryEntry{seed: seed, initialized: make(chan struct{}), done: make(chan struct{}), cancel: cancel}
	f.entries[seed.Key] = e
	f.mu.Unlock()
	// An initialization failure is still an original-key tombstone. No later
	// request may renew its deadline or reinterpret the same key under new facts.
	err := f.initialize(life, e)
	if err != nil {
		e.failure = err
		cancel()
		close(e.done)
	}
	close(e.initialized)
	return e, err
}

// findOriginal is consulted before resolving an automatic version or allocating
// another private candidate ID. It exposes only this process's original entry,
// never a row-reconstructed proof or the latest physical selection.
func (f *discPlayFactory) findOriginal(ctx context.Context, key playbackdisc.PreparationKey, principal identity.Principal, body string) (*discPlayFactoryEntry, bool, error) {
	if ctx == nil || dbwork.Snapshot(ctx) != nil {
		return nil, false, disc.ErrProtocol
	}
	f.mu.Lock()
	e, closed := f.entries[key], f.closed || !f.recovered
	f.mu.Unlock()
	if closed {
		return nil, false, playbackdisc.ErrCurrent
	}
	if e == nil {
		return nil, false, nil
	}
	if !sameDiscPlayPrincipal(e.seed.Principal, principal) {
		return nil, true, playbackdisc.ErrCurrent
	}
	if e.seed.BodyDigest != body {
		return nil, true, playbackdisc.ErrConflict
	}
	select {
	case <-ctx.Done():
		return nil, true, ctx.Err()
	case <-e.initialized:
		return e, true, e.failure
	}
}

func (e *discPlayFactoryEntry) prepared(ctx context.Context, current playbackdisc.PreparationContinuationCheck) (*discPlayPreparation, bool, error) {
	if ctx == nil || dbwork.Snapshot(ctx) != nil || current == nil {
		return nil, false, disc.ErrProtocol
	}
	select {
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case <-e.initialized:
	}
	if e.failure != nil {
		return nil, false, e.failure
	}
	p := e.operation
	if p == nil {
		return nil, false, playbackdisc.ErrCurrent
	}
	deadline, finite := p.ctx.Deadline()
	if !finite {
		return nil, false, disc.ErrProtocol
	}
	life, cancel := context.WithDeadline(ctx, deadline)
	stop := context.AfterFunc(p.ctx, cancel)
	defer func() { stop(); cancel() }()
	p.claimPublication.Lock()
	defer p.claimPublication.Unlock()
	p.mu.Lock()
	failure, closed := p.failure, p.closed || p.retiring || p.accepted
	claim := p.operation.Claim
	p.mu.Unlock()
	if failure != nil {
		return nil, false, failure
	}
	if closed || p.ctx.Err() != nil {
		return nil, false, playbackdisc.ErrCurrent
	}
	view, err := dbwork.BeginSnapshot(life, p.db)
	if err != nil {
		return nil, false, err
	}
	operation, err := p.store.CheckPreparationClaimTx(view.Context(), view.Tx(), claim, p.checkOriginalTx)
	if err == nil {
		err = current(view.Context(), view.Tx(), operation)
	}
	view.Rollback()
	if err != nil {
		// A refused live continuation is terminal. Only cancellation of this
		// HTTP request leaves the already finite background scope alone.
		if ctx.Err() == nil {
			p.mu.Lock()
			if p.failure == nil {
				p.failure = err
			}
			p.mu.Unlock()
			p.cancel()
		}
		return nil, false, err
	}
	if err := life.Err(); err != nil {
		return nil, false, err
	}
	select {
	case <-e.done:
		p.mu.Lock()
		state := p.operation.State
		p.mu.Unlock()
		if state != "prepared" {
			return nil, false, playbackdisc.ErrCurrent
		}
		return p, true, nil
	default:
		return p, false, nil // only genuine still-running preparation
	}
}

// Called once by composition before it installs this factory. Old rows are
// retirement targets only: no ticket, execution or measured value is recovered.
// The supplied finite row budget cannot forget terminal idempotency keys.
func (f *discPlayFactory) recoverUnaccepted(ctx context.Context) error {
	if ctx == nil || dbwork.Snapshot(ctx) != nil {
		return disc.ErrProtocol
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || f.recovered || len(f.entries) != 0 {
		return playbackdisc.ErrCurrent
	}
	after := ""
	for visited := 0; visited <= f.o.Limits.Retained; visited += 64 {
		next := ""
		err := dbwork.WithWriteTxContext(ctx, f.o.DB, dbwork.ClassMaintenance, func(check context.Context, tx *sql.Tx) error {
			claims, cursor, err := f.o.Store.UnacceptedPreparationClaimsTx(check, tx, after, 64)
			if err != nil {
				return err
			}
			for _, claim := range claims {
				if err := f.o.Store.RetirePreparationTx(check, tx, claim, playbackdisc.PreparationRetired); err != nil {
					return err
				}
			}
			next = cursor
			return nil
		})
		if err != nil {
			return err
		}
		if next == "" {
			f.recovered = true
			return nil
		}
		after = next
	}
	return playbackdisc.ErrCapacity
}

// Hooked into existing playback maintenance and new admission; no independent
// timer is introduced. Expired original-key entries remain tombstones.
func (f *discPlayFactory) sweepExpired(ctx context.Context) error {
	if ctx == nil || dbwork.Snapshot(ctx) != nil {
		return disc.ErrProtocol
	}
	f.mu.Lock()
	entries := make([]*discPlayFactoryEntry, 0, len(f.entries))
	for _, e := range f.entries {
		entries = append(entries, e)
	}
	f.mu.Unlock()
	var err error
	for _, e := range entries {
		select {
		case <-e.initialized:
		default:
			continue
		}
		select {
		case <-e.done:
		default:
			continue
		}
		if e.operation != nil && e.operation.ctx.Err() != nil {
			err = errors.Join(err, e.operation.Close(ctx))
		}
	}
	return err
}

func (f *discPlayFactory) initialize(ctx context.Context, e *discPlayFactoryEntry) (err error) {
	started := false
	defer func() {
		if !started {
			<-f.o.ProbeSlots
		}
	}()
	seed := e.seed
	pins, err := f.o.Tools(ctx)
	if err != nil {
		return err
	}
	tools := func(check context.Context) (playbackdisc.ToolPins, error) {
		actual, err := f.o.Tools(check)
		if err == nil && actual != pins {
			err = ErrStaleOffer
		}
		return actual, err
	}
	ticket, err := f.o.Captures.NewPlayCaptureTicket(ctx, ownedcapture.Request{Principal: seed.Principal, AssetID: seed.AssetID})
	if err != nil {
		return err
	}
	var conditional playbackdisc.Source
	if f.o.Conditional != nil {
		view, e := dbwork.BeginSnapshot(ctx, f.o.DB)
		if e != nil {
			return e
		}
		if e = seed.Check(view.Context(), view.Tx()); e == nil {
			conditional, e = f.o.Conditional.ResolveTx(view.Context(), view.Tx(), seed.Principal, seed.AssetID, ticket.ID())
		}
		view.Rollback()
		// Only a source-issued absence of the conditional adapter may try the
		// capture producer. That producer independently refuses every origin
		// for which its full-copy mode is not actually unavoidable/admitted.
		if e != nil && !errors.Is(e, storage.ErrRemoteRange) {
			return e
		}
		if e == nil && conditional.AssetID != seed.AssetID {
			return disc.ErrProtocol
		}
	}
	deadline, _ := ctx.Deadline()
	var pending playbackdisc.Preparation
	var family identity.FamilyState
	var binding playbackdisc.PreparationBinding
	check := func(check context.Context, tx *sql.Tx, candidate playbackdisc.Preparation) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := f.o.Identity.SessionFamilyTx(check, tx, seed.Principal)
		if err != nil {
			return err
		}
		if current.ID != family.ID || current.DeviceID != family.DeviceID || current.TokenGeneration != family.TokenGeneration || !current.AuthorizationHorizon.Equal(family.AuthorizationHorizon) || candidate.Key != seed.Key || candidate.Binding != binding {
			return playbackdisc.ErrCurrent
		}
		return seed.Check(check, tx)
	}
	// Source preflight is DB-only. Its exact tuple is fixed BEFORE the operation
	// row and remains the same through actual Admit/capture/facts publication.
	err = dbwork.WithWriteTxContext(ctx, f.o.DB, dbwork.ClassPlaybackStart, func(checkCtx context.Context, tx *sql.Tx) error {
		if err := seed.Check(checkCtx, tx); err != nil {
			return err
		}
		var err error
		family, err = f.o.Identity.SessionFamilyTx(checkCtx, tx, seed.Principal)
		if err != nil {
			return err
		}
		if family.DeviceID != seed.Key.DeviceID || !family.AuthorizationHorizon.After(time.Now()) || seed.Principal.ExpiresAt.UnixMilli() < deadline.UnixMilli() {
			return identity.ErrUnauthorized
		}
		actual := conditional
		if conditional.AssetID != "" {
			if err = f.o.Conditional.CheckTx(checkCtx, tx, conditional); err != nil {
				return err
			}
		} else {
			captured, e := f.o.Captures.ResolvePlayCaptureTx(checkCtx, tx, ticket)
			if e != nil {
				return e
			}
			actual = playbackdisc.Source(captured)
		}
		var tokenExpiry string
		if err = tx.QueryRowContext(checkCtx, `SELECT expires_at FROM authorization_family_tokens WHERE token_hash=? AND family_id=? AND generation=? AND retired=0`, seed.Principal.Hash, family.ID, family.TokenGeneration).Scan(&tokenExpiry); err != nil {
			return err
		}
		expires, err := time.Parse(time.RFC3339Nano, tokenExpiry)
		if err != nil {
			return identity.ErrUnauthorized
		}
		queue := seed.Queue
		scopedRequest := sessionRequestKey(seed.Principal, seed.LegacyRequestID)
		if queue.LegacyRequestID != "" && queue.LegacyRequestID != scopedRequest {
			return disc.ErrProtocol
		}
		if queue.StartKeyDigest != "" && queue.StartKeyDigest != seed.Key.StartKeyDigest || queue.StartBodyDigest != "" && queue.StartBodyDigest != seed.BodyDigest {
			return disc.ErrProtocol
		}
		queue.LegacyRequestID, queue.StartKeyDigest, queue.StartBodyDigest = scopedRequest, seed.Key.StartKeyDigest, seed.BodyDigest
		binding = playbackdisc.PreparationBinding{
			Actor: playbackdisc.Actor{ServerID: seed.Principal.ServerID, Authority: seed.Principal.Authority, AccountID: seed.Principal.AccountID, ProfileID: seed.Principal.ProfileID,
				DeviceID: family.DeviceID, FamilyID: family.ID, Grant: seed.Principal.Grant, SessionHash: seed.Principal.Hash, Epoch: int64(seed.Principal.Epoch), TokenGeneration: family.TokenGeneration, OriginalHorizonMs: family.AuthorizationHorizon.UnixMilli()},
			StartBodyDigest: seed.BodyDigest, ItemID: seed.ItemID, AssetID: seed.AssetID, PartIndex: seed.PartIndex, Source: actual, Queue: queue,
			ReplacesLegacySessionID: seed.Replaces.SessionID, ReplacesGeneration: seed.Replaces.Generation, OriginalTokenExpiresMs: expires.UnixMilli(), DeadlineMs: deadline.UnixMilli(),
		}
		pending, err = f.o.Store.AdmitPreparationTx(checkCtx, tx, identity.Token(), seed.Key, binding, f.o.Limits, check)
		return err
	})
	if err != nil {
		return err
	}
	claim := pending.Claim
	defer func() {
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			// Exact original claim only; an uncertain successor/accepted outcome
			// is retained rather than being reconstructed or globally retired.
			_ = dbwork.WithWriteTxContext(cleanup, f.o.DB, dbwork.ClassMaintenance, func(check context.Context, tx *sql.Tx) error {
				return f.o.Store.RetirePreparationTx(check, tx, claim, playbackdisc.PreparationRetired)
			})
		}
	}()
	// The pending operation committed above; the worker claim has its own
	// accepting writer. No acquisition can begin before both commits complete.
	var claimed playbackdisc.PreparationClaim
	err = dbwork.WithWriteTxContext(ctx, f.o.DB, dbwork.ClassPlaybackStart, func(checkCtx context.Context, tx *sql.Tx) error {
		var err error
		claimed, err = f.o.Store.ClaimPreparationTx(checkCtx, tx, pending.Claim, identity.Token(), check)
		return err
	})
	if err != nil {
		return err
	}
	claim = claimed
	e.operation, err = newDiscPlayPreparation(ctx, discPlayPreparationOptions{DB: f.o.DB, Store: f.o.Store, Captures: f.o.Captures, Ticket: ticket, Conditional: f.o.Conditional, ConditionalSource: conditional, Claim: claim, Original: check})
	if err != nil {
		return err
	}
	started = true
	e.operation.start = seed
	supervise.Go("playback.disc-play-prepare", func() {
		defer close(e.done)
		defer func() { <-f.o.ProbeSlots }()
		if err := e.operation.prepare(f.o.Tool, f.o.Probe, tools, seed.TitleKey, seed.TitleRequest); err != nil {
			e.cancel()
			_ = e.operation.Close(context.Background())
		}
	})
	return nil
}

// close joins only this factory's original workers and closes their own held
// descriptors. It starts no timer or replacement job and never transfers a queue.
func (f *discPlayFactory) close(ctx context.Context) error {
	if ctx == nil || dbwork.Snapshot(ctx) != nil {
		return disc.ErrProtocol
	}
	f.mu.Lock()
	f.closed = true
	entries := make([]*discPlayFactoryEntry, 0, len(f.entries))
	for _, e := range f.entries {
		e.cancel()
		entries = append(entries, e)
	}
	f.mu.Unlock()
	var result error
	for _, e := range entries {
		<-e.initialized
		<-e.done
		if e.operation != nil {
			result = errors.Join(result, e.operation.Close(ctx))
		}
	}
	return result
}
