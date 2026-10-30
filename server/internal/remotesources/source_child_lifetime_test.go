package remotesources

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestSourceRequestCancellationDoesNotRetireParentAuthority(t *testing.T) {
	f := newObservationFixture(t)
	identities, owner, family := observationOwner(t, f)
	ref, err := f.record(t, scanFacts("child-life"), 0)
	if err != nil {
		t.Fatal(err)
	}
	lease := captureOwnerSource(t, f, ref, identities, owner, SourceReading, func() {})
	defer lease.Close()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, check := range []func() error{
		func() error { return lease.Check(canceled) },
		func() error { _, err := lease.OpenRange(canceled); return err },
		func() error { _, err := lease.OpenSequential(canceled); return err },
	} {
		if err := check(); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled request was not refused", err)
		}
		if err := lease.Check(context.Background()); err != nil {
			t.Fatal("child request retired retained owner authority", err)
		}
	}
	view, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.CheckTx(canceled, view.Tx()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	view.Rollback()
	if err = lease.Check(context.Background()); err != nil {
		t.Fatal("canceled caller transaction poisoned the retained lease", err)
	}
	if _, err := f.s.db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=?`, family); err != nil {
		t.Fatal(err)
	}
	if err = lease.childFailure(canceled, context.Canceled); errors.Is(err, context.Canceled) || err == nil {
		t.Fatal("cancellation shielded actual owner revocation", err)
	}
	if _, err := f.s.db.Exec(`UPDATE authorization_session_families SET revoked=0 WHERE id=?`, family); err != nil {
		t.Fatal(err)
	}
	if err = lease.Check(context.Background()); err == nil {
		t.Fatal("owner ABA revived the revoked retained lease")
	}
}

// Registry bookkeeping needs no fake physical epoch or provider. Active
// actual range-close coverage belongs to the controlled-rclone controller case.
func TestSourceChildRegistryCloseLeavesParentLive(t *testing.T) {
	f := newObservationFixture(t)
	identities, owner, _ := observationOwner(t, f)
	ref, err := f.record(t, scanFacts("close-child"), 0)
	if err != nil {
		t.Fatal(err)
	}
	lease := captureOwnerSource(t, f, ref, identities, owner, SourceReading, func() {})
	defer lease.Close()
	child := &registryChild{closed: make(chan struct{})}
	if err = lease.keepChild(child); err != nil {
		t.Fatal(err)
	}
	lease.closeChildren()
	select {
	case <-child.closed:
	case <-time.After(time.Second):
		t.Fatal("retained child was not closed")
	}
	lease.dropChild(child)
	if err = lease.Check(context.Background()); err != nil {
		t.Fatal("closing child registry retired parent", err)
	}
}

type registryChild struct {
	once   sync.Once
	closed chan struct{}
}

func (c *registryChild) Close() error { c.once.Do(func() { close(c.closed) }); return nil }

func TestSourceTerminalFailureIsNeverDowngradedToChildCancellation(t *testing.T) {
	f := newObservationFixture(t)
	identities, owner, _ := observationOwner(t, f)
	ref, err := f.record(t, scanFacts("child-terminal"), 0)
	if err != nil {
		t.Fatal(err)
	}
	lease := captureOwnerSource(t, f, ref, identities, owner, SourceReading, func() {})
	defer lease.Close()
	lease.fail(storage.ErrRemoteChanged)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := lease.childFailure(canceled, context.Canceled); !errors.Is(err, storage.ErrRemoteChanged) {
		t.Fatal("request cancellation concealed source retirement", err)
	}
}
