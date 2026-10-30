package playback

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestStopClaimRebindAfterDrainKeepsFrozenOccurrence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, mutation := range []string{"state='paused'", "state='ended'", "generation=generation+1", "grant_hash='successor'", "request_id='successor'"} {
		t.Run(mutation, func(t *testing.T) {
			db, h, id := conversionOutputFixture(t)
			s := New(db)
			s.ConfigureHLS(h)
			ctx := context.Background()
			claim, err := s.ClaimStopOccurrence(ctx, id, 3)
			if err != nil {
				t.Fatal(err)
			}
			defer claim.Abort()
			w, err := dbwork.Begin(ctx, db, dbwork.ClassSecurityFence)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Rollback()
			if _, err = w.Tx().ExecContext(w.Context(), `UPDATE playback_sessions SET `+mutation+` WHERE id=?`, id); err != nil {
				t.Fatal(err)
			}
			proofs := 0
			err = claim.RebindAfterDrainTx(w.Context(), w.Tx(), func(context.Context, *sql.Tx) error { proofs++; return nil })
			accepted := mutation == "state='paused'" || mutation == "state='ended'"
			if accepted != (err == nil) {
				t.Fatalf("rebind accepted=%v: %v", accepted, err)
			}
			if proofs != 1 {
				t.Fatal("rebind skipped authority proof")
			}
			if accepted {
				if err = claim.FenceTx(w.Context(), w.Tx(), func(context.Context, *sql.Tx) error { return nil }); err != nil {
					t.Fatal(err)
				}
				if err = w.Commit(); err != nil {
					t.Fatal(err)
				}
				if err = claim.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestFinishStoppedOccurrenceRetriesExactJoinAfterUnknownCommit(t *testing.T) {
	db, h, id := conversionOutputFixture(t)
	s := New(db)
	s.ConfigureHLS(h)
	r := s.occurrenceRegistry()
	intent, err := r.intent(id)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var canceled atomic.Int32
	run := &occurrenceRun{generation: 3, cancel: func() { canceled.Add(1) }, done: done}
	if err = intent.install(run, func() {}); err != nil {
		t.Fatal(err)
	}
	intent.release()
	// Simulate a committed grant fence whose after-commit callback/HTTP reply
	// was lost. Completion must recover from durable exact-generation state.
	if err = dbwork.WithWriteTxContext(context.Background(), db, dbwork.ClassEstablishedPlayback, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE playback_sessions SET state='stopped' WHERE id=? AND generation=3`, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err = s.FinishStoppedOccurrence(wait, id, 3); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pending join: %v", err)
	}
	if canceled.Load() != 1 {
		t.Fatalf("canceled %d times", canceled.Load())
	}
	// Even if a successor now occupies the durable ID, retry joins the captured
	// original handle and never writes that successor's state or generation.
	if _, err = db.Exec(`UPDATE playback_sessions SET generation=4,state='playing' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	close(done)
	if err = s.FinishStoppedOccurrence(context.Background(), id, 3); err != nil {
		t.Fatal(err)
	}
	if canceled.Load() != 1 {
		t.Fatal("retry canceled handle twice")
	}
	var generation int
	var state string
	if err = db.QueryRow(`SELECT generation,state FROM playback_sessions WHERE id=?`, id).Scan(&generation, &state); err != nil {
		t.Fatal(err)
	}
	if generation != 4 || state != "playing" {
		t.Fatal("completion touched successor")
	}
	r.finished(id, intent.entry, run)
}
