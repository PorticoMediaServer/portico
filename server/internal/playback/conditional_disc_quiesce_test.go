//go:build linux || darwin

package playback

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// These regressions exercise the actual quiesce step over one retained
// conditional object and one supervised attempt. They use a temporary OS pipe,
// a fake object and a canceled copy completion; no constructor, helper,
// provider, subprocess, media bytes or native/runtime claim is involved.

type quiesceOrder struct {
	mu     sync.Mutex
	events []string
}

func (o *quiesceOrder) add(event string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.events = append(o.events, event)
	o.mu.Unlock()
}

func (o *quiesceOrder) index(event string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i, e := range o.events {
		if e == event {
			return i
		}
	}
	return -1
}

type quiesceObject struct {
	mu       sync.Mutex
	order    *quiesceOrder
	snapshot storage.Snapshot
	next     storage.Snapshot
	observes int
	closes   int
}

func (o *quiesceObject) Snapshot() storage.Snapshot   { return o.snapshot }
func (o *quiesceObject) Version() mediasource.Version { return mediasource.Version{} }
func (o *quiesceObject) Access() string               { return "finite_random" }
func (o *quiesceObject) OpenRange(context.Context, int64, int64) (io.ReadCloser, error) {
	return nil, storage.ErrRemoteOffline
}
func (o *quiesceObject) Observe(ctx context.Context) (storage.Snapshot, error) {
	o.order.add("observe")
	o.mu.Lock()
	o.observes++
	next := o.next
	o.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return storage.Snapshot{}, err
	}
	return next, nil
}
func (o *quiesceObject) Close() error {
	o.order.add("object-close")
	o.mu.Lock()
	o.closes++
	o.mu.Unlock()
	return nil
}
func (o *quiesceObject) counts() (observes, closes int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.observes, o.closes
}

type quiesceSource struct {
	order  *quiesceOrder
	closes int32
}

func (s *quiesceSource) Read([]byte) (int, error)    { return 0, io.EOF }
func (s *quiesceSource) Check(context.Context) error { return nil }
func (s *quiesceSource) Close() error {
	atomic.AddInt32(&s.closes, 1)
	s.order.add("source-close")
	return nil
}

type quiesceChecks struct {
	order     *quiesceOrder
	validate  func(int) error
	source    func(int) error
	validates int32
	sources   int32
}

func (c *quiesceChecks) validateFn(context.Context) error {
	n := int(atomic.AddInt32(&c.validates, 1))
	c.order.add("validate")
	if c.validate == nil {
		return nil
	}
	return c.validate(n)
}

func (c *quiesceChecks) sourceFn(context.Context) error {
	n := int(atomic.AddInt32(&c.sources, 1))
	c.order.add("check-source")
	if c.source == nil {
		return nil
	}
	return c.source(n)
}

// newQuiesceAttempt builds one attempt over a real pipe and registers its
// descriptor cleanup before the copy goroutine - and any later wait - starts.
// The completion sets its actual result, closes readDone, then joins through
// done exactly as the supervised copy does.
func newQuiesceAttempt(t *testing.T, object *quiesceObject, completion error) (*conditionalDiscAttempt, *os.File) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})
	released := make(chan struct{})
	var once sync.Once
	release := func() {
		once.Do(func() {
			close(released)
			object.order.add("release")
		})
	}
	attempt := &conditionalDiscAttempt{
		file:     reader,
		writer:   writer,
		source:   &quiesceSource{order: object.order},
		release:  release,
		readDone: make(chan struct{}),
		done:     make(chan struct{}),
	}
	// Register cancellation and a checked join before starting the fixture worker.
	t.Cleanup(func() {
		release()
		select {
		case <-attempt.done:
		case <-time.After(3 * time.Second):
			t.Error("quiesce fixture worker did not retire")
		}
	})
	go func() {
		<-released
		attempt.err = completion
		object.order.add("copy-join")
		close(attempt.readDone)
		close(attempt.done)
	}()
	return attempt, reader
}

func runQuiesce(t *testing.T, release func(), fn func() error) error {
	t.Helper()
	done := make(chan error, 1)
	joined := make(chan struct{})
	t.Cleanup(func() {
		release()
		select {
		case <-joined:
		case <-time.After(3 * time.Second):
			t.Error("quiesce invocation did not retire during cleanup")
		}
	})
	go func() { defer close(joined); done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("quiesce did not join its original attempt")
		return nil
	}
}

func newQuiesceOwner(t *testing.T, object *quiesceObject) *conditionalDiscPipe {
	t.Helper()
	life, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &conditionalDiscPipe{life: life, cancel: cancel, object: object}
}

func quiesceFixture(t *testing.T) (*conditionalDiscPipe, *quiesceObject, *quiesceChecks, *assets.PlaybackInput, storage.Snapshot) {
	t.Helper()
	order := &quiesceOrder{}
	snapshot := storage.Snapshot{Size: 16, ObjectID: "object", Revision: "revision-1"}
	object := &quiesceObject{order: order, snapshot: snapshot, next: snapshot}
	owner := newQuiesceOwner(t, object)
	checks := &quiesceChecks{order: order}
	input := &assets.PlaybackInput{}
	return owner, object, checks, input, snapshot
}

// A benign, close-canceled attempt is joined before this same object is freshly
// observed and the borrow cleared; the origin stays open for its own later input.
func TestQuiesceJoinsAttemptThenObservesSameObject(t *testing.T) {
	owner, object, checks, input, snapshot := quiesceFixture(t)
	attempt, reader := newQuiesceAttempt(t, object, context.Canceled)
	owner.active, input.File = attempt, reader
	err := runQuiesce(t, attempt.release, func() error {
		return quiesceConditionalDiscInput(context.Background(), owner, input, snapshot, checks.validateFn, checks.sourceFn)
	})
	if err != nil {
		t.Fatal(err)
	}
	if owner.active != nil || input.File != nil {
		t.Fatal("quiesce left its retired attempt or input file attached")
	}
	if observes, closes := object.counts(); observes != 1 || closes != 0 {
		t.Fatalf("object observed %d times and closed %d times", observes, closes)
	}
	if release, observe := object.order.index("release"), object.order.index("observe"); release == -1 || observe == -1 || release > observe {
		t.Fatal("the attempt was not joined before the fresh same-object observation")
	}
	if joined, observe := object.order.index("copy-join"), object.order.index("observe"); joined == -1 || joined > observe {
		t.Fatal("the copy did not finish before the fresh observation")
	}
	if checks.sources < 2 {
		t.Fatalf("quiesce proved the original only %d times around observation", checks.sources)
	}
	if owner.life.Err() != nil {
		t.Fatal("a successful quiesce retired the retained origin")
	}
	select {
	case <-attempt.done:
	default:
		t.Fatal("quiesce returned before joining the attempt")
	}
}

// A known non-cancel attempt failure survives quiesce and refuses without a
// fresh observation or a cleared borrow.
func TestQuiesceKeepsKnownAttemptFailureWithoutObserving(t *testing.T) {
	owner, object, checks, input, snapshot := quiesceFixture(t)
	failure := errors.New("helper failed before EOF")
	attempt, reader := newQuiesceAttempt(t, object, failure)
	owner.active, input.File = attempt, reader
	err := runQuiesce(t, attempt.release, func() error {
		return quiesceConditionalDiscInput(context.Background(), owner, input, snapshot, checks.validateFn, checks.sourceFn)
	})
	if err != failure {
		t.Fatalf("known attempt failure became %v", err)
	}
	if observes, _ := object.counts(); observes != 0 {
		t.Fatal("a known attempt failure still observed the object")
	}
	if owner.active != attempt || input.File != reader {
		t.Fatal("a refused quiesce cleared custody it still owns")
	}
	if owner.life.Err() == nil {
		t.Fatal("a known attempt failure did not retire the origin")
	}
}

// A changed snapshot after a benign stop refuses and adopts no facts.
func TestQuiesceRefusesChangedSnapshotWithoutAdopting(t *testing.T) {
	owner, object, checks, input, snapshot := quiesceFixture(t)
	object.next = storage.Snapshot{Size: 17, ObjectID: "object", Revision: "revision-2"}
	attempt, reader := newQuiesceAttempt(t, object, context.Canceled)
	owner.active, input.File = attempt, reader
	err := runQuiesce(t, attempt.release, func() error {
		return quiesceConditionalDiscInput(context.Background(), owner, input, snapshot, checks.validateFn, checks.sourceFn)
	})
	if !errors.Is(err, mediasource.ErrSourceChanged) {
		t.Fatalf("changed snapshot returned %v", err)
	}
	if observes, _ := object.counts(); observes != 1 {
		t.Fatalf("changed snapshot observed %d times", observes)
	}
	if owner.active != attempt || input.File != reader {
		t.Fatal("a changed snapshot cleared the retained borrow")
	}
	if owner.life.Err() == nil {
		t.Fatal("a changed snapshot did not retire the origin")
	}
}

// A refused original-current proof after the join refuses before observation.
func TestQuiesceRefusesRetiredCurrentBeforeObserving(t *testing.T) {
	owner, object, checks, input, snapshot := quiesceFixture(t)
	retired := errors.New("original operation retired")
	checks.source = func(int) error { return retired }
	attempt, reader := newQuiesceAttempt(t, object, context.Canceled)
	owner.active, input.File = attempt, reader
	err := runQuiesce(t, attempt.release, func() error {
		return quiesceConditionalDiscInput(context.Background(), owner, input, snapshot, checks.validateFn, checks.sourceFn)
	})
	if err != retired {
		t.Fatalf("retired proof returned %v", err)
	}
	if observes, _ := object.counts(); observes != 0 {
		t.Fatal("a retired original still observed the object")
	}
	if owner.active != attempt || input.File != reader {
		t.Fatal("a retired original cleared the retained borrow")
	}
}

// A proof that fails only after the fresh observation still refuses and keeps
// the borrow, so measured facts are never adopted without the original check.
func TestQuiesceRefusesProofFailingAfterObservation(t *testing.T) {
	owner, object, checks, input, snapshot := quiesceFixture(t)
	retired := errors.New("original operation retired after observation")
	checks.source = func(n int) error {
		if n >= 2 {
			return retired
		}
		return nil
	}
	attempt, reader := newQuiesceAttempt(t, object, context.Canceled)
	owner.active, input.File = attempt, reader
	err := runQuiesce(t, attempt.release, func() error {
		return quiesceConditionalDiscInput(context.Background(), owner, input, snapshot, checks.validateFn, checks.sourceFn)
	})
	if err != retired {
		t.Fatalf("post-observation proof returned %v", err)
	}
	if observes, _ := object.counts(); observes != 1 {
		t.Fatalf("post-observation refusal observed %d times", observes)
	}
	if owner.active != attempt || input.File != reader {
		t.Fatal("post-observation refusal cleared the retained borrow")
	}
}

// A carried SQL snapshot is refused before validation, the join or observation,
// so the caller keeps its custody outside that transaction.
func TestQuiesceRefusesCarriedSnapshotBeforeWork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	root := t.TempDir()
	db, err := persistence.OpenFresh(filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	view, err := dbwork.BeginSnapshot(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Rollback()
	owner, object, checks, input, snapshot := quiesceFixture(t)
	attempt, reader := newQuiesceAttempt(t, object, context.Canceled)
	owner.active, input.File = attempt, reader
	err = runQuiesce(t, attempt.release, func() error {
		return quiesceConditionalDiscInput(view.Context(), owner, input, snapshot, checks.validateFn, checks.sourceFn)
	})
	if !errors.Is(err, disc.ErrProtocol) {
		t.Fatalf("carried snapshot returned %v", err)
	}
	if checks.validates != 0 || checks.sources != 0 {
		t.Fatal("a carried snapshot ran validation or the original proof")
	}
	if observes, closes := object.counts(); observes != 0 || closes != 0 {
		t.Fatal("a carried snapshot observed or closed the object")
	}
	if object.order.index("release") != -1 {
		t.Fatal("a carried snapshot stopped the retained attempt")
	}
	if owner.active != attempt || input.File != reader {
		t.Fatal("a carried snapshot changed caller custody")
	}
}
