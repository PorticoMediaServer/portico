package playback

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/ownedcapture"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
)

// A private original-operation factory. Neither a preparation row nor a ticket
// ID can recreate this value. The actual start owner must retain it by its
// original key and close it outside SQL on every terminal outcome.
type discPlayPreparation struct {
	ctx               context.Context
	cancel            context.CancelFunc
	db                *sql.DB
	store             *playbackdisc.Store
	captures          *ownedcapture.Service
	ticket            *ownedcapture.PlayCaptureTicket
	conditional       DiscConditionalSource
	conditionalSource playbackdisc.Source
	original          playbackdisc.PreparationCheck
	start             discPlayStart // immutable first invocation; never replaced on a retry

	mu          sync.Mutex
	operation   playbackdisc.Preparation
	measurement *discPlayMeasurement
	receipt     ownedcapture.Receipt
	accepted    bool
	retiring    bool
	closed      bool
	failure     error
	closeErr    error
	borrow      *discPlayBorrow
	started     atomic.Bool
	// Polls cannot observe the committed ready CAS before the same sealed
	// measurement has been paired with its exact new revision in memory.
	claimPublication sync.Mutex
	// A measurement or a real POST borrow owns this lock. Close waits until
	// the helper has exited or the accepting outer transaction has finished.
	custody sync.Mutex
}

type discPlayPreparationOptions struct {
	DB                *sql.DB
	Store             *playbackdisc.Store
	Captures          *ownedcapture.Service
	Ticket            *ownedcapture.PlayCaptureTicket
	Conditional       DiscConditionalSource
	ConditionalSource playbackdisc.Source
	Claim             playbackdisc.PreparationClaim
	Original          playbackdisc.PreparationCheck
}

func newDiscPlayPreparation(ctx context.Context, o discPlayPreparationOptions) (*discPlayPreparation, error) {
	if ctx == nil || dbwork.Snapshot(ctx) != nil || o.DB == nil || o.Store == nil || o.Captures == nil || o.Ticket == nil || o.Original == nil {
		return nil, disc.ErrProtocol
	}
	if err := discPlayPreparationContext(ctx); err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(ctx)
	p := &discPlayPreparation{ctx: life, cancel: cancel, db: o.DB, store: o.Store, captures: o.Captures, ticket: o.Ticket, original: o.Original, conditional: o.Conditional, conditionalSource: o.ConditionalSource}
	view, err := dbwork.BeginSnapshot(life, o.DB)
	if err != nil {
		cancel()
		return nil, err
	}
	operation, err := o.Store.CheckPreparationClaimTx(view.Context(), view.Tx(), o.Claim, func(check context.Context, tx *sql.Tx, candidate playbackdisc.Preparation) error {
		if candidate.State != "preparing" || candidate.Claim != o.Claim {
			return playbackdisc.ErrCurrent
		}
		if err := o.Original(check, tx, candidate); err != nil {
			return err
		}
		return p.checkTicketTx(check, tx, candidate)
	})
	view.Rollback()
	if err != nil {
		cancel()
		return nil, err
	}
	deadline, _ := ctx.Deadline()
	// The caller cannot renew the committed operation's deadline by supplying
	// a fresh context. Use its exact millisecond boundary, never a later poll.
	if deadline.UnixMilli() != operation.Binding.DeadlineMs {
		cancel()
		return nil, disc.ErrProtocol
	}
	p.operation = operation
	return p, nil
}

func (p *discPlayPreparation) checkTicketTx(ctx context.Context, tx *sql.Tx, operation playbackdisc.Preparation) error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if p.conditionalSource.AssetID != "" {
		if p.conditional == nil || p.conditionalSource != operation.Binding.Source {
			return playbackdisc.ErrCurrent
		}
		return p.conditional.CheckTx(ctx, tx, p.conditionalSource)
	}
	actual, err := p.captures.ResolvePlayCaptureTx(ctx, tx, p.ticket)
	if err != nil {
		return err
	}
	if playbackdisc.Source(actual) != operation.Binding.Source {
		return playbackdisc.ErrCurrent
	}
	return p.ctx.Err()
}

// checkOriginalTx retains only worker/original-source proof. It cannot supply
// the separate current-POST continuation required for binding or acceptance.
func (p *discPlayPreparation) checkOriginalTx(ctx context.Context, tx *sql.Tx, candidate playbackdisc.Preparation) (err error) {
	defer func() {
		if err != nil && p != nil && p.ctx != nil && (ctx == nil || ctx.Err() == nil || p.ctx.Err() != nil) {
			p.mu.Lock()
			if p.failure == nil && !p.accepted {
				p.failure = err
			}
			p.mu.Unlock()
			p.cancel()
		}
	}()
	if ctx == nil || tx == nil {
		return disc.ErrProtocol
	}
	p.mu.Lock()
	original, closed := p.operation, p.closed || p.retiring
	p.mu.Unlock()
	if closed || candidate.ID != original.ID || candidate.Key != original.Key || candidate.Binding != original.Binding || candidate.CreatedMs != original.CreatedMs || candidate.Claim != original.Claim {
		return playbackdisc.ErrCurrent
	}
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if err := p.original(ctx, tx, candidate); err != nil {
		return err
	}
	return p.checkTicketTx(ctx, tx, candidate)
}

func (p *discPlayPreparation) checkClaimTx(ctx context.Context, tx *sql.Tx) error {
	p.mu.Lock()
	claim := p.operation.Claim
	p.mu.Unlock()
	_, err := p.store.CheckPreparationClaimTx(ctx, tx, claim, p.checkOriginalTx)
	return err
}

func (p *discPlayPreparation) checkClaim(ctx context.Context) error {
	if tx := dbwork.Snapshot(ctx); tx != nil {
		return p.checkClaimTx(ctx, tx)
	}
	view, err := dbwork.BeginSnapshot(ctx, p.db)
	if err != nil {
		return err
	}
	defer view.Rollback()
	return p.checkClaimTx(view.Context(), view.Tx())
}

// prepare owns one original range or capture-only measurement. The source
// preflight fixes the branch; the capture producer independently restricts full
// acquisition to its actual admitted capture-only mode.
// No session, decoder grant, queue transfer or background acceptance occurs.
func (p *discPlayPreparation) prepare(tool disc.Tool, probe assets.Probe, tools func(context.Context) (playbackdisc.ToolPins, error), titleKey string, requested catalog.DiscTitleRef) (err error) {
	if !p.started.CompareAndSwap(false, true) {
		return playbackdisc.ErrCurrent
	}
	p.custody.Lock()
	defer p.custody.Unlock()
	defer func() {
		if err != nil {
			p.mu.Lock()
			p.failure = err
			p.mu.Unlock()
			p.cancel()
		}
	}()
	if tools == nil || !discPlayConfiguredInputs(tool, probe) {
		return disc.ErrProtocol
	}
	if err = p.checkClaim(p.ctx); err != nil {
		return err
	}
	if _, err = tools(p.ctx); err != nil {
		return err
	}
	p.mu.Lock()
	operation := p.operation
	p.mu.Unlock()
	template := playbackdisc.Snapshot{Actor: operation.Binding.Actor, Operation: operation.Binding.Queue, Source: operation.Binding.Source}
	var measured *discPlayMeasurement
	if p.conditionalSource.AssetID != "" {
		object, e := p.conditional.Open(p.ctx, p.conditionalSource, p.checkClaimTx)
		if e != nil {
			return e
		}
		measured, err = measureConditionalDiscPlay(p.ctx, conditionalDiscMeasure{DB: p.db, Object: object, Tool: tool, Probe: probe, Template: template, TitleKey: titleKey, Requested: requested, Current: p.checkClaim, AcceptTx: p.checkClaimTx, Tools: tools})
	} else {
		receipt, e := p.captures.CapturePlayTicket(p.ctx, p.ticket, p.checkClaimTx)
		if e != nil {
			return e
		}
		p.mu.Lock()
		p.receipt = receipt
		p.mu.Unlock()
		reader, e := p.captures.Open(p.ctx, receipt)
		if e != nil {
			return e
		}
		measured, err = measureCapturedDiscPlay(p.ctx, capturedDiscMeasure{DB: p.db, Reader: reader, Tool: tool, Probe: probe, Template: template, TitleKey: titleKey, Requested: requested, Current: p.checkClaim, AcceptTx: p.checkClaimTx, Tools: tools})
	}
	if err != nil {
		return err
	}
	good := false
	defer func() {
		if !good {
			measured.Close()
		}
	}()
	var next playbackdisc.PreparationClaim
	p.claimPublication.Lock()
	defer func() {
		if err != nil {
			p.mu.Lock()
			p.failure = err
			p.mu.Unlock()
			p.cancel()
		}
		p.claimPublication.Unlock()
	}()
	err = dbwork.WithWriteTxContext(p.ctx, p.db, dbwork.ClassPlaybackStart, func(check context.Context, tx *sql.Tx) error {
		var e error
		next, e = p.store.MarkPreparationPreparedTx(check, tx, operation.Claim, func(check context.Context, tx *sql.Tx, candidate playbackdisc.Preparation) error {
			if err := p.checkOriginalTx(check, tx, candidate); err != nil {
				return err
			}
			return measured.CheckPreparedTx(check, tx)
		})
		return e
	})
	if err != nil {
		return err
	}
	// Only after COMMIT pair the same sealed value with its own next CAS
	// revision. No helper remains active under the retired claim at this point.
	p.mu.Lock()
	p.operation.Claim, p.operation.State = next, "prepared"
	p.measurement = measured
	p.mu.Unlock()
	good = true
	return nil
}

// Close is original-only cleanup, never active-presentation retirement. It must
// run after the outer accepting transaction, not while holding its writer.
// Failed/uncertain retirement preserves all capture retention and quota.
func (p *discPlayPreparation) Close(ctx context.Context) (closeErr error) {
	if p == nil {
		return nil
	}
	if ctx == nil || dbwork.Snapshot(ctx) != nil {
		return disc.ErrProtocol
	}
	p.mu.Lock()
	p.retiring = true
	p.mu.Unlock()
	p.cancel()
	p.custody.Lock()
	defer p.custody.Unlock()
	p.mu.Lock()
	if p.closed {
		err := p.closeErr
		p.mu.Unlock()
		return err
	}
	p.closed = true
	measurement, receipt, operation, accepted := p.measurement, p.receipt, p.operation, p.accepted
	p.measurement = nil
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.closeErr = closeErr
		p.mu.Unlock()
	}()
	var err error
	if measurement != nil {
		err = measurement.Close()
	}
	if accepted {
		return err // accepted input/retention belongs to ordinary presentation readers
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	retired := dbwork.WithWriteTxContext(cleanup, p.db, dbwork.ClassMaintenance, func(check context.Context, tx *sql.Tx) error {
		if e := p.store.RetirePreparationTx(check, tx, operation.Claim, playbackdisc.PreparationRetired); e != nil {
			return e
		}
		if measurement != nil {
			measurement.mu.Lock()
			bound, snapshot := measurement.bound, measurement.snapshot
			measurement.mu.Unlock()
			if bound {
				var exists bool
				if e := tx.QueryRowContext(check, `SELECT EXISTS(SELECT 1 FROM playback_disc_snapshots WHERE id=? AND session_id=? AND generation=?)`, snapshot.ID, snapshot.SessionID, snapshot.Generation).Scan(&exists); e != nil {
					return e
				}
				if exists {
					return p.store.RetireTx(check, tx, playbackdisc.Key{SessionID: snapshot.SessionID, Generation: snapshot.Generation}, snapshot.ID)
				}
			}
		}
		return nil
	})
	if retired != nil {
		return errors.Join(err, retired)
	}
	if receipt.ID != "" {
		// Only this factory's unique, unaccepted receipt may be retired here.
		// Physical cleanup/uncharge remain ownedcapture's existing collector.
		err = errors.Join(err, p.captures.Retire(cleanup, receipt))
	}
	return err
}
