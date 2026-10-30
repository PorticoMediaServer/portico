package remotesources

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// These regressions exercise the conditional disc wrapper's purpose, lifetime
// and range guards against the real observation fixture and read lease. Only the
// source object and its body are fakes; no provider, helper, subprocess or
// network is involved.

func isoObservationFixture(t *testing.T) (observationFixture, ObservationRef) {
	t.Helper()
	f := newObservationFixture(t)
	f.file = filepath.Join(filepath.Dir(f.file), "disc.iso")
	facts := scanFacts("iso")
	facts.Facts.Object = "disc.iso"
	ref, err := f.record(t, facts, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f, ref
}

type discTestObject struct {
	snapshot storage.Snapshot
	version  mediasource.Version
	bodies   []*discTestBody
	mu       sync.Mutex
	closes   int
}

func (o *discTestObject) Snapshot() storage.Snapshot   { return o.snapshot }
func (o *discTestObject) Version() mediasource.Version { return o.version }
func (o *discTestObject) Access() string               { return "finite_random" }
func (o *discTestObject) Observe(ctx context.Context) (storage.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return storage.Snapshot{}, err
	}
	return o.snapshot, nil
}
func (o *discTestObject) OpenRange(ctx context.Context, offset, length int64) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.bodies) == 0 {
		return nil, storage.ErrRemoteOffline
	}
	body := o.bodies[0]
	o.bodies = o.bodies[1:]
	return body, nil
}
func (o *discTestObject) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closes++
	return nil
}

// discTestBody blocks the first Read until the test releases it, then returns
// the supplied bytes. It counts its own Close calls.
type discTestBody struct {
	entered     chan struct{}
	release     chan struct{}
	enteredOnce sync.Once
	releaseOnce sync.Once
	data        []byte
	read        bool
	closed      int32
}

func newDiscTestBody(data string) *discTestBody {
	return &discTestBody{entered: make(chan struct{}), release: make(chan struct{}), data: []byte(data)}
}

func (b *discTestBody) unblock() { b.releaseOnce.Do(func() { close(b.release) }) }
func (b *discTestBody) closeCount() int {
	return int(atomic.LoadInt32(&b.closed))
}

func (b *discTestBody) Read(p []byte) (int, error) {
	b.enteredOnce.Do(func() { close(b.entered) })
	<-b.release
	if b.read {
		return 0, io.EOF
	}
	b.read = true
	return copy(p, b.data), nil
}

func (b *discTestBody) Close() error {
	atomic.AddInt32(&b.closed, 1)
	b.unblock()
	return nil
}

func newDiscTestVersion(t *testing.T) (mediasource.Version, storage.Snapshot) {
	t.Helper()
	snapshot := storage.Snapshot{Size: 16, ObjectID: "object", Revision: "revision"}
	version, err := mediasource.NewVersion(mediasource.Evidence{Kind: mediasource.StrongETag, Scope: "remote:1", Object: "object", Revision: `"etag"`, Size: 16})
	if err != nil {
		t.Fatal(err)
	}
	return version, snapshot
}

func discConditionalSource(t *testing.T, lease *SourceReadLease, object *discTestObject, version mediasource.Version, snapshot storage.Snapshot) *discConditionalObject {
	t.Helper()
	source := &discConditionalObject{lease: lease, object: object, version: version, snapshot: snapshot}
	t.Cleanup(func() { _ = source.Close() })
	return source
}

func discConditionalPlaybackLease(t *testing.T) (observationFixture, ObservationRef, *SourceReadLease) {
	t.Helper()
	f, ref := isoObservationFixture(t)
	identities, owner, _ := observationOwner(t, f)
	lease := captureOwnerSource(t, f, ref, identities, owner, SourcePlayback, func() {})
	t.Cleanup(func() { _ = lease.Close() })
	return f, ref, lease
}

// A lease retired while the body is blocked yields no bytes: the caller's buffer
// stays untouched and the range closes its body exactly once.
func TestDiscConditionalRangeDropsBlockedBytesAfterLeaseRetires(t *testing.T) {
	_, _, lease := discConditionalPlaybackLease(t)
	version, snapshot := newDiscTestVersion(t)
	body := newDiscTestBody("payload bytes")
	object := &discTestObject{snapshot: snapshot, version: version, bodies: []*discTestBody{body}}
	source := discConditionalSource(t, lease, object, version, snapshot)
	childCtx, releaseChild := lease.bound(context.Background())
	child := &discConditionalRange{ctx: childCtx, source: source, body: body, release: releaseChild}
	buffer := bytes.Repeat([]byte{'k'}, len("payload bytes"))
	sentinel := append([]byte(nil), buffer...)
	type readResult struct {
		n   int
		err error
	}
	result := make(chan readResult, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		n, err := child.Read(buffer)
		result <- readResult{n, err}
	}()
	// A failed assertion before the normal release must unblock the body and
	// join the read goroutine.
	t.Cleanup(func() {
		body.unblock()
		_ = child.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("range read did not retire during cleanup")
		}
	})
	select {
	case <-body.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("range read never reached the blocked body")
	}
	lease.Close()
	body.unblock()
	select {
	case got := <-result:
		if got.n != 0 {
			t.Fatalf("retired lease returned %d bytes", got.n)
		}
		if got.err == nil {
			t.Fatal("retired lease returned no error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("range read did not finish after the body was released")
	}
	if !bytes.Equal(buffer, sentinel) {
		t.Fatal("bytes from a retired source were published into the caller buffer")
	}
	if got := body.closeCount(); got != 1 {
		t.Fatalf("range closed its body %d times", got)
	}
	if err := lease.Check(context.Background()); err == nil {
		t.Fatal("retired lease stayed current")
	}
}

// The valid original control returns bytes, and closing a child range neither
// closes its body twice nor retires the shared logical lease for a sibling.
func TestDiscConditionalRangeValidReadAndChildClose(t *testing.T) {
	_, _, lease := discConditionalPlaybackLease(t)
	version, snapshot := newDiscTestVersion(t)
	first := newDiscTestBody("first")
	second := newDiscTestBody("second")
	object := &discTestObject{snapshot: snapshot, version: version, bodies: []*discTestBody{first, second}}
	source := discConditionalSource(t, lease, object, version, snapshot)
	siblingCtx, releaseSibling := lease.bound(context.Background())
	t.Cleanup(releaseSibling)
	first.unblock()
	childCtx, releaseChild := lease.bound(context.Background())
	child := &discConditionalRange{ctx: childCtx, source: source, body: first, release: releaseChild}
	t.Cleanup(func() { _ = child.Close() })
	buffer := make([]byte, len("first"))
	n, err := child.Read(buffer)
	if err != nil || n != len("first") || string(buffer) != "first" {
		t.Fatalf("valid read returned %d %v %q", n, err, buffer)
	}
	if err = child.Close(); err != nil {
		t.Fatal(err)
	}
	child.Close()
	if got := first.closeCount(); got != 1 {
		t.Fatalf("child body closed %d times", got)
	}
	if childCtx.Err() == nil {
		t.Fatal("closing the child range did not release its bound context")
	}
	if siblingCtx.Err() != nil {
		t.Fatal("closing a child retired an independent sibling context")
	}
	if err = lease.Check(context.Background()); err != nil {
		t.Fatalf("closing a child retired its original lease: %v", err)
	}
	second.unblock()
	sibling := &discConditionalRange{ctx: siblingCtx, source: source, body: second, release: releaseSibling}
	t.Cleanup(func() { _ = sibling.Close() })
	buffer = make([]byte, len("second"))
	n, err = sibling.Read(buffer)
	if err != nil || n != len("second") || string(buffer) != "second" {
		t.Fatalf("sibling read returned %d %v %q", n, err, buffer)
	}
	if err = sibling.Close(); err != nil {
		t.Fatal(err)
	}
	if got := second.closeCount(); got != 1 {
		t.Fatalf("sibling body closed %d times", got)
	}
	if siblingCtx.Err() == nil {
		t.Fatal("closing the sibling did not release its own bound context")
	}
	if err = lease.Check(context.Background()); err != nil {
		t.Fatalf("sibling close retired the original lease: %v", err)
	}
}

// Purpose and extension are refused before any acquisition, and only the
// supplied lease is closed; a sibling stays current and no source process starts.
func TestOpenDiscConditionalRefusesPurposeAndNonISOBeforeAcquire(t *testing.T) {
	// A reading-purpose lease over an ISO is refused on purpose.
	f, ref := isoObservationFixture(t)
	identities, owner, _ := observationOwner(t, f)
	reading := captureOwnerSource(t, f, ref, identities, owner, SourceReading, func() {})
	t.Cleanup(func() { _ = reading.Close() })
	sibling := captureOwnerSource(t, f, ref, identities, owner, SourcePlayback, func() {})
	t.Cleanup(func() { _ = sibling.Close() })
	if _, err := reading.OpenDiscConditional(context.Background()); !errors.Is(err, storage.ErrRemoteRange) {
		t.Fatalf("non-playback purpose returned %v", err)
	}
	if err := reading.Check(context.Background()); err == nil {
		t.Fatal("refused purpose left its supplied lease current")
	}
	if err := sibling.Check(context.Background()); err != nil {
		t.Fatalf("refusing one lease disturbed a sibling: %v", err)
	}
	if len(f.s.serveLeases) != 0 {
		t.Fatal("a refusal started source work")
	}
	// A playback lease over a non-ISO object is refused on extension.
	plain := newObservationFixture(t)
	plainIdentities, plainOwner, _ := observationOwner(t, plain)
	plainRef, err := plain.record(t, scanFacts("plain"), 0)
	if err != nil {
		t.Fatal(err)
	}
	nonISO := captureOwnerSource(t, plain, plainRef, plainIdentities, plainOwner, SourcePlayback, func() {})
	t.Cleanup(func() { _ = nonISO.Close() })
	if _, err = nonISO.OpenDiscConditional(context.Background()); !errors.Is(err, storage.ErrRemoteRange) {
		t.Fatalf("non-ISO object returned %v", err)
	}
	if len(plain.s.serveLeases) != 0 {
		t.Fatal("a non-ISO refusal started source work")
	}
}

// A carried SQL transaction is refused before IO and leaves the caller's lease
// custody intact for its own outside-transaction cleanup.
func TestOpenDiscConditionalRefusesOuterSQLBeforeIO(t *testing.T) {
	f, _, lease := discConditionalPlaybackLease(t)
	gated, err := dbwork.Begin(context.Background(), f.s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lease.OpenDiscConditional(gated.Context()); !errors.Is(err, storage.ErrRemoteConfig) {
		gated.Rollback()
		t.Fatalf("outer SQL context returned %v", err)
	}
	if err = lease.CheckTx(gated.Context(), gated.Tx()); err != nil {
		gated.Rollback()
		t.Fatalf("outer-SQL refusal closed the caller's lease in the same writer: %v", err)
	}
	if len(f.s.serveLeases) != 0 {
		gated.Rollback()
		t.Fatal("outer-SQL refusal started source work")
	}
	gated.Rollback()
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err = lease.Check(context.Background()); err == nil {
		t.Fatal("outside-transaction cleanup left the lease current")
	}
}
