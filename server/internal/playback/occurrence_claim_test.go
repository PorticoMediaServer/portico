package playback

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestOccurrenceClaimFencesLateIntentAndAbortReopens(t *testing.T) {
	var r occurrenceRegistry
	old, err := r.intent("media-1")
	if err != nil {
		t.Fatal(err)
	}
	claim, err := r.claim(context.Background(), "media-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = old.install(&occurrenceRun{generation: 1}, func() { t.Fatal("old intent published") }); !errors.Is(err, errOccurrenceClaimed) {
		t.Fatalf("late publication: %v", err)
	}
	claim.abort()
	if old.current() {
		t.Fatal("aborted claim revived the old epoch")
	}
	old.release()
	fresh, err := r.intent("media-1")
	if err != nil {
		t.Fatal(err)
	}
	fresh.release()
}

func TestOccurrenceClaimCommitOwnsExactRunsAndDoesNotReopen(t *testing.T) {
	var r occurrenceRegistry
	var oldCancelled, successorCancelled atomic.Int32
	old, err := r.intent("media-1")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	oldRun := &occurrenceRun{generation: 1, cancel: func() { oldCancelled.Add(1); close(done) }, done: done}
	if err = old.install(oldRun, func() {}); err != nil {
		t.Fatal(err)
	}
	other := &occurrenceRun{generation: 2, cancel: func() { successorCancelled.Add(1) }}
	if err = old.install(other, func() {}); err != nil {
		t.Fatal(err)
	}
	if _, err = r.claim(context.Background(), "media-1", 1); !errors.Is(err, errOccurrenceClaimed) {
		t.Fatalf("mixed generation must refuse: %v", err)
	}
	if oldCancelled.Load() != 0 || successorCancelled.Load() != 0 {
		t.Fatal("refusal cancelled a run")
	}
	r.finished("media-1", old.entry, other)
	claim, err := r.claim(context.Background(), "media-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := r.claim(context.Background(), "media-1", 1)
	if err != nil || claim != duplicate {
		t.Fatalf("claim copies: %v", err)
	}
	claim.markCommitted()
	if err = claim.commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	claim.abort()
	if err = duplicate.commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if oldCancelled.Load() != 1 || successorCancelled.Load() != 0 {
		t.Fatalf("wrong cancellations %d %d", oldCancelled.Load(), successorCancelled.Load())
	}
	if _, err = r.intent("media-1"); !errors.Is(err, errOccurrenceClaimed) {
		t.Fatalf("committed claim reopened: %v", err)
	}
	old.release()
	r.finished("media-1", old.entry, oldRun)
	r.finished("media-1", old.entry, other)
	fresh, err := r.intent("media-1")
	if err != nil {
		t.Fatal(err)
	}
	fresh.release()
}

func TestOccurrenceClaimTimeoutDoesNotAbortCommittedFence(t *testing.T) {
	var r occurrenceRegistry
	i, err := r.intent("media-1")
	if err != nil {
		t.Fatal(err)
	}
	run := &occurrenceRun{generation: 1, cancel: func() {}, done: make(chan struct{})}
	if err = i.install(run, func() {}); err != nil {
		t.Fatal(err)
	}
	claim, err := r.claim(context.Background(), "media-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	claim.markCommitted()
	if err = claim.commit(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("join: %v", err)
	}
	claim.abort()
	if _, err = r.intent("media-1"); !errors.Is(err, errOccurrenceClaimed) {
		t.Fatalf("timed-out committed fence reopened: %v", err)
	}
	i.release()
	r.finished("media-1", i.entry, run)
}

func TestOccurrenceClaimWaitsForOwnedPublicationBeforeWriter(t *testing.T) {
	var r occurrenceRegistry
	i, err := r.intent("media-1")
	if err != nil {
		t.Fatal(err)
	}
	run := &occurrenceRun{generation: 1}
	if err = i.install(run, func() {}); err != nil {
		t.Fatal(err)
	}
	release, err := r.publication("media-1", i.entry, run)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = r.claim(ctx, "media-1", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("bounded pending publication: %v", err)
	}
	// A timed-out, undelivered claim aborts; the next claim can succeed once the
	// same publication releases without holding the registry during the wait.
	release()
	claim, err := r.claim(context.Background(), "media-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.publication("media-1", i.entry, run); !errors.Is(err, errOccurrenceClaimed) {
		t.Fatalf("publication after claim: %v", err)
	}
	claim.abort()
	i.release()
	r.finished("media-1", i.entry, run)
}

func TestOccurrenceOtherClaimCopyCannotAbortAcceptingOwner(t *testing.T) {
	var r occurrenceRegistry
	phase, err := r.claim(context.Background(), "same", 1)
	if err != nil {
		t.Fatal(err)
	}
	owner := &StopOccurrenceClaim{phase: phase, id: "same"}
	other := &StopOccurrenceClaim{phase: phase, id: "same"}
	phase.fenceOwner = owner
	phase.fencing = true
	other.Abort()
	if phase.phase != 0 || !phase.entry.blocked {
		t.Fatal("other copy reopened accepting owner")
	}
	owner.Abort()
	if phase.phase != 2 || phase.entry.blocked {
		t.Fatal("rolled back owner did not release its barrier")
	}
}
