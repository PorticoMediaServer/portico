package remotesources

import (
	"context"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestSourceProcessSlotWaitsForPhysicalRetirement(t *testing.T) {
	f := newObservationFixture(t)
	ctx := context.Background()
	first, err := f.s.reserveSourceProcess(ctx, "remote", "1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Check(ctx); err != nil {
		t.Fatal(err)
	}
	f.s.stopServeProcess("remote")
	if first.CheckLocal() == nil {
		t.Fatal("source cancellation left lease live")
	}
	if _, err = f.s.reserveSourceProcess(ctx, "remote", "1", 0); !errors.Is(err, storage.ErrBusy) {
		t.Fatal("cancellation released a surviving process slot", err)
	}
	first.Release()
	second, err := f.s.reserveSourceProcess(ctx, "remote", "1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	first.Release()
	if err = second.Check(ctx); err != nil {
		t.Fatal("old release invalidated replacement slot", err)
	}
	if _, err = f.s.reserveSourceProcess(ctx, "remote", "1", 0); !errors.Is(err, storage.ErrBusy) {
		t.Fatal("old release deleted replacement slot", err)
	}
	f.s.stopServeProcesses()
	second.Release()
	if _, err = f.s.reserveSourceProcess(ctx, "remote", "1", 0); !errors.Is(err, storage.ErrRemoteOffline) {
		t.Fatal("shutdown accepted new process custody", err)
	}
}

func TestSourceProcessProfileCommitCancelsWithoutChangingIdentity(t *testing.T) {
	f := newObservationFixture(t)
	ctx := context.Background()
	identities, owner, _ := observationOwner(t, f)
	lease, err := f.s.reserveSourceProcess(ctx, "remote", "1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	profile, _ := SourceReadPreset("everything")
	gated, err := dbwork.Begin(ctx, f.s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.s.ApplySourceReadProfileTx(ctx, gated.Tx(), identities, owner, SourceReadProfileChange{SourceID: "remote", Generation: "1", ExpectedRevision: 0, Profile: profile})
	if err != nil {
		gated.Rollback()
		t.Fatal(err)
	}
	// No DB access from the local check while a real caller transaction is held.
	if err = lease.CheckLocal(); err != nil {
		gated.Rollback()
		t.Fatal(err)
	}
	if err = gated.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = f.s.ReadProfileCommitted(ctx, "remote", out.Profile.Revision); err != nil {
		t.Fatal(err)
	}
	if lease.CheckLocal() == nil {
		t.Fatal("committed profile did not cancel old process")
	}
	var generation int64
	if err = f.s.db.QueryRow(`SELECT generation FROM remote_sources WHERE id='remote'`).Scan(&generation); err != nil || generation != 1 {
		t.Fatal("profile changed source identity", generation, err)
	}
	if _, err = f.s.reserveSourceProcess(ctx, "remote", "1", out.Profile.Revision); !errors.Is(err, storage.ErrBusy) {
		t.Fatal("profile commit released physical custody", err)
	}
	lease.Release()
	replacement, err := f.s.reserveSourceProcess(ctx, "remote", "1", out.Profile.Revision)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Release()
	if err = f.s.ReadProfileCommitted(ctx, "remote", 0); !errors.Is(err, storage.ErrRemoteChanged) {
		t.Fatal("stale commit notification accepted", err)
	}
	if err = replacement.CheckLocal(); err != nil {
		t.Fatal("stale notification canceled current process", err)
	}
}

func TestSourceProcessRechecksActualRootGenerationAndProfile(t *testing.T) {
	for _, change := range []string{"root", "generation", "profile", "removed"} {
		t.Run(change, func(t *testing.T) {
			f := newObservationFixture(t)
			ctx := context.Background()
			lease, err := f.s.reserveSourceProcess(ctx, "remote", "1", 0)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			switch change {
			case "root":
				_, err = f.s.db.Exec(`UPDATE remote_sources SET root=?`, t.TempDir())
			case "generation":
				_, err = f.s.db.Exec(`UPDATE remote_sources SET generation=2`)
			case "profile":
				_, err = f.s.db.Exec(`INSERT INTO remote_source_read_profiles(source_id,revision,scan_depth,whole_file_reads,connections,requests_per_second,bytes_per_second) VALUES('remote',1,'complete',0,2,2,0)`)
			case "removed":
				_, err = f.s.db.Exec(`UPDATE remote_sources SET removed=1`)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = lease.Check(ctx); err == nil || lease.CheckLocal() == nil {
				t.Fatal("changed source retained process", err)
			}
			// Returning durable facts to A must never revive the canceled A lease.
			if _, err = f.s.db.Exec(`UPDATE remote_sources SET generation=1,removed=0`); err != nil {
				t.Fatal(err)
			}
			if lease.CheckLocal() == nil {
				t.Fatal("A-to-B-to-A revived process custody")
			}
		})
	}
}

func TestSourceProcessRequestCancellationDoesNotRevokeSharedSource(t *testing.T) {
	f := newObservationFixture(t)
	lease, err := f.s.reserveSourceProcess(context.Background(), "remote", "1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	request, cancel := context.WithCancel(context.Background())
	cancel()
	if err = lease.Check(request); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = lease.Check(context.Background()); err != nil {
		t.Fatal("one canceled request revoked shared process", err)
	}
}
