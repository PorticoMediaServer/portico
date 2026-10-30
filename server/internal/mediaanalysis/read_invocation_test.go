package mediaanalysis

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Root §64: one actual analysis invocation retains its own source/profile consent
// and physical facts through byte IO, decoder exit and the accepting publication.
// A failed or retired read never releases bytes, and cleanup belongs only to the
// original invocation.

type consentInput struct {
	data   []byte
	closed bool
}

func (i *consentInput) Evidence() string { return "observed:fixture" }
func (i *consentInput) Size() int64      { return int64(len(i.data)) }
func (i *consentInput) ReadExtent(_ context.Context, offset, length int64) ([]byte, error) {
	if offset < 0 || length < 0 || offset >= int64(len(i.data)) {
		return nil, nil
	}
	end := min(offset+length, int64(len(i.data)))
	return append([]byte(nil), i.data[offset:end]...), nil
}
func (i *consentInput) Validate(context.Context) error { return nil }
func (i *consentInput) Close() error                   { i.closed = true; return nil }

func consentInvocationFacts(t *testing.T, f *analysisConsentFixture) ReadFacts {
	t.Helper()
	ctx := context.Background()
	tx, closeRead, err := dbwork.BeginRead(ctx, f.c.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRead()
	facts, err := readFactsTx(ctx, tx, f.source, f.object, f.revision, f.item, f.asset)
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

func TestReadInvocationRetainsCheckerUntilOriginalClose(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newAnalysisConsentFixture(t, true, true)
	facts := consentInvocationFacts(t, f)
	s := &Service{db: f.c.DB}
	ctx := context.Background()
	retainedChecks, retainedCloses := 0, 0
	life, invocation := s.readContext(ctx, facts, func(check context.Context, tx *sql.Tx) error {
		return checkReadFactsTx(check, tx, facts)
	})
	defer invocation.Close()
	if err := invocation.Retain(func(check context.Context, tx *sql.Tx) error {
		retainedChecks++
		return checkReadFactsTx(check, tx, facts)
	}, func() error {
		retainedCloses++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The production wrapper's input closes after its bytes; the retained checker
	// and the original invocation must outlive it.
	child := &authorityInput{Input: &consentInput{data: []byte("payload")}, invocation: invocation}
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	if err := invocation.Check(life); err != nil {
		t.Fatal("retained checker failed after the child input closed", err)
	}
	if retainedChecks == 0 {
		t.Fatal("retained source checker was never consulted")
	}
	tx, closeRead, err := dbwork.BeginRead(ctx, f.c.DB)
	if err != nil {
		t.Fatal(err)
	}
	if err = checkReadPublicationTx(life, tx); err != nil {
		closeRead()
		t.Fatal("accepting publication refused the exact live invocation", err)
	}
	closeRead()
	f.setConsent(3, false)
	tx, closeRead, err = dbwork.BeginRead(ctx, f.c.DB)
	if err != nil {
		t.Fatal(err)
	}
	if err = checkReadPublicationTx(life, tx); !errors.Is(err, storage.ErrRemoteAnalysisDisabled) {
		closeRead()
		t.Fatalf("publication accepted disabled consent: %v", err)
	}
	closeRead()
	// Cleanup still runs while the original authority stays retired. Retire starts
	// the asynchronous original close; the explicit Close joins it, so the retained
	// source is released exactly once before Close returns.
	invocation.Retire()
	if err = invocation.Close(); err != nil {
		t.Fatal(err)
	}
	if retainedCloses != 1 {
		t.Fatalf("retained source closed %d times", retainedCloses)
	}
	if err = invocation.Check(context.Background()); err == nil {
		t.Fatal("closed invocation still checked")
	}
	if err = invocation.Retain(func(context.Context, *sql.Tx) error { return nil }, func() error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("closed invocation accepted another retained input: %v", err)
	}
	invocation.Close()
	if retainedCloses != 1 {
		t.Fatalf("repeated close released the retained source again: %d", retainedCloses)
	}
}

func TestAuthorityInputSuppressesBytesAfterAuthorityLoss(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newAnalysisConsentFixture(t, true, true)
	facts := consentInvocationFacts(t, f)
	s := &Service{db: f.c.DB}
	calls := 0
	life, invocation := s.readContext(context.Background(), facts, func(context.Context, *sql.Tx) error {
		calls++
		if calls > 1 {
			return storage.ErrRemoteChanged
		}
		return nil
	})
	defer invocation.Close()
	input := &authorityInput{Input: &consentInput{data: []byte("payload")}, invocation: invocation}
	data, err := input.ReadExtent(life, 0, int64(len("payload")))
	if err == nil {
		t.Fatal("read succeeded after the post-read authority check failed")
	}
	if data != nil {
		t.Fatal("bytes escaped after the post-read authority check failed")
	}
	// The observed failure retires this original; it cannot check again.
	if err := invocation.Check(context.Background()); err == nil {
		t.Fatal("authority failure did not retire the original invocation")
	}
}

func TestReadInvocationCancellationClosesRetainedInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newAnalysisConsentFixture(t, true, true)
	facts := consentInvocationFacts(t, f)
	s := &Service{db: f.c.DB}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	closed := make(chan struct{}, 1)
	_, invocation := s.readContext(parent, facts, func(context.Context, *sql.Tx) error { return nil })
	invocation.Retain(func(context.Context, *sql.Tx) error { return nil }, func() error {
		select {
		case closed <- struct{}{}:
		default:
		}
		return nil
	})
	cancel()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelling the original invocation did not close its retained input")
	}
	if err := invocation.Check(context.Background()); err == nil {
		t.Fatal("cancelled invocation still checked")
	}
}
