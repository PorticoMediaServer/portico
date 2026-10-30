package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestSourceRemovalBarrierRefusesReadersWithoutRetiringThem(t *testing.T) {
	f := newObservationFixture(t)
	identities, owner, _ := observationOwner(t, f)
	ref, err := f.record(t, scanFacts("remove"), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, purpose := range []SourceReadPurpose{SourcePlayback, SourceReading} {
		lease := captureOwnerSource(t, f, ref, identities, owner, purpose, func() { t.Error("removal retired a current reader") })
		if _, err = f.s.guardSourceRemoval(context.Background(), ref.SourceID); !errors.Is(err, mounts.ErrSourceInUse) {
			t.Fatal("active reader admitted removal", purpose, err)
		}
		if err = lease.Check(context.Background()); err != nil {
			t.Fatal("busy refusal changed reader authority", err)
		}
		lease.Close()
	}
	release, err := f.s.guardSourceRemoval(context.Background(), ref.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err = f.s.guardSourceRemoval(context.Background(), ref.SourceID); !errors.Is(err, mounts.ErrSourceInUse) {
		t.Fatal("second removal entered barrier", err)
	}
	if _, _, err = f.s.beginCandidateTest(context.Background(), ref.SourceID, ReadLimits{Connections: 2, RequestsPerSecond: 2}); !errors.Is(err, storage.ErrBusy) {
		t.Fatal("connection test entered removal barrier", err)
	}
	if _, err = f.s.ensureSourceServe(context.Background(), SourceReadBinding{Observation: ref}, ""); !errors.Is(err, storage.ErrBusy) {
		t.Fatal("producer entered removal barrier", err)
	}
	view, err := dbwork.BeginSnapshot(context.Background(), f.s.db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.s.CaptureReadSourceTx(context.Background(), view.Tx(), SourceSessionAdmission{Purpose: SourcePlayback, Observation: ref, OnChanged: func() {}, CheckTx: func(ctx context.Context, tx *sql.Tx, _ SourceReadBinding) error {
		return currentLocalOwnerTx(ctx, tx, identities, owner)
	}})
	view.Rollback()
	if !errors.Is(err, storage.ErrBusy) || len(f.s.serveLeases) != 0 {
		t.Fatal("removal admitted a new reader or process", err)
	}
	release()
	release()
	lease := captureOwnerSource(t, f, ref, identities, owner, SourceReading, func() {})
	lease.Close()
}
