package playback

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

type restartRetirementFixture struct {
	t       *testing.T
	db      *sql.DB
	h       *HLS
	id      string
	ctx     context.Context
	stop    context.CancelFunc
	entry   *occurrenceEntry
	run     *occurrenceRun
	window  *hlsWindow
	finish  func()
	workers sync.WaitGroup
}

func newRestartRetirementFixture(t *testing.T) *restartRetirementFixture {
	t.Helper()
	db, h, id := conversionOutputFixture(t)
	root, stop := context.WithCancel(context.Background())
	h.ctx = root
	ctx, cancel := context.WithCancel(root)
	done := make(chan struct{})
	run := &occurrenceRun{generation: 3, cancel: cancel, done: done}
	if err := db.QueryRow(`SELECT request_id,item_id,asset_id FROM playback_sessions WHERE id=?`, id).Scan(&run.requestID, &run.itemID, &run.assetID); err != nil {
		t.Fatal(err)
	}
	intent, err := h.occurrences.intent(id)
	if err != nil {
		t.Fatal(err)
	}
	window := &hlsWindow{generation: 3, start: 2, produced: 1, served: -1, videoConversion: true, cancel: cancel, done: done, run: run}
	h.mu.Lock()
	err = intent.install(run, func() {
		h.installOccurrenceLocked(id, run, cancel)
		h.windows[id] = window
	})
	h.mu.Unlock()
	intent.release()
	if err != nil {
		t.Fatal(err)
	}
	f := &restartRetirementFixture{t: t, db: db, h: h, id: id, stop: stop, entry: intent.entry, run: run, window: window}
	f.ctx = h.occurrenceContext(withProducerGeneration(ctx, 3), id, intent.entry, run)
	var once sync.Once
	f.finish = func() {
		once.Do(func() {
			h.mu.Lock()
			if h.windows[id] == window {
				h.removeOccurrenceLocked(id, run)
				delete(h.windows, id)
			}
			close(done)
			h.mu.Unlock()
			h.occurrences.finished(id, intent.entry, run)
		})
	}
	t.Cleanup(func() {
		stop()
		f.finish()
		joined := make(chan struct{})
		go func() { f.workers.Wait(); h.producers.Wait(); close(joined) }()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Error("restart fixture did not join its producers")
		}
	})
	return f
}

func (f *restartRetirementFixture) spawn(run func()) {
	f.workers.Add(1)
	go func() { defer f.workers.Done(); run() }()
}

func (f *restartRetirementFixture) restart() <-chan error {
	result := make(chan error, 1)
	f.spawn(func() { result <- f.h.startGeneration(context.Background(), f.id, 0, 3); close(result) })
	return result
}

func (f *restartRetirementFixture) wait(signal <-chan struct{}) {
	f.t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		f.t.Fatal("restart control did not reach its barrier")
	}
}

func (f *restartRetirementFixture) assertPlaying() {
	f.t.Helper()
	var state string
	if err := f.db.QueryRow(`SELECT state FROM playback_sessions WHERE id=?`, f.id).Scan(&state); err != nil || state != "playing" {
		f.t.Fatal("retired producer changed the live presentation", state, err)
	}
}

func TestHLSRestartRetainsPhysicalOwnershipWithoutAcceptingRetiredFailure(t *testing.T) {
	f := newRestartRetirementFixture(t)
	cancelled, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	failed := make(chan bool, 1)
	f.spawn(func() {
		<-f.ctx.Done()
		close(cancelled)
		<-release
		f.stop() // The replacement exits before opening this fixture's synthetic source.
		failed <- f.h.failed(f.ctx, f.id)
		f.finish()
	})
	started := f.restart()
	f.wait(cancelled)
	if !f.h.occurrences.ownsRun(f.id, f.entry, f.run) {
		t.Fatal("restart discarded owned IO before physical retirement")
	}
	select {
	case err := <-started:
		t.Fatal("restart returned before the old producer joined", err)
	default:
	}
	unblock()
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	if <-failed {
		t.Fatal("intentionally retired producer failed its same-generation successor")
	}
	f.assertPlaying()
}

func TestHLSRestartRejectsAProducerWriteQueuedBeforeRetirement(t *testing.T) {
	f := newRestartRetirementFixture(t)
	held, err := dbwork.Begin(context.Background(), f.db, dbwork.ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Rollback() })
	writeDone := make(chan struct{})
	written := make(chan error, 1)
	f.spawn(func() {
		ctx, cancel := persist(f.ctx)
		defer cancel()
		_, err := f.h.producerWrite(ctx, f.id, dbwork.ClassEstablishedPlayback, `UPDATE playback_sessions SET state='failed' WHERE id=?`, f.id)
		written <- err
		close(writeDone)
	})
	deadline := time.Now().Add(5 * time.Second)
	for dbwork.WriteGate().Waiting()[dbwork.ClassEstablishedPlayback.String()] == 0 {
		if time.Now().After(deadline) {
			t.Fatal("producer write never queued behind the controlled writer")
		}
		time.Sleep(time.Millisecond)
	}
	cancelled := make(chan struct{})
	f.spawn(func() {
		<-f.ctx.Done()
		close(cancelled)
		<-writeDone
		f.stop()
		f.finish()
	})
	started := f.restart()
	f.wait(cancelled)
	if !f.h.occurrences.ownsRun(f.id, f.entry, f.run) {
		t.Fatal("queued producer lost physical ownership before it joined")
	}
	if err := held.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-written; !errors.Is(err, errOccurrenceClaimed) {
		t.Fatal("retired write passed the accepting SQL writer", err)
	}
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	f.assertPlaying()
}
