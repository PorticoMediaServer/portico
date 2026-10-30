package playback

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/ownedcapture"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// DiscSubtitleTransition retains original input custody across one actual
// current command's writer. It is not reconstructed from rows or reused by a
// second command. Every change of bytes needs both V1 acceptance and the Store's
// opaque same-writer derived claim; normal reader predicates stay unchanged.
type discSubtitleTransitionKey struct{}

type DiscSubtitleTransition struct{ *discSubtitleTransitionState }

type discSubtitleTransitionState struct {
	service   *Service
	ctx       context.Context
	cancel    context.CancelFunc
	contexts  []func()
	principal identity.Principal
	parent    playbackdisc.Snapshot
	pin       playbackdisc.Pin
	reader    *ownedcapture.Reader
	object    storage.RemoteObject
	current   func(context.Context, *sql.Tx, int) error
	mu        sync.Mutex
	tx        *sql.Tx
	claim     *playbackdisc.DerivedGeneration
	childID   string
	child     bool
	once      sync.Once
	closeErr  error
	closed    atomic.Bool
}

func (s *Service) PrepareDiscSubtitleTransition(ctx context.Context, p identity.Principal, session string, current func(context.Context, *sql.Tx, int) error) (out *DiscSubtitleTransition, private bool, err error) {
	if s == nil || s.db == nil || ctx == nil || dbwork.Snapshot(ctx) != nil || current == nil {
		return nil, true, disc.ErrProtocol
	}
	view, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return nil, true, e
	}
	_, _, classified, e := privateDiscBindingTx(view.Context(), view.Tx(), session)
	view.Rollback()
	if e != nil || !classified {
		return nil, classified, e
	}
	var original playbackdisc.Snapshot
	var pin playbackdisc.Pin
	err = dbwork.WithWriteTxContext(ctx, s.db, dbwork.ClassEstablishedPlayback, func(check context.Context, tx *sql.Tx) error {
		var e error
		original, private, e = s.activePrivateDiscSnapshotTx(check, tx, session)
		if e != nil || !private {
			return e
		}
		if e = current(check, tx, original.Generation); e != nil {
			return e
		}
		var expiry int64
		if e = tx.QueryRowContext(check, `SELECT expires_ms FROM playback_disc_lifecycle WHERE snapshot_id=? AND state='active' AND sealed=1`, original.ID).Scan(&expiry); e != nil {
			return e
		}
		if until, finite := ctx.Deadline(); finite && until.UnixMilli() < expiry {
			expiry = until.UnixMilli()
		}
		pin, e = s.discInputs.store.PinTx(check, tx, playbackdisc.Key{SessionID: original.SessionID, Generation: original.Generation}, original.ID, identity.Token(), time.UnixMilli(expiry), s.checkActiveDiscSnapshotTx)
		return e
	})
	if err != nil || !private {
		return nil, private, err
	}
	life, cancel := context.WithDeadline(ctx, pin.ExpiresAt)
	t := &DiscSubtitleTransition{&discSubtitleTransitionState{service: s, ctx: life, cancel: cancel, principal: p, parent: original, pin: pin, current: current, childID: identity.Token()}}
	defer func() {
		if err != nil {
			t.Close()
		}
	}()
	pins, e := s.discInputs.tools(t.ctx)
	if e != nil {
		return nil, true, e
	}
	if pins != original.Tools {
		return nil, true, ErrStaleOffer
	}
	if original.Input.Captured != nil {
		t.reader, err = s.discInputs.captures.Open(t.ctx, original.Input.Captured.Receipt)
	} else if original.Input.Conditional != nil && s.discInputs.conditional != nil {
		t.object, err = s.discInputs.conditional.Open(t.ctx, original.Source, t.checkCallerSourceTx)
		if err == nil && (t.object == nil || t.object.Version().ID() != original.Input.Conditional.Version.ID()) {
			err = disc.ErrRemoteUnqualified
		}
	} else {
		err = disc.ErrRemoteUnqualified
	}
	if err != nil {
		return nil, true, err
	}
	return t, true, nil
}

func (t *DiscSubtitleTransition) checkCallerSourceTx(ctx context.Context, tx *sql.Tx) error {
	if t == nil || t.discSubtitleTransitionState == nil || t.closed.Load() || t.service == nil || t.current == nil || tx == nil {
		return disc.ErrProtocol
	}
	if err := t.ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	generation := t.parent.Generation
	if t.tx == tx && t.child {
		generation++
	}
	t.mu.Unlock()
	if err := t.current(ctx, tx, generation); err != nil {
		return err
	}
	if err := t.service.checkDiscSnapshotGenerationTx(ctx, tx, t.parent, generation); err != nil {
		return err
	}
	return ctx.Err()
}

func (t *DiscSubtitleTransition) CheckTx(ctx context.Context, tx *sql.Tx) error {
	if err := t.checkCallerSourceTx(ctx, tx); err != nil {
		return err
	}
	if t.reader != nil {
		if err := t.reader.CheckTx(ctx, tx); err != nil {
			return err
		}
	}
	if t.object != nil {
		retained, ok := t.object.(interface {
			CheckTx(context.Context, *sql.Tx) error
		})
		if !ok {
			return disc.ErrRemoteUnqualified
		}
		if err := retained.CheckTx(ctx, tx); err != nil {
			return err
		}
	}
	if t.object != nil && (t.object.Version().ID() != t.parent.Input.Conditional.Version.ID() || t.object.Snapshot().Size != t.parent.Source.Size || t.object.Snapshot().ModifiedNS != t.parent.Source.ModifiedNS) {
		return playbackdisc.ErrCurrent
	}
	return ctx.Err()
}

func (t *DiscSubtitleTransition) parentCheck(ctx context.Context, tx *sql.Tx, v playbackdisc.Snapshot) error {
	if !reflect.DeepEqual(v, t.parent) {
		return playbackdisc.ErrCurrent
	}
	return t.CheckTx(ctx, tx)
}

func (t *DiscSubtitleTransition) childCheck(ctx context.Context, tx *sql.Tx, v playbackdisc.Snapshot) error {
	expected := t.parent
	expected.ID = t.childID
	expected.Generation++
	if !reflect.DeepEqual(v, expected) {
		return playbackdisc.ErrCurrent
	}
	return t.CheckTx(ctx, tx)
}

func (t *DiscSubtitleTransition) BeforeGenerationTx(ctx context.Context, tx *sql.Tx, session string, generation int) error {
	if err := t.CheckTx(ctx, tx); err != nil {
		return err
	}
	if session != t.parent.SessionID || generation != t.parent.Generation {
		return playbackdisc.ErrCurrent
	}
	t.mu.Lock()
	if t.tx != nil {
		t.mu.Unlock()
		return playbackdisc.ErrCurrent
	}
	t.tx = tx
	t.mu.Unlock()
	return t.claimTx(ctx, tx)
}

func (t *DiscSubtitleTransition) claimTx(ctx context.Context, tx *sql.Tx) error {
	t.mu.Lock()
	exact := t.tx == tx && t.claim == nil
	t.mu.Unlock()
	if !exact {
		return playbackdisc.ErrCurrent
	}
	claim, err := t.service.discInputs.store.ClaimDerivedGenerationTx(ctx, tx, playbackdisc.Key{SessionID: t.parent.SessionID, Generation: t.parent.Generation}, t.parent.ID, playbackdisc.Key{SessionID: t.parent.SessionID, Generation: t.parent.Generation + 1}, t.childID, t.parentCheck)
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.claim = claim
	t.mu.Unlock()
	return nil
}

func (t *DiscSubtitleTransition) AfterGenerationTx(ctx context.Context, tx *sql.Tx, session string, generation int, resource string) (string, error) {
	if t == nil || t.discSubtitleTransitionState == nil || t.closed.Load() {
		return "", disc.ErrProtocol
	}
	t.mu.Lock()
	exact := t.tx == tx && t.claim != nil && !t.child
	claim := t.claim
	if exact {
		t.child = true
	}
	t.mu.Unlock()
	if !exact || session != t.parent.SessionID || generation != t.parent.Generation+1 {
		return "", playbackdisc.ErrCurrent
	}
	store := t.service.discInputs.store
	if err := store.PublishDerivedGenerationTx(ctx, tx, claim, t.childCheck); err != nil {
		return "", err
	}
	mapped, err := subtitles.DerivePrivateDiscResourcesTx(ctx, tx, t.principal, t.parent.ID, t.childID, resource, t.CheckTx)
	if err != nil {
		return "", err
	}
	if resource != "" {
		plan, e := loadDeliveryPlan(ctx, tx, session)
		if e != nil {
			return "", e
		}
		if plan != nil && plan.BurnIn != nil {
			if plan.BurnIn.ResourceID != resource {
				return "", playbackdisc.ErrCurrent
			}
			plan.BurnIn.ResourceID = mapped
			raw, e := json.Marshal(plan.BurnIn)
			if e != nil {
				return "", e
			}
			if _, e = tx.ExecContext(ctx, `UPDATE playback_delivery_plans SET burn_in_json=? WHERE session_id=?`, string(raw), session); e != nil {
				return "", e
			}
		}
	}
	if _, err = store.ActivateDerivedGenerationTx(ctx, tx, claim, t.childCheck); err != nil {
		return "", err
	}
	if err = store.RetireTx(ctx, tx, playbackdisc.Key{SessionID: t.parent.SessionID, Generation: t.parent.Generation}, t.parent.ID); err != nil {
		return "", err
	}
	return mapped, nil
}

func (t *DiscSubtitleTransition) Close() error {
	if t == nil || t.discSubtitleTransitionState == nil {
		return nil
	}
	t.once.Do(func() {
		t.closed.Store(true)
		t.cancel()
		t.mu.Lock()
		contexts := t.contexts
		t.contexts = nil
		t.mu.Unlock()
		for _, cancel := range contexts {
			cancel()
		}
		if t.reader != nil {
			t.closeErr = t.reader.Close()
		}
		if t.object != nil {
			t.closeErr = errors.Join(t.closeErr, t.object.Close())
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.ctx), 5*time.Second)
		defer cancel()
		t.closeErr = errors.Join(t.closeErr, dbwork.WithWriteTxContext(ctx, t.service.db, dbwork.ClassMaintenance, func(check context.Context, tx *sql.Tx) error {
			return t.service.discInputs.store.ReleasePinTx(check, tx, t.pin)
		}))
	})
	return t.closeErr
}

// Context carries only this original process transition to the existing
// delivery callback. It never replaces the normal resource/current checks.
func (t *DiscSubtitleTransition) Context(ctx context.Context) context.Context {
	if t == nil || t.discSubtitleTransitionState == nil || t.closed.Load() {
		return ctx
	}
	life, cancel := context.WithDeadline(ctx, t.pin.ExpiresAt)
	stop := context.AfterFunc(t.ctx, cancel)
	t.mu.Lock()
	if t.closed.Load() || len(t.contexts) >= 16 {
		t.mu.Unlock()
		stop()
		cancel()
		return life
	}
	t.contexts = append(t.contexts, func() { stop(); cancel() })
	t.mu.Unlock()
	if t.ctx.Err() != nil {
		cancel()
	}
	return context.WithValue(life, discSubtitleTransitionKey{}, t)
}
