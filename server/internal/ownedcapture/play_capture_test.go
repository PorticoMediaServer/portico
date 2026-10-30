//go:build linux || darwin

package ownedcapture

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// These regressions exercise CapturePlay's original-operation admission and the
// per-read/per-write checks against the package's actual managed-artifact and
// SQL receipt fixture. The fake producer policy is test admission only; it is
// not production authority.

// playGuard is a bounded original-operation check that records every call, the
// transaction it received and whether that transaction was carried by its
// context. It can be retired inside a copy so the checks after real bytes were
// produced are exercised.
type playGuard struct {
	mu       sync.Mutex
	calls    int
	nullTx   int
	detached int
	fail     error
	retired  bool
}

func (g *playGuard) check(ctx context.Context, tx *sql.Tx) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if tx == nil {
		g.nullTx++
	}
	if dbwork.Snapshot(ctx) != tx {
		g.detached++
	}
	return g.fail
}

func (g *playGuard) retire(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.retired {
		g.retired = true
		g.fail = err
	}
}

func (g *playGuard) counts() (calls, nullTx, detached int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls, g.nullTx, g.detached
}

func playCaptureContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func readyLifecycleRows(t *testing.T, s *Service) int {
	t.Helper()
	var n int
	if e := s.db.QueryRow(`SELECT count(*) FROM owned_capture_lifecycle WHERE state='ready'`).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

// The original check is mandatory, its deadline must be the finite original
// Play wait and not already expired, and carried transactions are refused
// before it or the source runs.
func TestCapturePlayRequiresOriginalCheckFiniteUnexpiredDeadlineAndNoOuterTx(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p := fixture(t)
	request := Request{AssetID: "physical-asset"}
	guard := &playGuard{}
	if _, e := s.CapturePlay(playCaptureContext(t), request, nil); !errors.Is(e, ErrInput) {
		t.Fatalf("nil original check returned %v", e)
	}
	if _, e := s.CapturePlay(context.Background(), request, guard.check); !errors.Is(e, ErrInput) {
		t.Fatalf("missing deadline returned %v", e)
	}
	long, cancel := context.WithTimeout(context.Background(), 125*time.Second)
	defer cancel()
	if _, e := s.CapturePlay(long, request, guard.check); !errors.Is(e, ErrInput) {
		t.Fatalf("deadline beyond the shared wait returned %v", e)
	}
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()
	if _, e := s.CapturePlay(expired, request, guard.check); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("expired original deadline returned %v", e)
	}
	// Both carried contexts are finite and rolled back as soon as they are
	// refused, before any later read of the store.
	carried, cancelCarried := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelCarried()
	outer, e := dbwork.Begin(carried, s.db, dbwork.ClassInteractive)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { outer.Rollback() })
	if _, e = s.CapturePlay(outer.Context(), request, guard.check); !errors.Is(e, ErrInput) {
		outer.Rollback()
		t.Fatalf("carried write transaction returned %v", e)
	}
	outer.Rollback()
	view, e := dbwork.BeginSnapshot(carried, s.db)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { view.Rollback() })
	if _, e = s.CapturePlay(view.Context(), request, guard.check); !errors.Is(e, ErrInput) {
		view.Rollback()
		t.Fatalf("carried read transaction returned %v", e)
	}
	view.Rollback()
	if calls, _, _ := guard.counts(); calls != 0 {
		t.Fatalf("original check ran %d times inside a carried transaction", calls)
	}
	if len(p.records) != 0 || p.reads != 0 {
		t.Fatal("source admission or bytes ran before the carried-transaction refusal")
	}
	if countRows(t, s, "owned_capture_receipts") != 0 || countRows(t, s, "owned_capture_sources") != 0 || readyLifecycleRows(t, s) != 0 {
		t.Fatal("a refused CapturePlay published rows")
	}
}

// A retired original operation is refused before admission, so no reservation,
// read or staging write exists for it.
func TestCapturePlayRefusesRetiredOriginalBeforeAdmission(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p := fixture(t)
	guard := &playGuard{}
	guard.retire(ErrCurrent)
	if _, e := s.CapturePlay(playCaptureContext(t), Request{AssetID: "physical-asset"}, guard.check); !errors.Is(e, ErrCurrent) {
		t.Fatalf("retired original returned %v", e)
	}
	calls, nullTx, detached := guard.counts()
	if calls == 0 {
		t.Fatal("the mandatory original check never ran")
	}
	if nullTx != 0 || detached != 0 {
		t.Fatalf("%d nil and %d detached transaction callbacks", nullTx, detached)
	}
	if len(p.records) != 0 || p.reads != 0 || p.persisted != 0 {
		t.Fatal("a refused original still admitted or touched source work")
	}
	if countRows(t, s, "owned_capture_receipts") != 0 || countRows(t, s, "owned_capture_sources") != 0 {
		t.Fatal("a refused original published rows")
	}
}

// The same original operation is rechecked around real reads, real staging
// writes and the receipt's own accepting transaction; every callback carries
// its exact transaction, and only that transaction may return a usable receipt.
func TestCapturePlayRepeatsOriginalAcrossReadsWritesAndAcceptingTx(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p := fixture(t)
	guard := &playGuard{}
	receipt, e := s.CapturePlay(playCaptureContext(t), Request{AssetID: "physical-asset"}, guard.check)
	if e != nil {
		t.Fatal(e)
	}
	if receipt.ID == "" || receipt.Object.Size != int64(len(p.data)) {
		t.Fatal("CapturePlay returned no usable receipt")
	}
	calls, nullTx, detached := guard.counts()
	if calls < 10 {
		t.Fatalf("original check ran only %d times across read, write and publication", calls)
	}
	if nullTx != 0 || detached != 0 {
		t.Fatalf("%d nil and %d detached transaction callbacks", nullTx, detached)
	}
	if p.reads == 0 || p.persisted == 0 {
		t.Fatal("capture never read or accounted real bytes")
	}
	if countRows(t, s, "owned_capture_receipts") != 1 || countRows(t, s, "owned_capture_sources") != 1 || readyLifecycleRows(t, s) != 1 {
		t.Fatal("successful CapturePlay did not publish exactly one ready receipt")
	}
	reader, e := s.Open(context.Background(), receipt)
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	got, e := io.ReadAll(reader.File())
	if e != nil || !bytes.Equal(got, p.data) {
		t.Fatal("published receipt did not open its actual bytes", e)
	}
}

// Retiring the original operation once the source's CommitTx has started is
// refused by the extra check after it: no receipt and no ready row survive, and
// the rolled-back commit retains nothing.
func TestCapturePlayRefusesRetiredOriginalAfterCommitTxStarted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p := fixture(t)
	guard := &playGuard{}
	committed := false
	p.beforeCommit = func(*fakeAcquisition) error {
		guard.retire(ErrCurrent)
		committed = true
		return nil
	}
	receipt, e := s.CapturePlay(playCaptureContext(t), Request{AssetID: "physical-asset"}, guard.check)
	if !committed {
		t.Fatal("the source's CommitTx never started")
	}
	if e == nil || !errors.Is(e, ErrCurrent) || receipt.ID != "" {
		t.Fatalf("retirement after CommitTx returned %q and %v", receipt.ID, e)
	}
	if _, nullTx, detached := guard.counts(); nullTx != 0 || detached != 0 {
		t.Fatalf("%d nil and %d detached transaction callbacks", nullTx, detached)
	}
	if countRows(t, s, "owned_capture_receipts") != 0 || readyLifecycleRows(t, s) != 0 {
		t.Fatal("a refused publication left a usable receipt or ready row")
	}
	for _, r := range p.records {
		if r.retained {
			t.Fatal("the rolled-back CommitTx retained the reservation")
		}
	}
	if p.persisted == 0 {
		t.Fatal("retirement was not observed after real staging writes")
	}
}

// retiringSource retires the original operation synchronously, immediately after
// the source produces its bytes, so the checked reader's post-read check runs on
// retirement.
type retiringSource struct {
	Acquisition
	once   sync.Once
	retire func()
}

func (r *retiringSource) Read(p []byte) (int, error) {
	n, e := r.Acquisition.Read(p)
	r.once.Do(r.retire)
	return n, e
}

// A read whose original retires synchronously after the source produced bytes
// returns no bytes and leaves the caller's cleared prefix zeroed, though the
// source really delivered them.
func TestCheckedReaderWithholdsBytesWhenOriginalRetiresDuringRead(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p := fixture(t)
	guard := &playGuard{}
	ctx := playCaptureContext(t)
	acquisition, e := s.producer.Admit(ctx, uniqueID(), Request{AssetID: "physical-asset"}, s.destination)
	if e != nil {
		t.Fatal(e)
	}
	defer acquisition.Close()
	controlled, ok := acquisition.(*fakeAcquisition)
	if !ok {
		t.Fatal("fixture did not supply its controlled acquisition")
	}
	check := func(c context.Context) error { return s.checkPlayCapture(c, guard.check) }
	source := &retiringSource{Acquisition: acquisition, retire: func() { guard.retire(ErrCurrent) }}
	reader := &checkedReader{ctx: ctx, source: source, original: check}
	buffer := bytes.Repeat([]byte{'k'}, 12)
	n, e := reader.Read(buffer)
	if n != 0 || !errors.Is(e, ErrCurrent) {
		t.Fatalf("retiring read returned %d bytes and %v", n, e)
	}
	for i, b := range buffer {
		if b != 0 {
			t.Fatalf("cleared caller byte %d was %q, not zero", i, b)
		}
	}
	if controlled.delivered < int64(len(buffer)) || p.reads == 0 {
		t.Fatal("the source never produced the withheld bytes")
	}
	calls, nullTx, detached := guard.counts()
	if calls < 2 || nullTx != 0 || detached != 0 {
		t.Fatalf("%d calls, %d nil and %d detached transaction callbacks", calls, nullTx, detached)
	}
}

// retiringWriter retires the original operation after the staging writer
// accepted its bytes, so accounting must still happen before the error.
type retiringWriter struct {
	buffer *bytes.Buffer
	retire func()
}

func (w *retiringWriter) Write(p []byte) (int, error) {
	n, e := w.buffer.Write(p)
	w.retire()
	return n, e
}

// A staging write that straddles original retirement still acknowledges the
// actual successful bytes to the source before the failure is returned.
func TestPersistedWriterAccountsSuccessfulBytesBeforeOriginalRefusal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, p := fixture(t)
	guard := &playGuard{}
	ctx := playCaptureContext(t)
	acquisition, e := s.producer.Admit(ctx, uniqueID(), Request{AssetID: "physical-asset"}, s.destination)
	if e != nil {
		t.Fatal(e)
	}
	defer acquisition.Close()
	controlled, ok := acquisition.(*fakeAcquisition)
	if !ok {
		t.Fatal("fixture did not supply its controlled acquisition")
	}
	payload := make([]byte, 8)
	if n, e := controlled.Read(payload); e != nil || n != len(payload) {
		t.Fatalf("fixture delivered %d bytes: %v", n, e)
	}
	check := func(c context.Context) error { return s.checkPlayCapture(c, guard.check) }
	staging := &bytes.Buffer{}
	sink := &persistedWriter{ctx: ctx, source: acquisition, destination: s.destination, writer: &retiringWriter{buffer: staging, retire: func() { guard.retire(ErrCurrent) }}, original: check}
	n, e := sink.Write(payload)
	if n != len(payload) || !errors.Is(e, ErrCurrent) {
		t.Fatalf("staging write returned %d bytes and %v", n, e)
	}
	if calls, nullTx, detached := guard.counts(); calls < 2 || nullTx != 0 || detached != 0 {
		t.Fatalf("%d calls, %d nil and %d detached transaction callbacks", calls, nullTx, detached)
	}
	if controlled.written != int64(n) || p.persisted != 1 {
		t.Fatalf("actual successful bytes were not acknowledged: writer %d, persisted %d", controlled.written, p.persisted)
	}
	if !bytes.Equal(staging.Bytes(), payload) {
		t.Fatal("the staging writer did not accept the reported bytes")
	}
}
