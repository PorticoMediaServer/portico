package playback

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Actual producer entry points are kept behind a genuinely held read pool. No
// provider or media executable is needed: an accepted stop must reject them
// before those ports can be reached, even after the closed entry is pruned.
func TestOccurrenceStopFencesActualClassicFiniteRenditionEntries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []string{"classic", "finite", "rendition"} {
		t.Run(kind, func(t *testing.T) {
			db, h, id := conversionOutputFixture(t)
			h.ctx = context.Background()
			service := New(db)
			service.ConfigureHLS(h)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			entryCtx := dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
			readers := dbwork.ReadHandle(entryCtx, db)
			readers.SetMaxOpenConns(1)
			held, err := readers.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			result := make(chan error, 1)
			go func() {
				switch kind {
				case "classic":
					result <- h.start(entryCtx, id, 0)
				case "finite":
					f := &FiniteHLS{h: h, actors: map[string]*finiteActor{}}
					_, err := f.ensure(entryCtx, id)
					result <- err
				case "rendition":
					result <- h.startRendition(entryCtx, id, AudioRenditionPlan{}, 0)
				}
			}()
			// Entry registration is the contract, not a guessed delay before the read.
			for {
				h.occurrences.mu.Lock()
				entry := h.occurrences.entries[id]
				registered := entry != nil && entry.intents > 0
				h.occurrences.mu.Unlock()
				if registered {
					break
				}
				select {
				case err := <-result:
					t.Fatalf("entry returned before held read: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(time.Millisecond):
				}
			}
			// A stalled SQL read must not own the producer mutex.
			locked := make(chan struct{})
			go func() { h.mu.Lock(); h.mu.Unlock(); close(locked) }()
			select {
			case <-locked:
			case <-ctx.Done():
				t.Fatal("SQL was held under h.mu")
			}
			claim, err := service.ClaimStopOccurrence(ctx, id, 3)
			if err != nil {
				t.Fatal(err)
			}
			defer claim.Abort()
			w, err := dbwork.Begin(ctx, db, dbwork.ClassSecurityFence)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Rollback()
			p := identity.Principal{Hash: "session", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile"}}
			if err = claim.FenceTx(w.Context(), w.Tx(), func(ctx context.Context, tx *sql.Tx) error {
				return service.CheckStopOccurrenceTx(ctx, tx, p, id, 3, func() string {
					var item string
					if err := tx.QueryRowContext(ctx, `SELECT pid(e.public_id) FROM playback_sessions p JOIN catalog_entities e ON e.id=p.item_id WHERE p.id=?`, id).Scan(&item); err != nil {
						t.Fatal(err)
					}
					return item
				}())
			}); err != nil {
				t.Fatal(err)
			}
			if err = w.Commit(); err != nil {
				t.Fatal(err)
			}
			if err = claim.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			held.Close()
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("pre-stop entry admitted after fence")
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			h.mu.Lock()
			active := len(h.active)
			windows := len(h.windows)
			h.mu.Unlock()
			if active != 0 || windows != 0 {
				t.Fatal("old entry installed a producer")
			}
		})
	}
}

func TestOccurrenceClaimFrozenPhysicalAndGrantDriftRollsBack(t *testing.T) {
	for _, mutation := range []string{`UPDATE playback_source_pins SET modified_ns=modified_ns+1 WHERE session_id=?`, `UPDATE playback_sessions SET grant_hash='replacement-grant' WHERE id=?`} {
		t.Run(mutation, func(t *testing.T) {
			db, h, id := conversionOutputFixture(t)
			service := New(db)
			service.ConfigureHLS(h)
			ctx := context.Background()
			claim, err := service.ClaimStopOccurrence(ctx, id, 3)
			if err != nil {
				t.Fatal(err)
			}
			w, err := dbwork.Begin(ctx, db, dbwork.ClassSecurityFence)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = w.Tx().ExecContext(w.Context(), mutation, id); err != nil {
				t.Fatal(err)
			}
			err = claim.FenceTx(w.Context(), w.Tx(), func(context.Context, *sql.Tx) error { return nil })
			if !errors.Is(err, ErrOccurrenceChanged) {
				t.Fatalf("changed original accepted: %v", err)
			}
			w.Rollback()
			claim.Abort()
			fresh, err := service.ClaimStopOccurrence(ctx, id, 3)
			if err != nil {
				t.Fatalf("rollback did not reopen original: %v", err)
			}
			fresh.Abort()
		})
	}
}

func TestHeldClaimKeepsActualWatcherAndOwnedReadsLiveUntilCommitted(t *testing.T) {
	db, h, id := conversionOutputFixture(t)
	h.ctx = context.Background()
	service := New(db)
	service.ConfigureHLS(h)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	owner, err := service.RetainPreparation(ctx, id, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	window := &hlsWindow{generation: 3, start: 0, served: -1, produced: -1}
	h.windows[id] = window
	held, err := h.occurrences.claim(ctx, id, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err = childprocess.CheckOwnedIO(owner.Context()); err != nil {
		t.Fatalf("held claim revoked owned read: %v", err)
	}
	if release, err := childprocess.AcquirePublication(owner.Context()); err == nil {
		release()
		t.Fatal("held claim admitted new source/process")
	}
	observed := make(chan struct{}, 4)
	watchDone := make(chan struct{})
	var watcherCancelled atomic.Int32
	go func() {
		defer close(watchDone)
		defer owner.Close()
		h.watchWindow(owner.Context(), id, filepath.Join(h.root, id), "", 0, -1, func() { watcherCancelled.Add(1) }, nil, nil, nil, window, func(ctx context.Context) error {
			if err := childprocess.CheckOwnedIO(ctx); err != nil {
				return err
			}
			select {
			case observed <- struct{}{}:
			default:
			}
			return nil
		})
	}()
	select {
	case <-observed:
	case <-ctx.Done():
		t.Fatal("held claim killed/blocked the existing watcher")
	}
	if watcherCancelled.Load() != 0 || owner.Context().Err() != nil {
		t.Fatal("held claim cancelled owned work")
	}
	held.abort()
	select {
	case <-observed:
	case <-ctx.Done():
		t.Fatal("Abort did not preserve watcher")
	}
	if watcherCancelled.Load() != 0 || owner.Context().Err() != nil {
		t.Fatal("Abort cancelled or replaced existing owner")
	}
	claim, err := service.ClaimStopOccurrence(ctx, id, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Abort()
	w, err := dbwork.Begin(ctx, db, dbwork.ClassSecurityFence)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Rollback()
	if err = claim.FenceTx(w.Context(), w.Tx(), func(context.Context, *sql.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if owner.Context().Err() != nil {
		t.Fatal("FenceTx cancelled before durable commit")
	}
	if err = w.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = claim.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-watchDone:
	case <-ctx.Done():
		t.Fatal("committed original did not join")
	}
	if owner.Context().Err() == nil || h.occurrences.ownsRun(id, owner.entry, owner.run) {
		t.Fatal("committed owner remained live")
	}
}

func TestHeldClaimDoesNotMaskIndependentSourceRetirement(t *testing.T) {
	db, h, id := conversionOutputFixture(t)
	service := New(db)
	service.ConfigureHLS(h)
	ctx := context.Background()
	owner, err := service.RetainPreparation(ctx, id, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	held, err := h.occurrences.claim(ctx, id, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer held.abort()
	if _, err = dbwork.ExecWrite(ctx, db, dbwork.ClassSecurityFence, `UPDATE playback_sessions SET state='failed' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if err = childprocess.CheckOwnedIO(owner.Context()); err == nil {
		t.Fatal("held claim masked independent durable retirement")
	}
	held.abort()
	if err = childprocess.CheckOwnedIO(owner.Context()); err == nil {
		t.Fatal("Abort revived independently retired source")
	}
}
